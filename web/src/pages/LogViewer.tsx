import { useEffect, useMemo, useState } from 'react'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Bug, Download, Search } from 'lucide-react'
import { get, put, type LogLevel, type LogLine } from '../api'
import { useMonitor } from './ServerMonitor'
import { Button, Card, ErrorNote, Input, Toggle } from '../ui'

// The log: the latest lines Box Office keeps in memory, newest first, to
// search, filter and download for a bug report. Debug logging is switched
// on here when something needs a closer look.

const REFRESH_MS = 5000

export type LogSearch = { q: string; level?: LogLevel; n: number }

const levels: { value: LogLevel | 'all'; label: string }[] = [
  { value: 'all', label: 'Everything' },
  { value: 'info', label: 'Info and up' },
  { value: 'warn', label: 'Warnings and errors' },
  { value: 'error', label: 'Errors' },
]

type Page = { lines: LogLine[]; more: boolean }

function logPath(base: string, q: { q?: string; level?: string; before?: number; limit?: number }) {
  const p = new URLSearchParams()
  if (q.q) p.set('q', q.q)
  if (q.level) p.set('level', q.level)
  if (q.before) p.set('before', String(q.before))
  if (q.limit) p.set('limit', String(q.limit))
  const s = p.toString()
  return base + (s ? '?' + s : '')
}

export function LogViewer({ search }: { search: LogSearch }) {
  const [text, setText] = useState(search.q)
  const [q, setQ] = useState(search.q)
  const [level, setLevel] = useState<LogLevel | 'all'>(search.level ?? 'all')
  const [live, setLive] = useState(true)
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
  const query = { q, level: level === 'all' ? undefined : level, limit: 200 }
  const logs = useInfiniteQuery({
    queryKey: ['logs', query.q, query.level],
    queryFn: ({ pageParam }) => get<Page>(logPath('/admin/logs', { ...query, before: pageParam || undefined })),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.more && last.lines.length ? last.lines[last.lines.length - 1].seq : undefined),
    refetchInterval: live ? REFRESH_MS : false,
  })
  const lines = useMemo(() => logs.data?.pages.flatMap((p) => p.lines) ?? [], [logs.data])
  return (
    <div className="flex flex-col gap-4">
      <DebugSwitch />
      <div className="flex flex-wrap items-end justify-between gap-3">
        <p className="max-w-2xl text-sm text-muted">
          Box Office keeps its latest log lines in memory, newest first. They're also wherever its output goes, such as <code className="text-xs">kubectl logs</code>. Download them to attach to a bug report.
        </p>
        <a href={'/api/v1' + logPath('/admin/logs/download', query)} download>
          <Button tabIndex={-1}>
            <Download className="size-4" aria-hidden /> Download
          </Button>
        </a>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <label className="relative min-w-0 flex-1 basis-64">
          <span className="sr-only">Search the log</span>
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted" aria-hidden />
          <Input type="search" value={text} onChange={(e) => setText(e.target.value)} placeholder="Search, such as a title, an indexer or an error" className="pl-9" />
        </label>
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Show">
          {levels.map((l) => (
            <button
              key={l.value}
              role="radio"
              aria-checked={level === l.value}
              onClick={() => setLevel(l.value)}
              className={`rounded-full border px-3 py-1.5 text-sm transition ${level === l.value ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:border-ink/40'}`}
            >
              {l.label}
            </button>
          ))}
        </div>
        <label className="flex cursor-pointer items-center gap-2 text-sm text-muted">
          <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} className="accent-[var(--marquee)]" />
          Keep updating
        </label>
      </div>
      <ErrorNote error={logs.error} />
      <Card className="overflow-hidden p-0!">
        <div className="max-h-[36rem] overflow-auto font-mono text-xs leading-relaxed" role="log" aria-live="off">
          {lines.length === 0 && !logs.isPending && <p className="p-4 font-sans text-sm text-muted">Nothing matches.</p>}
          {lines.map((l) => (
            <LogRow key={l.seq} line={l} />
          ))}
          {logs.hasNextPage && (
            <div className="p-3 font-sans">
              <Button onClick={() => logs.fetchNextPage()} busy={logs.isFetchingNextPage}>
                Show older lines
              </Button>
            </div>
          )}
        </div>
      </Card>
    </div>
  )
}

const timeOfDay = (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })

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
            label={<span className="font-medium">Debug logging</span>}
          />
          <p className="mt-2 text-sm text-muted">
            Logs every request, indexer search (with why each release was or wasn't taken), news server connection, download stage and task. Turn it on to find out why something didn't happen, then off
            again: it's a lot of lines. It stays on across restarts until you turn it off.
          </p>
          {m.data?.debugForced && <p className="mt-2 text-sm text-muted">BOXOFFICE_DEBUG is set in the deployment, so it stays on. Remove it and restart Box Office to turn it off.</p>}
          <ErrorNote error={set.error} />
        </div>
      </div>
    </Card>
  )
}
