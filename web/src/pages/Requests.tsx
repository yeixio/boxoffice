import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, ExternalLink, Flag, Inbox, Plus, Trash2, UserRound, X } from 'lucide-react'
import { api, del, get, patch, post, put } from '../api'
import { useProfiles, useRootFolders } from '../hooks'
import { formatLocale } from '../i18n'
import { problemLabel, stateText, type Issue, type Request } from '../portal/papi'
import { Badge, Button, Card, Empty, ErrorNote, Field, Heading, Input, Modal, Poster, Select, Spinner, Toggle } from '../ui'

// Requests is the admin side of the request site: what people asked for,
// the problems they reported, who can sign in, and how the site looks and
// works.

export type PortalSettings = {
  enabled: boolean
  name: string
  welcome: string
  accent: string
  host: string
  localSignIn: boolean
  signUp: boolean
  matineeUrl: string
  approval: 'manual' | 'auto'
  quotaMovies: number
  quotaShows: number
  movieProfileId: number
  showProfileId: number
}

export type Member = {
  id: number
  username: string
  displayName: string
  source: 'local' | 'matinee'
  autoApprove: '' | 'on' | 'off'
  quotaMovies: number
  quotaShows: number
  disabled: boolean
  createdAt: string
  seenAt?: string
  requests: number
  pending: number
}

export type RequestCounts = { requests: number; issues: number; enabled: boolean }

export const useRequestCounts = () => useQuery({ queryKey: ['requests', 'counts'], queryFn: () => get<RequestCounts>('/requests/counts'), refetchInterval: 60_000 })

const usePortalSettings = () => useQuery({ queryKey: ['portal-settings'], queryFn: () => get<{ settings: PortalSettings; hasLogo: boolean }>('/portal/settings') })

const tabs = [
  { id: 'requests', label: 'req.tabs.requests' },
  { id: 'problems', label: 'req.tabs.problems' },
  { id: 'people', label: 'req.tabs.people' },
  { id: 'site', label: 'req.tabs.site' },
] as const
type Tab = (typeof tabs)[number]['id']

export function Requests() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const counts = useRequestCounts()
  const tab = (tabs.find((x) => x.id === params.get('tab'))?.id ?? (counts.data && !counts.data.enabled ? 'site' : 'requests')) as Tab
  return (
    <>
      <Heading title={t('req.title')} sub={t('req.sub')} />
      <div className="mb-6 flex gap-1 overflow-x-auto border-b border-line" role="tablist">
        {tabs.map((tb) => {
          const n = tb.id === 'requests' ? counts.data?.requests : tb.id === 'problems' ? counts.data?.issues : 0
          return (
            <button
              key={tb.id}
              role="tab"
              aria-selected={tab === tb.id}
              onClick={() => setParams({ tab: tb.id }, { replace: true })}
              className={`-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium ${tab === tb.id ? 'border-marquee text-ink' : 'border-transparent text-muted hover:text-ink'}`}
            >
              {t(tb.label)}
              {!!n && <span className="rounded-full bg-marquee px-1.5 text-xs font-semibold leading-5 text-marquee-ink">{n}</span>}
            </button>
          )
        })}
      </div>
      {tab === 'requests' && <RequestList />}
      {tab === 'problems' && <ProblemList />}
      {tab === 'people' && <People />}
      {tab === 'site' && <SiteSettings />}
    </>
  )
}

function OffNote() {
  const counts = useRequestCounts()
  if (!counts.data || counts.data.enabled) return null
  return (
    <p className="mb-4 rounded-lg border border-gold/50 bg-gold/5 px-4 py-3 text-sm">
      <Trans i18nKey="req.off" components={{ link: <Link to="/requests?tab=site" className="font-medium text-marquee" /> }} />
    </p>
  )
}

const titlePage = (kind: string, id?: number) => (id ? `/${kind === 'movie' ? 'movies' : 'shows'}/${id}` : undefined)
const date = (s: string) => new Date(s).toLocaleDateString(formatLocale(), { day: 'numeric', month: 'short', year: 'numeric' })

function RequestList() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [filter, setFilter] = useState<'pending' | ''>('pending')
  const list = useQuery({ queryKey: ['requests', filter], queryFn: () => get<Request[]>(`/requests${filter ? `?status=${filter}` : ''}`) })
  const [choosing, setChoosing] = useState<Request | null>(null)
  const [declining, setDeclining] = useState<Request | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [busy, setBusy] = useState(0)
  const refresh = () => qc.invalidateQueries({ queryKey: ['requests'] })
  const approve = async (r: Request) => {
    setBusy(r.id)
    setError(null)
    try {
      await post(`/requests/${r.id}/approve`, {})
      refresh()
    } catch (err) {
      // Most often there's something to pick first, such as a folder.
      setError(err)
      setChoosing(r)
    } finally {
      setBusy(0)
    }
  }
  const remove = async (r: Request) => {
    setError(null)
    try {
      await del(`/requests/${r.id}`)
      refresh()
    } catch (err) {
      setError(err)
    }
  }
  return (
    <>
      <OffNote />
      <div className="mb-4 flex items-center gap-3">
        <Select value={filter} onChange={(v) => setFilter(v as 'pending' | '')} options={[{ value: 'pending', label: t('req.list.pending') }, { value: '', label: t('req.list.all') }]} />
      </div>
      <ErrorNote error={error} />
      {list.isPending && <Spinner />}
      {list.error && <ErrorNote error={list.error} />}
      {list.data?.length === 0 && (
        <Empty icon={<Inbox className="size-8" />} title={filter ? t('req.list.nothingWaiting') : t('req.list.none')}>
          {filter ? t('req.list.newShowHere') : t('req.list.onceAsked')}
        </Empty>
      )}
      <ul className="mt-2 flex flex-col gap-3">
        {list.data?.map((r) => {
          const page = titlePage(r.kind, r.titleId)
          return (
            <li key={r.id} className="flex flex-wrap gap-4 rounded-xl border border-line bg-card p-3 sm:flex-nowrap">
              <Poster src={r.poster} title={r.title} className="w-14 shrink-0" />
              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <div className="flex flex-wrap items-baseline gap-x-2">
                  {page ? (
                    <Link to={page} className="font-display font-semibold hover:text-marquee">
                      {r.title}
                    </Link>
                  ) : (
                    <span className="font-display font-semibold">{r.title}</span>
                  )}
                  <span className="text-sm text-muted">
                    {r.year ? `${r.year} · ` : ''}
                    {r.kind === 'movie' ? t('discover.movie') : t('discover.show')}
                  </span>
                </div>
                <p className="text-sm text-muted">
                  <UserRound className="me-1 inline size-3.5" aria-hidden />
                  {t('req.list.asked', { member: r.member, date: date(r.createdAt) })}
                </p>
                {r.note && <p className="text-sm">“{r.note}”</p>}
                {r.status !== 'pending' && (
                  <div>
                    <Badge tone={r.state === 'available' ? 'ok' : r.status === 'declined' ? 'plain' : 'gold'}>{stateText(r)}</Badge>
                    {r.reply && <span className="ms-2 text-sm text-muted">“{r.reply}”</span>}
                  </div>
                )}
              </div>
              <div className="flex w-full items-center justify-end gap-2 sm:w-auto">
                {r.status === 'pending' ? (
                  <>
                    <Button variant="ghost" onClick={() => setDeclining(r)}>
                      <X className="size-4" aria-hidden /> {t('req.list.decline')}
                    </Button>
                    <Button onClick={() => setChoosing(r)}>{t('req.list.choose')}</Button>
                    <Button variant="primary" busy={busy === r.id} onClick={() => approve(r)}>
                      <Check className="size-4" aria-hidden /> {t('req.list.approve')}
                    </Button>
                  </>
                ) : (
                  <Button variant="ghost" onClick={() => remove(r)} aria-label={t('req.list.removeFor', { title: r.title })} title={t('req.list.removeHint')}>
                    <Trash2 className="size-4" aria-hidden />
                  </Button>
                )}
              </div>
            </li>
          )
        })}
      </ul>
      {choosing && (
        <ApproveSheet
          r={choosing}
          onClose={() => setChoosing(null)}
          onDone={() => {
            setChoosing(null)
            setError(null)
            refresh()
          }}
        />
      )}
      {declining && (
        <DeclineSheet
          r={declining}
          onClose={() => setDeclining(null)}
          onDone={() => {
            setDeclining(null)
            refresh()
          }}
        />
      )}
    </>
  )
}

function ApproveSheet({ r, onClose, onDone }: { r: Request; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const profiles = useProfiles()
  const folders = useRootFolders(r.kind)
  const site = usePortalSettings()
  const def = r.kind === 'movie' ? site.data?.settings.movieProfileId : site.data?.settings.showProfileId
  const usable = (profiles.data ?? []).filter((p) => !p.locked)
  const [profile, setProfile] = useState(0)
  const [folder, setFolder] = useState('')
  useEffect(() => {
    if (!profile && usable.length) setProfile(def || usable[0].id)
  }, [def, usable, profile])
  useEffect(() => {
    if (!folder && folders.data?.length) setFolder(folders.data[0].path)
  }, [folders.data, folder])
  const go = useMutation({ mutationFn: () => post(`/requests/${r.id}/approve`, { profileId: profile, rootFolder: folder }), onSuccess: onDone })
  return (
    <Modal title={t('req.approve.title', { title: r.title })} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label={t('add.profile')}>{(id) => <Select id={id} value={profile} onChange={(v) => setProfile(Number(v))} options={usable.map((p) => ({ value: p.id, label: p.name }))} />}</Field>
        <Field label={t('req.approve.folder')} hint={folders.data?.length ? undefined : r.kind === 'movie' ? t('req.approve.noMovieRoot') : t('req.approve.noShowRoot')}>
          {(id) => <Select id={id} value={folder} onChange={setFolder} options={(folders.data ?? []).map((f) => ({ value: f.path, label: f.path }))} />}
        </Field>
        <ErrorNote error={go.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" busy={go.isPending} disabled={!profile || !folder} onClick={() => go.mutate()}>
            {t('req.approve.go')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function DeclineSheet({ r, onClose, onDone }: { r: Request; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const [reply, setReply] = useState('')
  const go = useMutation({ mutationFn: () => post(`/requests/${r.id}/decline`, { reply }), onSuccess: onDone })
  return (
    <Modal title={t('req.decline.title', { title: r.title })} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label={t('req.decline.why')} hint={t('req.decline.whyHint', { member: r.member })}>
          {(id) => <textarea id={id} rows={3} maxLength={500} value={reply} onChange={(e) => setReply(e.target.value)} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" />}
        </Field>
        <ErrorNote error={go.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="danger" busy={go.isPending} onClick={() => go.mutate()}>
            {t('req.list.decline')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function ProblemList() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [filter, setFilter] = useState<'open' | 'resolved'>('open')
  const list = useQuery({ queryKey: ['issues', filter], queryFn: () => get<Issue[]>(`/issues?status=${filter}`) })
  const [fixing, setFixing] = useState<Issue | null>(null)
  const reopen = useMutation({
    mutationFn: (i: Issue) => patch(`/issues/${i.id}`, { resolved: false, reply: i.reply ?? '' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['issues'] })
      qc.invalidateQueries({ queryKey: ['requests', 'counts'] })
    },
  })
  return (
    <>
      <OffNote />
      <div className="mb-4">
        <Select value={filter} onChange={(v) => setFilter(v as 'open' | 'resolved')} options={[{ value: 'open', label: t('req.problems.open') }, { value: 'resolved', label: t('req.problems.fixed') }]} />
      </div>
      <ErrorNote error={reopen.error} />
      {list.isPending && <Spinner />}
      {list.error && <ErrorNote error={list.error} />}
      {list.data?.length === 0 && (
        <Empty icon={<Flag className="size-8" />} title={filter === 'open' ? t('req.problems.noneOpen') : t('req.problems.noneFixed')}>
          {t('req.problems.empty')}
        </Empty>
      )}
      <ul className="mt-2 flex flex-col gap-3">
        {list.data?.map((i) => {
          const page = titlePage(i.kind, i.titleId)
          return (
            <li key={i.id} className="flex flex-wrap gap-4 rounded-xl border border-line bg-card p-3 sm:flex-nowrap">
              <Poster src={i.poster} title={i.title} className="w-14 shrink-0" />
              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <div className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-display font-semibold">{i.title}</span>
                  {i.season ? (
                    <span className="text-sm text-muted">
                      {i.episode ? t('req.problems.episode', { season: i.season, episode: i.episode }) : t('req.problems.season', { season: i.season })}
                    </span>
                  ) : null}
                </div>
                <p className="text-sm font-medium">{problemLabel(i.problem)}</p>
                {i.message && <p className="text-sm">“{i.message}”</p>}
                <p className="text-sm text-muted">
                  {t('req.problems.reported', { member: i.member, date: date(i.createdAt) })}
                </p>
                {i.reply && <p className="text-sm text-muted">{t('req.problems.reply', { reply: i.reply })}</p>}
              </div>
              <div className="flex w-full items-center justify-end gap-2 sm:w-auto">
                {page && (
                  <Link to={page} className="inline-flex items-center gap-1.5 rounded-md px-3 py-2 text-sm font-medium text-marquee hover:bg-line/60">
                    {i.kind === 'movie' ? t('req.problems.openMovie') : t('req.problems.openShow')}
                  </Link>
                )}
                {i.status === 'open' ? (
                  <Button variant="primary" onClick={() => setFixing(i)}>
                    <Check className="size-4" aria-hidden /> {t('req.problems.markFixed')}
                  </Button>
                ) : (
                  <Button variant="ghost" busy={reopen.isPending} onClick={() => reopen.mutate(i)}>
                    {t('req.problems.again')}
                  </Button>
                )}
              </div>
            </li>
          )
        })}
      </ul>
      {fixing && (
        <FixSheet
          i={fixing}
          onClose={() => setFixing(null)}
          onDone={() => {
            setFixing(null)
            qc.invalidateQueries({ queryKey: ['issues'] })
            qc.invalidateQueries({ queryKey: ['requests', 'counts'] })
          }}
        />
      )}
    </>
  )
}

function FixSheet({ i, onClose, onDone }: { i: Issue; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const [reply, setReply] = useState('')
  const go = useMutation({ mutationFn: () => patch(`/issues/${i.id}`, { resolved: true, reply }), onSuccess: onDone })
  return (
    <Modal title={t('req.fix.title', { title: i.title })} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-muted">
          <Trans i18nKey="req.fix.how" components={{ strong: <strong /> }} />
        </p>
        <Field label={t('req.fix.reply')} hint={t('req.fix.replyHint', { member: i.member })}>
          {(id) => <textarea id={id} rows={3} maxLength={1000} value={reply} onChange={(e) => setReply(e.target.value)} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" />}
        </Field>
        <ErrorNote error={go.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" busy={go.isPending} onClick={() => go.mutate()}>
            {t('req.problems.markFixed')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

const approvalOptions = [
  { value: '', label: 'req.approval.site' },
  { value: 'on', label: 'req.approval.on' },
  { value: 'off', label: 'req.approval.off' },
]

function useApprovalOptions() {
  const { t } = useTranslation()
  return approvalOptions.map((o) => ({ ...o, label: t(o.label) }))
}

function People() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const list = useQuery({ queryKey: ['portal-members'], queryFn: () => get<Member[]>('/portal/members') })
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<Member | null>(null)
  const refresh = () => qc.invalidateQueries({ queryKey: ['portal-members'] })
  return (
    <>
      <OffNote />
      <div className="mb-4 flex items-center justify-between gap-3">
        <p className="text-sm text-muted">{t('req.people.sub')}</p>
        <Button variant="primary" onClick={() => setAdding(true)}>
          <Plus className="size-4" aria-hidden /> {t('req.people.add')}
        </Button>
      </div>
      {list.isPending && <Spinner />}
      {list.error && <ErrorNote error={list.error} />}
      {list.data?.length === 0 && (
        <Empty icon={<UserRound className="size-8" />} title={t('req.people.nobody')}>
          {t('req.people.empty')}
        </Empty>
      )}
      {!!list.data?.length && (
        <div className="overflow-x-auto rounded-xl border border-line bg-card">
          <table className="w-full text-start text-sm">
            <thead className="border-b border-line text-xs uppercase tracking-wide text-muted">
              <tr>
                <th className="px-4 py-2 font-medium">{t('req.people.name')}</th>
                <th className="px-4 py-2 font-medium">{t('req.people.signsIn')}</th>
                <th className="px-4 py-2 font-medium">{t('req.people.requests')}</th>
                <th className="px-4 py-2 font-medium">{t('req.people.approval')}</th>
                <th className="px-4 py-2 font-medium">{t('req.people.seen')}</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {list.data.map((m) => (
                <tr key={m.id} className="border-b border-line last:border-0">
                  <td className="px-4 py-2">
                    <div className="font-medium">{m.displayName || m.username}</div>
                    <div className="text-xs text-muted">{m.username}</div>
                  </td>
                  <td className="px-4 py-2">{m.source === 'matinee' ? 'Matinee' : t('req.people.local')}</td>
                  <td className="px-4 py-2 tabular-nums">
                    {m.requests}
                    {m.pending ? <span className="text-muted"> {t('req.people.waiting', { count: m.pending })}</span> : null}
                  </td>
                  <td className="px-4 py-2">{t(approvalOptions.find((o) => o.value === m.autoApprove)?.label ?? 'req.approval.site')}</td>
                  <td className="px-4 py-2 text-muted">{m.disabled ? <Badge tone="bad">{t('req.people.turnedOff')}</Badge> : m.seenAt ? date(m.seenAt) : t('req.people.never')}</td>
                  <td className="px-4 py-2 text-end">
                    <Button variant="ghost" onClick={() => setEditing(m)}>
                      {t('req.people.edit')}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {adding && (
        <AddPerson
          onClose={() => setAdding(false)}
          onDone={() => {
            setAdding(false)
            refresh()
          }}
        />
      )}
      {editing && (
        <EditPerson
          m={editing}
          onClose={() => setEditing(null)}
          onDone={() => {
            setEditing(null)
            refresh()
            qc.invalidateQueries({ queryKey: ['requests'] })
          }}
        />
      )}
    </>
  )
}

function AddPerson({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const options = useApprovalOptions()
  const [username, setUsername] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [approval, setApproval] = useState('')
  const go = useMutation({ mutationFn: () => post('/portal/members', { username, displayName: name, password, autoApprove: approval }), onSuccess: onDone })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    go.mutate()
  }
  return (
    <Modal title={t('req.people.add')} onClose={onClose}>
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field label={t('conn.username')} hint={t('req.person.usernameHint')}>
          {(id) => <Input id={id} autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus />}
        </Field>
        <Field label={t('req.people.name')} hint={t('req.person.nameHint')}>
          {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}
        </Field>
        <Field label={t('conn.password')} hint={t('req.person.passwordHint')}>
          {(id) => <Input id={id} type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} required />}
        </Field>
        <Field label={t('req.people.approval')}>{(id) => <Select id={id} value={approval} onChange={setApproval} options={options} />}</Field>
        <ErrorNote error={go.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" type="button" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" type="submit" busy={go.isPending}>
            {t('conn.add')}
          </Button>
        </div>
      </form>
    </Modal>
  )
}

const limit = (v: string) => (v.trim() === '' ? -1 : Math.max(0, Math.floor(Number(v) || 0)))

function EditPerson({ m, onClose, onDone }: { m: Member; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const options = useApprovalOptions()
  const [name, setName] = useState(m.displayName)
  const [approval, setApproval] = useState<string>(m.autoApprove)
  const [movies, setMovies] = useState(m.quotaMovies < 0 ? '' : String(m.quotaMovies))
  const [shows, setShows] = useState(m.quotaShows < 0 ? '' : String(m.quotaShows))
  const [disabled, setDisabled] = useState(m.disabled)
  const [password, setPassword] = useState('')
  const [confirmRemove, setConfirmRemove] = useState(false)
  const save = useMutation({
    mutationFn: () =>
      patch(`/portal/members/${m.id}`, { displayName: name, autoApprove: approval, quotaMovies: limit(movies), quotaShows: limit(shows), disabled, ...(password ? { password } : {}) }),
    onSuccess: onDone,
  })
  const remove = useMutation({ mutationFn: () => del(`/portal/members/${m.id}`), onSuccess: onDone })
  return (
    <Modal title={m.displayName || m.username} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label={t('req.people.name')}>{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} placeholder={m.username} />}</Field>
        <Field label={t('req.people.approval')}>{(id) => <Select id={id} value={approval} onChange={setApproval} options={options} />}</Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t('req.person.moviesWeek')} hint={t('req.person.moviesWeekHint')}>
            {(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={movies} onChange={(e) => setMovies(e.target.value)} />}
          </Field>
          <Field label={t('req.person.showsWeek')}>{(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={shows} onChange={(e) => setShows(e.target.value)} />}</Field>
        </div>
        {m.source === 'local' ? (
          <Field label={t('req.person.newPassword')} hint={t('req.person.newPasswordHint')}>
            {(id) => <Input id={id} type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />}
          </Field>
        ) : (
          <p className="text-sm text-muted">{t('req.person.matinee')}</p>
        )}
        <Toggle checked={!disabled} onChange={(v) => setDisabled(!v)} label={t('req.person.canSignIn')} />
        <ErrorNote error={save.error ?? remove.error} />
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-4">
          {confirmRemove ? (
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              {t('req.person.removeAll', { count: m.requests })}
            </Button>
          ) : (
            <Button variant="ghost" onClick={() => setConfirmRemove(true)}>
              <Trash2 className="size-4" aria-hidden /> {t('dl.remove')}
            </Button>
          )}
          <div className="flex gap-2">
            <Button variant="ghost" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button variant="primary" busy={save.isPending} onClick={() => save.mutate()}>
              {t('common.save')}
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  )
}

function SiteSettings() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const data = usePortalSettings()
  const profiles = useProfiles()
  const [st, setSt] = useState<PortalSettings | null>(null)
  const [saved, setSaved] = useState(false)
  useEffect(() => {
    if (data.data && !st) setSt(data.data.settings)
  }, [data.data, st])
  const save = useMutation({
    mutationFn: (s: PortalSettings) => put<{ settings: PortalSettings; hasLogo: boolean }>('/portal/settings', s),
    onSuccess: (d) => {
      qc.setQueryData(['portal-settings'], d)
      setSt(d.settings)
      setSaved(true)
      qc.invalidateQueries({ queryKey: ['requests', 'counts'] })
    },
  })
  const logo = useMutation({
    mutationFn: (f: File | null) => (f ? api<{ settings: PortalSettings; hasLogo: boolean }>('PUT', '/portal/logo', f) : api<{ settings: PortalSettings; hasLogo: boolean }>('DELETE', '/portal/logo')),
    onSuccess: (d) => {
      qc.setQueryData(['portal-settings'], d)
      setLogoAt(Date.now())
    },
  })
  const [logoAt, setLogoAt] = useState(Date.now())
  if (data.isPending || !st) return <Spinner />
  if (data.error) return <ErrorNote error={data.error} />
  const set = (p: Partial<PortalSettings>) => {
    setSaved(false)
    setSt({ ...st, ...p })
  }
  const address = `${location.origin}/request`
  const profileOptions = [{ value: 0, label: t('req.site.firstProfile') }, ...(profiles.data ?? []).filter((p) => !p.locked).map((p) => ({ value: p.id, label: p.name }))]
  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
      <div className="flex flex-col gap-6">
        <Card>
          <h2 className="mb-1 font-display text-lg font-semibold">{t('req.site.title')}</h2>
          <p className="mb-4 text-sm text-muted">{t('req.site.sub')}</p>
          <div className="flex flex-col gap-4">
            <Toggle checked={st.enabled} onChange={(v) => set({ enabled: v })} label={t('req.site.open')} />
            <Field label={t('conn.address')} hint={t('req.site.addressHint')}>
              {() => (
                <div className="flex flex-wrap items-center gap-2">
                  <code className="rounded bg-paper px-2 py-1 text-sm">{address}</code>
                  <Button variant="ghost" onClick={() => navigator.clipboard?.writeText(address)} aria-label={t('req.site.copy')}>
                    <Copy className="size-4" aria-hidden />
                  </Button>
                  <a href="/request" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm font-medium text-marquee">
                    {t('req.site.openLink')} <ExternalLink className="size-3.5" aria-hidden />
                  </a>
                </div>
              )}
            </Field>
            <Field
              label={t('req.site.host')}
              hint={t('req.site.hostHint')}
            >
              {(id) => <Input id={id} value={st.host} onChange={(e) => set({ host: e.target.value })} placeholder="requests.example.com" />}
            </Field>
          </div>
        </Card>
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">{t('req.site.look')}</h2>
          <div className="flex flex-col gap-4">
            <Field label={t('req.people.name')}>{(id) => <Input id={id} maxLength={60} value={st.name} onChange={(e) => set({ name: e.target.value })} />}</Field>
            <Field label={t('req.site.welcome')} hint={t('req.site.welcomeHint')}>
              {(id) => <textarea id={id} rows={3} maxLength={1000} value={st.welcome} onChange={(e) => set({ welcome: e.target.value })} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" />}
            </Field>
            <Field label={t('req.site.colour')} hint={t('req.site.colourHint')}>
              {(id) => (
                <div className="flex items-center gap-2">
                  <input type="color" aria-label={t('req.site.pickColour')} value={st.accent || '#c8352a'} onChange={(e) => set({ accent: e.target.value })} className="h-9 w-12 rounded border border-line bg-paper" />
                  <Input id={id} className="max-w-32" value={st.accent} onChange={(e) => set({ accent: e.target.value })} placeholder="#c8352a" />
                  {st.accent && (
                    <Button variant="ghost" onClick={() => set({ accent: '' })}>
                      {t('req.site.reset')}
                    </Button>
                  )}
                </div>
              )}
            </Field>
            <Field label={t('req.site.logo')} hint={t('req.site.logoHint')}>
              {(id) => (
                <div className="flex flex-wrap items-center gap-3">
                  {data.data?.hasLogo && <img src={`/api/portal/logo?at=${logoAt}`} alt={t('req.site.logoAlt')} className="h-10 w-auto max-w-40 rounded bg-paper object-contain p-1" />}
                  <input
                    id={id}
                    type="file"
                    accept="image/png,image/jpeg,image/webp"
                    className="text-sm"
                    onChange={(e) => {
                      const f = e.target.files?.[0]
                      if (f) logo.mutate(f)
                      e.target.value = ''
                    }}
                  />
                  {data.data?.hasLogo && (
                    <Button variant="ghost" busy={logo.isPending} onClick={() => logo.mutate(null)}>
                      {t('dl.remove')}
                    </Button>
                  )}
                </div>
              )}
            </Field>
            <ErrorNote error={logo.error} />
          </div>
        </Card>
      </div>
      <div className="flex flex-col gap-6">
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">{t('req.site.signingIn')}</h2>
          <div className="flex flex-col gap-4">
            <Field
              label={t('move.matinee.address')}
              hint={t('req.site.matineeHint')}
            >
              {(id) => <Input id={id} value={st.matineeUrl} onChange={(e) => set({ matineeUrl: e.target.value })} placeholder="http://matinee.local:7171" />}
            </Field>
            <Toggle checked={st.localSignIn} onChange={(v) => set({ localSignIn: v, signUp: v && st.signUp })} label={t('req.site.local')} />
            <Toggle checked={st.signUp} disabled={!st.localSignIn} onChange={(v) => set({ signUp: v })} label={t('req.site.signUp')} />
          </div>
        </Card>
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">{t('req.title')}</h2>
          <div className="flex flex-col gap-4">
            <Field label={t('req.people.approval')} hint={t('req.site.approvalHint')}>
              {(id) => (
                <Select
                  id={id}
                  value={st.approval}
                  onChange={(v) => set({ approval: v as PortalSettings['approval'] })}
                  options={[
                    { value: 'manual', label: t('req.site.manual') },
                    { value: 'auto', label: t('req.site.auto') },
                  ]}
                />
              )}
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label={t('req.person.moviesWeek')} hint={t('req.site.moviesHint')}>
                {(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={st.quotaMovies} onChange={(e) => set({ quotaMovies: Math.max(0, Number(e.target.value) || 0) })} />}
              </Field>
              <Field label={t('req.person.showsWeek')}>
                {(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={st.quotaShows} onChange={(e) => set({ quotaShows: Math.max(0, Number(e.target.value) || 0) })} />}
              </Field>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Field label={t('req.site.movieProfile')}>{(id) => <Select id={id} value={st.movieProfileId} onChange={(v) => set({ movieProfileId: Number(v) })} options={profileOptions} />}</Field>
              <Field label={t('req.site.showProfile')}>{(id) => <Select id={id} value={st.showProfileId} onChange={(v) => set({ showProfileId: Number(v) })} options={profileOptions} />}</Field>
            </div>
            <p className="text-xs text-muted">{t('req.site.seasons')}</p>
          </div>
        </Card>
        <div className="flex items-center justify-end gap-3">
          {saved && <span className="text-sm text-ok">{t('common.savedButton')}</span>}
          <ErrorNote error={save.error} />
          <Button variant="primary" busy={save.isPending} onClick={() => save.mutate(st)}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </div>
  )
}
