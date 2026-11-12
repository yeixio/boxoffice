import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState, type ComponentType } from 'react'
import { NavLink } from 'react-router-dom'
import {
  CalendarDays,
  Clapperboard,
  Compass,
  DatabaseBackup,
  Download as DownloadIcon,
  Film,
  Gamepad2,
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
} from 'lucide-react'
import { get, type MediaTypeKey, type Settings as SettingsData, type Status } from './api'
import { Tray } from './Tray'
import { speed, useDownloads } from './pages/Downloads'
import { needsAttention, useHealth } from './pages/Health'
import { useRequestCounts } from './pages/Requests'

// label and a group's label are catalog keys.
type Item = { to: string; label: string; icon: ComponentType<{ className?: string }>; type?: MediaTypeKey }
type Group = { label: string; items: Item[] }

// The sections, grouped so a long list still reads at a glance. A section
// with a media type shows only once that type is turned on in Settings, so
// audiobooks, ebooks, comics and games slot in here the same way as music.
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
      { to: '/calendar', label: 'nav.calendar', icon: CalendarDays },
    ],
  },
  {
    label: 'nav.status',
    items: [
      { to: '/requests', label: 'nav.requests', icon: Inbox },
      { to: '/downloads', label: 'nav.downloads', icon: DownloadIcon },
      { to: '/activity', label: 'nav.activity', icon: History },
      { to: '/health', label: 'nav.health', icon: HeartPulse },
    ],
  },
  {
    label: 'nav.setup',
    items: [
      { to: '/import', label: 'nav.import', icon: Truck },
      { to: '/backups', label: 'nav.backups', icon: DatabaseBackup },
      { to: '/settings', label: 'nav.settings', icon: Cog },
    ],
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
  return { groups, problems, rate, waiting }
}

type Nav = ReturnType<typeof useNav>

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
      {!compact && <span className={`truncate font-display font-bold uppercase ${small ? 'text-base tracking-[0.14em]' : 'text-lg tracking-[0.18em]'}`}>Box Office</span>}
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
                {g.items.map(({ to, label, icon: Icon }) => (
                  <li key={to}>
                    <NavLink
                      to={to}
                      title={narrow ? t(label) : undefined}
                      aria-label={narrow ? t(label) : undefined}
                      className={({ isActive }) =>
                        `relative flex items-center gap-3 rounded-md py-2 text-sm font-medium transition ${narrow ? 'justify-center px-2' : 'px-3'} ${
                          isActive ? 'bg-ink text-paper' : 'text-muted hover:bg-line/50 hover:text-ink'
                        }`
                      }
                    >
                      <Icon className="size-4.5 shrink-0" aria-hidden />
                      {!narrow && <span className="truncate">{t(label)}</span>}
                      <Badge to={to} nav={nav} dot={narrow} />
                    </NavLink>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
        <div className={`flex flex-col gap-0.5 border-t border-line px-3 py-3 ${narrow ? 'items-center' : ''}`}>
          <button
            onClick={signOut}
            className={`flex items-center gap-3 rounded-md py-2 text-sm text-muted hover:bg-line/50 hover:text-ink ${narrow ? 'px-2' : 'px-3'}`}
            title={t('nav.signedInAs', { name: status.user?.username })}
            aria-label={t('nav.signOut')}
          >
            <LogOut className="size-4.5 shrink-0" aria-hidden />
            {!narrow && <span className="truncate">{t('nav.signOutName', { name: status.user?.username })}</span>}
          </button>
          {wide && (
            <button
              onClick={toggle}
              className={`flex items-center gap-3 rounded-md py-2 text-sm text-muted hover:bg-line/50 hover:text-ink ${narrow ? 'px-2' : 'px-3'}`}
              title={narrow ? t('nav.showNames') : t('nav.iconsOnly')}
              aria-label={narrow ? t('nav.showNames') : t('nav.iconsOnly')}
              aria-pressed={narrow}
            >
              {narrow ? <PanelLeftOpen className="size-4.5 shrink-0" aria-hidden /> : <PanelLeftClose className="size-4.5 shrink-0" aria-hidden />}
              {!narrow && t('nav.iconsOnly')}
            </button>
          )}
          {!narrow && <div className="px-3 pt-1 text-xs text-muted">{t('nav.version', { version: status.version })}</div>}
        </div>
      </div>
      <div className="perforated-y" aria-hidden />
    </aside>
  )
}

// PhoneNav is the menu that folds down from the header on a phone.
export function PhoneNav({ nav }: { nav: Nav }) {
  const { t } = useTranslation()
  return (
    <nav id="mobile-nav" className="flex max-h-[calc(100dvh-4rem)] flex-col overflow-y-auto border-t border-line px-4 py-2 md:hidden" aria-label={t('nav.main')}>
      {nav.groups.map((g) => (
        <div key={g.label} className="py-1">
          <div className="px-3 pb-1 pt-2 text-xs font-semibold uppercase tracking-wider text-muted">{t(g.label)}</div>
          <ul className="flex flex-col gap-1">
            {g.items.map(({ to, label, icon: Icon }) => (
              <li key={to}>
                <NavLink
                  to={to}
                  className={({ isActive }) => `flex items-center gap-3 rounded-md px-3 py-2.5 text-base font-medium ${isActive ? 'bg-ink text-paper' : 'text-ink hover:bg-line/50'}`}
                >
                  <Icon className="size-5" aria-hidden />
                  {t(label)}
                  <Badge to={to} nav={nav} />
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  )
}
