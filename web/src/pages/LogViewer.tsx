import { formatLocale } from '../i18n'
import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useMemo, useState } from 'react'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Bug, Download, Search, TriangleAlert } from 'lucide-react'
import { get, put, type LogLevel, type LogLine } from '../api'
import { useMonitor } from './ServerMonitor'
import { Button, Card, ErrorNote, Input, Toggle } from '../ui'

// The log: the latest lines Box Office keeps, newest first, to search,
// filter and download for a bug report. Debug logging is switched on here
// when something needs a closer look. The run before a restart is kept on
// disk, with its crash, and can be read here too.

const REFRESH_MS = 5000

export type LogSearch = { q: string; level?: LogLevel; n: number }

const levels: { value: LogLevel | 'all'; label: string }[] = [
  { value: 'all', label: 'log.everything' },
  { value: 'info', label: 'log.info' },
  { value: 'warn', label: 'log.warn' },
  { value: 'error', label: 'log.error' },
]

type Page = { lines: LogLine[]; more: boolean; crash?: string; started?: number; ended?: number }

type Run = 'this' | 'previous'

function logPath(base: string, q: { q?: string; level?: string; before?: number; limit?: number; run?: Run }) {
  const p = new URLSearchParams()
  if (q.run === 'previous') p.set('run', 'previous')
  if (q.q) p.set('q', q.q)
  if (q.level) p.set('level', q.level)
  if (q.before) p.set('before', String(q.before))
  if (q.limit) p.set('limit', String(q.limit))
  const s = p.toString()
  return base + (s ? '?' + s : '')
}

export function LogViewer({ search }: { search: LogSearch }) {
  const { t } = useTranslation()
  const [text, setText] = useState(search.q)
  const [q, setQ] = useState(search.q)
  const [level, setLevel] = useState<LogLevel | 'all'>(search.level ?? 'all')
  const [live, setLive] = useState(true)
  const [run, setRun] = useState<Run>('this')
  const m = useMonitor(false)
  const previous = m.data?.previousRun
  useEffect(() => {
    if (!search.n) return
    setText(search.q)
    setQ(search.q)
    setLevel(search.level ?? 'all')
  }, [search])
  // Search a moment after typing stops.
  useEffect(() => {
    const id = setTimeout(() => setQ(text.trim()), 300)
    return () => clearTimeout(id)
  }, [text])
  const showing: Run = previous ? run : 'this'
  const query = { q, level: level === 'all' ? undefined : level, limit: 200, run: showing }
  const logs = useInfiniteQuery({
    queryKey: ['logs', query.q, query.level, showing],
    queryFn: ({ pageParam }) => get<Page>(logPath('/admin/logs', { ...query, before: pageParam || undefined })),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.more && last.lines.length ? last.lines[last.lines.length - 1].seq : undefined),
    // The previous run doesn't change.
    refetchInterval: live && showing === 'this' ? REFRESH_MS : false,
  })
  const first = logs.data?.pages[0]
  const lines = useMemo(() => logs.data?.pages.flatMap((p) => p.lines) ?? [], [logs.data])
  return (
    <div className="flex flex-col gap-4">
      <DebugSwitch />
      <div className="flex flex-wrap items-end justify-between gap-3">
        <p className="max-w-2xl text-sm text-muted">
          <Trans i18nKey="log.intro" components={{ code: <code className="text-xs" /> }} />
        </p>
        <a href={'/api/v1' + logPath('/admin/logs/download', query)} download>
          <Button tabIndex={-1}>
            <Download className="size-4" aria-hidden /> {t('log.download')}
          </Button>
        </a>
      </div>
      {previous && (
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label={t('log.run')}>
          {(['this', 'previous'] as const).map((r) => (
            <button
              key={r}
              role="radio"
              aria-checked={showing === r}
              onClick={() => setRun(r)}
              className={`inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-sm transition ${showing === r ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:border-ink/40'}`}
            >
              {r === 'previous' && previous.crash && <TriangleAlert className="size-3.5 text-bad" aria-hidden />}
              {t(r === 'this' ? 'log.thisRun' : 'log.previousRun')}
            </button>
          ))}
        </div>
      )}
      {showing === 'previous' && first && <PreviousRun page={first} />}
      <div className="flex flex-wrap items-center gap-2">
        <label className="relative min-w-0 flex-1 basis-64">
          <span className="sr-only">{t('log.search')}</span>
          <Search className="pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2 text-muted" aria-hidden />
          <Input type="search" value={text} onChange={(e) => setText(e.target.value)} placeholder={t('log.searchPlaceholder')} className="ps-9" />
        </label>
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label={t('list.show')}>
          {levels.map((l) => (
            <button
              key={l.value}
              role="radio"
              aria-checked={level === l.value}
              onClick={() => setLevel(l.value)}
              className={`rounded-full border px-3 py-1.5 text-sm transition ${level === l.value ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:border-ink/40'}`}
            >
              {t(l.label)}
            </button>
          ))}
        </div>
        {showing === 'this' && (
          <label className="flex cursor-pointer items-center gap-2 text-sm text-muted">
            <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} className="accent-[var(--marquee)]" />
            {t('log.keepUpdating')}
          </label>
        )}
      </div>
      <ErrorNote error={logs.error} />
      <Card className="overflow-hidden p-0!">
        <div className="max-h-[36rem] overflow-auto font-mono text-xs leading-relaxed" role="log" aria-live="off">
          {lines.length === 0 && !logs.isPending && <p className="p-4 font-sans text-sm text-muted">{t('log.nothing')}</p>}
          {lines.map((l) => (
            <LogRow key={l.seq} line={l} />
          ))}
          {logs.hasNextPage && (
            <div className="p-3 font-sans">
              <Button onClick={() => logs.fetchNextPage()} busy={logs.isFetchingNextPage}>
                {t('log.older')}
              </Button>
            </div>
          )}
        </div>
      </Card>
    </div>
  )
}

// PreviousRun says when the run before this one ran and how it ended: the
// crash Go wrote, or that it left none.
function PreviousRun({ page }: { page: Page }) {
  const { t } = useTranslation()
  const at = (ms?: number) => (ms ? new Date(ms).toLocaleString(formatLocale()) : '')
  return (
    <Card className={`flex flex-col gap-3 ${page.crash ? 'border-bad/40' : ''}`}>
      {page.started ? <p className="text-sm text-muted">{t('log.previousSpan', { from: at(page.started), to: at(page.ended) })}</p> : null}
      {page.crash ? (
        <>
          <p className="flex items-center gap-2 text-sm font-medium text-bad">
            <TriangleAlert className="size-4 shrink-0" aria-hidden /> {t('log.crashed')}
          </p>
          <pre className="max-h-80 overflow-auto rounded-md bg-bad/5 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-words" dir="ltr">
            {page.crash}
          </pre>
        </>
      ) : (
        <p className="text-sm text-muted">{t('log.noCrash')}</p>
      )}
    </Card>
  )
}

const timeOfDay = (ms: number) => new Date(ms).toLocaleTimeString(formatLocale(), { hour: '2-digit', minute: '2-digit', second: '2-digit' })

function LogRow({ line }: { line: LogLine }) {
  const color = line.level === 'error' ? 'text-bad' : line.level === 'warn' ? 'text-gold' : 'text-muted'
  return (
    <div className={`flex gap-3 border-b border-line/50 px-3 py-1.5 last:border-0 ${line.level === 'error' ? 'bg-bad/5' : ''}`}>
      <span className="shrink-0 tabular-nums text-muted" title={new Date(line.time).toISOString()}>
        {timeOfDay(line.time)}
      </span>
      <span className={`w-12 shrink-0 font-semibold uppercase ${color}`}>{line.level}</span>
      <span className="min-w-0 flex-1 break-words">
        <span>{line.message}</span>
        {line.attrs?.map((a, i) => (
          <span key={i} className="whitespace-pre-wrap">
            {' '}
            <span className="text-muted">{a.key}=</span>
            <span className="break-all">{a.value}</span>
          </span>
        ))}
      </span>
    </div>
  )
}

function DebugSwitch() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const m = useMonitor(false)
  const on = m.data?.debug ?? false
  const set = useMutation({
    mutationFn: (v: boolean) => put('/admin/debug', { on: v }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['monitor'] })
      qc.invalidateQueries({ queryKey: ['logs'] })
    },
  })
  return (
    <Card className="flex flex-col gap-3">
      <div className="flex items-start gap-3">
        <Bug className="mt-0.5 size-5 shrink-0 text-muted" aria-hidden />
        <div className="min-w-0 flex-1">
          <Toggle
            checked={on}
            disabled={!m.data || set.isPending || (m.data.debugForced && on)}
            onChange={(v) => set.mutate(v)}
            label={<span className="font-medium">{t('log.debug')}</span>}
          />
          <p className="mt-2 text-sm text-muted">
            {t('log.debugBody')}
          </p>
          {m.data?.debugForced && <p className="mt-2 text-sm text-muted">{t('log.debugForced')}</p>}
          <ErrorNote error={set.error} />
        </div>
      </div>
    </Card>
  )
}
