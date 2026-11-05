// Settings for downloading: the downloader itself, indexers, news servers
// and download clients. Each can be added and changed here; passwords and
// keys left blank keep the saved ones.
import { useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CircleCheck, Pencil, Plus, Trash2 } from 'lucide-react'
import { del, get, post, put, type AutoSearch, type Connections as ConnectionsData, type DataCapSettings, type DownloadClient, type IndexerCaps, type Indexer, type IndexerState, type UsenetServer } from '../api'
import { bytes, count, gb, when } from '../format'
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
      <p className="mb-4 text-sm text-muted">Box Office downloads from your news servers itself. The first two switches start off.</p>
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
                Off, finished downloads wait in <span className="break-all">{d.data.folder.path}</span>. On, Box Office moves the video into the title's folder, and only if that folder takes writes: a library mounted read-only is never touched.
              </span>
            </>
          }
        />
        <Toggle
          checked={s.deleteFailed}
          onChange={(v) => save.mutate({ deleteFailed: v })}
          disabled={save.isPending}
          label={
            <>
              Delete the files of failed downloads
              <span className="block text-xs text-muted">
                Only when they can't be used: too much is gone from the news servers, or too damaged to repair. A download that failed to unpack, or is waiting to go into the library, keeps its files so you can try again.
              </span>
            </>
          }
        />
        <NumberField
          label="Delete from quarantine after"
          hint="Days a release that didn't look safe stays in quarantine. 0 keeps it until you delete it on the Downloads page."
          value={s.quarantineDays}
          min={0}
          max={3650}
          suffix="days"
          busy={save.isPending}
          onSave={(v) => save.mutate({ quarantineDays: v })}
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
          Downloads go to <span className="break-all">{d.data.folder.path}</span>
          {d.data.folder.freeSpace > 0 && ` (${bytes(d.data.folder.freeSpace)} free)`}. Set BOXOFFICE_DOWNLOADS_DIR on the server to use another folder.
        </p>
        {d.data.folder.problem && <ErrorNote error={d.data.folder.problem} />}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}

// ---- the monthly data cap ----

export function NumberField({ label, hint, value, onSave, min = 1, max, busy, suffix }: { label: string; hint: ReactNode; value: number; onSave: (v: number) => void; min?: number; max?: number; busy: boolean; suffix?: string }) {
  const [v, setV] = useState<string | null>(null)
  const n = Number(v)
  const ok = v !== null && v.trim() !== '' && Number.isFinite(n) && n >= min && (max === undefined || n <= max) && n !== value
  return (
    <Field label={label} hint={hint}>
      {(id) => (
        <div className="flex flex-wrap items-center gap-2">
          <Input id={id} type="number" min={min} max={max} value={v ?? String(value)} onChange={(e) => setV(e.target.value)} className="max-w-32" />
          {suffix && <span className="text-sm text-muted">{suffix}</span>}
          {ok && (
            <Button
              busy={busy}
              onClick={() => {
                onSave(n)
                setV(null)
              }}
            >
              Save
            </Button>
          )}
        </div>
      )}
    </Field>
  )
}

const longDay = (iso: string) => new Date(iso).toLocaleDateString(undefined, { month: 'long', day: 'numeric' })

export function DataCapSettingsCard() {
  const qc = useQueryClient()
  const d = useDownloads()
  const save = useMutation({
    mutationFn: (s: Partial<DataCapSettings>) => {
      const c = d.data!.cap
      return put('/downloads/cap', { enabled: c.enabled, gb: c.gb, upgradesAt: c.upgradesAt, missingAt: c.missingAt, resetDay: c.resetDay, ...s })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['downloads'] }),
  })
  if (!d.data) return null
  const c = d.data.cap
  const pct = c.limit > 0 ? Math.min(100, (c.used / c.limit) * 100) : 0
  const tone = c.level === 'full' ? 'bg-bad' : c.level ? 'bg-gold' : 'bg-marquee'
  return (
    <Card>
      <h2 id="data-cap" className="mb-1 scroll-mt-24 font-display text-lg font-semibold">
        Monthly data cap
      </h2>
      <p className="mb-4 text-sm text-muted">
        If your internet plan has a monthly allowance, Box Office keeps its downloads under it: as the month fills up, upgrades wait first, then automatic searches for missing titles, then
        downloading itself. Everything carries on when the month starts again.
      </p>
      <div className="flex flex-col gap-4">
        <div>
          <div className="mb-1 flex justify-between gap-2 text-sm">
            <span>
              {gb(c.used)} downloaded since {longDay(c.since)}
            </span>
            {c.enabled && <span className="shrink-0 text-muted">of {gb(c.limit)}</span>}
          </div>
          {c.enabled && (
            <div className="relative h-2 overflow-hidden rounded-full bg-line" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)} aria-label="Data cap used">
              <div className={`h-full ${tone}`} style={{ width: `${pct}%` }} />
              <div className="absolute inset-y-0 w-0.5 bg-muted/60" style={{ left: `${c.upgradesAt}%` }} />
              <div className="absolute inset-y-0 w-0.5 bg-muted/60" style={{ left: `${c.missingAt}%` }} />
            </div>
          )}
          {c.level && (
            <p className="mt-2 text-sm">
              <Badge tone={c.level === 'full' ? 'bad' : 'gold'}>{c.level === 'full' ? 'Downloads waiting' : c.level === 'missing' ? 'Automatic searches waiting' : 'Upgrades waiting'}</Badge>{' '}
              <span className="text-xs text-muted">until {longDay(c.resets)}</span>
            </p>
          )}
          <p className="mt-1 text-xs text-muted">This counts what Box Office downloads. It can't see the rest of your home's use, so leave room for it.</p>
        </div>
        <Toggle checked={c.enabled} onChange={(v) => save.mutate({ enabled: v })} disabled={save.isPending} label="Keep under a monthly cap" />
        {c.enabled && (
          <>
            <NumberField
              label="Allowance a month"
              suffix="GB"
              value={c.gb}
              busy={save.isPending}
              onSave={(v) => save.mutate({ gb: v })}
              hint={
                <>
                  1000 GB is 1 TB. To find yours:
                  <Steps>
                    <li>Sign in to your internet provider's website or app.</li>
                    <li>Open Usage, Data usage or My plan. It shows the monthly allowance (such as 1.2 TB) and the day your month starts.</li>
                  </Steps>
                </>
              }
            />
            <NumberField
              label="Upgrades wait at"
              suffix={`% of it (${gb((c.gb * 1e9 * c.upgradesAt) / 100)})`}
              value={c.upgradesAt}
              max={100}
              busy={save.isPending}
              onSave={(v) => save.mutate({ upgradesAt: v })}
              hint="Better copies of what you have already wait from here."
            />
            <NumberField
              label="Missing titles wait at"
              suffix={`% of it (${gb((c.gb * 1e9 * c.missingAt) / 100)})`}
              value={c.missingAt}
              max={100}
              busy={save.isPending}
              onSave={(v) => save.mutate({ missingAt: v })}
              hint="From here, Box Office stops searching and grabbing by itself. Downloads you start yourself go on until the cap."
            />
            <NumberField label="Month starts on day" value={c.resetDay} max={28} busy={save.isPending} onSave={(v) => save.mutate({ resetDay: v })} hint="Your provider's bill or usage page says which day your allowance starts again." />
          </>
        )}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}

// ---- automatic searches ----

export function AutoSearchSettings() {
  const qc = useQueryClient()
  const a = useQuery({ queryKey: ['search-auto'], queryFn: () => get<AutoSearch>('/search/auto') })
  const [perDay, setPerDay] = useState<string | null>(null)
  const done = (v: AutoSearch) => qc.setQueryData(['search-auto'], v)
  const save = useMutation({ mutationFn: (s: Partial<AutoSearch['settings']>) => put<AutoSearch>('/search/auto', { ...a.data!.settings, ...s }), onSuccess: done })
  const run = useMutation({ mutationFn: () => post<AutoSearch>('/search/auto/run', {}), onSuccess: done })
  const sync = useMutation({ mutationFn: () => post<AutoSearch>('/search/rss/run', {}), onSuccess: done })
  if (!a.data) return null
  const s = a.data.settings
  const last = a.data.last
  const rss = a.data.lastRss
  return (
    <Card>
      <h2 id="automatic-searches" className="mb-1 scroll-mt-24 font-display text-lg font-semibold">
        Missing titles and upgrades
      </h2>
      <p className="mb-4 text-sm text-muted">
        Box Office finds what's wanted two ways. RSS sync reads each indexer's list of new releases every few minutes, which catches new episodes and movies as they're posted. The library patrol
        searches every hour for a few monitored titles that have no copy yet (new ones first, then ones missing longer), then for ones below the quality profile's good-enough quality. A better copy
        replaces the old one, which is set aside in the library's .recycle folder (see Recycle folders above to free that space).
      </p>
      <div className="flex flex-col gap-4">
        <p className="text-sm">
          Wanted now: {count(a.data.missing, 'missing title', 'missing titles')}, {count(a.data.upgrades, 'upgrade', 'upgrades')}.
        </p>
        {a.data.cap && <p className="text-sm text-gold">{a.data.cap}.</p>}
        <Toggle
          checked={s.rss}
          onChange={(v) => save.mutate({ rss: v })}
          disabled={save.isPending}
          label={
            <>
              RSS sync
              <span className="block text-xs text-muted">Only while Download releases is on, and for indexers with RSS on. One call to each indexer each time, or up to 5 after a long gap.</span>
            </>
          }
        />
        {s.rss && (
          <NumberField label="Every" suffix="minutes" value={s.rssMinutes} min={10} max={1440} busy={save.isPending} onSave={(v) => save.mutate({ rssMinutes: v })} hint="15 suits most indexers; 10 is the least." />
        )}
        {s.rss &&
          (a.data.rssPaused ? (
            <p className="text-xs text-muted">Not syncing: {a.data.rssPaused}.</p>
          ) : (
            <div className="flex flex-wrap items-center gap-3">
              <Button variant="plain" busy={sync.isPending} onClick={() => sync.mutate()}>
                Sync now
              </Button>
              {rss && (
                <span className="text-xs text-muted">
                  Last sync {when(rss.at)}:{' '}
                  {rss.skipped ? rss.skipped : `${count(rss.releases, 'new release', 'new releases')}, grabbed ${rss.grabbed?.length ? rss.grabbed.join(', ') : 'nothing'}`}
                  {rss.problems?.length ? ` (${rss.problems.join('; ')})` : ''}
                </span>
              )}
            </div>
          ))}
        <Toggle
          checked={s.enabled}
          onChange={(v) => save.mutate({ enabled: v })}
          disabled={save.isPending}
          label={
            <>
              Library patrol
              <span className="block text-xs text-muted">
                Only while Download releases is on. Each title waits a day before it's searched again, and longer (up to 30 days) while searches keep finding nothing. A season with 3 or more
                episodes wanted is searched as one, which can bring a season pack.
              </span>
            </>
          }
        />
        <Field
          label="Searches a day"
          hint="Each search asks every indexer once. The patrol never uses more than half of what an indexer has left for the day, so searches you start yourself always have room. Set your indexer's daily limits under Indexers."
        >
          {(id) => (
            <div className="flex gap-2">
              <Input id={id} type="number" min={1} value={perDay ?? String(s.perDay)} onChange={(e) => setPerDay(e.target.value)} className="max-w-40" />
              {perDay !== null && Number(perDay) !== s.perDay && (
                <Button busy={save.isPending} onClick={() => save.mutate({ perDay: Math.max(1, Number(perDay) || 1) }, { onSuccess: () => setPerDay(null) })}>
                  Save
                </Button>
              )}
            </div>
          )}
        </Field>
        {a.data.paused ? (
          <p className="text-xs text-muted">Not searching: {a.data.paused}.</p>
        ) : (
          <div>
            <Button variant="plain" busy={run.isPending} onClick={() => run.mutate()}>
              Search now
            </Button>
          </div>
        )}
        {last && (
          <p className="text-xs text-muted">
            Last search {when(last.finishedAt || last.createdAt)}: {last.status === 'queued' || last.status === 'running' ? 'running now' : last.message}
          </p>
        )}
        <ErrorNote error={save.error || run.error} />
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
                <IndexerBadge state={d.indexerStates?.[i.id]} />
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
            badges={
              <>
                {!s.enabled && off}
                <QuotaBadge server={s} quota={d.quotas?.[s.id]} />
              </>
            }
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
      {indexer && <IndexerForm indexer={indexer === 'new' ? null : indexer} state={indexer === 'new' ? undefined : d.indexerStates?.[indexer.id]} onClose={() => setIndexer(null)} />}
      {server && <ServerForm server={server === 'new' ? null : server} left={server === 'new' ? undefined : d.quotas?.[server.id]?.left} onClose={() => setServer(null)} />}
      {client && <ClientForm client={client === 'new' ? null : client} onClose={() => setClient(null)} />}
    </>
  )
}

function IndexerBadge({ state }: { state?: IndexerState }) {
  if (!state) return null
  if (state.resting)
    return (
      <Badge tone="bad">
        <AlertTriangle className="size-3" /> resting
      </Badge>
    )
  if (state.queryLimit) return <Badge>{`${state.today.queries} of ${state.queryLimit} today`}</Badge>
  return null
}

function QuotaBadge({ server, quota }: { server: UsenetServer; quota?: { left: number; problem?: string } }) {
  if (quota?.problem) return <Badge tone="bad">{server.expires && new Date(server.expires) < new Date() ? 'expired' : 'used up'}</Badge>
  if (quota && quota.left >= 0) return <Badge tone={quota.left * 10 < server.quota ? 'gold' : 'plain'}>{`${gb(quota.left)} left`}</Badge>
  return null
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

function IndexerForm({ indexer, state, onClose }: { indexer: Indexer | null; state?: IndexerState; onClose: () => void }) {
  const saved = useSaved(onClose)
  const qc = useQueryClient()
  const reset = useMutation({ mutationFn: () => post(`/indexers/${indexer!.id}/reset`, {}), onSuccess: () => qc.invalidateQueries({ queryKey: ['connections'] }) })
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
    dailyQueries: indexer?.dailyQueries ?? 0,
    dailyGrabs: indexer?.dailyGrabs ?? 0,
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
        {state && <IndexerStatus state={state} onReset={() => reset.mutate()} resetting={reset.isPending} />}
        <ErrorNote error={reset.error} />
        <Field
          label="Address"
          hint={
            <>
              The indexer's API address. To find it:
              <Steps>
                <li>Sign in to the indexer's website.</li>
                <li>Open its API page (often under your profile, or "API" in the menu).</li>
                <li>Copy the API address it gives, like https://api.indexer.example. If it only gives a website, use that.</li>
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
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Searches a day" hint="Your account's daily API limit; 0 if you don't know it.">
            {(id) => <Input id={id} type="number" min={0} value={f.dailyQueries} onChange={(e) => set({ dailyQueries: Math.max(0, Number(e.target.value) || 0) })} className="max-w-32" />}
          </Field>
          <Field label="Downloads a day" hint="Your account's daily NZB download (grab) limit; 0 if you don't know it.">
            {(id) => <Input id={id} type="number" min={0} value={f.dailyGrabs} onChange={(e) => set({ dailyGrabs: Math.max(0, Number(e.target.value) || 0) })} className="max-w-32" />}
          </Field>
        </div>
        <p className="-mt-2 text-xs text-muted">
          {state?.limitsReported ? (
            `The indexer reports its own limits (${state.queryLimit ?? '?'} searches, ${state.grabLimit ?? '?'} downloads a day); numbers here take their place.`
          ) : (
            <>
              To find them, open your profile or account page on the indexer's website: it lists the API calls (or API hits) and downloads (or grabs) a day your membership allows. Box Office stays
              inside them, and fills them in itself for indexers that report them.
            </>
          )}
        </p>
        <Toggle checked={f.enableSearch} onChange={(v) => set({ enableSearch: v })} label="Use it for searches" />
        <Toggle
          checked={f.enableRss}
          onChange={(v) => set({ enableRss: v })}
          label={
            <>
              Use it for RSS sync
              <span className="block text-xs text-muted">Reads its newest releases every few minutes for anything wanted.</span>
            </>
          }
        />
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

function IndexerStatus({ state, onReset, resetting }: { state: IndexerState; onReset: () => void; resetting: boolean }) {
  return (
    <div className={`rounded-md border p-3 text-sm ${state.resting ? 'border-bad/40 bg-bad/5' : 'border-line'}`}>
      {state.resting ? (
        <>
          <p className="font-medium">Resting until {when(state.restingUntil)}</p>
          <p className="mt-1 text-xs text-muted">
            After {count(state.failures, 'error', 'errors')} in a row, Box Office waits before asking it again, longer each time. Its last answer: {state.lastError}
          </p>
          <Button className="mt-2" busy={resetting} onClick={onReset}>
            Try again now
          </Button>
        </>
      ) : (
        <p>
          Today: {count(state.today.queries, 'search', 'searches')}
          {state.queryLimit ? ` of ${state.queryLimit}` : ''}, {count(state.today.grabs, 'download', 'downloads')}
          {state.grabLimit ? ` of ${state.grabLimit}` : ''}
          <span className="block text-xs text-muted">
            Limits reset at midnight UTC.{state.rssSyncedAt ? ` Last RSS sync ${when(state.rssSyncedAt)}.` : ''}
          </span>
        </p>
      )}
    </div>
  )
}

// ---- news servers ----

// dateInput is the last day an account works, for a date field: it's kept
// as the moment that day ends.
function dateInput(iso?: string) {
  if (!iso) return ''
  const d = new Date(new Date(iso).getTime() - 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function ServerForm({ server, left, onClose }: { server: UsenetServer | null; left?: number; onClose: () => void }) {
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
    quotaGb: server?.quota && left !== undefined && left >= 0 ? Math.round(left / 1e8) / 10 : 0,
    expires: dateInput(server?.expires),
  })
  const [block, setBlock] = useState(!!server?.quota || !!server?.expires)
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
        <Toggle
          checked={block}
          onChange={(v) => {
            setBlock(v)
            if (!v) set({ quotaGb: 0, expires: '' })
          }}
          label={
            <>
              It's a block account, or it expires
              <span className="block text-xs text-muted">A block is an amount of data bought once. Box Office counts what it downloads from it, and stops using it when it runs out or expires.</span>
            </>
          }
        />
        {block && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Data left" hint="In GB, as the provider's account page shows it now. 0 for no limit.">
              {(id) => <Input id={id} type="number" min={0} step="0.1" value={f.quotaGb} onChange={(e) => set({ quotaGb: Math.max(0, Number(e.target.value) || 0) })} className="max-w-32" />}
            </Field>
            <Field label="Expires on" hint="The last day it works, from the provider's account page. Blank for never.">
              {(id) => <Input id={id} type="date" value={f.expires} onChange={(e) => set({ expires: e.target.value })} className="max-w-44" />}
            </Field>
          </div>
        )}
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
