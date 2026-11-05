import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Folder, FolderUp, KeyRound, Plus, Trash2 } from 'lucide-react'
import { del, get, post, put, type Profile, type RootFolder, type Settings as SettingsData, type SystemInfo } from '../api'
import { bytes, day } from '../format'
import { useProfiles, useQualities, useRootFolders } from '../hooks'
import { Connections, DownloadSettings } from './Connections'
import { Badge, Button, Card, ErrorNote, Field, Heading, Input, Modal, Spinner, Toggle } from '../ui'

export function Settings() {
  return (
    <>
      <Heading title="Settings" />
      <div className="grid gap-6 lg:grid-cols-2">
        <div className="flex flex-col gap-6">
          <General />
          <ConnectMatinee />
          <MoveIn />
          <Account />
        </div>
        <div className="flex flex-col gap-6">
          <RootFolders kind="movie" />
          <RootFolders kind="series" />
          <Profiles />
        </div>
      </div>
      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <div className="flex flex-col gap-6">
          <DownloadSettings />
          <Connections section="news-servers" />
        </div>
        <div className="flex flex-col gap-6">
          <Connections section="indexers" />
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
  const rows = [
    ['Radarr address', origin + '/radarr'],
    ['Sonarr address', origin + '/sonarr'],
  ]
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">Connect Matinee</h2>
      <p className="mb-4 text-sm text-muted">
        In Matinee's admin settings, under its Radarr and Sonarr connections, enter these addresses and the API key. Box Office answers Matinee the way Radarr and Sonarr do. If you moved in from them,
        the Move in page can do this for you.
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

function RootFolders({ kind }: { kind: 'movie' | 'series' }) {
  const qc = useQueryClient()
  const folders = useRootFolders(kind)
  const [picking, setPicking] = useState(false)
  const remove = useMutation({ mutationFn: (f: RootFolder) => del(`/rootfolders/${f.id}`), onSuccess: () => qc.invalidateQueries({ queryKey: ['rootfolders', kind] }) })
  return (
    <Card>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold">{kind === 'movie' ? 'Movie' : 'TV'} root folders</h2>
        <Button variant="ghost" onClick={() => setPicking(true)}>
          <Plus className="size-4" aria-hidden /> Add
        </Button>
      </div>
      {folders.isPending && <Spinner />}
      {folders.data?.length === 0 && <p className="text-sm text-muted">None yet. Each title gets its own folder inside one of these.</p>}
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
      {picking && <FolderPicker kind={kind} onClose={() => setPicking(false)} />}
    </Card>
  )
}

function FolderPicker({ kind, onClose }: { kind: 'movie' | 'series'; onClose: () => void }) {
  const qc = useQueryClient()
  const [path, setPath] = useState('/')
  const [typed, setTyped] = useState('/')
  const list = useQuery({ queryKey: ['fs', path], queryFn: () => get<{ path: string; parent: string; folders: string[] }>(`/fs?path=${encodeURIComponent(path)}`) })
  const add = useMutation({
    mutationFn: () => post('/rootfolders', { kind, path: typed }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rootfolders', kind] })
      onClose()
    },
  })
  const go = (p: string) => {
    setPath(p)
    setTyped(p)
  }
  return (
    <Modal title={`Add a ${kind === 'movie' ? 'movie' : 'TV'} root folder`} onClose={onClose}>
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
