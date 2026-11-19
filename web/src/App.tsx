import { LanguageSync } from './i18n/sync'
import { useTranslation } from 'react-i18next'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useState } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { Menu, X } from 'lucide-react'
import { get, onSignedOut, post, put, type Status } from './api'
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
import { AuthorPage, BookLink, Books } from './pages/Books'
import { AlbumLink, ArtistDetail } from './pages/ArtistDetail'
import { AddGame } from './pages/GameAdd'
import { GamePage, Games, GameSystemPage } from './pages/Games'
import { AlbumPage, Photos } from './pages/Photos'
import { Calendar } from './pages/Calendar'
import { Activity } from './pages/Activity'
import { Downloads } from './pages/Downloads'
import { Import } from './pages/Import'
import { ImportFiles } from './pages/ImportFiles'
import { Backups } from './pages/Backups'
import { Settings } from './pages/Settings'
import { Problems } from './pages/Problems'
import { Health } from './pages/Health'
import { Requests } from './pages/Requests'

export function App() {
  const { t } = useTranslation()
  const status = useQuery({ queryKey: ['status'], queryFn: () => get<Status>('/status') })
  const qc = useQueryClient()
  // When the session has run out, asking who's signed in again shows the
  // sign-in page; signing in comes back to the same page.
  useEffect(() => {
    onSignedOut(() => {
      if (qc.getQueryData<Status>(['status'])?.user) qc.setQueryData<Status>(['status'], (old) => old && { ...old, user: undefined })
    })
    return () => onSignedOut(() => {})
  }, [qc])
  if (status.isPending) return <Spinner />
  if (status.isError) return <p className="p-8 text-bad">{t('app.notAnswering', { error: status.error.message })}</p>
  if (!status.data.setup || !status.data.user) return <SignIn firstRun={!status.data.setup} />
  return <Shell status={status.data} />
}

function Shell({ status }: { status: Status }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const nav = useNav(status)
  // On a phone the sections fold into a menu, closed again on moving.
  const [menu, setMenu] = useState(false)
  const location = useLocation()
  useEffect(() => setMenu(false), [location.pathname, location.search])
  // A language picked on the sign-in page becomes the account's.
  const adopt = useCallback(
    (language: string) => void put('/me/language', { language }).then(() => qc.invalidateQueries({ queryKey: ['status'] })),
    [qc],
  )
  const signOut = async () => {
    await post('/logout')
    // Show sign-in at once, and forget what this person was looking at.
    qc.setQueryData<Status>(['status'], (old) => old && { ...old, user: undefined })
    qc.removeQueries({ predicate: (q) => q.queryKey[0] !== 'status' })
  }
  return (
    <div className="min-h-screen md:flex">
      <LanguageSync saved={status.user?.language ?? ''} adopt={adopt} />
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
            aria-label={menu ? t('app.closeMenu') : t('app.menu')}
            aria-expanded={menu}
            aria-controls="mobile-nav"
          >
            {menu ? <X className="size-5" aria-hidden /> : <Menu className="size-5" aria-hidden />}
            {nav.problems > 0 && !menu && <span className="absolute end-0.5 top-0.5 size-2 rounded-full bg-marquee" aria-hidden />}
          </button>
        </div>
        {menu && <PhoneNav nav={nav} status={status} signOut={signOut} />}
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
          <Route path="/music/album/:id" element={<AlbumLink />} />
          <Route path="/music/:id" element={<ArtistDetail />} />
          <Route path="/books" element={<Books key="ebook" format="ebook" />} />
          <Route path="/books/:id" element={<AuthorPage key="ebook" format="ebook" />} />
          <Route path="/books/book/:id" element={<BookLink format="ebook" />} />
          <Route path="/audiobooks/book/:id" element={<BookLink format="audiobook" />} />
          <Route path="/audiobooks" element={<Books key="audiobook" format="audiobook" />} />
          <Route path="/audiobooks/:id" element={<AuthorPage key="audiobook" format="audiobook" />} />
          <Route path="/games" element={<Games />} />
          <Route path="/games/add" element={<AddGame />} />
          <Route path="/games/:id" element={<GameSystemPage />} />
          <Route path="/games/game/:id" element={<GamePage />} />
          <Route path="/photos" element={<Photos />} />
          <Route path="/photos/albums/:id" element={<AlbumPage />} />
          <Route path="/calendar" element={<Calendar />} />
          <Route path="/requests" element={<Requests />} />
          <Route path="/downloads" element={<Downloads />} />
          <Route path="/activity" element={<Activity />} />
          <Route path="/health" element={<Health />} />
          <Route path="/import" element={<Import />} />
          <Route path="/import-files" element={<ImportFiles />} />
          <Route path="/backups" element={<Backups />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/problems" element={<Problems />} />
          <Route path="*" element={<Navigate to="/discover" replace />} />
        </Routes>
      </main>
      <footer className="mx-auto max-w-7xl px-4 pb-8 text-xs text-muted md:hidden">{t('nav.version', { version: status.version })}</footer>
      </div>
    </div>
  )
}
