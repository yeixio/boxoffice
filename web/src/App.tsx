import { useQuery, useQueryClient } from '@tanstack/react-query'
import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { CalendarDays, Clapperboard, DatabaseBackup, Film, History, LogOut, Settings as Cog, Truck, Tv } from 'lucide-react'
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

export function App() {
  const status = useQuery({ queryKey: ['status'], queryFn: () => get<Status>('/status') })
  if (status.isPending) return <Spinner />
  if (status.isError) return <p className="p-8 text-bad">Box Office isn't answering: {status.error.message}</p>
  if (!status.data.setup || !status.data.user) return <SignIn firstRun={!status.data.setup} />
  return <Shell status={status.data} />
}

const nav = [
  { to: '/movies', label: 'Movies', icon: Film },
  { to: '/shows', label: 'Shows', icon: Tv },
  { to: '/calendar', label: 'Coming up', icon: CalendarDays },
  { to: '/activity', label: 'Activity', icon: History },
  { to: '/import', label: 'Move in', icon: Truck },
  { to: '/backups', label: 'Backups', icon: DatabaseBackup },
  { to: '/settings', label: 'Settings', icon: Cog },
]

function Shell({ status }: { status: Status }) {
  const qc = useQueryClient()
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
          <nav className="-mx-1 flex flex-1 gap-1 overflow-x-auto" aria-label="Main">
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
              </NavLink>
            ))}
          </nav>
          <Tray />
          <button onClick={signOut} className="flex items-center gap-1.5 text-sm text-muted hover:text-ink" title={`Signed in as ${status.user?.username}`} aria-label="Sign out">
            <LogOut className="size-4" aria-hidden />
            <span className="hidden sm:inline">Sign out</span>
          </button>
        </div>
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
