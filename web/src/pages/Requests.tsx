import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, ExternalLink, Flag, Inbox, Plus, Trash2, UserRound, X } from 'lucide-react'
import { api, del, get, patch, post, put } from '../api'
import { useProfiles, useRootFolders } from '../hooks'
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
  { id: 'requests', label: 'Requests' },
  { id: 'problems', label: 'Problems' },
  { id: 'people', label: 'People' },
  { id: 'site', label: 'Request site' },
] as const
type Tab = (typeof tabs)[number]['id']

export function Requests() {
  const [params, setParams] = useSearchParams()
  const counts = useRequestCounts()
  const tab = (tabs.find((t) => t.id === params.get('tab'))?.id ?? (counts.data && !counts.data.enabled ? 'site' : 'requests')) as Tab
  return (
    <>
      <Heading title="Requests" sub="What people asked for on the request site, and the problems they found." />
      <div className="mb-6 flex gap-1 overflow-x-auto border-b border-line" role="tablist">
        {tabs.map((t) => {
          const n = t.id === 'requests' ? counts.data?.requests : t.id === 'problems' ? counts.data?.issues : 0
          return (
            <button
              key={t.id}
              role="tab"
              aria-selected={tab === t.id}
              onClick={() => setParams({ tab: t.id }, { replace: true })}
              className={`-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium ${tab === t.id ? 'border-marquee text-ink' : 'border-transparent text-muted hover:text-ink'}`}
            >
              {t.label}
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
      The request site is turned off. Turn it on under{' '}
      <Link to="/requests?tab=site" className="font-medium text-marquee">
        Request site
      </Link>
      .
    </p>
  )
}

const titlePage = (kind: string, id?: number) => (id ? `/${kind === 'movie' ? 'movies' : 'shows'}/${id}` : undefined)
const date = (s: string) => new Date(s).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })

function RequestList() {
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
        <Select value={filter} onChange={(v) => setFilter(v as 'pending' | '')} options={[{ value: 'pending', label: 'Waiting for approval' }, { value: '', label: 'All requests' }]} />
      </div>
      <ErrorNote error={error} />
      {list.isPending && <Spinner />}
      {list.error && <ErrorNote error={list.error} />}
      {list.data?.length === 0 && (
        <Empty icon={<Inbox className="size-8" />} title={filter ? 'Nothing waiting' : 'No requests yet'}>
          {filter ? 'New requests from the request site show up here.' : 'Once people ask for titles on the request site, they show up here.'}
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
                    {r.kind === 'movie' ? 'Movie' : 'Show'}
                  </span>
                </div>
                <p className="text-sm text-muted">
                  <UserRound className="mr-1 inline size-3.5" aria-hidden />
                  {r.member} asked {date(r.createdAt)}
                </p>
                {r.note && <p className="text-sm">“{r.note}”</p>}
                {r.status !== 'pending' && (
                  <div>
                    <Badge tone={r.state === 'available' ? 'ok' : r.status === 'declined' ? 'plain' : 'gold'}>{stateText(r)}</Badge>
                    {r.reply && <span className="ml-2 text-sm text-muted">“{r.reply}”</span>}
                  </div>
                )}
              </div>
              <div className="flex w-full items-center justify-end gap-2 sm:w-auto">
                {r.status === 'pending' ? (
                  <>
                    <Button variant="ghost" onClick={() => setDeclining(r)}>
                      <X className="size-4" aria-hidden /> Decline
                    </Button>
                    <Button onClick={() => setChoosing(r)}>Choose…</Button>
                    <Button variant="primary" busy={busy === r.id} onClick={() => approve(r)}>
                      <Check className="size-4" aria-hidden /> Approve
                    </Button>
                  </>
                ) : (
                  <Button variant="ghost" onClick={() => remove(r)} aria-label={`Remove the request for ${r.title}`} title="Remove the request (the title stays)">
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
    <Modal title={`Approve ${r.title}`} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label="Quality profile">{(id) => <Select id={id} value={profile} onChange={(v) => setProfile(Number(v))} options={usable.map((p) => ({ value: p.id, label: p.name }))} />}</Field>
        <Field label="Folder" hint={folders.data?.length ? undefined : `Add a root folder for ${r.kind === 'movie' ? 'movies' : 'shows'} under Settings first.`}>
          {(id) => <Select id={id} value={folder} onChange={setFolder} options={(folders.data ?? []).map((f) => ({ value: f.path, label: f.path }))} />}
        </Field>
        <ErrorNote error={go.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" busy={go.isPending} disabled={!profile || !folder} onClick={() => go.mutate()}>
            Approve and look for it
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function DeclineSheet({ r, onClose, onDone }: { r: Request; onClose: () => void; onDone: () => void }) {
  const [reply, setReply] = useState('')
  const go = useMutation({ mutationFn: () => post(`/requests/${r.id}/decline`, { reply }), onSuccess: onDone })
  return (
    <Modal title={`Decline ${r.title}`} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label="Why" hint={`Optional. ${r.member} sees it under My requests.`}>
          {(id) => <textarea id={id} rows={3} maxLength={500} value={reply} onChange={(e) => setReply(e.target.value)} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" />}
        </Field>
        <ErrorNote error={go.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="danger" busy={go.isPending} onClick={() => go.mutate()}>
            Decline
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function ProblemList() {
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
        <Select value={filter} onChange={(v) => setFilter(v as 'open' | 'resolved')} options={[{ value: 'open', label: 'Open' }, { value: 'resolved', label: 'Fixed' }]} />
      </div>
      <ErrorNote error={reopen.error} />
      {list.isPending && <Spinner />}
      {list.error && <ErrorNote error={list.error} />}
      {list.data?.length === 0 && (
        <Empty icon={<Flag className="size-8" />} title={filter === 'open' ? 'No open problems' : 'Nothing fixed yet'}>
          When someone reports a problem with a title on the request site, it shows up here.
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
                      Season {i.season}
                      {i.episode ? `, episode ${i.episode}` : ''}
                    </span>
                  ) : null}
                </div>
                <p className="text-sm font-medium">{problemLabel(i.problem)}</p>
                {i.message && <p className="text-sm">“{i.message}”</p>}
                <p className="text-sm text-muted">
                  {i.member} reported it {date(i.createdAt)}
                </p>
                {i.reply && <p className="text-sm text-muted">Your reply: “{i.reply}”</p>}
              </div>
              <div className="flex w-full items-center justify-end gap-2 sm:w-auto">
                {page && (
                  <Link to={page} className="inline-flex items-center gap-1.5 rounded-md px-3 py-2 text-sm font-medium text-marquee hover:bg-line/60">
                    Open {i.kind === 'movie' ? 'movie' : 'show'}
                  </Link>
                )}
                {i.status === 'open' ? (
                  <Button variant="primary" onClick={() => setFixing(i)}>
                    <Check className="size-4" aria-hidden /> Mark fixed
                  </Button>
                ) : (
                  <Button variant="ghost" busy={reopen.isPending} onClick={() => reopen.mutate(i)}>
                    Open again
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
  const [reply, setReply] = useState('')
  const go = useMutation({ mutationFn: () => patch(`/issues/${i.id}`, { resolved: true, reply }), onSuccess: onDone })
  return (
    <Modal title={`Fixed: ${i.title}`} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-muted">
          To swap a bad file, open the title and use <strong>Choose a release</strong> or <strong>Search</strong>; then mark the problem fixed here.
        </p>
        <Field label="Reply" hint={`Optional. ${i.member} sees it under Problems.`}>
          {(id) => <textarea id={id} rows={3} maxLength={1000} value={reply} onChange={(e) => setReply(e.target.value)} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" />}
        </Field>
        <ErrorNote error={go.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" busy={go.isPending} onClick={() => go.mutate()}>
            Mark fixed
          </Button>
        </div>
      </div>
    </Modal>
  )
}

const approvalOptions = [
  { value: '', label: 'Follow the site setting' },
  { value: 'on', label: 'Approve on their own' },
  { value: 'off', label: 'Always wait for an admin' },
]

function People() {
  const qc = useQueryClient()
  const list = useQuery({ queryKey: ['portal-members'], queryFn: () => get<Member[]>('/portal/members') })
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<Member | null>(null)
  const refresh = () => qc.invalidateQueries({ queryKey: ['portal-members'] })
  return (
    <>
      <OffNote />
      <div className="mb-4 flex items-center justify-between gap-3">
        <p className="text-sm text-muted">People who can sign in to the request site. They can't open this admin UI.</p>
        <Button variant="primary" onClick={() => setAdding(true)}>
          <Plus className="size-4" aria-hidden /> Add someone
        </Button>
      </div>
      {list.isPending && <Spinner />}
      {list.error && <ErrorNote error={list.error} />}
      {list.data?.length === 0 && (
        <Empty icon={<UserRound className="size-8" />} title="Nobody yet">
          Add people here, let them make their own accounts, or let them sign in with Matinee, under Request site.
        </Empty>
      )}
      {!!list.data?.length && (
        <div className="overflow-x-auto rounded-xl border border-line bg-card">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-line text-xs uppercase tracking-wide text-muted">
              <tr>
                <th className="px-4 py-2 font-medium">Name</th>
                <th className="px-4 py-2 font-medium">Signs in with</th>
                <th className="px-4 py-2 font-medium">Requests</th>
                <th className="px-4 py-2 font-medium">Approval</th>
                <th className="px-4 py-2 font-medium">Last seen</th>
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
                  <td className="px-4 py-2">{m.source === 'matinee' ? 'Matinee' : 'An account here'}</td>
                  <td className="px-4 py-2 tabular-nums">
                    {m.requests}
                    {m.pending ? <span className="text-muted"> ({m.pending} waiting)</span> : null}
                  </td>
                  <td className="px-4 py-2">{approvalOptions.find((o) => o.value === m.autoApprove)?.label}</td>
                  <td className="px-4 py-2 text-muted">{m.disabled ? <Badge tone="bad">Turned off</Badge> : m.seenAt ? date(m.seenAt) : 'Never'}</td>
                  <td className="px-4 py-2 text-right">
                    <Button variant="ghost" onClick={() => setEditing(m)}>
                      Edit
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
    <Modal title="Add someone" onClose={onClose}>
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Username" hint="What they sign in with. No spaces.">
          {(id) => <Input id={id} autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus />}
        </Field>
        <Field label="Name" hint="Optional. What the request site calls them.">
          {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}
        </Field>
        <Field label="Password" hint="At least 8 characters. Pass it on to them; they can change it on the request site.">
          {(id) => <Input id={id} type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} required />}
        </Field>
        <Field label="Approval">{(id) => <Select id={id} value={approval} onChange={setApproval} options={approvalOptions} />}</Field>
        <ErrorNote error={go.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" busy={go.isPending}>
            Add
          </Button>
        </div>
      </form>
    </Modal>
  )
}

const limit = (v: string) => (v.trim() === '' ? -1 : Math.max(0, Math.floor(Number(v) || 0)))

function EditPerson({ m, onClose, onDone }: { m: Member; onClose: () => void; onDone: () => void }) {
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
        <Field label="Name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} placeholder={m.username} />}</Field>
        <Field label="Approval">{(id) => <Select id={id} value={approval} onChange={setApproval} options={approvalOptions} />}</Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Movies a week" hint="Empty follows the site; 0 is no limit.">
            {(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={movies} onChange={(e) => setMovies(e.target.value)} />}
          </Field>
          <Field label="Shows a week">{(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={shows} onChange={(e) => setShows(e.target.value)} />}</Field>
        </div>
        {m.source === 'local' ? (
          <Field label="New password" hint="Leave empty to keep theirs. Setting one signs them out.">
            {(id) => <Input id={id} type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />}
          </Field>
        ) : (
          <p className="text-sm text-muted">They sign in with their Matinee account, so their password is set in Matinee.</p>
        )}
        <Toggle checked={!disabled} onChange={(v) => setDisabled(!v)} label="Can sign in" />
        <ErrorNote error={save.error ?? remove.error} />
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-4">
          {confirmRemove ? (
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              Remove them and their {m.requests} request{m.requests === 1 ? '' : 's'}
            </Button>
          ) : (
            <Button variant="ghost" onClick={() => setConfirmRemove(true)}>
              <Trash2 className="size-4" aria-hidden /> Remove
            </Button>
          )}
          <div className="flex gap-2">
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button variant="primary" busy={save.isPending} onClick={() => save.mutate()}>
              Save
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  )
}

function SiteSettings() {
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
  const profileOptions = [{ value: 0, label: 'The first profile' }, ...(profiles.data ?? []).filter((p) => !p.locked).map((p) => ({ value: p.id, label: p.name }))]
  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
      <div className="flex flex-col gap-6">
        <Card>
          <h2 className="mb-1 font-display text-lg font-semibold">The request site</h2>
          <p className="mb-4 text-sm text-muted">A site of its own for the people who watch: they find movies and shows, ask for them, follow how it's going, and report problems.</p>
          <div className="flex flex-col gap-4">
            <Toggle checked={st.enabled} onChange={(v) => set({ enabled: v })} label="Open the request site" />
            <Field label="Address" hint="Share this with the people who use it.">
              {() => (
                <div className="flex flex-wrap items-center gap-2">
                  <code className="rounded bg-paper px-2 py-1 text-sm">{address}</code>
                  <Button variant="ghost" onClick={() => navigator.clipboard?.writeText(address)} aria-label="Copy the address">
                    <Copy className="size-4" aria-hidden />
                  </Button>
                  <a href="/request" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm font-medium text-marquee">
                    Open <ExternalLink className="size-3.5" aria-hidden />
                  </a>
                </div>
              )}
            </Field>
            <Field
              label="Its own host name"
              hint="Optional, such as requests.example.com. Point that name at Box Office (in DNS and your ingress or reverse proxy), and there the request site is the whole site. This admin UI stays at its usual address."
            >
              {(id) => <Input id={id} value={st.host} onChange={(e) => set({ host: e.target.value })} placeholder="requests.example.com" />}
            </Field>
          </div>
        </Card>
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">Look</h2>
          <div className="flex flex-col gap-4">
            <Field label="Name">{(id) => <Input id={id} maxLength={60} value={st.name} onChange={(e) => set({ name: e.target.value })} />}</Field>
            <Field label="Welcome text" hint="Shown on the sign-in page and at the top of Discover.">
              {(id) => <textarea id={id} rows={3} maxLength={1000} value={st.welcome} onChange={(e) => set({ welcome: e.target.value })} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" />}
            </Field>
            <Field label="Colour" hint="Buttons, links and highlights. Empty uses Box Office's red.">
              {(id) => (
                <div className="flex items-center gap-2">
                  <input type="color" aria-label="Pick a colour" value={st.accent || '#c8352a'} onChange={(e) => set({ accent: e.target.value })} className="h-9 w-12 rounded border border-line bg-paper" />
                  <Input id={id} className="max-w-32" value={st.accent} onChange={(e) => set({ accent: e.target.value })} placeholder="#c8352a" />
                  {st.accent && (
                    <Button variant="ghost" onClick={() => set({ accent: '' })}>
                      Reset
                    </Button>
                  )}
                </div>
              )}
            </Field>
            <Field label="Logo" hint="PNG, JPEG or WebP, up to 512 KB. Shown in place of the ticket.">
              {(id) => (
                <div className="flex flex-wrap items-center gap-3">
                  {data.data?.hasLogo && <img src={`/api/portal/logo?at=${logoAt}`} alt="The logo" className="h-10 w-auto max-w-40 rounded bg-paper object-contain p-1" />}
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
                      Remove
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
          <h2 className="mb-4 font-display text-lg font-semibold">Signing in</h2>
          <div className="flex flex-col gap-4">
            <Field
              label="Matinee's address"
              hint="To let people sign in with their Matinee account: the address you open Matinee at, such as http://matinee.local:7171. Box Office checks their password with Matinee. Leave it empty to turn this off."
            >
              {(id) => <Input id={id} value={st.matineeUrl} onChange={(e) => set({ matineeUrl: e.target.value })} placeholder="http://matinee.local:7171" />}
            </Field>
            <Toggle checked={st.localSignIn} onChange={(v) => set({ localSignIn: v, signUp: v && st.signUp })} label="Accounts made here, under People" />
            <Toggle checked={st.signUp} disabled={!st.localSignIn} onChange={(v) => set({ signUp: v })} label="Let people make their own account" />
          </div>
        </Card>
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">Requests</h2>
          <div className="flex flex-col gap-4">
            <Field label="Approval" hint="Each person can have their own setting, under People.">
              {(id) => (
                <Select
                  id={id}
                  value={st.approval}
                  onChange={(v) => set({ approval: v as PortalSettings['approval'] })}
                  options={[
                    { value: 'manual', label: 'An admin approves each request' },
                    { value: 'auto', label: 'Approve requests on their own' },
                  ]}
                />
              )}
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Movies a week" hint="Per person. 0 is no limit.">
                {(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={st.quotaMovies} onChange={(e) => set({ quotaMovies: Math.max(0, Number(e.target.value) || 0) })} />}
              </Field>
              <Field label="Shows a week">
                {(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={st.quotaShows} onChange={(e) => set({ quotaShows: Math.max(0, Number(e.target.value) || 0) })} />}
              </Field>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Movie profile">{(id) => <Select id={id} value={st.movieProfileId} onChange={(v) => set({ movieProfileId: Number(v) })} options={profileOptions} />}</Field>
              <Field label="Show profile">{(id) => <Select id={id} value={st.showProfileId} onChange={(v) => set({ showProfileId: Number(v) })} options={profileOptions} />}</Field>
            </div>
            <p className="text-xs text-muted">Approved shows watch the seasons set under Settings → New shows.</p>
          </div>
        </Card>
        <div className="flex items-center justify-end gap-3">
          {saved && <span className="text-sm text-ok">Saved</span>}
          <ErrorNote error={save.error} />
          <Button variant="primary" busy={save.isPending} onClick={() => save.mutate(st)}>
            Save
          </Button>
        </div>
      </div>
    </div>
  )
}
