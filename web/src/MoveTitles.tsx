// Moving titles to another root folder: a whole library, or titles picked
// on Movies or Shows, to a new drive, say. The dialog shows what would
// move before anything does; the card in Settings → Library follows along,
// and the tray shows it from anywhere.

import i18n from './i18n'
import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FolderInput, Pause, Play, RotateCcw, X } from 'lucide-react'
import { get, post, put, del, ApiError, type MovePlan, type MoveStatus, type RootFolder, type TitleMove } from './api'
import { useRootFolders } from './hooks'
import { bytes } from './format'
import { eta, speed } from './pages/Downloads'
import { NumberField } from './pages/Connections'
import { Badge, Button, Card, ErrorNote, Field, Modal, Select, Spinner } from './ui'

type Kind = 'movie' | 'series'

const active = (m: TitleMove) => m.state === 'waiting' || m.state === 'copying' || m.state === 'copied' || m.state === 'switched'

// useMoves follows the moves, often while one is going.
export function useMoves() {
  return useQuery({
    queryKey: ['moves'],
    queryFn: () => get<MoveStatus>('/moves'),
    refetchInterval: (q) => (q.state.data?.moves.some(active) && !q.state.data.paused ? 2000 : 30_000),
  })
}

// MoveDialog picks where to move titles to, shows what would move, and
// starts it. ids are titles picked on Movies or Shows; from is a root
// folder, for all of its titles.
export function MoveDialog({ kind, ids, from, onClose, onStarted }: { kind: Kind; ids?: number[]; from?: string; onClose: () => void; onStarted?: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const roots = useRootFolders(kind)
  const others = (roots.data ?? []).filter((r) => r.path !== from)
  const [to, setTo] = useState('')
  const pick = { kind, ids, from, to }
  const plan = useQuery({
    queryKey: ['move-plan', kind, ids, from, to],
    queryFn: () => post<MovePlan>('/moves/plan', pick),
    enabled: to !== '',
    staleTime: 0,
    gcTime: 0,
  })
  const start = useMutation({
    mutationFn: (p: MovePlan) => post<MoveStatus>('/moves', { ...pick, expectCount: p.count, expectSize: p.size }),
    onSuccess: (st) => {
      qc.setQueryData(['moves'], st)
      qc.invalidateQueries({ queryKey: ['rootfolders'] })
      onStarted?.()
    },
    // What's on disk changed since: look again.
    onError: (e) => e instanceof ApiError && e.status === 409 && plan.refetch(),
  })
  const p = plan.data
  const label = (r: RootFolder) => (r.accessible ? t('relocate.rootFree', { path: r.path, size: bytes(r.freeSpace) }) : r.path)
  if (start.isSuccess) {
    return (
      <Modal title={t('relocate.title')} onClose={onClose}>
        <div className="flex flex-col gap-4 text-sm">
          <p>{t('relocate.started', { count: start.variables?.count ?? 0, to })}</p>
          <div className="flex flex-wrap justify-end gap-2">
            <Link to="/settings#moves" className="inline-flex items-center rounded-md border border-line px-3.5 py-2 font-medium hover:border-ink/40" onClick={onClose}>
              {t('relocate.follow')}
            </Link>
            <Button variant="primary" onClick={onClose}>
              {t('relocate.done')}
            </Button>
          </div>
        </div>
      </Modal>
    )
  }
  return (
    <Modal title={t('relocate.title')} onClose={onClose} wide>
      <div className="flex flex-col gap-4 text-sm">
        <p className="text-muted">{from ? t('relocate.introAll', { from }) : t('relocate.intro', { count: ids?.length ?? 0 })}</p>
        {roots.isPending ? (
          <Spinner />
        ) : others.length === 0 ? (
          <p>
            {t('relocate.noOther')}{' '}
            <Link to={`/settings#folders-${kind}`} className="font-medium text-marquee" onClick={onClose}>
              {t('relocate.addRoot')}
            </Link>
          </p>
        ) : (
          <Field label={t('relocate.to')} hint={t('relocate.toHint')}>
            {(id) => <Select id={id} value={to} onChange={setTo} options={[{ value: '', label: t('relocate.pickRoot') }, ...others.map((r) => ({ value: r.path, label: label(r) }))]} />}
          </Field>
        )}
        {plan.isFetching && <Spinner label={t('relocate.looking')} />}
        <ErrorNote error={plan.error} />
        {p && !plan.isFetching && <PlanView plan={p} />}
        <ErrorNote error={start.error} />
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" disabled={!p || !!p.problem || p.count === 0 || plan.isFetching} busy={start.isPending} onClick={() => p && start.mutate(p)}>
            <FolderInput className="size-4" aria-hidden /> {p && !p.problem ? t('relocate.move', { count: p.count, size: bytes(p.size) }) : t('relocate.moveNone')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function PlanView({ plan }: { plan: MovePlan }) {
  const { t } = useTranslation()
  const noted = plan.titles.filter((x) => x.notes?.length)
  return (
    <div className="flex flex-col gap-3">
      {plan.count > 0 && (
        <p>
          {t('relocate.plan', { count: plan.count, files: plan.files, size: bytes(plan.size) })}{' '}
          {plan.need > 0 ? t('relocate.space', { need: bytes(plan.need), free: bytes(plan.freeSpace) }) : t('relocate.sameDisk')}
        </p>
      )}
      {plan.problem && <p className="rounded-md border border-bad/40 px-3 py-2 text-bad">{plan.problem}</p>}
      {plan.count > 0 && <p className="text-xs text-muted">{t('relocate.safety')}</p>}
      {noted.length > 0 && (
        <details>
          <summary className="cursor-pointer font-medium">{t('relocate.notes', { count: noted.length })}</summary>
          <ul className="mt-2 flex flex-col gap-1 text-xs">
            {noted.map((x) => (
              <li key={x.id}>
                <span className="font-medium">{x.title}</span>: <span className="text-muted">{x.notes!.join('; ')}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
      {plan.skipped.length > 0 && (
        <details>
          <summary className="cursor-pointer font-medium">{t('relocate.skipped', { count: plan.skipped.length })}</summary>
          <ul className="mt-2 flex flex-col gap-1 text-xs">
            {plan.skipped.map((x) => (
              <li key={x.id}>
                <span className="font-medium">{x.title}</span>: <span className="text-muted">{x.problem}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
}

const stateTone: Record<TitleMove['state'], 'plain' | 'ok' | 'bad' | 'gold' | 'marquee'> = {
  waiting: 'plain',
  copying: 'marquee',
  copied: 'marquee',
  switched: 'marquee',
  done: 'ok',
  failed: 'bad',
  skipped: 'gold',
  cancelled: 'plain',
}

// MovesCard is Settings → Library → Move titles: what's moving, how far
// it's got, and pausing, cancelling and the speed limit.
export function MovesCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const moves = useMoves()
  const act = useMutation({
    mutationFn: (what: 'pause' | 'resume' | 'cancel' | 'retry' | 'clear') => (what === 'clear' ? del('/moves/finished').then(() => get<MoveStatus>('/moves')) : post<MoveStatus>(`/moves/${what}`)),
    onSuccess: (st) => qc.setQueryData(['moves'], st),
  })
  const limit = useMutation({ mutationFn: (speedLimit: number) => put<MoveStatus>('/moves/speed', { speedLimit }), onSuccess: (st) => qc.setQueryData(['moves'], st) })
  const st = moves.data
  const going = st?.moves.filter(active) ?? []
  const finished = st?.moves.filter((m) => !active(m)) ?? []
  const failed = finished.filter((m) => m.state === 'failed').length
  const pct = st && st.size > 0 ? Math.min(100, (st.done / st.size) * 100) : 0
  const rate = st?.progress?.speed ?? 0
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('relocate.card')}</h2>
      <p className="mb-4 text-sm text-muted">{t('relocate.cardSub')}</p>
      {moves.isPending ? (
        <Spinner />
      ) : moves.isError ? (
        <ErrorNote error={moves.error} />
      ) : (
        <div className="flex flex-col gap-4 text-sm">
          {going.length === 0 && finished.length === 0 && <p>{t('relocate.nothing')}</p>}
          {going.length > 0 && (
            <div className="flex flex-col gap-2">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span className="font-medium">{st!.paused ? t('relocate.paused', { count: going.length }) : t('relocate.moving', { count: going.length })}</span>
                <span className="text-muted">{t('relocate.bytes', { done: bytes(st!.done), size: bytes(st!.size) })}</span>
                {!st!.paused && rate > 0 && <span className="text-muted">{speed(rate)}</span>}
                {!st!.paused && rate > 0 && <span className="text-muted">{eta(st!.size - st!.done, rate)}</span>}
              </div>
              <div className="h-1.5 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)}>
                <div className="h-full bg-marquee transition-all" style={{ width: `${pct}%` }} />
              </div>
              {st!.paused && st!.pausedWhy && <p className="text-bad">{st!.pausedWhy}</p>}
              {st!.current && st!.progress && (
                <p className="text-muted">
                  {t(`relocate.stage.${st!.progress.stage}`, { title: st!.current.title, done: bytes(st!.progress.done), size: bytes(st!.progress.size) })}
                  {st!.progress.file && <span className="block truncate text-xs">{st!.progress.file}</span>}
                </p>
              )}
              <div className="flex flex-wrap gap-2">
                {st!.paused ? (
                  <Button busy={act.isPending && act.variables === 'resume'} onClick={() => act.mutate('resume')}>
                    <Play className="size-4" aria-hidden /> {t('relocate.resume')}
                  </Button>
                ) : (
                  <Button busy={act.isPending && act.variables === 'pause'} onClick={() => act.mutate('pause')}>
                    <Pause className="size-4" aria-hidden /> {t('relocate.pause')}
                  </Button>
                )}
                <Button variant="danger" busy={act.isPending && act.variables === 'cancel'} onClick={() => act.mutate('cancel')}>
                  <X className="size-4" aria-hidden /> {t('relocate.cancel')}
                </Button>
              </div>
              <p className="text-xs text-muted">{t('relocate.pauseHint')}</p>
            </div>
          )}
          <ErrorNote error={act.error} />
          {st && st.moves.length > 0 && (
            <ul className="flex max-h-96 flex-col divide-y divide-line overflow-y-auto">
              {st.moves.map((m) => (
                <li key={m.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                  <span className="min-w-0 flex-1 basis-48">
                    <span className="font-medium">{m.title}</span>
                    <span className="block break-all text-xs text-muted">{t('relocate.fromTo', { from: m.fromRoot, to: m.toRoot })}</span>
                    {m.note && <span className={`block text-xs ${m.state === 'failed' ? 'text-bad' : 'text-muted'}`}>{m.note}</span>}
                  </span>
                  <span className="text-xs text-muted">{bytes(m.size)}</span>
                  <Badge tone={stateTone[m.state]}>{t(`relocate.state.${m.state}`)}</Badge>
                </li>
              ))}
            </ul>
          )}
          {finished.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {failed > 0 && (
                <Button busy={act.isPending && act.variables === 'retry'} onClick={() => act.mutate('retry')}>
                  <RotateCcw className="size-4" aria-hidden /> {t('relocate.retry', { count: failed })}
                </Button>
              )}
              <Button variant="ghost" busy={act.isPending && act.variables === 'clear'} onClick={() => act.mutate('clear')}>
                {t('relocate.clear')}
              </Button>
            </div>
          )}
          {st && <NumberField label={t('relocate.speedLimit')} hint={t('relocate.speedLimitHint')} value={st.speedLimit} min={0} max={100000} suffix="MB/s" busy={limit.isPending} onSave={(v) => limit.mutate(v)} />}
          <ErrorNote error={limit.error} />
        </div>
      )}
    </Card>
  )
}

// Finished moves show in the tray this long, so how they went can be seen.
const recent = 30 * 60 * 1000

// moveTrayItem is the tray's line for the moves, or null when there are
// none going or just finished.
export function moveTrayItem(st: MoveStatus | undefined, now = Date.now()) {
  if (!st) return null
  const going = st.moves.filter(active)
  const to = '/settings#moves'
  if (going.length === 0) {
    const ended = st.moves.filter((m) => (m.state === 'done' || m.state === 'failed') && m.finishedAt && now - Date.parse(m.finishedAt) < recent)
    if (ended.length === 0) return null
    const failed = ended.filter((m) => m.state === 'failed').length
    return {
      key: 'title-moves',
      state: failed ? ('failed' as const) : ('done' as const),
      title: i18n.t('relocate.moved', { count: ended.length - failed }),
      detail: failed ? i18n.t('relocate.notMoved', { count: failed }) : undefined,
      to,
      action: i18n.t('tray.seeIt'),
    }
  }
  const total = st.moves.filter((m) => m.state !== 'skipped' && m.state !== 'cancelled').length
  const done = st.moves.filter((m) => m.state === 'done' || m.state === 'failed').length
  return {
    key: 'title-moves',
    state: 'running' as const,
    title: st.paused ? i18n.t('relocate.paused', { count: going.length }) : i18n.t('relocate.moving', { count: going.length }),
    detail: st.current?.title,
    done,
    total,
    to,
    action: i18n.t('tray.seeProgress'),
  }
}
