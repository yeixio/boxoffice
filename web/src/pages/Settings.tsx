import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Folder, FolderUp, KeyRound, Plus, Trash2 } from 'lucide-react'
import { del, get, post, put, type MediaType, type MediaTypeKey, type Profile, type Recycle, type RootFolder, type RootKind, type Settings as SettingsData, type SystemInfo } from '../api'
import { bytes, count, day } from '../format'
import { useProfiles, useQualities, useRootFolders } from '../hooks'
import { AutoSearchSettings, Connections, DataCapSettingsCard, DownloadSettings } from './Connections'
import { Badge, Button, Card, ErrorNote, Field, Heading, Input, Modal, Select, Spinner, Toggle } from '../ui'

export function Settings() {
  return (
    <>
      <Heading title="Settings" />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="flex flex-col gap-6">
          <General />
          <MediaTypes />
          <ConnectMatinee />
          <MoveIn />
          <Account />
        </div>
        <div className="flex flex-col gap-6">
          <RootFolders kind="movie" />
          <RootFolders kind="series" />
          <MediaTypeFolders />
          <RecycleFolders />
          <Profiles />
        </div>
      </div>
      <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="flex flex-col gap-6">
          <DownloadSettings />
          <AutoSearchSettings />
          <DataCapSettingsCard />
        </div>
        <div className="flex flex-col gap-6">
          <Connections section="indexers" />
          <Connections section="news-servers" />
          <Connections section="download-clients" />
        </div>
      </div>
      <System />
    </>
  )
}

function useSettings() {
  return useQuery({ queryKey: ['settings'], queryFn: () => get<SettingsData>('/settings') })
}

function General() {
  const qc = useQueryClient()
  const s = useSettings()
  const [key, setKey] = useState('')
  const save = useMutation({
    mutationFn: () => put<SettingsData>('/settings', { tmdbKey: key }),
    onSuccess: (d) => {
      qc.setQueryData(['settings'], d)
      setKey('')
    },
  })
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">TMDB</h2>
      <p className="mb-4 text-sm text-muted">
        Box Office looks up movies and shows on TMDB. Get a free key at <a className="text-marquee" href="https://www.themoviedb.org/settings/api" target="_blank" rel="noreferrer">themoviedb.org</a>; the API key and the longer read access token both work.
      </p>
      {s.data && (
        <p className="mb-3 text-sm">
          {s.data.hasTmdbKey ? <Badge tone="ok">{s.data.tmdbKeyFromEnv ? 'Set on the server' : 'Saved'}</Badge> : <Badge tone="bad">Not set</Badge>}
        </p>
      )}
      <div className="flex gap-2">
        <Input aria-label="TMDB key" placeholder={s.data?.hasTmdbKey ? 'Enter a new key to replace it' : 'TMDB key'} value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off" />
        <Button variant="primary" busy={save.isPending} disabled={!key} onClick={() => save.mutate()}>
          Check and save
        </Button>
      </div>
      <ErrorNote error={save.error} />
    </Card>
  )
}

// MoveIn is where Move in lives once it has left the menu.
function MoveIn() {
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">Move in</h2>
      <p className="mb-4 text-sm text-muted">Bring everything over from Radarr, Sonarr or NZBGet. The old app is only read from, so it's safe to do again, such as for an app you add later.</p>
      <Link to="/import">
        <Button>Open Move in</Button>
      </Link>
    </Card>
  )
}

function ConnectMatinee() {
  const qc = useQueryClient()
  const s = useSettings()
  const [shown, setShown] = useState(false)
  const [copied, setCopied] = useState('')
  const regen = useMutation({ mutationFn: () => post<SettingsData>('/settings/apikey'), onSuccess: (d) => qc.setQueryData(['settings'], d) })
  const copy = (what: string, v: string) => {
    navigator.clipboard?.writeText(v)
    setCopied(what)
    setTimeout(() => setCopied(''), 1500)
  }
  const origin = window.location.origin
  const rows = [['Address', origin]]
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">Connect Matinee</h2>
      <p className="mb-4 text-sm text-muted">
        In Matinee, go to Settings → Add-ons → Box Office and choose Connect. Matinee looks for Box Office on its own; if it can't find it, enter this address. Then paste the API key.
        Requests, posters and notifications all go through that one connection.
      </p>
      <dl className="flex flex-col gap-3 text-sm">
        {rows.map(([label, v]) => (
          <div key={label} className="flex items-center justify-between gap-3">
            <dt className="text-muted">{label}</dt>
            <dd className="flex items-center gap-2">
              <code className="break-all">{v}</code>
              <button onClick={() => copy(label, v)} aria-label={`Copy ${label}`} className="rounded p-1 text-muted hover:bg-line">
                <Copy className="size-3.5" />
              </button>
              {copied === label && <span className="text-xs text-ok">Copied</span>}
            </dd>
          </div>
        ))}
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted">API key</dt>
          <dd className="flex items-center gap-2">
            <code className="break-all">{s.data ? (shown ? s.data.apiKey : '•'.repeat(16)) : '…'}</code>
            <button onClick={() => setShown(!shown)} className="text-xs text-marquee">
              {shown ? 'Hide' : 'Show'}
            </button>
            {s.data && (
              <button onClick={() => copy('key', s.data.apiKey)} aria-label="Copy the API key" className="rounded p-1 text-muted hover:bg-line">
                <Copy className="size-3.5" />
              </button>
            )}
            {copied === 'key' && <span className="text-xs text-ok">Copied</span>}
          </dd>
        </div>
      </dl>
      {s.data?.apiKeyFromEnv ? (
        <p className="mt-4 text-xs text-muted">The key is set on the server (BOXOFFICE_API_KEY), so it's changed there.</p>
      ) : (
        <Button className="mt-4" busy={regen.isPending} onClick={() => regen.mutate()}>
          <KeyRound className="size-4" aria-hidden /> Make a new key
        </Button>
      )}
      <ErrorNote error={regen.error} />
    </Card>
  )
}

// MediaTypes turns on the kinds of media beyond movies and TV. Each is off
// until it's turned on here.
function MediaTypes() {
  const qc = useQueryClient()
  const s = useSettings()
  const save = useMutation({
    mutationFn: (v: { key: MediaTypeKey; on: boolean }) => put<SettingsData>('/settings', { mediaTypes: { [v.key]: v.on } }),
    onSuccess: (d) => {
      qc.setQueryData(['settings'], d)
      qc.invalidateQueries({ queryKey: ['rootfolders'] })
    },
  })
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">More than movies and TV</h2>
      <p className="mb-4 text-sm text-muted">
        Turn on the other kinds of media you want Box Office to look after. Each is off until you turn it on, and turning one off later keeps its folders and settings. Box Office
        never changes files already in these libraries.
      </p>
      {s.isPending && <Spinner />}
      <ErrorNote error={s.error} />
      <div className="flex flex-col gap-4">
        {s.data?.mediaTypes.map((t) => (
          <Toggle
            key={t.key}
            checked={t.enabled}
            disabled={save.isPending}
            onChange={(on) => save.mutate({ key: t.key, on })}
            label={
              <span>
                <span className="font-medium">{t.name}</span>
                {t.replaces && <span className="text-muted"> (in place of {t.replaces})</span>}
                <span className="block text-sm text-muted">{t.summary}</span>
              </span>
            }
          />
        ))}
      </div>
      {s.data?.mediaTypes.some((t) => t.enabled) && (
        <p className="mt-4 text-sm text-muted">Pick a root folder for each one you turned on, below. Adding and searching for these arrives in the next Box Office updates.</p>
      )}
      <ErrorNote error={save.error} />
    </Card>
  )
}

// MediaTypeFolders shows a root folder card for each media type that's on.
function MediaTypeFolders() {
  const s = useSettings()
  return (
    <>
      {s.data?.mediaTypes
        .filter((t) => t.enabled)
        .map((t) => (
          <RootFolders key={t.key} kind={t.key} type={t} />
        ))}
    </>
  )
}

const rootTitle = (kind: RootKind, type?: MediaType) => (kind === 'movie' ? 'Movie' : kind === 'series' ? 'TV' : (type?.name ?? kind))

function RootFolders({ kind, type }: { kind: RootKind; type?: MediaType }) {
  const qc = useQueryClient()
  const folders = useRootFolders(kind)
  const [picking, setPicking] = useState(false)
  const remove = useMutation({
    mutationFn: (f: RootFolder) => del(`/rootfolders/${f.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rootfolders', kind] })
      qc.invalidateQueries({ queryKey: ['settings'] })
    },
  })
  return (
    <Card>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold">{rootTitle(kind, type)} root folders</h2>
        <Button variant="ghost" onClick={() => setPicking(true)}>
          <Plus className="size-4" aria-hidden /> Add
        </Button>
      </div>
      {folders.isPending && <Spinner />}
      {folders.data?.length === 0 && <p className="text-sm text-muted">None yet. Each title gets its own folder inside one of these.</p>}
      {folders.data?.length === 0 && type && type.suggested.length > 0 && <SuggestedFolders kind={kind} type={type} />}
      <ul className="flex flex-col divide-y divide-line">
        {folders.data?.map((f) => (
          <li key={f.id} className="flex items-center gap-3 py-2 text-sm">
            <Folder className="size-4 text-muted" aria-hidden />
            <code className="flex-1 break-all">{f.path}</code>
            {f.accessible ? <span className="text-xs text-muted">{bytes(f.freeSpace)} free</span> : <Badge tone="bad">can't see it</Badge>}
            <button onClick={() => remove.mutate(f)} aria-label={`Remove ${f.path}`} className="rounded p-1 text-muted hover:bg-line hover:text-bad">
              <Trash2 className="size-4" />
            </button>
          </li>
        ))}
      </ul>
      <ErrorNote error={remove.error} />
      {picking && <FolderPicker kind={kind} title={rootTitle(kind, type)} onClose={() => setPicking(false)} />}
    </Card>
  )
}

// RecycleFolders shows the copies upgrades replaced, set aside in each
// library's .recycle folder, and frees their space.
function RecycleFolders() {
  const qc = useQueryClient()
  const recycle = useQuery({ queryKey: ['recycle'], queryFn: () => get<Recycle>('/recycle') })
  const [confirm, setConfirm] = useState(false)
  const set = (r: Recycle) => qc.setQueryData(['recycle'], r)
  const keep = useMutation({ mutationFn: (keepDays: number) => put<Recycle>('/recycle', { keepDays }), onSuccess: set })
  const empty = useMutation({
    mutationFn: () => post<Recycle>('/recycle/empty'),
    onSuccess: (r) => {
      set(r)
      setConfirm(false)
    },
  })
  const folders = recycle.data?.folders ?? []
  const total = folders.reduce((n, f) => n + f.size, 0)
  const files = folders.reduce((n, f) => n + f.count, 0)
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">Recycle folders</h2>
      <p className="mb-4 text-sm text-muted">
        When a better copy replaces one, the old copy is set aside in a <code>.recycle</code> folder in its library folder, by the day it was replaced, rather than deleted. To keep one,
        move it out of there yourself before it's deleted. These copies still take space until they're deleted.
      </p>
      {recycle.isPending ? (
        <Spinner />
      ) : recycle.isError ? (
        <ErrorNote error={recycle.error} />
      ) : (
        <div className="flex flex-col gap-4">
          {folders.length === 0 ? (
            <p className="text-sm">Nothing has been set aside yet.</p>
          ) : (
            <ul className="flex flex-col divide-y divide-line text-sm">
              {folders.map((f) => (
                <li key={f.folder} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                  <code className="min-w-0 flex-1 basis-48 break-all">{f.folder}</code>
                  <span className="text-muted">
                    {count(f.count, 'file', 'files')}, {bytes(f.size)}
                    {f.oldest && `, oldest ${day(f.oldest)}`}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <Field label="Delete replaced copies" hint="Box Office checks every few hours and deletes, for good, copies set aside longer than this.">
            {(id) => (
              <Select
                id={id}
                value={recycle.data.keepDays}
                onChange={(v) => keep.mutate(Number(v))}
                options={[
                  { value: 0, label: 'Never (empty them yourself)' },
                  { value: 7, label: 'After 7 days' },
                  { value: 14, label: 'After 14 days' },
                  { value: 30, label: 'After 30 days' },
                  { value: 90, label: 'After 90 days' },
                  ...([0, 7, 14, 30, 90].includes(recycle.data.keepDays) ? [] : [{ value: recycle.data.keepDays, label: `After ${recycle.data.keepDays} days` }]),
                ]}
              />
            )}
          </Field>
          <div>
            <Button variant="danger" disabled={total === 0} onClick={() => setConfirm(true)}>
              <Trash2 className="size-4" aria-hidden /> Empty recycle folders{total > 0 && ` (${bytes(total)})`}
            </Button>
          </div>
          <ErrorNote error={keep.error} />
        </div>
      )}
      {confirm && (
        <Modal title="Empty the recycle folders?" onClose={() => setConfirm(false)}>
          <p className="text-sm">
            This deletes {count(files, 'replaced copy', 'replaced copies')} ({bytes(total)}) for good, freeing the space. They can't be got back. The copies in your library aren't touched.
          </p>
          <ErrorNote error={empty.error} />
          <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button variant="ghost" onClick={() => setConfirm(false)}>
              Keep them
            </Button>
            <Button variant="danger" busy={empty.isPending} onClick={() => empty.mutate()}>
              <Trash2 className="size-4" aria-hidden /> Delete {bytes(total)}
            </Button>
          </div>
        </Modal>
      )}
    </Card>
  )
}

// SuggestedFolders offers, with one click, the folders Box Office found
// where a media type's library is usually mounted.
function SuggestedFolders({ kind, type }: { kind: RootKind; type: MediaType }) {
  const qc = useQueryClient()
  const add = useMutation({
    mutationFn: (path: string) => post('/rootfolders', { kind, path }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rootfolders', kind] })
      qc.invalidateQueries({ queryKey: ['settings'] })
    },
  })
  return (
    <div className="mt-2 text-sm">
      <p className="mb-2">Box Office found {type.suggested.length === 1 ? 'a folder that looks like' : 'folders that look like'} your {type.name.toLowerCase()} library:</p>
      <div className="flex flex-wrap gap-2">
        {type.suggested.map((p) => (
          <Button key={p} busy={add.isPending && add.variables === p} onClick={() => add.mutate(p)}>
            <Plus className="size-4" aria-hidden /> <code>{p}</code>
          </Button>
        ))}
      </div>
      <ErrorNote error={add.error} />
    </div>
  )
}

function FolderPicker({ kind, title, onClose }: { kind: RootKind; title: string; onClose: () => void }) {
  const qc = useQueryClient()
  const [path, setPath] = useState('/')
  const [typed, setTyped] = useState('/')
  const list = useQuery({ queryKey: ['fs', path], queryFn: () => get<{ path: string; parent: string; folders: string[] }>(`/fs?path=${encodeURIComponent(path)}`) })
  const add = useMutation({
    mutationFn: () => post('/rootfolders', { kind, path: typed }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rootfolders', kind] })
      qc.invalidateQueries({ queryKey: ['settings'] })
      onClose()
    },
  })
  const go = (p: string) => {
    setPath(p)
    setTyped(p)
  }
  return (
    <Modal title={`Add a ${title === 'TV' ? title : title.toLowerCase()} root folder`} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <Input aria-label="Folder" value={typed} onChange={(e) => setTyped(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && go(typed)} />
        <ul className="max-h-72 overflow-y-auto rounded-md border border-line text-sm">
          {list.data?.parent && (
            <li>
              <button className="flex w-full items-center gap-2 px-3 py-1.5 hover:bg-line" onClick={() => go(list.data.parent)}>
                <FolderUp className="size-4 text-muted" aria-hidden /> ..
              </button>
            </li>
          )}
          {list.data?.folders.map((f) => (
            <li key={f}>
              <button className="flex w-full items-center gap-2 px-3 py-1.5 hover:bg-line" onClick={() => go((list.data.path === '/' ? '' : list.data.path) + '/' + f)}>
                <Folder className="size-4 text-muted" aria-hidden /> {f}
              </button>
            </li>
          ))}
        </ul>
        <ErrorNote error={list.error || add.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" busy={add.isPending} onClick={() => add.mutate()}>
            Add this folder
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function Profiles() {
  const profiles = useProfiles()
  const qualities = useQualities()
  const [editing, setEditing] = useState<Profile | 'new' | null>(null)
  const name = (id: number) => qualities.data?.find((q) => q.id === id)?.name ?? '?'
  return (
    <Card>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold">Quality profiles</h2>
        <Button variant="ghost" onClick={() => setEditing('new')}>
          <Plus className="size-4" aria-hidden /> New
        </Button>
      </div>
      <ul className="flex flex-col divide-y divide-line">
        {profiles.data?.map((p) => (
          <li key={p.id}>
            <button className="w-full py-2 text-left text-sm hover:text-marquee" onClick={() => setEditing(p)}>
              <span className="font-medium">{p.name}</span>
              <span className="block text-xs text-muted">
                {p.allowed.length} qualities, good enough at {name(p.cutoff)}
                {p.upgrade ? ', upgrades on' : ''}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {editing && <ProfileEditor profile={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
    </Card>
  )
}

function ProfileEditor({ profile, onClose }: { profile: Profile | null; onClose: () => void }) {
  const qc = useQueryClient()
  const qualities = useQualities()
  const [name, setName] = useState(profile?.name ?? '')
  const [allowed, setAllowed] = useState<number[]>(profile?.allowed ?? [])
  const [cutoff, setCutoff] = useState(profile?.cutoff ?? 0)
  const [upgrade, setUpgrade] = useState(profile?.upgrade ?? true)
  const done = () => {
    qc.invalidateQueries({ queryKey: ['profiles'] })
    onClose()
  }
  const save = useMutation({
    mutationFn: () => {
      const body = { name, allowed, cutoff: allowed.includes(cutoff) ? cutoff : allowed[allowed.length - 1], upgrade }
      return profile ? put(`/profiles/${profile.id}`, body) : post('/profiles', body)
    },
    onSuccess: done,
  })
  const remove = useMutation({ mutationFn: () => del(`/profiles/${profile!.id}`), onSuccess: done })
  const toggle = (id: number) => setAllowed(allowed.includes(id) ? allowed.filter((x) => x !== id) : [...allowed, id])
  return (
    <Modal title={profile ? `Edit ${profile.name}` : 'New quality profile'} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label="Name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
        <div>
          <div className="mb-2 text-sm font-medium">Qualities, worst to best</div>
          <ul className="grid grid-cols-2 gap-1.5 text-sm">
            {qualities.data?.map((q) => (
              <li key={q.id} className="flex items-center gap-2">
                <input type="checkbox" id={`q${q.id}`} checked={allowed.includes(q.id)} onChange={() => toggle(q.id)} className="accent-[var(--marquee)]" />
                <label htmlFor={`q${q.id}`} className="flex-1">
                  {q.name}
                </label>
                {allowed.includes(q.id) && (
                  <button onClick={() => setCutoff(q.id)} className={`text-xs ${cutoff === q.id ? 'font-semibold text-marquee' : 'text-muted hover:text-ink'}`}>
                    {cutoff === q.id ? 'good enough' : 'set as good enough'}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </div>
        <Toggle checked={upgrade} onChange={setUpgrade} label="Upgrade until the good-enough quality" />
        <ErrorNote error={save.error || remove.error} />
        <div className="flex justify-between gap-2">
          {profile ? (
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              Delete
            </Button>
          ) : (
            <span />
          )}
          <Button variant="primary" busy={save.isPending} disabled={!name || allowed.length === 0} onClick={() => save.mutate()}>
            Save
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function Account() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const change = useMutation({
    mutationFn: () => put('/me/password', { current, new: next }),
    onSuccess: () => window.location.assign('/'),
  })
  return (
    <Card>
      <h2 className="mb-4 font-display text-lg font-semibold">Your password</h2>
      <div className="flex flex-col gap-3">
        <Field label="Current password">{(id) => <Input id={id} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />}</Field>
        <Field label="New password" hint="At least 8 characters. Every device signs in again.">
          {(id) => <Input id={id} type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />}
        </Field>
        <ErrorNote error={change.error} />
        <Button className="self-end" busy={change.isPending} disabled={!current || next.length < 8} onClick={() => change.mutate()}>
          Change password
        </Button>
      </div>
    </Card>
  )
}

function System() {
  const s = useQuery({ queryKey: ['system'], queryFn: () => get<SystemInfo>('/system') })
  if (!s.data) return null
  return (
    <p className="mt-8 text-xs text-muted">
      Box Office {s.data.version} on {s.data.os}, {s.data.go}, {s.data.database === 'postgres' ? 'PostgreSQL' : 'SQLite'} · data in <code>{s.data.dataDir}</code> · running since {day(s.data.startedAt)}
    </p>
  )
}
