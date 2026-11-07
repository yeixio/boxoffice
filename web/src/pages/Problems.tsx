import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, CircleAlert, RefreshCw, Undo2, Trash2, Wrench } from 'lucide-react'
import { get, post, type LibProblem, type ProblemAction, type ProblemFix, type ProblemKind, type ProblemMedia, type ProblemsState } from '../api'
import { bytes, when } from '../format'
import { Badge, Button, Card, ErrorNote, Heading, Modal, Spinner } from '../ui'

// The order the kinds of problem are shown in, most worth fixing first.
const kinds: ProblemKind[] = ['duplicate', 'parts', 'disc_image', 'untracked', 'missing', 'naming', 'unknown', 'not_listed', 'leftover', 'empty']

const busy = (s?: ProblemsState) => !!s && (s.status.looking || !!s.status.fixing || s.status.queued.length > 0 || !!s.status.deciding)

export function useProblems() {
  return useQuery({
    queryKey: ['problems'],
    queryFn: () => get<ProblemsState>('/problems'),
    refetchInterval: (q) => (busy(q.state.data) ? 1500 : false),
  })
}

// key turns the server's snake_case names into the catalog's camelCase keys.
const key = (name: string) => name.replace(/_(\w)/g, (_, c: string) => c.toUpperCase())

// rel shows a path inside its library folder.
const rel = (root: string, path: string) => (path.startsWith(root + '/') ? path.slice(root.length + 1) : path)
const base = (path: string) => path.slice(path.lastIndexOf('/') + 1)

export function Problems() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const state = useProblems()
  const set = (s: ProblemsState) => qc.setQueryData(['problems'], s)
  const look = useMutation({ mutationFn: () => post<ProblemsState>('/problems/look'), onSuccess: set })
  const s = state.data
  // Box Office keeps what the daily look found, across restarts; only if
  // it has never looked does the first visit look right away.
  useEffect(() => {
    if (s && !s.report && !s.status.looking && !look.isPending && !look.isSuccess) look.mutate()
  }, [s, look])
  // What fixes change shows on the titles' pages too.
  const fixing = s?.status.fixing?.id
  useEffect(() => {
    qc.invalidateQueries({ queryKey: ['movies'] })
    qc.invalidateQueries({ queryKey: ['series'] })
  }, [fixing, qc])

  return (
    <>
      <Heading
        title={t('problems.title')}
        sub={<Trans i18nKey="problems.sub" components={{ code: <code /> }} />}
        actions={
          <Button busy={look.isPending || s?.status.looking} onClick={() => look.mutate()}>
            <RefreshCw className="size-4" aria-hidden /> {t('problems.look')}
          </Button>
        }
      />
      {state.isPending ? (
        <Spinner />
      ) : state.isError ? (
        <ErrorNote error={state.error} />
      ) : (
        <div className="flex flex-col gap-6">
          <Status state={s!} />
          <ErrorNote error={look.error} />
          <Waiting state={s!} />
          {s!.report && <Found state={s!} />}
          <Recent fixes={s!.fixes} />
        </div>
      )}
    </>
  )
}

function Status({ state }: { state: ProblemsState }) {
  const { t } = useTranslation()
  const st = state.status
  const notes: string[] = []
  for (const r of state.report?.roots ?? []) {
    if (!r.readable) notes.push(t('problems.unreadable', { path: r.path }))
    else if (!r.writable) notes.push(t('problems.readOnly', { path: r.path }))
  }
  const needsFFmpeg = state.report?.problems.some((p) => p.fix?.action === 'convert' || p.fix?.action === 'join')
  if (!st.ffmpeg && needsFFmpeg) notes.push(t('problems.noFFmpeg'))
  return (
    <div className="flex flex-col gap-2 text-sm">
      <p className="text-muted">
        {st.looking
          ? t('problems.looking', { done: st.done, total: st.total || '…' })
          : st.lookedAt
            ? t('problems.lookedAt', { when: when(st.lookedAt) })
            : t('problems.never')}
        {!st.looking && st.next && <> {t('problems.next', { when: when(st.next) })}</>}
      </p>
      {st.error && <p className="text-bad" role="alert">{st.error}</p>}
      {notes.map((n) => (
        <p key={n} className="flex items-start gap-2 text-gold">
          <CircleAlert className="mt-0.5 size-4 shrink-0" aria-hidden /> {n}
        </p>
      ))}
    </div>
  )
}

function Found({ state }: { state: ProblemsState }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const problems = state.report!.problems
  const [picked, setPicked] = useState<Record<string, boolean>>({})
  const [keep, setKeep] = useState<Record<string, string>>({})
  const [choice, setChoice] = useState<Record<string, ProblemAction>>({})
  // Parts that look like copies are kept one by default; joining is a pick.
  const as = (p: LibProblem): ProblemAction | undefined => choice[p.id] ?? (p.copies ? 'keep_one' : p.fix?.action)
  const fix = useMutation({
    mutationFn: (ids: string[]) => {
      const pick: Record<string, ProblemAction> = {}
      for (const p of problems) if (ids.includes(p.id) && as(p) && as(p) !== p.fix?.action) pick[p.id] = as(p)!
      return post<ProblemsState>('/problems/fix', { ids, keep, as: pick })
    },
    onSuccess: (s) => {
      qc.setQueryData(['problems'], s)
      setPicked({})
    },
  })
  const groups = useMemo(() => kinds.map((k) => ({ kind: k, list: problems.filter((p) => p.kind === k) })).filter((g) => g.list.length > 0), [problems])
  const fixable = (p: LibProblem) => !!p.fix && !p.fix.blocked && (state.status.ffmpeg || (as(p) !== 'convert' && as(p) !== 'join'))
  const pending = (p: LibProblem) => state.status.fixing?.id === p.id || state.status.queued.includes(p.id)
  const selected = problems.filter((p) => picked[p.id] && fixable(p) && !pending(p))
  if (problems.length === 0) {
    return (
      <Card>
        <p className="text-sm">{t('problems.none')}</p>
      </Card>
    )
  }
  return (
    <>
      {groups.map((g) => {
        const ready = g.list.filter((p) => fixable(p) && !pending(p))
        return (
          <Card key={g.kind}>
            <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0 flex-1 basis-64">
                <h2 className="font-display text-lg font-semibold">
                  {t(`problems.kinds.${key(g.kind)}.title`)} <span className="text-muted">({g.list.length})</span>
                </h2>
                <p className="text-sm text-muted">{t(`problems.kinds.${key(g.kind)}.hint`)}</p>
              </div>
              {/* Folders not on the list are often movies or shows Box Office
                  doesn't know yet: each is set aside only when picked. */}
              {ready.length > 1 && g.kind !== 'not_listed' && (
                <Button busy={fix.isPending} onClick={() => fix.mutate(ready.map((p) => p.id))}>
                  <Wrench className="size-4" aria-hidden /> {t('problems.fixAll', { count: ready.length })}
                </Button>
              )}
            </div>
            <ul className="flex flex-col divide-y divide-line">
              {g.list.map((p) => (
                <Row
                  key={p.id}
                  p={p}
                  state={state}
                  fixable={fixable(p)}
                  picked={!!picked[p.id]}
                  onPick={(v) => setPicked((x) => ({ ...x, [p.id]: v }))}
                  keep={keep[p.id] ?? p.fix?.keep ?? ''}
                  onKeep={(path) => setKeep((x) => ({ ...x, [p.id]: path }))}
                  action={as(p)}
                  onAction={(a) => setChoice((x) => ({ ...x, [p.id]: a }))}
                  onFix={() => fix.mutate([p.id])}
                  busy={fix.isPending}
                />
              ))}
            </ul>
          </Card>
        )
      })}
      <ErrorNote error={fix.error} />
      {selected.length > 0 && (
        <div className="sticky bottom-4 z-10 flex justify-end">
          <Button variant="primary" busy={fix.isPending} onClick={() => fix.mutate(selected.map((p) => p.id))} className="shadow-lg">
            <Wrench className="size-4" aria-hidden /> {t('problems.fixSelected', { count: selected.length })}
          </Button>
        </div>
      )}
    </>
  )
}

function Row(props: {
  p: LibProblem
  state: ProblemsState
  fixable: boolean
  picked: boolean
  onPick: (v: boolean) => void
  keep: string
  onKeep: (path: string) => void
  action?: ProblemAction
  onAction: (a: ProblemAction) => void
  onFix: () => void
  busy: boolean
}) {
  const { t } = useTranslation()
  const { p, state } = props
  const fixing = state.status.fixing?.id === p.id ? state.status.fixing : undefined
  const queued = state.status.queued.includes(p.id)
  const failed = state.status.failed.find((f) => f.id === p.id)
  const action = props.action
  // The other way to fix parts that look like copies.
  const other: ProblemAction | undefined = p.copies ? (action === 'join' ? 'keep_one' : 'join') : undefined
  const link = p.title && (p.title.kind === 'movie' ? `/movies/${p.title.id}` : `/shows/${p.title.id}`)
  const name = p.title ? (p.title.year ? `${p.title.name} (${p.title.year})` : p.title.name) : (p.files[0]?.name ?? base(p.files[0]?.path ?? ''))
  // A folder that isn't any title's is usually one to add, not set aside.
  const root = state.report?.roots.find((r) => r.path === p.root)
  const scan = p.kind === 'not_listed' && !p.title && p.files[0]?.dir && root ? `/settings?tab=library#folders-${root.kind}` : undefined
  const detail = p.detail && !/^\d+$/.test(p.detail) ? (/^S\d/.test(p.detail) ? p.detail : t(`problems.detail.${key(p.detail)}`)) : ''
  return (
    <li className="flex flex-col gap-2 py-3 text-sm">
      <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
        {props.fixable && !fixing && !queued && (
          <input type="checkbox" className="mt-1" checked={props.picked} onChange={(e) => props.onPick(e.target.checked)} aria-label={t('problems.pick', { name })} />
        )}
        <div className="min-w-0 flex-1 basis-60">
          <p className="font-medium">
            {link ? (
              <Link to={link} className="hover:underline">
                {name}
              </Link>
            ) : (
              <span className="break-all">{name}</span>
            )}
            {detail && <span className="ms-2 text-muted">{detail}</span>}
            {p.title?.locked && (
              <span className="ms-2">
                <Badge>{t('problems.blocked.locked')}</Badge>
              </span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {scan && !fixing && !queued && (
            <Link to={scan} className="inline-flex items-center rounded-md border border-line bg-card px-3 py-1.5 text-xs font-medium hover:border-ink/40">
              {t('problems.addWithScan')}
            </Link>
          )}
          {fixing ? (
            <span className="text-xs font-medium text-marquee">
              {t('problems.fixing')}
              {fixing.progress > 0 && ` ${Math.round(fixing.progress * 100)}%`}
            </span>
          ) : queued ? (
            <span className="text-xs text-muted">{t('problems.queued')}</span>
          ) : p.fix ? (
            <Button className="px-3 py-1.5 text-xs" busy={props.busy} disabled={!props.fixable} onClick={props.onFix} title={p.fix.blocked ? t(`problems.blocked.${key(p.fix.blocked)}`) : undefined}>
              {t(`problems.act.${key(action ?? '')}`)}
            </Button>
          ) : null}
        </div>
      </div>
      {p.copies && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <p className="flex items-start gap-1.5 text-gold">
            <CircleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden /> {t('problems.copiesNote')}
          </p>
          {other && !fixing && !queued && props.fixable && (
            <button type="button" className="font-medium text-marquee hover:underline" onClick={() => props.onAction(other)}>
              {t(`problems.instead.${key(other)}`)}
            </button>
          )}
        </div>
      )}
      <Change {...props} />
      {p.fix?.blocked && p.fix.blocked !== 'locked' && <p className="text-xs text-gold">{t(`problems.blocked.${key(p.fix.blocked)}`)}</p>}
      {failed && (
        <p className="text-xs text-bad" role="alert">
          {t('problems.failed', { error: failed.error })}
        </p>
      )}
    </li>
  )
}

// Change shows the files and what fixing does to them, before anything is done.
function Change({ p, keep, onKeep, fixable, action }: { p: LibProblem; keep: string; onKeep: (path: string) => void; fixable: boolean; action?: ProblemAction }) {
  const { t } = useTranslation()
  const fix = p.fix
  const file = (f: LibProblem['files'][number], extra?: ReactNode) => (
    <>
      <code className="break-all">{rel(p.root, f.path)}</code>
      <span className="text-muted">
        {' '}
        {f.size > 0 && bytes(f.size)}
        {f.dir && (f.count ?? 0) > 1 && ` · ${t('problems.filesIn', { count: f.count })}`}
        {f.episode && ` · ${f.episode}`}
        {f.recorded && ` · ${t('problems.recorded')}`}
      </span>
      {extra}
      {f.name && <span className="block text-xs text-gold">{t('problems.garbledName', { name: f.name })}</span>}
      {(f.media || f.edition) && <MediaLine media={f.media} edition={f.edition} />}
    </>
  )
  if (action === 'keep_one') {
    return (
      <fieldset className="flex flex-col gap-1 ps-1">
        <legend className="sr-only">{t('problems.keepWhich')}</legend>
        {p.files.map((f) => (
          <label key={f.path} className="flex items-start gap-2">
            <input type="radio" className="mt-1" name={`keep-${p.id}`} checked={keep === f.path} disabled={!fixable} onChange={() => onKeep(f.path)} />
            <span className="min-w-0">
              {file(f, <span className={keep === f.path ? 'text-ok' : 'text-muted'}> · {keep === f.path ? t('problems.keeps') : t('problems.setsAside')}</span>)}
            </span>
          </label>
        ))}
      </fieldset>
    )
  }
  const moves = fix?.moves ?? []
  return (
    <ul className="flex flex-col gap-1 ps-1">
      {moves.length > 0
        ? moves.map((m) => (
            <li key={m.from} className="flex flex-wrap items-center gap-x-2">
              <code className="break-all text-muted line-through decoration-muted/50">{rel(p.root, m.from)}</code>
              <ArrowRight className="size-3.5 shrink-0 rtl:rotate-180" aria-hidden />
              <code className="break-all">{rel(p.root, m.to)}</code>
              {p.files[0]?.name && m.from === p.files[0].path && <span className="w-full text-xs text-gold">{t('problems.garbledName', { name: p.files[0].name })}</span>}
            </li>
          ))
        : p.files.map((f) => <li key={f.path}>{file(f)}</li>)}
      {(action === 'convert' || action === 'join') && fix?.output && (
        <li className="flex flex-wrap items-center gap-x-2">
          <ArrowRight className="size-3.5 shrink-0 rtl:rotate-180" aria-hidden />
          <code className="break-all">{rel(p.root, fix.output)}</code>
          <span className="text-muted">{t('problems.remuxNote')}</span>
        </li>
      )}
    </ul>
  )
}

// MediaLine says what's in a video, to tell copies and parts apart: how
// long it runs, its picture, its sound and its cut.
function MediaLine({ media, edition }: { media?: ProblemMedia; edition?: string }) {
  const { t } = useTranslation()
  const bits: string[] = []
  if (media) {
    if (media.duration > 0) bits.push(runtime(media.duration))
    const pic = [resolution(media.width, media.height), media.video, media.hdr].filter(Boolean).join(' ')
    if (pic) bits.push(pic)
    if (media.audio?.length) bits.push(media.audio.join(', '))
    if (media.subtitles) bits.push(t('problems.subtitles', { count: media.subtitles }))
  }
  if (edition) bits.push(edition)
  else if (media?.title) bits.push(`“${media.title}”`)
  return <span className="block text-xs text-muted">{bits.join(' · ')}</span>
}

function runtime(sec: number) {
  const s = Math.round(sec)
  return `${Math.floor(s / 3600)}:${String(Math.floor(s / 60) % 60).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
}

function resolution(w?: number, h?: number) {
  if (!w || !h) return ''
  if (w >= 3800 || h >= 2100) return '4K'
  if (w >= 1900 || h >= 1060) return '1080p'
  if (w >= 1260 || h >= 700) return '720p'
  return `${h}p`
}

// Why fixes set originals aside, in the order the groups are shown: the
// ones most likely to hold something wanted first.
const reasons = ['notListed', 'secondFolder', 'unknown', 'duplicate', 'parts', 'discImage', 'leftover', 'other'] as const
type Reason = (typeof reasons)[number]

function reasonOf(f: ProblemFix): Reason {
  if (f.detail === 'second_folder') return 'secondFolder'
  if (!f.files.some((x) => x.role === 'aside')) return 'other'
  switch (f.kind) {
    case 'not_listed':
      return 'notListed'
    case 'unknown':
    case 'duplicate':
    case 'parts':
    case 'leftover':
      return f.kind
    case 'disc_image':
      return 'discImage'
  }
  return 'other'
}

const asideOf = (f: ProblemFix) => f.files.filter((x) => x.role === 'aside')
const sizeOf = (list: ProblemFix[]) => list.reduce((n, f) => n + asideOf(f).reduce((m, x) => m + x.size, 0), 0)

// Waiting lists fixes whose originals are set aside in the recycle folder,
// grouped by why, for the admin to put back or delete for good, one by
// one or a group at a time.
function Waiting({ state }: { state: ProblemsState }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const waiting = state.fixes.filter((f) => f.state === 'waiting')
  const deciding = state.status.deciding
  const [confirm, setConfirm] = useState<ProblemFix[] | null>(null)
  const set = (s: ProblemsState) => qc.setQueryData(['problems'], s)
  const putBack = useMutation({ mutationFn: (id: number) => post<ProblemsState>(`/problems/fixes/${id}/putback`), onSuccess: set })
  const done = useMutation({ mutationFn: (id: number) => post<ProblemsState>(`/problems/fixes/${id}/delete`), onSuccess: set })
  const decide = useMutation({
    mutationFn: (v: { fixes: ProblemFix[]; action: 'put_back' | 'delete' }) =>
      post<ProblemsState>('/problems/fixes/decide', { ids: v.fixes.map((f) => f.id), action: v.action, expectSize: sizeOf(v.fixes) }),
    onSuccess: (s) => {
      set(s)
      setConfirm(null)
    },
  })
  const groups = useMemo(() => reasons.map((r) => ({ reason: r, list: waiting.filter((f) => reasonOf(f) === r) })).filter((g) => g.list.length > 0), [waiting])
  if (waiting.length === 0 && !deciding && state.status.undecided.length === 0) return null
  const del = (fixes: ProblemFix[]) => {
    // Nothing set aside: there's nothing to delete, only to agree to.
    if (fixes.every((f) => asideOf(f).length === 0)) {
      if (fixes.length === 1) done.mutate(fixes[0].id)
      else decide.mutate({ fixes, action: 'delete' })
    } else setConfirm(fixes)
  }
  const confirmAside = confirm?.flatMap((f) => asideOf(f).map((x) => ({ ...x, root: f.root }))) ?? []
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">
        {t('problems.waiting.title')} <span className="text-muted">({waiting.length})</span>
      </h2>
      <p className="mb-3 text-sm text-muted">
        <Trans i18nKey="problems.waiting.sub" components={{ code: <code />, b: <strong className="text-ink" /> }} />
      </p>
      {deciding && (
        <p className="mb-3 flex items-center gap-2 text-sm font-medium text-marquee" role="status">
          <RefreshCw className="size-4 animate-spin" aria-hidden />
          {t(`problems.waiting.deciding.${deciding.action === 'delete' ? 'delete' : 'putBack'}`, { done: deciding.done, total: deciding.total, size: bytes(deciding.freed) })}
        </p>
      )}
      {state.status.undecided.map((u) => (
        <p key={u.id} className="mb-1 text-xs text-bad" role="alert">
          {t('problems.waiting.undecided', { title: u.title, error: u.error })}
        </p>
      ))}
      <div className="flex flex-col gap-5">
        {groups.map((g) => (
          <Group
            key={g.reason}
            reason={g.reason}
            fixes={g.list}
            locked={!!deciding || decide.isPending}
            onPutBackAll={() => decide.mutate({ fixes: g.list, action: 'put_back' })}
            onDeleteAll={() => del(g.list)}
            putBack={(id) => putBack.mutate(id)}
            putting={putBack.isPending ? putBack.variables : undefined}
            onDelete={(f) => del([f])}
          />
        ))}
      </div>
      <ErrorNote error={putBack.error ?? done.error ?? (confirm ? null : decide.error)} />
      {confirm && (
        <Modal title={t('problems.waiting.confirmTitle')} onClose={() => setConfirm(null)}>
          <p className="text-sm">{t('problems.waiting.confirmBody', { count: confirmAside.length, size: bytes(sizeOf(confirm)) })}</p>
          <ul className="mt-3 flex max-h-60 flex-col gap-1 overflow-auto text-xs">
            {confirmAside.map((x) => (
              <li key={x.dest}>
                <code className="break-all">{rel(x.root, x.src)}</code> <span className="text-muted">{bytes(x.size)}</span>
              </li>
            ))}
          </ul>
          <ErrorNote error={decide.error} />
          <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button variant="ghost" onClick={() => setConfirm(null)}>
              {t('problems.waiting.cancel')}
            </Button>
            <Button variant="danger" busy={decide.isPending} onClick={() => decide.mutate({ fixes: confirm, action: 'delete' })}>
              <Trash2 className="size-4" aria-hidden /> {t('problems.waiting.delete')}
            </Button>
          </div>
        </Modal>
      )}
    </Card>
  )
}

// How many of a group's fixes show before Show all.
const groupFirst = 5

function Group(props: {
  reason: Reason
  fixes: ProblemFix[]
  locked: boolean
  onPutBackAll: () => void
  onDeleteAll: () => void
  putBack: (id: number) => void
  putting?: number
  onDelete: (f: ProblemFix) => void
}) {
  const { t } = useTranslation()
  const { fixes, reason } = props
  const [all, setAll] = useState(false)
  const shown = all ? fixes : fixes.slice(0, groupFirst)
  const size = sizeOf(fixes)
  const anyAside = fixes.some((f) => asideOf(f).length > 0)
  return (
    <section className="rounded-md border border-line p-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-64">
          <h3 className="font-semibold">
            {t(`problems.waiting.reasons.${reason}.title`)}{' '}
            <span className="font-normal text-muted">
              ({fixes.length}
              {size > 0 && ` · ${bytes(size)}`})
            </span>
          </h3>
          <p className="text-sm text-muted">{t(`problems.waiting.reasons.${reason}.hint`)}</p>
        </div>
        {fixes.length > 1 && (
          <div className="flex flex-wrap gap-2">
            {anyAside && (
              <Button variant="ghost" className="px-3 py-1.5 text-xs" disabled={props.locked} onClick={props.onPutBackAll}>
                <Undo2 className="size-4" aria-hidden /> {t('problems.waiting.putBackAll', { count: fixes.length })}
              </Button>
            )}
            {anyAside ? (
              <Button variant="danger" className="px-3 py-1.5 text-xs" disabled={props.locked} onClick={props.onDeleteAll}>
                <Trash2 className="size-4" aria-hidden /> {t('problems.waiting.deleteAll', { count: fixes.length })}
              </Button>
            ) : (
              <Button className="px-3 py-1.5 text-xs" disabled={props.locked} onClick={props.onDeleteAll}>
                {t('problems.waiting.allGood', { count: fixes.length })}
              </Button>
            )}
          </div>
        )}
      </div>
      <ul className="mt-2 flex flex-col divide-y divide-line">
        {shown.map((f) => {
          const aside = asideOf(f)
          const asideSize = aside.reduce((n, x) => n + x.size, 0)
          const made = f.files.find((x) => x.role === 'made')
          const moved = f.files.filter((x) => x.role === 'moved')
          return (
            <li key={f.id} className="flex flex-wrap items-center gap-x-3 gap-y-2 py-2.5 text-sm">
              <div className="min-w-0 flex-1 basis-60">
                <p className="font-medium break-all">{f.title}</p>
                <p className="text-xs text-muted">
                  {aside.length > 0 && <code className="break-all">{rel(f.root, aside[0].src)}</code>}
                  {aside.length > 1 && ` +${aside.length - 1}`}
                  {asideSize > 0 && ` · ${bytes(asideSize)}`}
                  {made && ` · ${t('problems.waiting.made', { name: base(made.dest) })}`}
                  {moved.length > 0 && ` · ${t('problems.waiting.moved', { count: moved.length })}`}
                  {` · ${when(f.createdAt)}`}
                </p>
              </div>
              <div className="flex gap-2">
                <Button variant="ghost" className="px-3 py-1.5 text-xs" disabled={props.locked} busy={props.putting === f.id} onClick={() => props.putBack(f.id)}>
                  <Undo2 className="size-4" aria-hidden /> {t('problems.waiting.putBack')}
                </Button>
                {aside.length > 0 ? (
                  <Button variant="danger" className="px-3 py-1.5 text-xs" disabled={props.locked} onClick={() => props.onDelete(f)}>
                    <Trash2 className="size-4" aria-hidden /> {t('problems.waiting.deleteOriginals')}
                  </Button>
                ) : (
                  <Button className="px-3 py-1.5 text-xs" disabled={props.locked} onClick={() => props.onDelete(f)}>
                    {t('problems.waiting.ok')}
                  </Button>
                )}
              </div>
            </li>
          )
        })}
      </ul>
      {fixes.length > groupFirst && (
        <button type="button" className="mt-1 text-sm font-medium text-marquee hover:underline" onClick={() => setAll(!all)}>
          {all ? t('problems.waiting.showFewer') : t('problems.waiting.showAll', { count: fixes.length })}
        </button>
      )}
    </section>
  )
}

function Recent({ fixes }: { fixes: ProblemFix[] }) {
  const { t } = useTranslation()
  const recent = fixes.filter((f) => f.state !== 'waiting')
  if (recent.length === 0) return null
  return (
    <Card>
      <h2 className="mb-3 font-display text-lg font-semibold">{t('problems.recent')}</h2>
      <ul className="flex flex-col divide-y divide-line text-sm">
        {recent.map((f) => (
          <li key={f.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
            <span className="min-w-0 flex-1 basis-60 break-all">
              {t(`problems.did.${key(f.action)}`)}: {f.title}
            </span>
            <Badge tone={f.state === 'done' ? 'ok' : 'plain'}>{t(`problems.state.${f.state}`)}</Badge>
            <span className="text-xs text-muted">{when(f.decidedAt ?? f.createdAt)}</span>
          </li>
        ))}
      </ul>
    </Card>
  )
}

// ProblemsCard sits with the library settings and leads to the page.
export function ProblemsCard() {
  const { t } = useTranslation()
  const state = useProblems()
  const s = state.data
  const found = s?.report?.problems.length
  const waiting = s?.fixes.filter((f) => f.state === 'waiting').length ?? 0
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('problems.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('problems.cardSub')}</p>
      <div className="flex flex-wrap items-center gap-3">
        <Link to="/problems" className="inline-flex items-center gap-2 rounded-md border border-line bg-card px-3.5 py-2 text-sm font-medium hover:border-ink/40">
          <Wrench className="size-4" aria-hidden /> {t('problems.open')}
        </Link>
        {found !== undefined && <span className="text-sm text-muted">{t('problems.foundLast', { count: found })}</span>}
        {waiting > 0 && <Badge tone="gold">{t('problems.waitingCount', { count: waiting })}</Badge>}
      </div>
    </Card>
  )
}
