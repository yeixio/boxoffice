// Settings for downloading: the downloader itself, indexers, news servers
// and download clients. Each can be added and changed here; passwords and
// keys left blank keep the saved ones.
import { useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CircleCheck, Pencil, Plus, Trash2 } from 'lucide-react'
import { del, get, post, put, type Connections as ConnectionsData, type DownloadClient, type IndexerCaps, type Indexer, type UsenetServer } from '../api'
import { bytes, count } from '../format'
import { Badge, Button, Card, ErrorNote, Field, Input, Modal, Select, Toggle } from '../ui'
import { useDownloads } from './Downloads'

export function useConnections() {
  return useQuery({ queryKey: ['connections'], queryFn: () => get<ConnectionsData>('/connections') })
}

function Steps({ children }: { children: ReactNode }) {
  return <ol className="mt-1 list-decimal space-y-0.5 pl-4">{children}</ol>
}

// ---- the downloader ----

export function DownloadSettings() {
  const qc = useQueryClient()
  const d = useDownloads()
  const [limit, setLimit] = useState<string | null>(null)
  const save = useMutation({
    mutationFn: (s: Partial<NonNullable<typeof d.data>['settings']>) => put('/downloads/settings', { ...d.data!.settings, ...s }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['downloads'] }),
  })
  if (!d.data) return null
  const s = d.data.settings
  return (
    <Card>
      <h2 id="downloads" className="mb-1 scroll-mt-24 font-display text-lg font-semibold">
        Downloads
      </h2>
      <p className="mb-4 text-sm text-muted">Box Office downloads from your news servers itself. Both switches start off.</p>
      <div className="flex flex-col gap-4">
        <Toggle
          checked={s.enabled}
          onChange={(v) => save.mutate({ enabled: v })}
          disabled={save.isPending}
          label={
            <>
              Download releases
              <span className="block text-xs text-muted">
                {d.data.newsServers === 0 ? 'Add a news server below first.' : 'Grab what searches find, and NZBs you add on the Downloads page.'}
              </span>
            </>
          }
        />
        <Toggle
          checked={s.import}
          onChange={(v) => save.mutate({ import: v })}
          disabled={save.isPending}
          label={
            <>
              Move finished downloads into the library
              <span className="block text-xs text-muted">
                Off, finished downloads wait in {d.data.folder.path}. On, Box Office moves the video into the title's folder, and only if that folder takes writes: a library mounted read-only is never touched.
              </span>
            </>
          }
        />
        <Field label="Speed limit" hint="In KB/s. Leave it at 0 for no limit.">
          {(id) => (
            <div className="flex gap-2">
              <Input id={id} type="number" min={0} value={limit ?? String(s.speedLimit)} onChange={(e) => setLimit(e.target.value)} className="max-w-40" />
              {limit !== null && Number(limit) !== s.speedLimit && (
                <Button busy={save.isPending} onClick={() => save.mutate({ speedLimit: Math.max(0, Number(limit) || 0) }, { onSuccess: () => setLimit(null) })}>
                  Save
                </Button>
              )}
            </div>
          )}
        </Field>
        <p className="text-xs text-muted">
          Downloads go to {d.data.folder.path}
          {d.data.folder.freeSpace > 0 && ` (${bytes(d.data.folder.freeSpace)} free)`}. Set BOXOFFICE_DOWNLOADS_DIR on the server to use another folder.
        </p>
        {d.data.folder.problem && <ErrorNote error={d.data.folder.problem} />}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}

// ---- lists ----

function Row({ main, sub, badges, onEdit, onRemove }: { main: string; sub: string; badges?: ReactNode; onEdit: () => void; onRemove: () => void }) {
  return (
    <li className="flex items-center gap-2 py-2 text-sm">
      <button onClick={onEdit} className="min-w-0 flex-1 text-left">
        <div className="truncate font-medium">{main}</div>
        <div className="truncate text-xs text-muted">{sub}</div>
      </button>
      <div className="flex shrink-0 flex-wrap justify-end gap-1">{badges}</div>
      <button onClick={onEdit} aria-label={`Edit ${main}`} className="rounded p-1 text-muted hover:bg-line hover:text-ink">
        <Pencil className="size-4" />
      </button>
      <button onClick={onRemove} aria-label={`Remove ${main}`} className="rounded p-1 text-muted hover:bg-line hover:text-bad">
        <Trash2 className="size-4" />
      </button>
    </li>
  )
}

const needsKey = (
  <Badge tone="bad">
    <AlertTriangle className="size-3" /> needs its key
  </Badge>
)
const off = <Badge>off</Badge>

function Section({ id, title, sub, onAdd, children, empty }: { id: string; title: string; sub: ReactNode; onAdd: () => void; children: ReactNode; empty: boolean }) {
  return (
    <Card>
      <div className="mb-1 flex items-center justify-between gap-2">
        <h2 id={id} className="scroll-mt-24 font-display text-lg font-semibold">
          {title}
        </h2>
        <Button variant="ghost" onClick={onAdd}>
          <Plus className="size-4" aria-hidden /> Add
        </Button>
      </div>
      <p className="mb-2 text-sm text-muted">{sub}</p>
      {empty ? <p className="py-2 text-sm text-muted">None yet.</p> : <ul className="divide-y divide-line">{children}</ul>}
    </Card>
  )
}

export function Connections({ section }: { section: 'indexers' | 'news-servers' | 'download-clients' }) {
  const qc = useQueryClient()
  const c = useConnections()
  const remove = useMutation({ mutationFn: (path: string) => del(path), onSuccess: () => qc.invalidateQueries({ queryKey: ['connections'] }) })
  const [indexer, setIndexer] = useState<Indexer | 'new' | null>(null)
  const [server, setServer] = useState<UsenetServer | 'new' | null>(null)
  const [client, setClient] = useState<DownloadClient | 'new' | null>(null)
  if (!c.data) return null
  const d = c.data
  return (
    <>
      {section === 'indexers' && <Section
        id="indexers"
        title="Indexers"
        sub={<>Where Box Office searches for releases.{d.blocklist > 0 && ` ${count(d.blocklist, 'release is', 'releases are')} on the blocklist.`}</>}
        onAdd={() => setIndexer('new')}
        empty={!d.indexers.length}
      >
        {d.indexers.map((i) => (
          <Row
            key={i.id}
            main={i.name}
            sub={i.url}
            badges={
              <>
                {i.missingSecret && !i.hasApiKey ? needsKey : <Badge>{i.protocol}</Badge>}
                {!i.enableSearch && off}
              </>
            }
            onEdit={() => setIndexer(i)}
            onRemove={() => remove.mutate(`/indexers/${i.id}`)}
          />
        ))}
      </Section>}
      {section === 'news-servers' && <Section id="news-servers" title="News servers" sub="Your Usenet provider's servers, which downloads come from." onAdd={() => setServer('new')} empty={!d.usenetServers.length}>
        {d.usenetServers.map((s) => (
          <Row
            key={s.id}
            main={s.name}
            sub={`${s.host}:${s.port}${s.useTls ? ', encrypted' : ''}, ${count(s.connections, 'connection', 'connections')}${s.level > 0 ? ', backup' : ''}`}
            badges={!s.enabled && off}
            onEdit={() => setServer(s)}
            onRemove={() => remove.mutate(`/usenetservers/${s.id}`)}
          />
        ))}
      </Section>}
      {section === 'download-clients' && <Section
        id="download-clients"
        title="Download clients"
        sub="Other download apps. Box Office downloads Usenet releases itself; these are kept for torrents, which come in a later update."
        onAdd={() => setClient('new')}
        empty={!d.downloadClients.length}
      >
        {d.downloadClients.map((dc) => (
          <Row
            key={dc.id}
            main={dc.name}
            sub={`${clientKinds.find((k) => k.value === dc.kind)?.label ?? dc.kind} at ${dc.host}:${dc.port}${dc.category ? `, ${dc.category}` : ''}`}
            badges={
              <>
                {dc.missingSecret && !dc.hasPassword && needsKey}
                {!dc.enabled && off}
              </>
            }
            onEdit={() => setClient(dc)}
            onRemove={() => remove.mutate(`/downloadclients/${dc.id}`)}
          />
        ))}
      </Section>}
      <ErrorNote error={remove.error} />
      {indexer && <IndexerForm indexer={indexer === 'new' ? null : indexer} onClose={() => setIndexer(null)} />}
      {server && <ServerForm server={server === 'new' ? null : server} onClose={() => setServer(null)} />}
      {client && <ClientForm client={client === 'new' ? null : client} onClose={() => setClient(null)} />}
    </>
  )
}

function useSaved(onClose: () => void) {
  const qc = useQueryClient()
  return () => {
    qc.invalidateQueries({ queryKey: ['connections'] })
    qc.invalidateQueries({ queryKey: ['downloads'] })
    qc.invalidateQueries({ queryKey: ['health'] })
    onClose()
  }
}

function SecretField({ label, has, value, onChange, hint }: { label: string; has: boolean; value: string; onChange: (v: string) => void; hint: ReactNode }) {
  return (
    <Field label={label} hint={hint}>
      {(id) => <Input id={id} type="password" autoComplete="off" value={value} placeholder={has ? 'Saved; type a new one to change it' : ''} onChange={(e) => onChange(e.target.value)} />}
    </Field>
  )
}

// ---- indexers ----

function IndexerForm({ indexer, onClose }: { indexer: Indexer | null; onClose: () => void }) {
  const saved = useSaved(onClose)
  const [f, setF] = useState({
    name: indexer?.name ?? '',
    protocol: indexer?.protocol ?? 'usenet',
    url: indexer?.url ?? '',
    apiPath: indexer?.apiPath ?? '',
    apiKey: '',
    categories: indexer?.categories ?? [2000, 5000],
    enableRss: indexer?.enableRss ?? true,
    enableSearch: indexer?.enableSearch ?? true,
    priority: indexer?.priority ?? 25,
  })
  const set = (p: Partial<typeof f>) => setF({ ...f, ...p })
  const body = () => ({ ...f, id: indexer?.id ?? 0 })
  const [caps, setCaps] = useState<IndexerCaps | null>(null)
  const test = useMutation({
    mutationFn: () => post<IndexerCaps>('/indexers/test', body()),
    onSuccess: (c) => {
      setCaps(c)
      // Pick the movie and TV categories it has, if none are picked.
      const ids = c.categories.map((x) => x.id)
      const want = [2000, 5000].filter((x) => ids.includes(x))
      if (want.length && !f.categories.some((x) => ids.includes(x) || c.categories.some((cc) => cc.subs?.some((s) => s.id === x)))) set({ categories: want })
    },
  })
  const save = useMutation({ mutationFn: () => (indexer ? put(`/indexers/${indexer.id}`, body()) : post('/indexers', body())), onSuccess: saved })
  const toggleCat = (id: number) => set({ categories: f.categories.includes(id) ? f.categories.filter((x) => x !== id) : [...f.categories, id] })
  const shownCats = caps?.categories.filter((c) => c.id >= 2000 && c.id < 3000 || c.id >= 5000 && c.id < 6000)
  return (
    <Modal title={indexer ? `Edit ${indexer.name}` : 'Add an indexer'} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field
          label="Address"
          hint={
            <>
              The indexer's API address. To find it:
              <Steps>
                <li>Sign in to the indexer's website.</li>
                <li>Open its API page (often under your profile, or "API" in the menu).</li>
                <li>Copy the API address it gives, like https://api.nzbgeek.info. If it only gives a website, use that.</li>
              </Steps>
              For an indexer in Prowlarr, use Prowlarr's address for it, like http://prowlarr:9696/1.
            </>
          }
        >
          {(id) => <Input id={id} value={f.url} placeholder="https://api.example.com" onChange={(e) => set({ url: e.target.value })} />}
        </Field>
        <SecretField
          label="API key"
          has={!!indexer?.hasApiKey}
          value={f.apiKey}
          onChange={(v) => set({ apiKey: v })}
          hint={
            <>
              <Steps>
                <li>On the indexer's website, open your profile or API page.</li>
                <li>Copy the API key (a long string of letters and numbers) and paste it here.</li>
              </Steps>
              For an indexer in Prowlarr, it's Prowlarr's own key: Settings, General, API Key.
            </>
          }
        />
        <div className="flex flex-wrap items-center gap-3">
          <Button busy={test.isPending} disabled={!f.url} onClick={() => test.mutate()}>
            Test
          </Button>
          {test.isSuccess && (
            <span className="flex items-center gap-1 text-sm text-ok">
              <CircleCheck className="size-4" /> It answers{caps?.movieSearch ? ', with movie' : ''}
              {caps?.tvSearch ? ' and TV' : ''} search
            </span>
          )}
        </div>
        <ErrorNote error={test.error} />
        <Field label="Name" hint="Leave it blank to name it after its address.">
          {(id) => <Input id={id} value={f.name} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
        <Field label="Kind">
          {(id) => (
            <Select
              id={id}
              value={f.protocol}
              onChange={(v) => set({ protocol: v })}
              options={[
                { value: 'usenet', label: 'Usenet (Newznab)' },
                { value: 'torrent', label: 'Torrent (Torznab)' },
              ]}
            />
          )}
        </Field>
        <div>
          <div className="mb-1 text-sm font-medium">Categories</div>
          {shownCats?.length ? (
            <ul className="grid grid-cols-1 gap-1 text-sm sm:grid-cols-2">
              {shownCats.flatMap((c) => [c, ...(c.subs ?? [])]).map((c) => (
                <li key={c.id} className="flex items-center gap-2">
                  <input type="checkbox" id={`cat${c.id}`} checked={f.categories.includes(c.id)} onChange={() => toggleCat(c.id)} className="accent-[var(--marquee)]" />
                  <label htmlFor={`cat${c.id}`}>
                    {c.name} <span className="text-xs text-muted">{c.id}</span>
                  </label>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted">
              {f.categories.length ? `Searching ${f.categories.join(', ')}. ` : ''}Press Test to pick from the indexer's own list. Movies are 2000 and TV is 5000 on nearly every indexer.
            </p>
          )}
        </div>
        <Field label="API path" hint="Nearly always /api, which is used when this is blank. Change it only if the indexer's API page says so.">
          {(id) => <Input id={id} value={f.apiPath} placeholder="/api" onChange={(e) => set({ apiPath: e.target.value })} />}
        </Field>
        <Field label="Priority" hint="1 to 50. When two indexers have the same release, the lower number wins.">
          {(id) => <Input id={id} type="number" min={1} max={50} value={f.priority} onChange={(e) => set({ priority: Number(e.target.value) })} className="max-w-32" />}
        </Field>
        <Toggle checked={f.enableSearch} onChange={(v) => set({ enableSearch: v })} label="Use it for searches" />
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={!f.url || (!indexer?.hasApiKey && !f.apiKey)} onClick={() => save.mutate()}>
            Save
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// ---- news servers ----

function ServerForm({ server, onClose }: { server: UsenetServer | null; onClose: () => void }) {
  const saved = useSaved(onClose)
  const [f, setF] = useState({
    name: server?.name ?? '',
    host: server?.host ?? '',
    port: server?.port ?? 563,
    useTls: server?.useTls ?? true,
    username: server?.username ?? '',
    password: '',
    connections: server?.connections ?? 8,
    level: server?.level ?? 0,
    enabled: server?.enabled ?? true,
  })
  const [portTouched, setPortTouched] = useState(!!server)
  const set = (p: Partial<typeof f>) => setF({ ...f, ...p })
  const body = () => ({ ...f, id: server?.id ?? 0 })
  const test = useMutation({ mutationFn: () => post('/usenetservers/test', body()) })
  const save = useMutation({ mutationFn: () => (server ? put(`/usenetservers/${server.id}`, body()) : post('/usenetservers', body())), onSuccess: saved })
  return (
    <Modal title={server ? `Edit ${server.name}` : 'Add a news server'} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-muted">
          Your Usenet provider's welcome email, or the account page on its website, lists the server address, your username and password, and how many connections your plan allows.
        </p>
        <Field label="Server address" hint="Like news.newshosting.com. Use the one for your region if your provider lists several.">
          {(id) => <Input id={id} value={f.host} placeholder="news.example.com" onChange={(e) => set({ host: e.target.value })} />}
        </Field>
        <Toggle
          checked={f.useTls}
          onChange={(v) => set({ useTls: v, port: portTouched ? f.port : v ? 563 : 119 })}
          label={
            <>
              Encrypted connection (SSL/TLS)
              <span className="block text-xs text-muted">Almost every provider offers it; it stops your internet provider seeing what you download.</span>
            </>
          }
        />
        <Field label="Port" hint="563 for encrypted, 119 for plain, unless your provider says otherwise.">
          {(id) => (
            <Input
              id={id}
              type="number"
              value={f.port}
              onChange={(e) => {
                setPortTouched(true)
                set({ port: Number(e.target.value) })
              }}
              className="max-w-32"
            />
          )}
        </Field>
        <Field label="Username">{(id) => <Input id={id} autoComplete="off" value={f.username} onChange={(e) => set({ username: e.target.value })} />}</Field>
        <SecretField label="Password" has={!!server?.hasPassword} value={f.password} onChange={(v) => set({ password: v })} hint="Your Usenet account's password, from the provider's welcome email." />
        <div className="flex flex-wrap items-center gap-3">
          <Button busy={test.isPending} disabled={!f.host} onClick={() => test.mutate()}>
            Test
          </Button>
          {test.isSuccess && (
            <span className="flex items-center gap-1 text-sm text-ok">
              <CircleCheck className="size-4" /> Signed in
            </span>
          )}
        </div>
        <ErrorNote error={test.error} />
        <Field label="Connections" hint="Your plan's limit, from your provider's account page. More connections download faster; going over the limit gets connections turned down.">
          {(id) => <Input id={id} type="number" min={1} max={100} value={f.connections} onChange={(e) => set({ connections: Number(e.target.value) })} className="max-w-32" />}
        </Field>
        <Field label="Use it as" hint="A backup server is only asked for articles the main ones don't have, as with a block account.">
          {(id) => (
            <Select
              id={id}
              value={f.level > 0 ? 1 : 0}
              onChange={(v) => set({ level: Number(v) })}
              options={[
                { value: 0, label: 'A main server' },
                { value: 1, label: 'A backup server' },
              ]}
            />
          )}
        </Field>
        <Field label="Name" hint="Leave it blank to name it after its address.">
          {(id) => <Input id={id} value={f.name} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
        <Toggle checked={f.enabled} onChange={(v) => set({ enabled: v })} label="Use this server" />
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={!f.host} onClick={() => save.mutate()}>
            Save
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// ---- download clients ----

const clientKinds = [
  { value: 'deluge', label: 'Deluge', port: 8112, secret: 'Password', where: "Deluge's web interface password: the one you sign in to Deluge's web page with (Preferences, Interface)." },
  { value: 'qbittorrent', label: 'qBittorrent', port: 8080, secret: 'Password', where: "qBittorrent's Web UI password (Tools, Options, Web UI)." },
  { value: 'transmission', label: 'Transmission', port: 9091, secret: 'Password', where: "Transmission's remote access password (Preferences, Remote), if you set one." },
  { value: 'sabnzbd', label: 'SABnzbd', port: 8080, secret: 'API key', where: "In SABnzbd, open Config, General, and copy the API Key." },
  { value: 'nzbget', label: 'NZBGet', port: 6789, secret: 'Password', where: "NZBGet's control password (Settings, Security, ControlPassword)." },
]

function ClientForm({ client, onClose }: { client: DownloadClient | null; onClose: () => void }) {
  const saved = useSaved(onClose)
  const [f, setF] = useState({
    name: client?.name ?? '',
    kind: client?.kind ?? 'deluge',
    host: client?.host ?? '',
    port: client?.port ?? 8112,
    useTls: client?.useTls ?? false,
    urlBase: client?.urlBase ?? '',
    username: client?.username ?? '',
    password: '',
    category: client?.category ?? '',
    priority: client?.priority ?? 1,
    enabled: client?.enabled ?? true,
  })
  const set = (p: Partial<typeof f>) => setF({ ...f, ...p })
  const kind = clientKinds.find((k) => k.value === f.kind) ?? clientKinds[0]
  const save = useMutation({ mutationFn: () => (client ? put(`/downloadclients/${client.id}`, f) : post('/downloadclients', f)), onSuccess: saved })
  return (
    <Modal title={client ? `Edit ${client.name}` : 'Add a download client'} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label="App">
          {(id) => (
            <Select
              id={id}
              value={f.kind}
              onChange={(v) => {
                const k = clientKinds.find((x) => x.value === v)!
                set({ kind: v, port: client ? f.port : k.port })
              }}
              options={clientKinds.map((k) => ({ value: k.value, label: k.label }))}
            />
          )}
        </Field>
        <Field label="Address" hint={`The name or IP address of the computer ${kind.label} runs on, as Box Office reaches it. In Kubernetes, that's usually its service name, like ${kind.value}.`}>
          {(id) => <Input id={id} value={f.host} placeholder={kind.value} onChange={(e) => set({ host: e.target.value })} />}
        </Field>
        <Field label="Port" hint={`${kind.label} uses ${kind.port} unless you changed it.`}>
          {(id) => <Input id={id} type="number" value={f.port} onChange={(e) => set({ port: Number(e.target.value) })} className="max-w-32" />}
        </Field>
        <Toggle checked={f.useTls} onChange={(v) => set({ useTls: v })} label="Its web address starts with https" />
        <Field label="URL base" hint="Only if you reach its web page under a path, like /deluge. Usually blank.">
          {(id) => <Input id={id} value={f.urlBase} onChange={(e) => set({ urlBase: e.target.value })} />}
        </Field>
        {f.kind !== 'deluge' && f.kind !== 'sabnzbd' && (
          <Field label="Username">{(id) => <Input id={id} autoComplete="off" value={f.username} onChange={(e) => set({ username: e.target.value })} />}</Field>
        )}
        <SecretField label={kind.secret} has={!!client?.hasPassword} value={f.password} onChange={(v) => set({ password: v })} hint={kind.where} />
        <Field label="Category" hint="The label or category Box Office's downloads get in the app, so they're kept apart from others. Several can be given, separated by commas.">
          {(id) => <Input id={id} value={f.category} placeholder="movies, tv" onChange={(e) => set({ category: e.target.value })} />}
        </Field>
        <Field label="Name" hint="Leave it blank to name it after the app.">
          {(id) => <Input id={id} value={f.name} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
        <Toggle checked={f.enabled} onChange={(v) => set({ enabled: v })} label="Use it" />
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={!f.host} onClick={() => save.mutate()}>
            Save
          </Button>
        </div>
      </div>
    </Modal>
  )
}
