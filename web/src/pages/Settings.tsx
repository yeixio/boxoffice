import { LanguagePicker } from '../LanguagePicker'
import i18n from '../i18n'
import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, Copy, Lock, Search, Folder, FolderUp, KeyRound, LoaderCircle, Plus, RotateCcw, Trash2 } from 'lucide-react'
import { del, get, post, put, type Status, type MediaType, type MediaTypeKey, type Profile, type Recycle, type RestartInfo, type RootFolder, type RootKind, type Settings as SettingsData, type ShowDefaults, type SystemInfo } from '../api'
import { bytes, day } from '../format'
import { monitorOptions, useProfiles, useQualities, useRootFolders, useShowDefaults } from '../hooks'
import { AutoSearchSettings, Connections, DataCapSettingsCard, DownloadSettings, NumberField } from './Connections'
import { ScanButton, scannable } from './LibraryScan'
import { EreaderSettingsCard } from './Ereaders'
import { ProblemsCard } from './Problems'
import { QualityCheckCard } from './QualityCheck'
import { matches, spans, words } from './settingsSearch'
import { Badge, Button, Card, ErrorNote, Field, Input, Modal, Select, Spinner, Toggle } from '../ui'

// The sections Settings is split into, in tab order. "elsewhere" is never a
// tab: it only lists, while searching, settings that live on other pages.
const sectionIds = ['general', 'library', 'downloads', 'searching', 'apps'] as const
type SectionId = (typeof sectionIds)[number]
const isSection = (s: string | null | undefined): s is SectionId => sectionIds.includes(s as SectionId)

// A card on the page. keys are catalog keys for words people may search
// for that aren't on the card until it's opened, like a form's fields, and
// terms are names that read the same in every language. A card with inner
// is already given its id inside, so its wrapper doesn't take it.
type CardDef = { id: string; el: ReactNode; keys?: string[]; terms?: string[]; inner?: boolean }
type SectionDef = { id: SectionId | 'elsewhere'; cards: CardDef[] }

const mediaKinds: MediaTypeKey[] = ['music', 'audiobook', 'ebook', 'comic']

const sections: SectionDef[] = [
  {
    id: 'general',
    cards: [
      { id: 'language', el: <Language /> },
      { id: 'tmdb', el: <General />, keys: ['settings.tmdbKey'], terms: ['API'] },
      { id: 'password', el: <Account /> },
      { id: 'restart', el: <Restart />, keys: ['settings.restartNow'], terms: ['reboot', 'Kubernetes', 'Docker'] },
    ],
  },
  {
    id: 'library',
    cards: [
      { id: 'media-types', el: <MediaTypes /> },
      { id: 'folders-movie', el: <RootFolders kind="movie" />, keys: ['settings.addFolder', 'files.folder'] },
      { id: 'folders-series', el: <RootFolders kind="series" />, keys: ['settings.addFolder', 'files.folder'] },
      ...mediaKinds.map((k) => ({ id: `folders-${k}`, el: <TypeFolders kind={k} />, keys: ['settings.addFolder', 'files.folder'] })),
      { id: 'new-shows', el: <NewShows /> },
      { id: 'problems', el: <ProblemsCard /> },
      { id: 'quality-check', el: <QualityCheckCard />, keys: ['title.quality', 'qualityCheck.corrections'], terms: ['ffprobe', 'FFmpeg', 'HDR', 'Dolby Vision', 'MediaInfo', '4K', '1080p'] },
      { id: 'recycle', el: <RecycleFolders /> },
    ],
  },
  {
    id: 'downloads',
    cards: [
      { id: 'downloads', el: <DownloadSettings />, inner: true, terms: ['Usenet', 'NZB'] },
      {
        id: 'news-servers',
        el: <Connections section="news-servers" />,
        inner: true,
        keys: ['conn.server.host', 'conn.port', 'conn.username', 'conn.password', 'conn.server.tls', 'conn.server.connections', 'conn.server.useAs', 'conn.server.block', 'conn.server.left', 'conn.server.expires'],
        terms: ['Usenet', 'TLS', 'SSL'],
      },
      {
        id: 'download-clients',
        el: <Connections section="download-clients" />,
        inner: true,
        keys: ['conn.client.app', 'conn.address', 'conn.port', 'conn.client.https', 'conn.client.urlBase', 'conn.username', 'conn.password', 'conn.client.apiKey', 'conn.client.category'],
        terms: ['SABnzbd', 'NZBGet', 'qBittorrent', 'Transmission', 'Deluge', 'torrent'],
      },
    ],
  },
  {
    id: 'searching',
    cards: [
      {
        id: 'indexers',
        el: <Connections section="indexers" />,
        inner: true,
        keys: ['conn.address', 'conn.indexer.apiKey', 'conn.indexer.kind', 'conn.indexer.categories', 'conn.indexer.apiPath', 'conn.indexer.priority', 'conn.auto.perDay', 'conn.indexer.grabs', 'conn.indexer.forSearch', 'conn.indexer.forRss'],
        terms: ['Prowlarr', 'Newznab', 'Torznab', 'NZB', 'API'],
      },
      { id: 'profiles', el: <Profiles />, keys: ['settings.newProfileTitle', 'settings.qualities', 'settings.goodEnough', 'settings.upgradeUntil'] },
      { id: 'automatic-searches', el: <AutoSearchSettings />, inner: true, terms: ['RSS'] },
      { id: 'data-cap', el: <DataCapSettingsCard />, inner: true },
    ],
  },
  {
    id: 'apps',
    cards: [
      { id: 'matinee', el: <ConnectMatinee />, terms: ['API'] },
      { id: 'ereaders', el: <Ereaders />, terms: ['Kindle', 'PocketBook', 'SMTP'] },
      { id: 'move-in', el: <MoveIn />, terms: ['Radarr', 'Sonarr', 'Lidarr', 'Readarr'] },
    ],
  },
  {
    id: 'elsewhere',
    cards: [
      { id: 'alerts', el: <Elsewhere to="/health?tab=alerts" title="health.tabs.alerts" sub="health.subs.alerts" />, terms: ['webhook', 'Discord', 'ntfy', 'Home Assistant', 'Prometheus', 'email'] },
      { id: 'backups', el: <Elsewhere to="/backups" title="nav.backups" sub="backups.sub" />, keys: ['backups.schedule', 'backups.passphrase', 'backups.alsoSend'], terms: ['S3', 'MinIO'] },
    ],
  },
]

export function Settings() {
  const { t, i18n: { language } } = useTranslation()
  const [params] = useSearchParams()
  const { hash, key: locationKey } = useLocation()
  const navigate = useNavigate()
  const tab: SectionId = isSection(params.get('tab')) ? (params.get('tab') as SectionId) : 'general'
  const [query, setQuery] = useState('')
  const q = useMemo(() => words(query), [query])
  const searching = q.length > 0
  const root = useRef<HTMLDivElement>(null)
  const box = useRef<HTMLInputElement>(null)
  const [found, setFound] = useState<ReadonlyMap<string, string[]>>(() => new Map())
  // tick moves on whenever the words on the cards change, as their data
  // loads, so searching and jumping to a card see the cards as they are.
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const el = root.current
    if (!el) return
    let frame = 0
    const o = new MutationObserver(() => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => setTick((n) => n + 1))
    })
    o.observe(el, { childList: true, subtree: true, characterData: true })
    return () => {
      o.disconnect()
      cancelAnimationFrame(frame)
    }
  }, [])

  // The extra words each card can be found by, in this language and in
  // English, since guides and forums mostly name settings in English.
  const extras = useMemo(() => {
    const en = i18n.getFixedT('en')
    return new Map(
      sections.flatMap((s) =>
        s.cards.map((c) => [
          c.id,
          {
            text: [t(`settings.sections.${s.id}.label`), ...(c.keys ?? []).flatMap((k) => [t(k), en(k)]), ...(c.terms ?? [])].join(' '),
            fields: (c.keys ?? []).map((k) => ({ label: t(k), en: en(k) })),
          },
        ] as const),
      ),
    )
  }, [t, language])

  useLayoutEffect(() => {
    if (!searching || !root.current) return
    const next = new Map<string, string[]>()
    root.current.querySelectorAll<HTMLElement>('[data-card]').forEach((el) => {
      const id = el.dataset.card!
      const text = cardText(el)
      const extra = extras.get(id)
      if (!text.trim() || !matches(`${text} ${extra?.text ?? ''}`, q)) return
      // Fields in the card's forms that the search found, but that aren't
      // on the card itself, are named under it.
      const inside = matches(text, q) ? [] : (extra?.fields ?? []).filter((f) => q.some((w) => matches(`${f.label} ${f.en}`, [w]))).map((f) => f.label)
      next.set(id, [...new Set(inside)])
    })
    setFound((prev) => (prev.size === next.size && [...next].every(([id, v]) => prev.get(id)?.join() === v.join()) ? prev : next))
  }, [searching, q, tick, extras])

  // Mark the words found on the cards showing.
  useEffect(() => {
    if (typeof CSS === 'undefined' || !('highlights' in CSS)) return
    CSS.highlights.delete('settings-search')
    if (!searching || !root.current) return
    const ranges: Range[] = []
    root.current.querySelectorAll<HTMLElement>('[data-card]').forEach((el) => {
      if (!found.has(el.dataset.card!)) return
      for (const n of textNodes(el)) {
        for (const [start, end] of spans(n.textContent ?? '', q)) {
          const r = new Range()
          r.setStart(n, start)
          r.setEnd(n, end)
          ranges.push(r)
        }
      }
    })
    CSS.highlights.set('settings-search', new Highlight(...ranges))
    return () => {
      CSS.highlights.delete('settings-search')
    }
  }, [searching, q, found, tick])

  // A link to a setting, such as /settings#news-servers, opens its
  // section and brings its card into view.
  const shown = useRef('')
  useEffect(() => {
    if (!hash || shown.current === locationKey) return
    const target = document.getElementById(decodeURIComponent(hash.slice(1)))
    if (!target || !root.current?.contains(target)) return
    const section = target.closest<HTMLElement>('[data-section]')?.dataset.section
    if (isSection(section) && section !== tab) {
      navigate({ search: section === 'general' ? '' : `?tab=${section}`, hash }, { replace: true })
      return
    }
    if (!target.textContent?.trim()) return
    shown.current = locationKey
    setQuery('')
    const card = target.closest<HTMLElement>('[data-card]')?.parentElement ?? target
    card.scrollIntoView({ block: 'start' })
    const glow = ['rounded-xl', 'ring-2', 'ring-marquee', 'transition-shadow']
    card.classList.add(...glow)
    setTimeout(() => card.classList.remove(...glow), 2000)
  }, [hash, locationKey, tab, tick, navigate])

  // "/" jumps to the search box, as on many sites.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement
      if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey || el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName)) return
      e.preventDefault()
      box.current?.focus()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const go = (to: SectionId) => {
    setQuery('')
    navigate({ search: to === 'general' ? '' : `?tab=${to}` }, { replace: true })
  }
  return (
    <>
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0 flex-1">
          <h1 className="font-display text-3xl font-bold tracking-tight">{t('nav.settings')}</h1>
          <p className="mt-1 text-muted">{searching ? t('settings.search.found', { query: query.trim() }) : t(`settings.sections.${tab}.sub`)}</p>
        </div>
        <div className="relative w-full shrink-0 sm:w-72">
          <Search className="pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2 text-muted" aria-hidden />
          <input
            ref={box}
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && setQuery('')}
            placeholder={t('settings.search.placeholder')}
            aria-label={t('settings.search.placeholder')}
            aria-describedby="settings-search-hint"
            className="w-full rounded-md border border-line bg-paper py-2 pe-3 ps-9 text-sm placeholder:text-muted/70"
          />
          <span id="settings-search-hint" className="sr-only">
            {t('settings.search.hint')}
          </span>
        </div>
      </div>
      {!searching && (
        <div className="mb-6 flex flex-wrap gap-2 sm:flex-nowrap sm:gap-1 sm:overflow-x-auto sm:border-b sm:border-line" role="tablist" aria-label={t('nav.settings')}>
          {sectionIds.map((id) => (
            <button
              key={id}
              role="tab"
              aria-selected={tab === id}
              onClick={() => go(id)}
              className={`shrink-0 rounded-full border px-3.5 py-1.5 text-sm font-medium transition sm:-mb-px sm:rounded-none sm:border-0 sm:border-b-2 sm:px-3 sm:py-2 ${
                tab === id ? 'border-ink bg-ink text-paper sm:border-marquee sm:bg-transparent sm:text-ink' : 'border-line text-muted hover:text-ink sm:border-transparent'
              }`}
            >
              {t(`settings.sections.${id}.label`)}
            </button>
          ))}
        </div>
      )}
      <p className="sr-only" aria-live="polite">
        {searching ? t('settings.search.found', { query: query.trim() }) : ''}
      </p>
      {searching && found.size === 0 && (
        <Card>
          <p className="font-medium">{t('settings.search.none', { query: query.trim() })}</p>
          <p className="mt-1 text-sm text-muted">{t('settings.search.noneHint')}</p>
        </Card>
      )}
      <div ref={root} role={searching ? undefined : 'tabpanel'}>
        {sections.map((s) => {
          const open = searching ? s.cards.some((c) => found.has(c.id)) : s.id === tab
          return (
            <div key={s.id} data-section={s.id} hidden={!open}>
              {searching && <h2 className="mb-3 mt-2 text-xs font-semibold uppercase tracking-wider text-muted">{t(`settings.sections.${s.id}.label`)}</h2>}
              <div className="gap-6 lg:columns-2">
                {s.cards.map((c) => (
                  <div key={c.id} id={c.inner ? undefined : c.id} hidden={searching && !found.has(c.id)} className="mb-6 scroll-mt-20 break-inside-avoid has-[>[data-card]:empty]:hidden">
                    <div data-card={c.id}>{c.el}</div>
                    {searching && !!found.get(c.id)?.length && <p className="mt-2 px-1 text-xs text-muted">{t('settings.search.inside', { fields: found.get(c.id)!.join(', ') })}</p>}
                  </div>
                ))}
              </div>
            </div>
          )
        })}
      </div>
      {!searching && tab === 'general' && <System />}
    </>
  )
}

// textNodes are the words on a card people can read, leaving out the
// choices folded away in drop-down lists, such as every language's name.
function textNodes(el: HTMLElement) {
  const out: Text[] = []
  const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) => {
      const at = n.parentElement
      // The card itself may be hidden by an earlier search; only what's
      // hidden inside it doesn't count.
      const hidden = at?.closest('[hidden]')
      return at?.closest('option, select') || (hidden && el.contains(hidden)) ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT
    },
  })
  for (let n = walk.nextNode(); n; n = walk.nextNode()) out.push(n as Text)
  return out
}

const cardText = (el: HTMLElement) => textNodes(el).map((n) => n.data).join(' ')

// Elsewhere points, while searching, to settings kept on another page.
function Elsewhere({ to, title, sub }: { to: string; title: string; sub: string }) {
  const { t } = useTranslation()
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t(title)}</h2>
      <p className="mb-4 text-sm text-muted">{t(sub)}</p>
      <Link to={to} className="inline-flex items-center gap-2 rounded-md border border-line bg-card px-3.5 py-2 text-sm font-medium hover:border-ink/40">
        {t('settings.openPage', { page: t(title) })} <ArrowRight className="size-4 rtl:-scale-x-100" aria-hidden />
      </Link>
    </Card>
  )
}

function useSettings() {
  return useQuery({ queryKey: ['settings'], queryFn: () => get<SettingsData>('/settings') })
}

// Language is the admin's own: it follows them to every browser they sign
// in on.
function Language() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const status = useQuery({ queryKey: ['status'], queryFn: () => get<Status>('/status') })
  const pick = useMutation({
    mutationFn: (language: string) => put('/me/language', { language }),
    onMutate: (language) => qc.setQueryData<Status>(['status'], (s) => s && s.user && { ...s, user: { ...s.user, language } }),
    onSettled: () => qc.invalidateQueries({ queryKey: ['status'] }),
  })
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('common:language.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('settings.languageHint')}</p>
      <LanguagePicker saved={status.data?.user?.language ?? (status.data ? '' : undefined)} busy={pick.isPending} onPick={(code) => pick.mutate(code)} />
      <ErrorNote error={pick.error} />
    </Card>
  )
}

function General() {
  const { t } = useTranslation()
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
        <Trans i18nKey="settings.tmdbIntro" components={{ link: <a className="text-marquee" href="https://www.themoviedb.org/settings/api" target="_blank" rel="noreferrer" /> }} />
      </p>
      {s.data?.tmdbKeyBuiltIn && (
        <p className="mb-3 text-sm text-muted">{t('settings.tmdbBuiltIn')}</p>
      )}
      {s.data && (
        <p className="mb-3 text-sm">
          {s.data.hasTmdbKey ? <Badge tone="ok">{s.data.tmdbKeyBuiltIn ? t('settings.builtIn') : s.data.tmdbKeyFromEnv ? t('settings.onServer') : t('settings.savedBadge')}</Badge> : <Badge tone="bad">{t('settings.notSet')}</Badge>}
        </p>
      )}
      <div className="flex gap-2">
        <Input aria-label={t('settings.tmdbKey')} placeholder={s.data?.hasTmdbKey ? t('settings.replaceKey') : t('settings.tmdbKey')} value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off" />
        <Button variant="primary" busy={save.isPending} disabled={!key} onClick={() => save.mutate()}>
          {t('settings.checkSave')}
        </Button>
      </div>
      <ErrorNote error={save.error} />
    </Card>
  )
}

// MoveIn is where Move in lives once it has left the menu.
function MoveIn() {
  const { t } = useTranslation()
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('nav.import')}</h2>
      <p className="mb-4 text-sm text-muted">{t('settings.moveInSub')}</p>
      <Link to="/import">
        <Button>{t('settings.openMoveIn')}</Button>
      </Link>
    </Card>
  )
}

function ConnectMatinee() {
  const { t } = useTranslation()
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
  const rows = [[t('settings.address'), origin]]
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.matinee')}</h2>
      <p className="mb-4 text-sm text-muted">
        {t('settings.matineeSub')}
      </p>
      <dl className="flex flex-col gap-3 text-sm">
        {rows.map(([label, v]) => (
          <div key={label} className="flex items-center justify-between gap-3">
            <dt className="text-muted">{label}</dt>
            <dd className="flex items-center gap-2">
              <code className="break-all">{v}</code>
              <button onClick={() => copy(label, v)} aria-label={t('settings.copyThe', { what: label })} className="rounded p-1 text-muted hover:bg-line">
                <Copy className="size-3.5" />
              </button>
              {copied === label && <span className="text-xs text-ok">{t('common.copied')}</span>}
            </dd>
          </div>
        ))}
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted">{t('settings.apiKey')}</dt>
          <dd className="flex items-center gap-2">
            <code className="break-all">{s.data ? (shown ? s.data.apiKey : '•'.repeat(16)) : '…'}</code>
            <button onClick={() => setShown(!shown)} className="text-xs text-marquee">
              {shown ? t('settings.hide') : t('settings.show')}
            </button>
            {s.data && (
              <button onClick={() => copy('key', s.data.apiKey)} aria-label={t('settings.copyKey')} className="rounded p-1 text-muted hover:bg-line">
                <Copy className="size-3.5" />
              </button>
            )}
            {copied === 'key' && <span className="text-xs text-ok">{t('common.copied')}</span>}
          </dd>
        </div>
      </dl>
      {s.data?.apiKeyFromEnv ? (
        <p className="mt-4 text-xs text-muted">{t('settings.keyEnv')}</p>
      ) : (
        <Button className="mt-4" busy={regen.isPending} onClick={() => regen.mutate()}>
          <KeyRound className="size-4" aria-hidden /> {t('settings.newKey')}
        </Button>
      )}
      <ErrorNote error={regen.error} />
    </Card>
  )
}

// MediaTypes turns on the kinds of media beyond movies and TV. Each is off
// until it's turned on here.
function MediaTypes() {
  const { t } = useTranslation()
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
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.more')}</h2>
      <p className="mb-4 text-sm text-muted">
        {t('settings.moreSub')}
      </p>
      {s.isPending && <Spinner />}
      <ErrorNote error={s.error} />
      <div className="flex flex-col gap-4">
        {s.data?.mediaTypes.map((m) => (
          <Toggle
            key={m.key}
            checked={m.enabled}
            disabled={save.isPending}
            onChange={(on) => save.mutate({ key: m.key, on })}
            label={
              <span>
                <span className="font-medium">{typeName(m)}</span>
                {m.replaces && <span className="text-muted"> {t('settings.inPlaceOf', { app: m.replaces })}</span>}
                <span className="block text-sm text-muted">{t(`mediaTypes.${m.key}.summary`, { defaultValue: m.summary })}</span>
              </span>
            }
          />
        ))}
      </div>
      {s.data?.mediaTypes.some((m) => m.enabled) && (
        <p className="mt-4 text-sm text-muted">{t('settings.pickRoot')}</p>
      )}
      <ErrorNote error={save.error} />
    </Card>
  )
}

// Ereaders sets up sending ebooks to e-readers, once ebooks are on. Until
// then it says how to turn them on, so the setting can still be found.
function Ereaders() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const s = useSettings()
  const turnOn = useMutation({
    mutationFn: () => put<SettingsData>('/settings', { mediaTypes: { ebook: true } }),
    onSuccess: (d) => {
      qc.setQueryData(['settings'], d)
      qc.invalidateQueries({ queryKey: ['rootfolders'] })
    },
  })
  const ebooks = s.data?.mediaTypes.find((m) => m.key === 'ebook')
  if (!ebooks) return null
  if (ebooks.enabled) return <EreaderSettingsCard />
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('ereaders.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('settings.ereadersOff', { ebooks: typeName(ebooks) })}</p>
      <Button busy={turnOn.isPending} onClick={() => turnOn.mutate()}>
        {t('settings.turnOnType', { name: typeName(ebooks) })}
      </Button>
      <ErrorNote error={turnOn.error} />
    </Card>
  )
}

// TypeFolders shows a media type's root folders once the type is on.
function TypeFolders({ kind }: { kind: MediaTypeKey }) {
  const s = useSettings()
  const type = s.data?.mediaTypes.find((m) => m.key === kind && m.enabled)
  return type ? <RootFolders kind={kind} type={type} /> : null
}

// typeName is a media type's name in the page's language.
const typeName = (m: MediaType) => i18n.t(`mediaTypes.${m.key}.name`, { defaultValue: m.name })

// rootTitle heads a kind's root folders, and addTitle its folder picker.
const rootTitle = (kind: RootKind, type?: MediaType) =>
  kind === 'movie' || kind === 'series' ? i18n.t(`settings.roots.${kind}`) : i18n.t('settings.roots.other', { name: type ? typeName(type) : kind })
const addTitle = (kind: RootKind, type?: MediaType) =>
  kind === 'movie' || kind === 'series' ? i18n.t(`settings.addRoot.${kind}`) : i18n.t('settings.addRoot.other', { name: type ? typeName(type) : kind })

function RootFolders({ kind, type }: { kind: RootKind; type?: MediaType }) {
  const { t } = useTranslation()
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
        <h2 className="font-display text-lg font-semibold">{rootTitle(kind, type)}</h2>
        <Button variant="ghost" onClick={() => setPicking(true)}>
          <Plus className="size-4" aria-hidden /> {t('settings.add')}
        </Button>
      </div>
      {folders.isPending && <Spinner />}
      {folders.data?.length === 0 && <p className="text-sm text-muted">{t('settings.noRoots')}</p>}
      {folders.data?.length === 0 && type && type.suggested.length > 0 && <SuggestedFolders kind={kind} type={type} />}
      <ul className="flex flex-col divide-y divide-line">
        {folders.data?.map((f) => (
          <li key={f.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm">
            <Folder className="size-4 text-muted" aria-hidden />
            <code className="min-w-48 flex-1 break-all">{f.path}</code>
            {f.accessible ? <span className="text-xs text-muted">{t('settings.free', { size: bytes(f.freeSpace) })}</span> : <Badge tone="bad">{t('settings.cantSee')}</Badge>}
            {f.accessible && scannable(kind) && <ScanButton folder={f} />}
            <button onClick={() => remove.mutate(f)} aria-label={t('settings.removePath', { path: f.path })} className="rounded p-1 text-muted hover:bg-line hover:text-bad">
              <Trash2 className="size-4" />
            </button>
          </li>
        ))}
      </ul>
      {scannable(kind) && !!folders.data?.length && (
        <p className="mt-2 text-xs text-muted">{t(`settings.scanFor.${kind === 'movie' || kind === 'series' || kind === 'music' ? kind : 'books'}`)}</p>
      )}
      <ErrorNote error={remove.error} />
      {picking && <FolderPicker kind={kind} title={addTitle(kind, type)} onClose={() => setPicking(false)} />}
    </Card>
  )
}

// NewShows sets which seasons a new show watches when nobody picks, for
// shows added here and requests from Matinee. Shows already on the list,
// and ones brought over by Move in, keep what they have.
function NewShows() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const d = useShowDefaults()
  const save = useMutation({
    mutationFn: (s: Partial<ShowDefaults>) => put<ShowDefaults>('/series/defaults', { ...d.data!, ...s }),
    onSuccess: (v) => qc.setQueryData(['show-defaults'], v),
  })
  if (!d.data) return null
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.newShows')}</h2>
      <p className="mb-4 text-sm text-muted">
        {t('settings.newShowsSub')}
      </p>
      <div className="flex flex-col gap-4">
        <Field label={t('add.which')} hint={t('settings.specialsOut')}>
          {(id) => <Select id={id} value={d.data.monitor} onChange={(v) => save.mutate({ monitor: v })} options={monitorOptions(d.data.recent)} />}
        </Field>
        {d.data.monitor === 'recent' && (
          <NumberField
            label={t('add.howMany')}
            value={d.data.recent}
            max={99}
            busy={save.isPending}
            onSave={(v) => save.mutate({ recent: v })}
            hint={t('settings.howManyHint')}
          />
        )}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}

// RecycleFolders shows the copies upgrades replaced, set aside in each
// library's .recycle folder, and frees their space.
function RecycleFolders() {
  const { t } = useTranslation()
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
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.recycle')}</h2>
      <p className="mb-4 text-sm text-muted">
        <Trans i18nKey="settings.recycleSub" components={{ code: <code /> }} />
      </p>
      {recycle.isPending ? (
        <Spinner />
      ) : recycle.isError ? (
        <ErrorNote error={recycle.error} />
      ) : (
        <div className="flex flex-col gap-4">
          {folders.length === 0 ? (
            <p className="text-sm">{t('settings.nothingAside')}</p>
          ) : (
            <ul className="flex flex-col divide-y divide-line text-sm">
              {folders.map((f) => (
                <li key={f.folder} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                  <code className="min-w-0 flex-1 basis-48 break-all">{f.folder}</code>
                  <span className="text-muted">
                    {t('scan.files', { count: f.count, size: bytes(f.size) })}
                    {f.oldest && t('settings.oldest', { date: day(f.oldest) })}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <Field label={t('settings.deleteReplaced')} hint={t('settings.deleteReplacedHint')}>
            {(id) => (
              <Select
                id={id}
                value={recycle.data.keepDays}
                onChange={(v) => keep.mutate(Number(v))}
                options={[
                  { value: 0, label: t('settings.never') },
                  ...[7, 14, 30, 90].map((n) => ({ value: n, label: t('settings.after', { count: n }) })),
                  ...([0, 7, 14, 30, 90].includes(recycle.data.keepDays) ? [] : [{ value: recycle.data.keepDays, label: t('settings.after', { count: recycle.data.keepDays }) }]),
                ]}
              />
            )}
          </Field>
          <div>
            <Button variant="danger" disabled={total === 0} onClick={() => setConfirm(true)}>
              <Trash2 className="size-4" aria-hidden /> {t('settings.empty')}{total > 0 && ` (${bytes(total)})`}
            </Button>
          </div>
          <ErrorNote error={keep.error} />
        </div>
      )}
      {confirm && (
        <Modal title={t('settings.emptyTitle')} onClose={() => setConfirm(false)}>
          <p className="text-sm">
            {t('settings.emptyBody', { count: files, size: bytes(total) })}
          </p>
          <ErrorNote error={empty.error} />
          <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button variant="ghost" onClick={() => setConfirm(false)}>
              {t('dlBulk.keep')}
            </Button>
            <Button variant="danger" busy={empty.isPending} onClick={() => empty.mutate()}>
              <Trash2 className="size-4" aria-hidden /> {t('remove.delete', { size: bytes(total) })}
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
  const { t } = useTranslation()
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
      <p className="mb-2">{t(type.suggested.length === 1 ? 'settings.foundOne' : 'settings.foundMany', { name: typeName(type) })}</p>
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
  const { t } = useTranslation()
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
    <Modal title={title} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <Input aria-label={t('files.folder')} value={typed} onChange={(e) => setTyped(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && go(typed)} />
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
            {t('common.cancel')}
          </Button>
          <Button variant="primary" busy={add.isPending} onClick={() => add.mutate()}>
            {t('settings.addFolder')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function Profiles() {
  const { t } = useTranslation()
  const profiles = useProfiles()
  const qualities = useQualities()
  const [editing, setEditing] = useState<Profile | 'new' | null>(null)
  const name = (id: number) => qualities.data?.find((q) => q.id === id)?.name ?? '?'
  return (
    <Card>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold">{t('settings.profiles')}</h2>
        <Button variant="ghost" onClick={() => setEditing('new')}>
          <Plus className="size-4" aria-hidden /> {t('settings.newProfile')}
        </Button>
      </div>
      <ul className="flex flex-col divide-y divide-line">
        {profiles.data?.map((p) => (
          <li key={p.id}>
            {p.locked ? (
              <div className="flex gap-2 py-2 text-sm">
                <Lock className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
                <span>
                  <span className="font-medium">{p.name}</span>
                  <span className="block text-xs text-muted">
                    {t('settings.lockedProfile')}
                  </span>
                </span>
              </div>
            ) : (
            <button className="w-full py-2 text-start text-sm hover:text-marquee" onClick={() => setEditing(p)}>
              <span className="font-medium">{p.name}</span>
              <span className="block text-xs text-muted">
                {t('settings.profileSummary', { count: p.allowed.length, cutoff: name(p.cutoff) })}
                {p.upgrade ? t('settings.upgradesOn') : ''}
              </span>
            </button>
            )}
          </li>
        ))}
      </ul>
      {editing && <ProfileEditor profile={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
    </Card>
  )
}

function ProfileEditor({ profile, onClose }: { profile: Profile | null; onClose: () => void }) {
  const { t } = useTranslation()
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
    <Modal title={profile ? t('settings.edit', { name: profile.name }) : t('settings.newProfileTitle')} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label={t('alerts.name')}>{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
        <div>
          <div className="mb-2 text-sm font-medium">{t('settings.qualities')}</div>
          <ul className="grid grid-cols-2 gap-1.5 text-sm">
            {qualities.data?.map((q) => (
              <li key={q.id} className="flex items-center gap-2">
                <input type="checkbox" id={`q${q.id}`} checked={allowed.includes(q.id)} onChange={() => toggle(q.id)} className="accent-[var(--marquee)]" />
                <label htmlFor={`q${q.id}`} className="flex-1">
                  {q.name}
                </label>
                {allowed.includes(q.id) && (
                  <button onClick={() => setCutoff(q.id)} className={`text-xs ${cutoff === q.id ? 'font-semibold text-marquee' : 'text-muted hover:text-ink'}`}>
                    {cutoff === q.id ? t('settings.goodEnough') : t('settings.setGoodEnough')}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </div>
        <Toggle checked={upgrade} onChange={setUpgrade} label={t('settings.upgradeUntil')} />
        <ErrorNote error={save.error || remove.error} />
        <div className="flex justify-between gap-2">
          {profile ? (
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              {t('settings.delete')}
            </Button>
          ) : (
            <span />
          )}
          <Button variant="primary" busy={save.isPending} disabled={!name || allowed.length === 0} onClick={() => save.mutate()}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function Account() {
  const { t } = useTranslation()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const change = useMutation({
    mutationFn: () => put('/me/password', { current, new: next }),
    onSuccess: () => window.location.assign('/'),
  })
  return (
    <Card>
      <h2 className="mb-4 font-display text-lg font-semibold">{t('settings.password')}</h2>
      <div className="flex flex-col gap-3">
        <Field label={t('settings.current')}>{(id) => <Input id={id} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />}</Field>
        <Field label={t('settings.new')} hint={t('settings.newHint')}>
          {(id) => <Input id={id} type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />}
        </Field>
        <ErrorNote error={change.error} />
        <Button className="self-end" busy={change.isPending} disabled={!current || next.length < 8} onClick={() => change.mutate()}>
          {t('settings.change')}
        </Button>
      </div>
    </Card>
  )
}

// Restart stops Box Office cleanly and starts it again. Downloads stop
// where they are and carry on after, and the page waits for the new run,
// told apart by when it started, then loads again.
function Restart() {
  const { t } = useTranslation()
  const [confirm, setConfirm] = useState(false)
  const info = useQuery({ queryKey: ['restart'], queryFn: () => get<RestartInfo>('/admin/restart'), enabled: confirm, staleTime: 0 })
  const [since, setSince] = useState<string>()
  const go = useMutation({
    mutationFn: () => post<{ startedAt: string }>('/admin/restart'),
    onSuccess: (r) => {
      setConfirm(false)
      setSince(r.startedAt)
    },
  })
  const n = info.data?.downloads ?? 0
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.restart')}</h2>
      <p className="mb-4 text-sm text-muted">{t('settings.restartSub')}</p>
      <Button onClick={() => setConfirm(true)}>
        <RotateCcw className="size-4" aria-hidden /> {t('settings.restartButton')}
      </Button>
      {confirm && (
        <Modal title={t('settings.restartTitle')} onClose={() => setConfirm(false)}>
          <div className="flex flex-col gap-3 text-sm">
            <p>{t('settings.restartBody')}</p>
            {n > 0 && <p className="font-medium">{t('settings.restartDownloads', { n })}</p>}
          </div>
          <ErrorNote error={go.error} />
          <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button variant="ghost" onClick={() => setConfirm(false)}>
              {t('common.cancel')}
            </Button>
            <Button variant="danger" busy={go.isPending} onClick={() => go.mutate()}>
              <RotateCcw className="size-4" aria-hidden /> {t('settings.restartNow')}
            </Button>
          </div>
        </Modal>
      )}
      {since && <Restarting since={since} />}
    </Card>
  )
}

// Restarting covers the page while Box Office is away, and loads the page
// again once a new run answers. It says so if that takes long.
function Restarting({ since }: { since: string }) {
  const { t } = useTranslation()
  const [slow, setSlow] = useState(false)
  useEffect(() => {
    let done = false
    const look = async () => {
      try {
        const s = await get<SystemInfo>('/system')
        if (!done && s.startedAt !== since) {
          done = true
          window.location.reload()
        }
      } catch {
        // Still away.
      }
    }
    const every = window.setInterval(look, 2000)
    const late = window.setTimeout(() => setSlow(true), 2 * 60 * 1000)
    return () => {
      done = true
      window.clearInterval(every)
      window.clearTimeout(late)
    }
  }, [since])
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div role="status" aria-live="polite" className="flex w-full max-w-md flex-col items-center gap-3 rounded-xl border border-line bg-card p-6 text-center text-ink">
        <LoaderCircle className="size-8 animate-spin text-muted" aria-hidden />
        <h2 className="font-display text-lg font-semibold">{t('settings.restarting')}</h2>
        <p className="text-sm text-muted">{t('settings.restartingSub')}</p>
        {slow && (
          <>
            <p className="text-sm">{t('settings.restartSlow')}</p>
            <Button onClick={() => window.location.reload()}>{t('settings.reload')}</Button>
          </>
        )}
      </div>
    </div>
  )
}

function System() {
  const s = useQuery({ queryKey: ['system'], queryFn: () => get<SystemInfo>('/system') })
  if (!s.data) return null
  return (
    <p className="mt-8 text-xs text-muted">
      <Trans
        i18nKey="settings.system"
        values={{ version: s.data.version, os: s.data.os, go: s.data.go, db: s.data.database === 'postgres' ? 'PostgreSQL' : 'SQLite', dir: s.data.dataDir, since: day(s.data.startedAt) }}
        components={{ code: <code /> }}
      />
    </p>
  )
}
