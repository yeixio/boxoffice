import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { LogOut, Menu, X } from 'lucide-react'
import { get, post, type Status } from './api'
import { Spinner } from './ui'
import { Tray } from './Tray'
import { Logo, PhoneNav, Sidebar, useNav } from './Nav'
import { SignIn } from './pages/SignIn'
import { Discover, DiscoverRowPage } from './pages/Discover'
import { Movies } from './pages/Movies'
import { MovieDetail } from './pages/MovieDetail'
import { Shows } from './pages/Shows'
import { ShowDetail } from './pages/ShowDetail'
import { Music } from './pages/Music'
import { AuthorPage, Books } from './pages/Books'
import { ArtistDetail } from './pages/ArtistDetail'
import { Calendar } from './pages/Calendar'
import { Activity } from './pages/Activity'
import { Downloads } from './pages/Downloads'
import { Import } from './pages/Import'
import { Backups } from './pages/Backups'
import { Settings } from './pages/Settings'
import { Health } from './pages/Health'
import { Requests } from './pages/Requests'

export function App() {
  const status = useQuery({ queryKey: ['status'], queryFn: () => get<Status>('/status') })
  if (status.isPending) return <Spinner />
  if (status.isError) return <p className="p-8 text-bad">Box Office isn't answering: {status.error.message}</p>
  if (!status.data.setup || !status.data.user) return <SignIn firstRun={!status.data.setup} />
  return <Shell status={status.data} />
}

function Shell({ status }: { status: Status }) {
  const qc = useQueryClient()
  const nav = useNav(status)
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
    <div className="min-h-screen md:flex">
      <Sidebar status={status} nav={nav} signOut={signOut} />
      <div className="min-w-0 flex-1">
      <header className="sticky top-0 z-20 bg-paper/95 backdrop-blur md:hidden">
        <div className="flex items-center gap-x-3 px-4 py-3">
          <Logo />
          <div className="flex-1" />
          <Tray />
          <button
            onClick={() => setMenu(!menu)}
            className="relative flex items-center rounded-md p-1.5 text-muted hover:text-ink"
            aria-label={menu ? 'Close menu' : 'Menu'}
            aria-expanded={menu}
            aria-controls="mobile-nav"
          >
            {menu ? <X className="size-5" aria-hidden /> : <Menu className="size-5" aria-hidden />}
            {nav.problems > 0 && !menu && <span className="absolute right-0.5 top-0.5 size-2 rounded-full bg-marquee" aria-hidden />}
          </button>
          <button onClick={signOut} className="flex items-center gap-1.5 text-sm text-muted hover:text-ink" title={`Signed in as ${status.user?.username}`} aria-label="Sign out">
            <LogOut className="size-4" aria-hidden />
            <span className="hidden sm:inline">Sign out</span>
          </button>
        </div>
        {menu && <PhoneNav nav={nav} />}
        <div className="perforated" aria-hidden />
      </header>
      <main className="mx-auto max-w-7xl px-4 py-8 md:px-8">
        <Routes>
          <Route path="/" element={<Navigate to="/discover" replace />} />
          <Route path="/discover" element={<Discover />} />
          <Route path="/discover/:id" element={<DiscoverRowPage />} />
          <Route path="/movies" element={<Movies />} />
          <Route path="/movies/:id" element={<MovieDetail />} />
          <Route path="/shows" element={<Shows />} />
          <Route path="/shows/:id" element={<ShowDetail />} />
          <Route path="/music" element={<Music />} />
          <Route path="/music/:id" element={<ArtistDetail />} />
          <Route path="/books" element={<Books key="ebook" format="ebook" />} />
          <Route path="/books/:id" element={<AuthorPage key="ebook" format="ebook" />} />
          <Route path="/audiobooks" element={<Books key="audiobook" format="audiobook" />} />
          <Route path="/audiobooks/:id" element={<AuthorPage key="audiobook" format="audiobook" />} />
          <Route path="/calendar" element={<Calendar />} />
          <Route path="/requests" element={<Requests />} />
          <Route path="/downloads" element={<Downloads />} />
          <Route path="/activity" element={<Activity />} />
          <Route path="/health" element={<Health />} />
          <Route path="/import" element={<Import />} />
          <Route path="/backups" element={<Backups />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="*" element={<Navigate to="/discover" replace />} />
        </Routes>
      </main>
      <footer className="mx-auto max-w-7xl px-4 pb-8 text-xs text-muted md:hidden">Box Office {status.version}</footer>
      </div>
    </div>
  )
}
