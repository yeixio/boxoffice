// Settings for downloading: the downloader itself, indexers, news servers
// and download clients. Each can be added and changed here; passwords and
// keys left blank keep the saved ones.
import { Trans, useTranslation } from 'react-i18next'
import { useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CircleCheck, Pencil, Plus, Trash2 } from 'lucide-react'
import { del, get, post, put, type AutoSearch, type Connections as ConnectionsData, type DataCapSettings, type DownloadClient, type IndexerCaps, type Indexer, type IndexerState, type UsenetServer } from '../api'
import { bytes, gb, when } from '../format'
import { formatList, formatLocale } from '../i18n'
import { Badge, Button, Card, ErrorNote, Field, Input, Modal, Select, Toggle } from '../ui'
import { useDownloads } from './Downloads'

export function useConnections() {
  return useQuery({ queryKey: ['connections'], queryFn: () => get<ConnectionsData>('/connections') })
}

function Steps({ children }: { children: ReactNode }) {
  return <ol className="mt-1 list-decimal space-y-0.5 ps-4">{children}</ol>
}

// ---- the downloader ----

export function DownloadSettings() {
  const qc = useQueryClient()
  const { t } = useTranslation()
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
        {t('conn.dl.title')}
      </h2>
      <p className="mb-4 text-sm text-muted">{t('conn.dl.sub')}</p>
      <div className="flex flex-col gap-4">
        <Toggle
          checked={s.enabled}
          onChange={(v) => save.mutate({ enabled: v })}
          disabled={save.isPending}
          label={
            <>
              {t('conn.dl.enable')}
              <span className="block text-xs text-muted">
                {d.data.newsServers === 0 ? t('conn.dl.addServer') : t('conn.dl.enableHint')}
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
              {t('conn.dl.import')}
              <span className="block text-xs text-muted">
                <Trans i18nKey="conn.dl.importHint" values={{ path: d.data.folder.path }} components={{ path: <span className="break-all" /> }} />
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
              {t('conn.dl.deleteFailed')}
              <span className="block text-xs text-muted">
                {t('conn.dl.deleteFailedHint')}
              </span>
            </>
          }
        />
        <NumberField
          label={t('conn.dl.quarantine')}
          hint={t('conn.dl.quarantineHint')}
          value={s.quarantineDays}
          min={0}
          max={3650}
          suffix={t('conn.days')}
          busy={save.isPending}
          onSave={(v) => save.mutate({ quarantineDays: v })}
        />
        <Field label={t('conn.dl.speed')} hint={t('conn.dl.speedHint')}>
          {(id) => (
            <div className="flex gap-2">
              <Input id={id} type="number" min={0} value={limit ?? String(s.speedLimit)} onChange={(e) => setLimit(e.target.value)} className="max-w-40" />
              {limit !== null && Number(limit) !== s.speedLimit && (
                <Button busy={save.isPending} onClick={() => save.mutate({ speedLimit: Math.max(0, Number(limit) || 0) }, { onSuccess: () => setLimit(null) })}>
                  {t('common.save')}
                </Button>
              )}
            </div>
          )}
        </Field>
        <p className="text-xs text-muted">
          <Trans
            i18nKey={d.data.folder.freeSpace > 0 ? 'conn.dl.folderFree' : 'conn.dl.folder'}
            values={{ path: d.data.folder.path, free: bytes(d.data.folder.freeSpace) }}
            components={{ path: <span className="break-all" /> }}
          />
        </p>
        {d.data.folder.problem && <ErrorNote error={d.data.folder.problem} />}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}

// ---- the monthly data cap ----

export function NumberField({ label, hint, value, onSave, min = 1, max, busy, suffix }: { label: string; hint: ReactNode; value: number; onSave: (v: number) => void; min?: number; max?: number; busy: boolean; suffix?: string }) {
  const { t } = useTranslation()
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
              {t('common.save')}
            </Button>
          )}
        </div>
      )}
    </Field>
  )
}

const longDay = (iso: string) => new Date(iso).toLocaleDateString(formatLocale(), { month: 'long', day: 'numeric' })

export function DataCapSettingsCard() {
  const qc = useQueryClient()
  const { t } = useTranslation()
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
        {t('conn.cap.title')}
      </h2>
      <p className="mb-4 text-sm text-muted">{t('conn.cap.sub')}</p>
      <div className="flex flex-col gap-4">
        <div>
          <div className="mb-1 flex justify-between gap-2 text-sm">
            <span>{t('conn.cap.used', { used: gb(c.used), since: longDay(c.since) })}</span>
            {c.enabled && <span className="shrink-0 text-muted">{t('conn.cap.of', { limit: gb(c.limit) })}</span>}
          </div>
          {c.enabled && (
            <div className="relative h-2 overflow-hidden rounded-full bg-line" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)} aria-label={t('conn.cap.meter')}>
              <div className={`h-full ${tone}`} style={{ width: `${pct}%` }} />
              <div className="absolute inset-y-0 w-0.5 bg-muted/60" style={{ insetInlineStart: `${c.upgradesAt}%` }} />
              <div className="absolute inset-y-0 w-0.5 bg-muted/60" style={{ insetInlineStart: `${c.missingAt}%` }} />
            </div>
          )}
          {c.level && (
            <p className="mt-2 text-sm">
              <Badge tone={c.level === 'full' ? 'bad' : 'gold'}>{c.level === 'full' ? t('conn.cap.full') : c.level === 'missing' ? t('conn.cap.missing') : t('conn.cap.upgrades')}</Badge>{' '}
              <span className="text-xs text-muted">{t('conn.cap.until', { day: longDay(c.resets) })}</span>
            </p>
          )}
          <p className="mt-1 text-xs text-muted">{t('conn.cap.counts')}</p>
        </div>
        <Toggle checked={c.enabled} onChange={(v) => save.mutate({ enabled: v })} disabled={save.isPending} label={t('conn.cap.enable')} />
        {c.enabled && (
          <>
            <NumberField
              label={t('conn.cap.allowance')}
              suffix={t('conn.cap.gb')}
              value={c.gb}
              busy={save.isPending}
              onSave={(v) => save.mutate({ gb: v })}
              hint={
                <>
                  {t('conn.cap.allowanceHint')}
                  <Steps>
                    <li>{t('conn.cap.step1')}</li>
                    <li>{t('conn.cap.step2')}</li>
                  </Steps>
                </>
              }
            />
            <NumberField
              label={t('conn.cap.upgradesAt')}
              suffix={t('conn.cap.pctOf', { size: gb((c.gb * 1e9 * c.upgradesAt) / 100) })}
              value={c.upgradesAt}
              max={100}
              busy={save.isPending}
              onSave={(v) => save.mutate({ upgradesAt: v })}
              hint={t('conn.cap.upgradesAtHint')}
            />
            <NumberField
              label={t('conn.cap.missingAt')}
              suffix={t('conn.cap.pctOf', { size: gb((c.gb * 1e9 * c.missingAt) / 100) })}
              value={c.missingAt}
              max={100}
              busy={save.isPending}
              onSave={(v) => save.mutate({ missingAt: v })}
              hint={t('conn.cap.missingAtHint')}
            />
            <NumberField label={t('conn.cap.resetDay')} value={c.resetDay} max={28} busy={save.isPending} onSave={(v) => save.mutate({ resetDay: v })} hint={t('conn.cap.resetDayHint')} />
          </>
        )}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}

// ---- automatic searches ----

export function AutoSearchSettings() {
  const { t } = useTranslation()
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
        {t('conn.auto.title')}
      </h2>
      <p className="mb-4 text-sm text-muted">{t('conn.auto.sub')}</p>
      <div className="flex flex-col gap-4">
        <p className="text-sm">
          {t(!s.upgrades && a.data.upgrades > 0 ? 'conn.auto.wantedUpgradesOff' : 'conn.auto.wanted', {
            missing: t('conn.auto.missing', { count: a.data.missing }),
            upgrades: t('conn.auto.upgrades', { count: a.data.upgrades }),
          })}
        </p>
        {a.data.cap && <p className="text-sm text-gold">{a.data.cap}.</p>}
        <Toggle
          checked={s.rss}
          onChange={(v) => save.mutate({ rss: v })}
          disabled={save.isPending}
          label={
            <>
              {t('conn.auto.rss')}
              <span className="block text-xs text-muted">{t('conn.auto.rssHint')}</span>
            </>
          }
        />
        {s.rss && (
          <NumberField label={t('conn.auto.every')} suffix={t('conn.auto.minutes')} value={s.rssMinutes} min={10} max={1440} busy={save.isPending} onSave={(v) => save.mutate({ rssMinutes: v })} hint={t('conn.auto.everyHint')} />
        )}
        {s.rss &&
          (a.data.rssPaused ? (
            <p className="text-xs text-muted">{t('conn.auto.notSyncing', { reason: a.data.rssPaused })}</p>
          ) : (
            <div className="flex flex-wrap items-center gap-3">
              <Button variant="plain" busy={sync.isPending} onClick={() => sync.mutate()}>
                {t('conn.auto.syncNow')}
              </Button>
              {rss && (
                <span className="text-xs text-muted">
                  {t('conn.auto.lastSync', {
                    when: when(rss.at),
                    what: rss.skipped
                      ? rss.skipped
                      : t('conn.auto.synced', { releases: t('conn.auto.newReleases', { count: rss.releases }), grabbed: rss.grabbed?.length ? formatList(rss.grabbed) : t('conn.auto.nothing') }),
                  })}
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
              {t('conn.auto.patrol')}
              <span className="block text-xs text-muted">{t('conn.auto.patrolHint')}</span>
            </>
          }
        />
        <Toggle
          checked={s.upgrades}
          onChange={(v) => save.mutate({ upgrades: v })}
          disabled={save.isPending}
          label={
            <>
              {t('conn.auto.upgradesToggle')}
              <span className="block text-xs text-muted">{t('conn.auto.upgradesHint')}</span>
            </>
          }
        />
        <NumberField
          label={t('conn.auto.perMonth')}
          suffix={t('conn.auto.thisMonth', { count: a.data.upgradesThisMonth })}
          value={s.upgradesPerMonth}
          min={0}
          busy={save.isPending}
          onSave={(v) => save.mutate({ upgradesPerMonth: v })}
          hint={t('conn.auto.perMonthHint')}
        />
        <Field
          label={t('conn.auto.perDay')}
          hint={t('conn.auto.perDayHint')}
        >
          {(id) => (
            <div className="flex gap-2">
              <Input id={id} type="number" min={1} value={perDay ?? String(s.perDay)} onChange={(e) => setPerDay(e.target.value)} className="max-w-40" />
              {perDay !== null && Number(perDay) !== s.perDay && (
                <Button busy={save.isPending} onClick={() => save.mutate({ perDay: Math.max(1, Number(perDay) || 1) }, { onSuccess: () => setPerDay(null) })}>
                  {t('common.save')}
                </Button>
              )}
            </div>
          )}
        </Field>
        {a.data.paused ? (
          <p className="text-xs text-muted">{t('conn.auto.notSearching', { reason: a.data.paused })}</p>
        ) : (
          <div>
            <Button variant="plain" busy={run.isPending} onClick={() => run.mutate()}>
              {t('conn.auto.searchNow')}
            </Button>
          </div>
        )}
        {last && (
          <p className="text-xs text-muted">
            {t('conn.auto.lastSearch', { when: when(last.finishedAt || last.createdAt), what: last.status === 'queued' || last.status === 'running' ? t('conn.auto.runningNow') : last.message })}
          </p>
        )}
        <ErrorNote error={save.error || run.error} />
      </div>
    </Card>
  )
}

// ---- lists ----

function Row({ main, sub, badges, onEdit, onRemove }: { main: string; sub: string; badges?: ReactNode; onEdit: () => void; onRemove: () => void }) {
  const { t } = useTranslation()
  return (
    <li className="flex items-center gap-2 py-2 text-sm">
      <button onClick={onEdit} className="min-w-0 flex-1 text-start">
        <div className="truncate font-medium">{main}</div>
        <div className="truncate text-xs text-muted">{sub}</div>
      </button>
      <div className="flex shrink-0 flex-wrap justify-end gap-1">{badges}</div>
      <button onClick={onEdit} aria-label={t('conn.edit', { name: main })} className="rounded p-1 text-muted hover:bg-line hover:text-ink">
        <Pencil className="size-4" />
      </button>
      <button onClick={onRemove} aria-label={t('conn.remove', { name: main })} className="rounded p-1 text-muted hover:bg-line hover:text-bad">
        <Trash2 className="size-4" />
      </button>
    </li>
  )
}

function NeedsKey() {
  const { t } = useTranslation()
  return (
    <Badge tone="bad">
      <AlertTriangle className="size-3" /> {t('conn.needsKey')}
    </Badge>
  )
}

function Off() {
  const { t } = useTranslation()
  return <Badge>{t('conn.off')}</Badge>
}

function Section({ id, title, sub, onAdd, children, empty }: { id: string; title: string; sub: ReactNode; onAdd: () => void; children: ReactNode; empty: boolean }) {
  const { t } = useTranslation()
  return (
    <Card>
      <div className="mb-1 flex items-center justify-between gap-2">
        <h2 id={id} className="scroll-mt-24 font-display text-lg font-semibold">
          {title}
        </h2>
        <Button variant="ghost" onClick={onAdd}>
          <Plus className="size-4" aria-hidden /> {t('conn.add')}
        </Button>
      </div>
      <p className="mb-2 text-sm text-muted">{sub}</p>
      {empty ? <p className="py-2 text-sm text-muted">{t('conn.none')}</p> : <ul className="divide-y divide-line">{children}</ul>}
    </Card>
  )
}

export function Connections({ section }: { section: 'indexers' | 'news-servers' | 'download-clients' }) {
  const { t } = useTranslation()
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
        title={t('conn.indexers.title')}
        sub={<>{t('conn.indexers.sub')}{d.blocklist > 0 && ` ${t('conn.indexers.blocklist', { count: d.blocklist })}`}</>}
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
                {i.missingSecret && !i.hasApiKey ? <NeedsKey /> : <Badge>{i.protocol}</Badge>}
                {!i.enableSearch && <Off />}
                <IndexerBadge state={d.indexerStates?.[i.id]} />
              </>
            }
            onEdit={() => setIndexer(i)}
            onRemove={() => remove.mutate(`/indexers/${i.id}`)}
          />
        ))}
      </Section>}
      {section === 'news-servers' && <Section id="news-servers" title={t('conn.servers.title')} sub={t('conn.servers.sub')} onAdd={() => setServer('new')} empty={!d.usenetServers.length}>
        {d.usenetServers.map((s) => (
          <Row
            key={s.id}
            main={s.name}
            sub={((row: string) => (s.level > 0 ? t('conn.servers.backup', { what: row }) : row))(
              t(s.useTls ? 'conn.servers.rowTls' : 'conn.servers.row', { host: s.host, port: s.port, connections: t('conn.servers.connections', { count: s.connections }) }),
            )}
            badges={
              <>
                {!s.enabled && <Off />}
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
        title={t('conn.clients.title')}
        sub={t('conn.clients.sub')}
        onAdd={() => setClient('new')}
        empty={!d.downloadClients.length}
      >
        {d.downloadClients.map((dc) => (
          <Row
            key={dc.id}
            main={dc.name}
            sub={t(dc.category ? 'conn.clients.rowCategory' : 'conn.clients.row', { app: clientKinds.find((k) => k.value === dc.kind)?.label ?? dc.kind, host: dc.host, port: dc.port, category: dc.category })}
            badges={
              <>
                {dc.missingSecret && !dc.hasPassword && <NeedsKey />}
                {!dc.enabled && <Off />}
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
  const { t } = useTranslation()
  if (!state) return null
  if (state.resting)
    return (
      <Badge tone="bad">
        <AlertTriangle className="size-3" /> {t('conn.resting')}
      </Badge>
    )
  if (state.queryLimit) return <Badge>{t('conn.today', { used: state.today.queries, limit: state.queryLimit })}</Badge>
  return null
}

function QuotaBadge({ server, quota }: { server: UsenetServer; quota?: { left: number; problem?: string } }) {
  const { t } = useTranslation()
  if (quota?.problem) return <Badge tone="bad">{server.expires && new Date(server.expires) < new Date() ? t('conn.expired') : t('conn.usedUp')}</Badge>
  if (quota && quota.left >= 0) return <Badge tone={quota.left * 10 < server.quota ? 'gold' : 'plain'}>{t('conn.left', { size: gb(quota.left) })}</Badge>
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
  const { t } = useTranslation()
  return (
    <Field label={label} hint={hint}>
      {(id) => <Input id={id} type="password" autoComplete="off" value={value} placeholder={has ? t('conn.secretSaved') : ''} onChange={(e) => onChange(e.target.value)} />}
    </Field>
  )
}

// ---- indexers ----

function IndexerForm({ indexer, state, onClose }: { indexer: Indexer | null; state?: IndexerState; onClose: () => void }) {
  const { t } = useTranslation()
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
    <Modal title={indexer ? t('conn.edit', { name: indexer.name }) : t('conn.indexer.add')} onClose={onClose}>
      <div className="flex flex-col gap-4">
        {state && <IndexerStatus state={state} onReset={() => reset.mutate()} resetting={reset.isPending} />}
        <ErrorNote error={reset.error} />
        <Field
          label={t('conn.address')}
          hint={
            <>
              {t('conn.indexer.addressHint')}
              <Steps>
                <li>{t('conn.indexer.address1')}</li>
                <li>{t('conn.indexer.address2')}</li>
                <li>{t('conn.indexer.address3')}</li>
              </Steps>
              {t('conn.indexer.addressProwlarr')}
            </>
          }
        >
          {(id) => <Input id={id} value={f.url} placeholder="https://api.example.com" onChange={(e) => set({ url: e.target.value })} />}
        </Field>
        <SecretField
          label={t('conn.indexer.apiKey')}
          has={!!indexer?.hasApiKey}
          value={f.apiKey}
          onChange={(v) => set({ apiKey: v })}
          hint={
            <>
              <Steps>
                <li>{t('conn.indexer.key1')}</li>
                <li>{t('conn.indexer.key2')}</li>
              </Steps>
              {t('conn.indexer.keyProwlarr')}
            </>
          }
        />
        <div className="flex flex-wrap items-center gap-3">
          <Button busy={test.isPending} disabled={!f.url} onClick={() => test.mutate()}>
            {t('conn.test')}
          </Button>
          {test.isSuccess && (
            <span className="flex items-center gap-1 text-sm text-ok">
              <CircleCheck className="size-4" />{' '}
              {t(caps?.movieSearch && caps?.tvSearch ? 'conn.indexer.answersBoth' : caps?.movieSearch ? 'conn.indexer.answersMovie' : caps?.tvSearch ? 'conn.indexer.answersTv' : 'conn.indexer.answers')}
            </span>
          )}
        </div>
        <ErrorNote error={test.error} />
        <Field label={t('conn.name')} hint={t('conn.nameHint')}>
          {(id) => <Input id={id} value={f.name} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
        <Field label={t('conn.indexer.kind')}>
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
          <div className="mb-1 text-sm font-medium">{t('conn.indexer.categories')}</div>
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
              {f.categories.length ? `${t('conn.indexer.searching', { categories: f.categories.join(', ') })} ` : ''}
              {t('conn.indexer.pressTest')}
            </p>
          )}
        </div>
        <Field label={t('conn.indexer.apiPath')} hint={t('conn.indexer.apiPathHint')}>
          {(id) => <Input id={id} value={f.apiPath} placeholder="/api" onChange={(e) => set({ apiPath: e.target.value })} />}
        </Field>
        <Field label={t('conn.indexer.priority')} hint={t('conn.indexer.priorityHint')}>
          {(id) => <Input id={id} type="number" min={1} max={50} value={f.priority} onChange={(e) => set({ priority: Number(e.target.value) })} className="max-w-32" />}
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('conn.auto.perDay')} hint={t('conn.indexer.queriesHint')}>
            {(id) => <Input id={id} type="number" min={0} value={f.dailyQueries} onChange={(e) => set({ dailyQueries: Math.max(0, Number(e.target.value) || 0) })} className="max-w-32" />}
          </Field>
          <Field label={t('conn.indexer.grabs')} hint={t('conn.indexer.grabsHint')}>
            {(id) => <Input id={id} type="number" min={0} value={f.dailyGrabs} onChange={(e) => set({ dailyGrabs: Math.max(0, Number(e.target.value) || 0) })} className="max-w-32" />}
          </Field>
        </div>
        <p className="-mt-2 text-xs text-muted">
          {state?.limitsReported ? t('conn.indexer.reported', { searches: state.queryLimit ?? '?', grabs: state.grabLimit ?? '?' }) : t('conn.indexer.findLimits')}
        </p>
        <Toggle checked={f.enableSearch} onChange={(v) => set({ enableSearch: v })} label={t('conn.indexer.forSearch')} />
        <Toggle
          checked={f.enableRss}
          onChange={(v) => set({ enableRss: v })}
          label={
            <>
              {t('conn.indexer.forRss')}
              <span className="block text-xs text-muted">{t('conn.indexer.forRssHint')}</span>
            </>
          }
        />
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button variant="primary" busy={save.isPending} disabled={!f.url || (!indexer?.hasApiKey && !f.apiKey)} onClick={() => save.mutate()}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function IndexerStatus({ state, onReset, resetting }: { state: IndexerState; onReset: () => void; resetting: boolean }) {
  const { t } = useTranslation()
  return (
    <div className={`rounded-md border p-3 text-sm ${state.resting ? 'border-bad/40 bg-bad/5' : 'border-line'}`}>
      {state.resting ? (
        <>
          <p className="font-medium">{t('conn.status.resting', { when: when(state.restingUntil) })}</p>
          <p className="mt-1 text-xs text-muted">{t('conn.status.errors', { count: state.failures, error: state.lastError })}</p>
          <Button className="mt-2" busy={resetting} onClick={onReset}>
            {t('conn.status.tryAgain')}
          </Button>
        </>
      ) : (
        <p>
          {t('conn.status.today', {
            searches: t(state.queryLimit ? 'conn.status.searchesOf' : 'conn.status.searches', { count: state.today.queries, limit: state.queryLimit }),
            downloads: t(state.grabLimit ? 'conn.status.downloadsOf' : 'conn.status.downloads', { count: state.today.grabs, limit: state.grabLimit }),
          })}
          <span className="block text-xs text-muted">
            {t('conn.status.reset')}
            {state.rssSyncedAt ? ` ${t('conn.status.lastRss', { when: when(state.rssSyncedAt) })}` : ''}
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
  const { t } = useTranslation()
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
    <Modal title={server ? t('conn.edit', { name: server.name }) : t('conn.server.add')} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-muted">
          {t('conn.server.where')}
        </p>
        <Field label={t('conn.server.host')} hint={t('conn.server.hostHint')}>
          {(id) => <Input id={id} value={f.host} placeholder="news.example.com" onChange={(e) => set({ host: e.target.value })} />}
        </Field>
        <Toggle
          checked={f.useTls}
          onChange={(v) => set({ useTls: v, port: portTouched ? f.port : v ? 563 : 119 })}
          label={
            <>
              {t('conn.server.tls')}
              <span className="block text-xs text-muted">{t('conn.server.tlsHint')}</span>
            </>
          }
        />
        <Field label={t('conn.port')} hint={t('conn.server.portHint')}>
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
        <Field label={t('conn.username')}>{(id) => <Input id={id} autoComplete="off" value={f.username} onChange={(e) => set({ username: e.target.value })} />}</Field>
        <SecretField label={t('conn.password')} has={!!server?.hasPassword} value={f.password} onChange={(v) => set({ password: v })} hint={t('conn.server.passwordHint')} />
        <div className="flex flex-wrap items-center gap-3">
          <Button busy={test.isPending} disabled={!f.host} onClick={() => test.mutate()}>
            {t('conn.test')}
          </Button>
          {test.isSuccess && (
            <span className="flex items-center gap-1 text-sm text-ok">
              <CircleCheck className="size-4" /> {t('conn.server.signedIn')}
            </span>
          )}
        </div>
        <ErrorNote error={test.error} />
        <Field label={t('conn.server.connections')} hint={t('conn.server.connectionsHint')}>
          {(id) => <Input id={id} type="number" min={1} max={100} value={f.connections} onChange={(e) => set({ connections: Number(e.target.value) })} className="max-w-32" />}
        </Field>
        <Field label={t('conn.server.useAs')} hint={t('conn.server.useAsHint')}>
          {(id) => (
            <Select
              id={id}
              value={f.level > 0 ? 1 : 0}
              onChange={(v) => set({ level: Number(v) })}
              options={[
                { value: 0, label: t('conn.server.main') },
                { value: 1, label: t('conn.server.backupServer') },
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
              {t('conn.server.block')}
              <span className="block text-xs text-muted">{t('conn.server.blockHint')}</span>
            </>
          }
        />
        {block && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t('conn.server.left')} hint={t('conn.server.leftHint')}>
              {(id) => <Input id={id} type="number" min={0} step="0.1" value={f.quotaGb} onChange={(e) => set({ quotaGb: Math.max(0, Number(e.target.value) || 0) })} className="max-w-32" />}
            </Field>
            <Field label={t('conn.server.expires')} hint={t('conn.server.expiresHint')}>
              {(id) => <Input id={id} type="date" value={f.expires} onChange={(e) => set({ expires: e.target.value })} className="max-w-44" />}
            </Field>
          </div>
        )}
        <Field label={t('conn.name')} hint={t('conn.nameHint')}>
          {(id) => <Input id={id} value={f.name} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
        <Toggle checked={f.enabled} onChange={(v) => set({ enabled: v })} label={t('conn.server.use')} />
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button variant="primary" busy={save.isPending} disabled={!f.host} onClick={() => save.mutate()}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// ---- download clients ----

const clientKinds = [
  { value: 'deluge', label: 'Deluge', port: 8112, secret: 'conn.client.password' },
  { value: 'qbittorrent', label: 'qBittorrent', port: 8080, secret: 'conn.client.password' },
  { value: 'transmission', label: 'Transmission', port: 9091, secret: 'conn.client.password' },
  { value: 'sabnzbd', label: 'SABnzbd', port: 8080, secret: 'conn.client.apiKey' },
  { value: 'nzbget', label: 'NZBGet', port: 6789, secret: 'conn.client.password' },
]

function ClientForm({ client, onClose }: { client: DownloadClient | null; onClose: () => void }) {
  const { t } = useTranslation()
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
    <Modal title={client ? t('conn.edit', { name: client.name }) : t('conn.client.add')} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label={t('conn.client.app')}>
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
        <Field label={t('conn.address')} hint={t('conn.client.hostHint', { app: kind.label, service: kind.value })}>
          {(id) => <Input id={id} value={f.host} placeholder={kind.value} onChange={(e) => set({ host: e.target.value })} />}
        </Field>
        <Field label={t('conn.port')} hint={t('conn.client.portHint', { app: kind.label, port: kind.port })}>
          {(id) => <Input id={id} type="number" value={f.port} onChange={(e) => set({ port: Number(e.target.value) })} className="max-w-32" />}
        </Field>
        <Toggle checked={f.useTls} onChange={(v) => set({ useTls: v })} label={t('conn.client.https')} />
        <Field label={t('conn.client.urlBase')} hint={t('conn.client.urlBaseHint')}>
          {(id) => <Input id={id} value={f.urlBase} onChange={(e) => set({ urlBase: e.target.value })} />}
        </Field>
        {f.kind !== 'deluge' && f.kind !== 'sabnzbd' && (
          <Field label={t('conn.username')}>{(id) => <Input id={id} autoComplete="off" value={f.username} onChange={(e) => set({ username: e.target.value })} />}</Field>
        )}
        <SecretField label={t(kind.secret)} has={!!client?.hasPassword} value={f.password} onChange={(v) => set({ password: v })} hint={t(`conn.client.${kind.value}`)} />
        <Field label={t('conn.client.category')} hint={t('conn.client.categoryHint')}>
          {(id) => <Input id={id} value={f.category} placeholder={t('conn.client.categoryPlaceholder')} onChange={(e) => set({ category: e.target.value })} />}
        </Field>
        <Field label={t('conn.name')} hint={t('conn.client.nameHint')}>
          {(id) => <Input id={id} value={f.name} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
        <Toggle checked={f.enabled} onChange={(v) => set({ enabled: v })} label={t('conn.client.use')} />
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button variant="primary" busy={save.isPending} disabled={!f.host} onClick={() => save.mutate()}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
