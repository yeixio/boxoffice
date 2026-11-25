import { useTranslation } from 'react-i18next'
import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleCheck, RefreshCw, RotateCcw, SkipForward } from 'lucide-react'
import { get, post, put, type PlayOnRecording, type PlayOnSettings, type PlayOnState } from '../api'
import { bytes, when } from '../format'
import { Badge, Button, Card, ErrorNote, Field, Input, Toggle } from '../ui'

// PlayOn Cloud: recordings queued in the PlayOn Cloud app are downloaded
// once they're finished and filed in the library through Import files.
// PlayOn deletes them after a few days, so Box Office checks every 15
// minutes. PlayOn has no published API, so this may break if PlayOn
// changes its apps.

export const playOnDocs = 'https://github.com/yeixio/boxoffice/blob/main/docs/playon.md'

type Form = { on: boolean; email: string; password: string; forget: boolean }

const toForm = (s: PlayOnSettings): Form => ({ on: s.config.on, email: s.config.email, password: '', forget: false })

const tone: Record<PlayOnState, 'plain' | 'ok' | 'bad' | 'gold' | 'marquee'> = {
  new: 'plain',
  downloading: 'marquee',
  filed: 'ok',
  check: 'gold',
  failed: 'bad',
  skipped: 'plain',
  expired: 'bad',
}

const quality = (q?: number) => (q === 2 ? '1080p' : q === 1 ? '720p' : '')

export function PlayOnCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { data, error: loadError } = useQuery({
    queryKey: ['playon'],
    queryFn: () => get<PlayOnSettings>('/playon'),
    refetchInterval: (q) => (q.state.data?.status.running ? 3000 : 60000),
  })
  const [form, setForm] = useState<Form>()
  const [dirty, setDirty] = useState(false)
  useEffect(() => {
    if (data && !dirty) setForm(toForm(data))
  }, [data, dirty])
  const keep = (v: PlayOnSettings) => qc.setQueryData(['playon'], v)
  const save = useMutation({
    mutationFn: (f: Form) => put<PlayOnSettings>('/playon', f),
    onSuccess: (v) => {
      keep(v)
      setForm(toForm(v))
      setDirty(false)
    },
  })
  const test = useMutation({ mutationFn: (f: Form) => post<{ ok: boolean; ready: number }>('/playon/test', f) })
  const check = useMutation({ mutationFn: () => post<PlayOnSettings>('/playon/check'), onSuccess: keep })
  const act = useMutation({
    mutationFn: ({ id, what }: { id: number; what: 'retry' | 'skip' }) => post<PlayOnSettings>(`/playon/recordings/${id}/${what}`),
    onSuccess: keep,
  })
  if (!data || !form) {
    return (
      <Card>
        <h2 className="mb-1 font-display text-lg font-semibold">{t('playon.title')}</h2>
        <ErrorNote error={loadError} />
      </Card>
    )
  }
  const change = (f: (draft: Form) => void) => {
    const next = { ...form }
    f(next)
    setForm(next)
    setDirty(true)
    test.reset()
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(form)
  }
  const c = data.config
  const st = data.status
  const linked = c.email !== '' && c.hasPassword && !form.forget
  const samePassword = c.hasPassword && form.email === c.email && !form.forget
  const canTest = form.email !== '' && (form.password !== '' || samePassword)
  return (
    <Card>
      <div className="mb-1 flex flex-wrap items-center gap-2">
        <h2 className="font-display text-lg font-semibold">{t('playon.title')}</h2>
        {c.on ? <Badge tone="ok">{t('playon.on')}</Badge> : linked ? <Badge>{t('playon.off')}</Badge> : null}
      </div>
      <p className="mb-4 text-sm text-muted">
        {t('playon.sub')}{' '}
        <a href={playOnDocs} target="_blank" rel="noreferrer" className="text-marquee hover:underline">
          {t('playon.docs')}
        </a>
      </p>
      <form onSubmit={submit} className="flex flex-col gap-5">
        <details className="text-sm" open={!linked}>
          <summary className="cursor-pointer text-muted hover:text-ink">{t('playon.how')}</summary>
          <ol className="mt-2 flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 ps-8 pe-4 text-muted">
            {['playon.help.1', 'playon.help.2', 'playon.help.3', 'playon.help.4'].map((s) => (
              <li key={s}>{t(s)}</li>
            ))}
          </ol>
        </details>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t('playon.email')}>
            {(id) => <Input id={id} type="email" autoComplete="off" value={form.email} onChange={(e) => change((f) => ((f.email = e.target.value), (f.forget = false)))} />}
          </Field>
          <Field label={t('playon.password')}>
            {(id) => (
              <Input
                id={id}
                type="password"
                autoComplete="new-password"
                value={form.password}
                placeholder={samePassword ? t('playon.keepPassword') : ''}
                onChange={(e) => change((f) => (f.password = e.target.value))}
              />
            )}
          </Field>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" disabled={!canTest} busy={test.isPending} onClick={() => test.mutate(form)}>
            {t('playon.test')}
          </Button>
          {linked && (
            <Button type="button" variant="ghost" onClick={() => change((f) => ((f.forget = true), (f.on = false), (f.email = ''), (f.password = '')))}>
              {t('playon.forget')}
            </Button>
          )}
          {test.isSuccess && (
            <span className="inline-flex items-center gap-1 text-sm text-ok">
              <CircleCheck className="size-4" aria-hidden /> {t('playon.works', { count: test.data.ready })}
            </span>
          )}
        </div>
        <ErrorNote error={test.error} />
        <Toggle
          checked={form.on}
          onChange={(v) => change((f) => (f.on = v))}
          label={
            <span>
              {t('playon.bringIn')}
              <span className="block text-xs text-muted">{t('playon.bringInHint')}</span>
            </span>
          }
        />
        <ErrorNote error={save.error} />
        <div>
          <Button variant="primary" type="submit" busy={save.isPending} disabled={!dirty}>
            {t('common.save')}
          </Button>
        </div>
      </form>

      {(c.on || data.recordings.length > 0) && (
        <>
          <h3 className="mb-2 mt-6 text-sm font-semibold">{t('playon.recordings')}</h3>
          <div className="flex flex-wrap items-center gap-2 text-sm">
            {st.running ? (
              <Badge tone="marquee">{t('playon.checking')}</Badge>
            ) : st.checkedAt ? (
              <span className="text-muted">{t('playon.checkedAt', { when: when(st.checkedAt) })}</span>
            ) : null}
            {st.recording > 0 && <Badge>{t('playon.inPlayOn', { count: st.recording })}</Badge>}
            {(data.counts.filed ?? 0) > 0 && <Badge tone="ok">{t('playon.filedCount', { count: data.counts.filed })}</Badge>}
            {(data.counts.check ?? 0) > 0 && <Badge tone="gold">{t('playon.checkCount', { count: data.counts.check })}</Badge>}
            {data.expiring > 0 && <Badge tone="bad">{t('playon.expiring', { count: data.expiring })}</Badge>}
          </div>
          {st.current && (
            <p className="mt-2 text-sm">
              {t('playon.downloading', { name: st.current.name, done: bytes(st.current.done), total: st.current.total > 0 ? bytes(st.current.total) : '?' })}
            </p>
          )}
          {st.paused && <p className="mt-2 text-sm text-gold">{st.paused}</p>}
          {st.error && <p className="mt-2 text-sm text-bad">{st.error}</p>}
          {(data.counts.check ?? 0) > 0 && (
            <p className="mt-2 text-sm text-muted">
              {t('playon.checkHint')}{' '}
              <Link to="/import-files" className="text-marquee hover:underline">
                {t('playon.openImport')}
              </Link>
            </p>
          )}
          <div className="mt-3">
            <Button onClick={() => check.mutate()} busy={check.isPending} disabled={!c.on || st.running}>
              <RefreshCw className="size-4" aria-hidden /> {t('playon.checkNow')}
            </Button>
          </div>
          <ErrorNote error={check.error ?? act.error} />
          {data.recordings.length === 0 ? (
            <p className="mt-3 text-sm text-muted">{t('playon.none')}</p>
          ) : (
            <ul className="mt-3 max-h-96 divide-y divide-line overflow-y-auto rounded-md border border-line text-sm">
              {data.recordings.map((r) => (
                <RecordingRow key={r.id} r={r} busy={act.isPending} onAct={(what) => act.mutate({ id: r.id, what })} />
              ))}
            </ul>
          )}
        </>
      )}
    </Card>
  )
}

function RecordingRow({ r, busy, onAct }: { r: PlayOnRecording; busy: boolean; onAct: (what: 'retry' | 'skip') => void }) {
  const { t } = useTranslation()
  const facts = [r.provider, quality(r.quality), r.size ? bytes(r.size) : ''].filter(Boolean)
  return (
    <li className="flex flex-col gap-1 px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="min-w-0 font-medium break-words">{r.name}</span>
        <Badge tone={tone[r.state]}>{t(`playon.state.${r.state}`)}</Badge>
        {facts.length > 0 && <span className="text-xs text-muted">{facts.join(' · ')}</span>}
        <span className="ms-auto flex gap-1">
          {(r.state === 'failed' || r.state === 'skipped') && (
            <Button variant="ghost" busy={busy} onClick={() => onAct('retry')}>
              <RotateCcw className="size-4" aria-hidden /> {t('playon.retry')}
            </Button>
          )}
          {(r.state === 'new' || r.state === 'failed') && (
            <Button variant="ghost" busy={busy} onClick={() => onAct('skip')}>
              <SkipForward className="size-4" aria-hidden /> {t('playon.skip')}
            </Button>
          )}
        </span>
      </div>
      <span className="text-xs text-muted">
        {r.state === 'filed' && r.filedTo
          ? t('playon.filedTo', { path: r.filedTo })
          : r.note || (r.expires && (r.state === 'new' || r.state === 'failed') ? t('playon.expires', { when: when(r.expires) }) : '')}
      </span>
    </li>
  )
}
