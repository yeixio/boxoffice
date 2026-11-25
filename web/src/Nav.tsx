import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState, type ComponentType } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import {
  CalendarDays,
  Clapperboard,
  Compass,
  Download as DownloadIcon,
  Film,
  Gamepad2,
  Images,
  HeartPulse,
  History,
  Inbox,
  LogOut,
  Music as MusicIcon,
  BookOpen,
  Headphones,
  PanelLeftClose,
  PanelLeftOpen,
  Settings as Cog,
  Truck,
  Tv,
  UserRound,
} from 'lucide-react'
import { get, type MediaTypeKey, type Settings as SettingsData, type Status } from './api'
import { Tray } from './Tray'
import { speed, useDownloads } from './pages/Downloads'
import { needsAttention, useHealth } from './pages/Health'
import { useRequestCounts } from './pages/Requests'

// label and a group's label are catalog keys. also lists other pages that
// belong to an item, which keep it marked as the one you're in.
type Item = { to: string; label: string; icon: ComponentType<{ className?: string }>; type?: MediaTypeKey; also?: string[] }
type Group = { label: string; items: Item[] }

// The sections, grouped so a long list still reads at a glance. A section
// with a media type shows only once that type is turned on in Settings, so
// audiobooks, ebooks, comics, games and home media slot in here the same way as music.
// Only things people do are here; Settings and the signed-in person's own
// account sit at the foot, apart from them (docs/design/ui-audit-2026-11.md).
const allGroups: Group[] = [
  {
    label: 'nav.library',
    items: [
      { to: '/discover', label: 'nav.discover', icon: Compass },
      { to: '/movies', label: 'nav.movies', icon: Film },
      { to: '/shows', label: 'nav.shows', icon: Tv },
      { to: '/music', label: 'nav.music', icon: MusicIcon, type: 'music' },
      { to: '/books', label: 'nav.books', icon: BookOpen, type: 'ebook' },
      { to: '/audiobooks', label: 'nav.audiobooks', icon: Headphones, type: 'audiobook' },
      { to: '/games', label: 'nav.games', icon: Gamepad2, type: 'game' },
      { to: '/photos', label: 'nav.photos', icon: Images, type: 'home' },
      { to: '/calendar', label: 'nav.calendar', icon: CalendarDays },
    ],
  },
  {
    label: 'nav.status',
    items: [
      { to: '/requests', label: 'nav.requests', icon: Inbox },
      { to: '/downloads', label: 'nav.downloads', icon: DownloadIcon, also: ['/import-files'] },
      { to: '/activity', label: 'nav.activity', icon: History },
      { to: '/health', label: 'nav.health', icon: HeartPulse },
    ],
  },
  {
    label: 'nav.setup',
    items: [{ to: '/import', label: 'nav.import', icon: Truck }],
  },
]

// useNav gives the sections to show, and what each one has to say: Health
// counts the problems to fix, Downloads shows the speed while downloading.
export function useNav(status: Status) {
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => get<SettingsData>('/settings') })
  const on = (key: MediaTypeKey) => !!settings.data?.mediaTypes.find((t) => t.key === key)?.enabled
  // Move in is for setting up: it leaves the menu once the admin has
  // finished moving in, for Settings.
  const shown = (i: Item) => (i.to !== '/import' || !status.movedIn) && (!i.type || on(i.type))
  const groups = allGroups.map((g) => ({ ...g, items: g.items.filter(shown) })).filter((g) => g.items.length)
  const problems = needsAttention(useHealth().data)
  const dl = useDownloads().data
  const rate = dl?.settings.enabled && !dl.settings.paused ? dl.speed : 0
  const counts = useRequestCounts().data
  const waiting = counts ? counts.requests + counts.issues : 0
  // Downloads still to come in, for the browser tab's title.
  const queued = dl?.queue?.length ?? 0
  return { groups, problems, rate, waiting, queued, on, ready: !!settings.data }
}

type Nav = ReturnType<typeof useNav>

// useHere says whether a link is to the page shown. Settings and Your
// account are the same page on different tabs, so the tab tells them apart.
function useHere() {
  const { pathname, search } = useLocation()
  const account = pathname === '/settings' && new URLSearchParams(search).get('tab') === 'account'
  return (to: string, also: string[] = []) => {
    if (to === '/settings?tab=account') return account
    if (to === '/settings') return pathname === '/settings' && !account
    return [to, ...also].some((p) => pathname === p || pathname.startsWith(p + '/'))
  }
}

function Badge({ to, nav, dot }: { to: string; nav: Nav; dot?: boolean }) {
  const { t } = useTranslation()
  if (to === '/health' && nav.problems > 0) {
    if (dot) return <span className="absolute end-1.5 top-1.5 size-2 rounded-full bg-marquee" aria-label={t('nav.toFix', { count: nav.problems })} />
    return (
      <span className="ms-auto rounded-full bg-marquee px-1.5 text-xs font-semibold leading-5 text-marquee-ink" aria-label={t('nav.toFix', { count: nav.problems })}>
        {nav.problems}
      </span>
    )
  }
  if (to === '/requests' && nav.waiting > 0) {
    if (dot) return <span className="absolute end-1.5 top-1.5 size-2 rounded-full bg-marquee" aria-label={t('nav.waiting', { count: nav.waiting })} />
    return (
      <span className="ms-auto rounded-full bg-marquee px-1.5 text-xs font-semibold leading-5 text-marquee-ink" aria-label={t('nav.waiting', { count: nav.waiting })}>
        {nav.waiting}
      </span>
    )
  }
  if (to === '/downloads' && nav.rate > 0 && !dot) {
    return <span className="ms-auto rounded-full border border-line px-1.5 text-xs leading-5 tabular-nums">{speed(nav.rate)}</span>
  }
  return null
}

export function Logo({ compact, small }: { compact?: boolean; small?: boolean }) {
  return (
    <NavLink to="/discover" className="flex min-w-0 items-center gap-2" aria-label={compact ? 'Box Office' : undefined}>
      <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-marquee text-marquee-ink">
        <Clapperboard className="size-4.5" aria-hidden />
      </span>
      {!compact && <span className={`truncate font-display max-[359px]:sr-only font-bold uppercase ${small ? 'text-base tracking-[0.14em]' : 'text-lg tracking-[0.18em]'}`}>Box Office</span>}
    </NavLink>
  )
}

const key = 'boxoffice.sidebar'

function load() {
  try {
    return localStorage.getItem(key) === 'narrow'
  } catch {
    return false
  }
}

// useWide is true when the screen has room for the sidebar's names.
function useWide() {
  const query = '(min-width: 1024px)'
  const [wide, setWide] = useState(() => typeof matchMedia === 'undefined' || matchMedia(query).matches)
  useEffect(() => {
    if (typeof matchMedia === 'undefined') return
    const m = matchMedia(query)
    const change = () => setWide(m.matches)
    m.addEventListener('change', change)
    return () => m.removeEventListener('change', change)
  }, [])
  return wide
}

// Sidebar holds the sections down the left on a computer. It narrows to
// icons on a small screen, or when asked to, and remembers that choice.
export function Sidebar({ status, nav, signOut }: { status: Status; nav: Nav; signOut: () => void }) {
  const { t } = useTranslation()
  const wide = useWide()
  const [narrowed, setNarrowed] = useState(load)
  const narrow = narrowed || !wide
  const here = useHere()
  const look = (on: boolean) => (on ? 'bg-ink text-paper' : 'text-muted hover:bg-line/50 hover:text-ink')
  const name = status.user?.username ?? ''
  // On the icons-only rail, a name shows beside an icon at once when it's
  // pointed at or reached with the keyboard, since not every icon speaks
  // for itself. It's placed on the page, as the rail's list would clip it.
  const [tip, setTip] = useState<{ text: string; top: number; left: number } | null>(null)
  const tipFor = (text: string) =>
    narrow
      ? {
          onMouseEnter: (e: { currentTarget: HTMLElement }) => show(e.currentTarget, text),
          onFocus: (e: { currentTarget: HTMLElement }) => show(e.currentTarget, text),
          onMouseLeave: () => setTip(null),
          onBlur: () => setTip(null),
        }
      : {}
  const show = (el: HTMLElement, text: string) => {
    const r = el.getBoundingClientRect()
    const rtl = getComputedStyle(el).direction === 'rtl'
    setTip({ text, top: r.top + r.height / 2, left: rtl ? r.left - 8 : r.right + 8 })
  }
  useEffect(() => {
    if (!narrow) setTip(null)
  }, [narrow])
  const toggle = () => {
    setNarrowed(!narrowed)
    try {
      localStorage.setItem(key, narrowed ? 'wide' : 'narrow')
    } catch {
      // The choice just isn't remembered.
    }
  }
  return (
    <aside className={`sticky top-0 hidden h-screen shrink-0 self-start md:flex ${narrow ? 'w-[4.5rem]' : 'w-64'}`}>
      <div className="flex min-w-0 flex-1 flex-col bg-paper">
        <div className={`flex items-center gap-1 px-4 pb-2 pt-4 ${narrow ? 'flex-col' : ''}`}>
          <Logo compact={narrow} small />
          {!narrow && <div className="flex-1" />}
          <div className={narrow ? 'mt-2' : ''}>
            <Tray side={narrow} />
          </div>
        </div>
        <nav className="flex-1 overflow-y-auto px-3 pb-3" aria-label={t('nav.main')}>
          {nav.groups.map((g) => (
            <div key={g.label} className="mt-4">
              {narrow ? (
                <div className="mx-2 mb-2 border-t border-line" aria-hidden />
              ) : (
                <div className="mb-1 px-3 text-xs font-semibold uppercase tracking-wider text-muted">{t(g.label)}</div>
              )}
              <ul className="flex flex-col gap-0.5">
                {g.items.map(({ to, label, icon: Icon, also }) => (
                  <li key={to}>
                    <Link
                      to={to}
                      aria-label={narrow ? t(label) : undefined}
                      aria-current={here(to, also) ? 'page' : undefined}
                      {...tipFor(t(label))}
                      className={`relative flex items-center gap-3 rounded-md py-2 text-sm font-medium transition ${narrow ? 'justify-center px-2' : 'px-3'} ${look(here(to, also))}`}
                    >
                      <Icon className="size-4.5 shrink-0" aria-hidden />
                      {!narrow && <span className="truncate">{t(label)}</span>}
                      <Badge to={to} nav={nav} dot={narrow} />
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
        <div className={`flex flex-col gap-0.5 border-t border-line px-3 py-3 ${narrow ? 'items-center' : ''}`}>
          <Link
            to="/settings"
            aria-current={here('/settings') ? 'page' : undefined}
            aria-label={narrow ? t('nav.settings') : undefined}
            {...tipFor(t('nav.settings'))}
            className={`flex items-center gap-3 rounded-md py-2 text-sm font-medium transition ${narrow ? 'px-2' : 'px-3'} ${look(here('/settings'))}`}
          >
            <Cog className="size-4.5 shrink-0" aria-hidden />
            {!narrow && <span className="truncate">{t('nav.settings')}</span>}
          </Link>
          <div className={`flex items-center gap-0.5 ${narrow ? 'flex-col' : ''}`}>
            <Link
              to="/settings?tab=account"
              aria-current={here('/settings?tab=account') ? 'page' : undefined}
              aria-label={t('nav.yourAccount', { name })}
              {...tipFor(t('nav.yourAccount', { name }))}
              className={`flex min-w-0 flex-1 items-center gap-3 rounded-md py-2 text-sm font-medium transition ${narrow ? 'px-2' : 'px-3'} ${look(here('/settings?tab=account'))}`}
            >
              <UserRound className="size-4.5 shrink-0" aria-hidden />
              {!narrow && <span className="truncate">{name}</span>}
            </Link>
            <button
              onClick={signOut}
              className="flex shrink-0 items-center rounded-md p-2 text-muted hover:bg-line/50 hover:text-ink"
              aria-label={t('nav.signOutName', { name })}
              {...(narrow ? tipFor(t('nav.signOutName', { name })) : { title: t('nav.signOutName', { name }) })}
            >
              <LogOut className="size-4.5 shrink-0" aria-hidden />
            </button>
          </div>
          {wide && (
            <button
              onClick={toggle}
              className={`flex items-center gap-3 rounded-md py-2 text-sm text-muted hover:bg-line/50 hover:text-ink ${narrow ? 'px-2' : 'px-3'}`}
              aria-label={narrow ? t('nav.showNames') : t('nav.iconsOnly')}
              aria-pressed={narrow}
              {...tipFor(t('nav.showNames'))}
            >
              {narrow ? <PanelLeftOpen className="size-4.5 shrink-0" aria-hidden /> : <PanelLeftClose className="size-4.5 shrink-0" aria-hidden />}
              {!narrow && t('nav.iconsOnly')}
            </button>
          )}
          {!narrow && <div className="px-3 pt-1 text-xs text-muted">{t('nav.version', { version: status.version })}</div>}
        </div>
      </div>
      <div className="perforated-y" aria-hidden />
      {narrow && tip && (
        <span
          aria-hidden
          style={{ top: tip.top, left: tip.left }}
          className="pointer-events-none fixed z-50 -translate-y-1/2 whitespace-nowrap rounded-md bg-ink px-2 py-1 text-xs font-medium text-paper shadow-md rtl:-translate-x-full"
        >
          {tip.text}
        </span>
      )}
    </aside>
  )
}

// PhoneNav is the menu that folds down from the header on a phone.
// Signing out sits at its end, away from the menu button, so a tap that
// misses the menu doesn't sign anyone out.
export function PhoneNav({ nav, status, signOut }: { nav: Nav; status: Status; signOut: () => void }) {
  const { t } = useTranslation()
  const here = useHere()
  const row = 'flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-start text-base font-medium'
  const look = (on: boolean) => (on ? 'bg-ink text-paper' : 'text-ink hover:bg-line/50')
  const name = status.user?.username ?? ''
  return (
    <nav id="mobile-nav" className="flex max-h-[calc(100dvh-4rem)] flex-col overflow-y-auto border-t border-line px-4 py-2 md:hidden" aria-label={t('nav.main')}>
      {nav.groups.map((g) => (
        <div key={g.label} className="py-1">
          <div className="px-3 pb-1 pt-2 text-xs font-semibold uppercase tracking-wider text-muted">{t(g.label)}</div>
          <ul className="flex flex-col gap-1">
            {g.items.map(({ to, label, icon: Icon, also }) => (
              <li key={to}>
                <Link to={to} aria-current={here(to, also) ? 'page' : undefined} className={`${row} ${look(here(to, also))}`}>
                  <Icon className="size-5" aria-hidden />
                  {t(label)}
                  <Badge to={to} nav={nav} />
                </Link>
              </li>
            ))}
          </ul>
        </div>
      ))}
      <div className="mt-1 flex flex-col gap-1 border-t border-line py-2">
        <Link to="/settings" aria-current={here('/settings') ? 'page' : undefined} className={`${row} ${look(here('/settings'))}`}>
          <Cog className="size-5" aria-hidden />
          {t('nav.settings')}
        </Link>
        <Link to="/settings?tab=account" aria-current={here('/settings?tab=account') ? 'page' : undefined} className={`${row} ${look(here('/settings?tab=account'))}`}>
          <UserRound className="size-5" aria-hidden />
          {t('nav.yourAccount', { name })}
        </Link>
        <button onClick={signOut} className={`${row} text-ink hover:bg-line/50`}>
          <LogOut className="size-5" aria-hidden />
          {t('nav.signOutName', { name })}
        </button>
        <div className="px-3 pt-1 text-xs text-muted">{t('nav.version', { version: status.version })}</div>
      </div>
    </nav>
  )
}
