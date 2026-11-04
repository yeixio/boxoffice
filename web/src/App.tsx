import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { NavLink, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { CalendarDays, Clapperboard, DatabaseBackup, Film, HeartPulse, History, LogOut, Menu, Settings as Cog, Truck, Tv, X } from 'lucide-react'
import { get, post, type Status } from './api'
import { Spinner } from './ui'
import { Tray } from './Tray'
import { SignIn } from './pages/SignIn'
import { Movies } from './pages/Movies'
import { MovieDetail } from './pages/MovieDetail'
import { Shows } from './pages/Shows'
import { ShowDetail } from './pages/ShowDetail'
import { Calendar } from './pages/Calendar'
import { Activity } from './pages/Activity'
import { Import } from './pages/Import'
import { Backups } from './pages/Backups'
import { Settings } from './pages/Settings'
import { Health, needsAttention, useHealth } from './pages/Health'

export function App() {
  const status = useQuery({ queryKey: ['status'], queryFn: () => get<Status>('/status') })
  if (status.isPending) return <Spinner />
  if (status.isError) return <p className="p-8 text-bad">Box Office isn't answering: {status.error.message}</p>
  if (!status.data.setup || !status.data.user) return <SignIn firstRun={!status.data.setup} />
  return <Shell status={status.data} />
}

// Move in is for setting up: it leaves the menu once the admin has finished
// moving in, for Settings.
const allNav = [
  { to: '/movies', label: 'Movies', icon: Film },
  { to: '/shows', label: 'Shows', icon: Tv },
  { to: '/calendar', label: 'Coming up', icon: CalendarDays },
  { to: '/activity', label: 'Activity', icon: History },
  { to: '/health', label: 'Health', icon: HeartPulse },
  { to: '/import', label: 'Move in', icon: Truck },
  { to: '/backups', label: 'Backups', icon: DatabaseBackup },
  { to: '/settings', label: 'Settings', icon: Cog },
]

function Shell({ status }: { status: Status }) {
  const qc = useQueryClient()
  const nav = allNav.filter((n) => n.to !== '/import' || !status.movedIn)
  // Health shows how many problems need fixing.
  const problems = needsAttention(useHealth().data)
  const badge = (to: string) =>
    to === '/health' && problems > 0 ? (
      <span className="rounded-full bg-marquee px-1.5 text-xs font-semibold leading-5 text-marquee-ink" aria-label={`${problems} to fix`}>
        {problems}
      </span>
    ) : null
  // On a phone the sections fold into a menu, closed again on moving.
  const [menu, setMenu] = useState(false)
  const location = useLocation()
  useEffect(() => setMenu(false), [location.pathname, location.search])
  const signOut = async () => {
    await post('/logout')
    qc.clear()
    qc.invalidateQueries({ queryKey: ['status'] })
  }
  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-20 bg-paper/95 backdrop-blur">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center gap-x-6 gap-y-2 px-4 py-3">
          <NavLink to="/movies" className="flex items-center gap-2">
            <span className="flex size-8 items-center justify-center rounded-md bg-marquee text-marquee-ink">
              <Clapperboard className="size-4.5" aria-hidden />
            </span>
            <span className="font-display text-lg font-bold uppercase tracking-[0.18em]">Box Office</span>
          </NavLink>
          <nav className="-mx-1 hidden flex-1 gap-1 overflow-x-auto md:flex" aria-label="Main">
            {nav.map(({ to, label, icon: Icon }) => (
              <NavLink
                key={to}
                to={to}
                className={({ isActive }) =>
                  `flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm font-medium transition ${isActive ? 'bg-ink text-paper' : 'text-muted hover:text-ink'}`
                }
              >
                <Icon className="size-4" aria-hidden />
                {label}
                {badge(to)}
              </NavLink>
            ))}
          </nav>
          <div className="flex-1 md:hidden" />
          <Tray />
          <button
            onClick={() => setMenu(!menu)}
            className="relative flex items-center rounded-md p-1.5 text-muted hover:text-ink md:hidden"
            aria-label={menu ? 'Close menu' : 'Menu'}
            aria-expanded={menu}
            aria-controls="mobile-nav"
          >
            {menu ? <X className="size-5" aria-hidden /> : <Menu className="size-5" aria-hidden />}
            {problems > 0 && !menu && <span className="absolute right-0.5 top-0.5 size-2 rounded-full bg-marquee" aria-hidden />}
          </button>
          <button onClick={signOut} className="flex items-center gap-1.5 text-sm text-muted hover:text-ink" title={`Signed in as ${status.user?.username}`} aria-label="Sign out">
            <LogOut className="size-4" aria-hidden />
            <span className="hidden sm:inline">Sign out</span>
          </button>
        </div>
        {menu && (
          <nav id="mobile-nav" className="mx-auto flex max-w-7xl flex-col gap-1 border-t border-line px-4 py-3 md:hidden" aria-label="Main">
            {nav.map(({ to, label, icon: Icon }) => (
              <NavLink
                key={to}
                to={to}
                className={({ isActive }) => `flex items-center gap-3 rounded-md px-3 py-2.5 text-base font-medium ${isActive ? 'bg-ink text-paper' : 'text-ink hover:bg-line/50'}`}
              >
                <Icon className="size-5" aria-hidden />
                {label}
                {badge(to)}
              </NavLink>
            ))}
          </nav>
        )}
        <div className="perforated" aria-hidden />
      </header>
      <main className="mx-auto max-w-7xl px-4 py-8">
        <Routes>
          <Route path="/" element={<Navigate to="/movies" replace />} />
          <Route path="/movies" element={<Movies />} />
          <Route path="/movies/:id" element={<MovieDetail />} />
          <Route path="/shows" element={<Shows />} />
          <Route path="/shows/:id" element={<ShowDetail />} />
          <Route path="/calendar" element={<Calendar />} />
          <Route path="/activity" element={<Activity />} />
          <Route path="/health" element={<Health />} />
          <Route path="/import" element={<Import />} />
          <Route path="/backups" element={<Backups />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="*" element={<Navigate to="/movies" replace />} />
        </Routes>
      </main>
      <footer className="mx-auto max-w-7xl px-4 pb-8 text-xs text-muted">Box Office {status.version}</footer>
    </div>
  )
}
