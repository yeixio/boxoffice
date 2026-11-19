import { LanguageSync } from './i18n/sync'
import { useTranslation } from 'react-i18next'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { lazy, Suspense, useCallback, useEffect, useState, type ComponentType } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { Menu, X } from 'lucide-react'
import { get, onSignedOut, post, put, type Status } from './api'
import { Spinner } from './ui'
import { Tray } from './Tray'
import { PageBoundary, SkipLink } from './Broken'
import { Logo, PhoneNav, Sidebar, useNav } from './Nav'
import { SignIn } from './pages/SignIn'
// Each page is fetched when it's first opened, so the first load is only
// what every page shares. A page that can't be fetched (Box Office was
// updated since this tab loaded, and its files have new names) reloads the
// tab once to get the new ones.
function page<M, K extends keyof M>(load: () => Promise<M>, name: K) {
  return lazy(async () => {
    try {
      const m = await load()
      try {
        sessionStorage.removeItem('boxoffice-reloaded')
      } catch {
        // No storage: nothing to forget.
      }
      return { default: m[name] as M[K] & ComponentType<any> } // eslint-disable-line @typescript-eslint/no-explicit-any
    } catch (err) {
      let again = false
      try {
        again = !sessionStorage.getItem('boxoffice-reloaded')
        sessionStorage.setItem('boxoffice-reloaded', '1')
      } catch {
        // No storage: don't risk reloading over and over.
      }
      if (again) {
        location.reload()
        return new Promise<never>(() => {})
      }
      throw err
    }
  })
}

const discover = () => import('./pages/Discover')
const Discover = page(discover, 'Discover')
const DiscoverRowPage = page(discover, 'DiscoverRowPage')
const Movies = page(() => import('./pages/Movies'), 'Movies')
const MovieDetail = page(() => import('./pages/MovieDetail'), 'MovieDetail')
const Shows = page(() => import('./pages/Shows'), 'Shows')
const ShowDetail = page(() => import('./pages/ShowDetail'), 'ShowDetail')
const Music = page(() => import('./pages/Music'), 'Music')
const books = () => import('./pages/Books')
const Books = page(books, 'Books')
const AuthorPage = page(books, 'AuthorPage')
const BookLink = page(books, 'BookLink')
const artist = () => import('./pages/ArtistDetail')
const AlbumLink = page(artist, 'AlbumLink')
const ArtistDetail = page(artist, 'ArtistDetail')
const AddGame = page(() => import('./pages/GameAdd'), 'AddGame')
const games = () => import('./pages/Games')
const Games = page(games, 'Games')
const GamePage = page(games, 'GamePage')
const GameSystemPage = page(games, 'GameSystemPage')
const photos = () => import('./pages/Photos')
const Photos = page(photos, 'Photos')
const AlbumPage = page(photos, 'AlbumPage')
const Calendar = page(() => import('./pages/Calendar'), 'Calendar')
const Activity = page(() => import('./pages/Activity'), 'Activity')
const Downloads = page(() => import('./pages/Downloads'), 'Downloads')
const Import = page(() => import('./pages/Import'), 'Import')
const ImportFiles = page(() => import('./pages/ImportFiles'), 'ImportFiles')
const Backups = page(() => import('./pages/Backups'), 'Backups')
const Settings = page(() => import('./pages/Settings'), 'Settings')
const Problems = page(() => import('./pages/Problems'), 'Problems')
const Health = page(() => import('./pages/Health'), 'Health')
const Requests = page(() => import('./pages/Requests'), 'Requests')

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
      <SkipLink />
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
      <main id="content" tabIndex={-1} className="mx-auto max-w-7xl px-4 py-8 outline-none md:px-8">
        <PageBoundary at={location.pathname}>
        <Suspense fallback={<Spinner />}>
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
        </Suspense>
        </PageBoundary>
      </main>
      <footer className="mx-auto max-w-7xl px-4 pb-8 text-xs text-muted md:hidden">{t('nav.version', { version: status.version })}</footer>
      </div>
    </div>
  )
}
