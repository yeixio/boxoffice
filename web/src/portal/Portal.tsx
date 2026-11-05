import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, NavLink, Navigate, Route, Routes, useParams } from 'react-router-dom'
import { Check, ChevronRight, Clock, Film, Flag, LoaderCircle, LogOut, Plus, Search, Star, Ticket, UserRound } from 'lucide-react'
import { ApiError } from '../api'
import { Button, Empty, ErrorNote, Field, Input, Modal, Poster, Select, Spinner } from '../ui'
import { papi, pget, ppost, problemLabel, problems, stateText, type DiscoverHome, type DiscoverPage, type DiscoverTitle, type Issue, type Member, type Problem, type Request, type Site } from './papi'

// The request site: where the people who watch ask for movies and shows,
// follow what they asked for, and report problems. It has the admin's name,
// colour and logo, and none of the admin UI.

const useSite = () => useQuery({ queryKey: ['portal', 'site'], queryFn: () => pget<Site>('/site') })

export function Portal() {
  const site = useSite()
  useBrand(site.data)
  if (site.isPending) return <Spinner />
  if (site.isError) return <p className="p-8 text-bad">This site isn't answering: {site.error.message}</p>
  if (!site.data.enabled) {
    return (
      <Centered site={site.data}>
        <p className="text-center text-muted">This request site isn't open right now.</p>
      </Centered>
    )
  }
  if (!site.data.member) return <PortalSignIn site={site.data} />
  return <Shell site={site.data} member={site.data.member} />
}

// useBrand puts the site's name and colour on the page.
function useBrand(site?: Site) {
  useEffect(() => {
    if (!site) return
    document.title = site.name
    const root = document.documentElement
    if (site.accent) {
      root.style.setProperty('--marquee', site.accent)
      root.style.setProperty('--marquee-ink', inkFor(site.accent))
    } else {
      root.style.removeProperty('--marquee')
      root.style.removeProperty('--marquee-ink')
    }
  }, [site])
}

// inkFor is a colour that reads on top of the accent: near-black on light
// colours, white on dark ones.
export function inkFor(hex: string): string {
  const n = parseInt(hex.slice(1), 16)
  const lin = (c: number) => {
    const v = c / 255
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
  }
  const l = 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255)
  return l > 0.4 ? '#15120f' : '#ffffff'
}

function Brand({ site, large }: { site: Site; large?: boolean }) {
  return (
    <Link to="/" className="flex min-w-0 items-center gap-2.5">
      {site.hasLogo ? (
        <img src="/api/portal/logo" alt="" className={`${large ? 'h-14' : 'h-8'} w-auto max-w-40 shrink-0 object-contain`} />
      ) : (
        <span className={`flex ${large ? 'size-14 rounded-xl' : 'size-8 rounded-md'} shrink-0 items-center justify-center bg-marquee text-marquee-ink`}>
          <Ticket className={large ? 'size-7' : 'size-4.5'} aria-hidden />
        </span>
      )}
      {!large && <span className="truncate font-display text-lg font-bold">{site.name}</span>}
    </Link>
  )
}

function Centered({ site, children }: { site: Site; children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <Brand site={site} large />
          <h1 className="font-display text-3xl font-bold">{site.name}</h1>
          {site.welcome && <p className="whitespace-pre-line text-muted">{site.welcome}</p>}
        </div>
        {children}
      </div>
    </div>
  )
}

// PortalSignIn signs someone in with an account here or their Matinee
// account, or lets them make an account when the admin allows it.
function PortalSignIn({ site }: { site: Site }) {
  const qc = useQueryClient()
  const [mode, setMode] = useState<'matinee' | 'local' | 'signup'>(site.matinee ? 'matinee' : 'local')
  const [username, setUsername] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      if (mode === 'signup') await ppost('/signup', { username, displayName: name, password })
      else await ppost('/login', { username, password, matinee: mode === 'matinee' })
      await qc.invalidateQueries({ queryKey: ['portal'] })
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  const choices = [
    site.matinee && { value: 'matinee' as const, label: 'Matinee account' },
    site.localSignIn && { value: 'local' as const, label: 'Account here' },
  ].filter((x) => !!x)
  return (
    <Centered site={site}>
      <form onSubmit={submit} className="flex flex-col gap-4 rounded-xl border border-line bg-card p-6">
        {mode !== 'signup' && choices.length > 1 && (
          <div className="grid grid-cols-2 gap-1 rounded-lg bg-paper p-1" role="tablist" aria-label="Sign in with">
            {choices.map((c) => (
              <button
                key={c.value}
                type="button"
                role="tab"
                aria-selected={mode === c.value}
                onClick={() => setMode(c.value)}
                className={`rounded-md px-2 py-1.5 text-sm font-medium transition ${mode === c.value ? 'bg-card shadow-sm' : 'text-muted hover:text-ink'}`}
              >
                {c.label}
              </button>
            ))}
          </div>
        )}
        {mode === 'matinee' && <p className="text-sm text-muted">Use the username and password you watch with in Matinee.</p>}
        {mode === 'signup' && <p className="text-sm text-muted">Make an account to ask for movies and shows.</p>}
        <Field label="Username">{(id) => <Input id={id} autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus />}</Field>
        {mode === 'signup' && (
          <Field label="Your name" hint="What the admins see. Leave it empty to use your username.">
            {(id) => <Input id={id} autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />}
          </Field>
        )}
        <Field label="Password" hint={mode === 'signup' ? 'At least 8 characters.' : undefined}>
          {(id) => <Input id={id} type="password" autoComplete={mode === 'signup' ? 'new-password' : 'current-password'} value={password} onChange={(e) => setPassword(e.target.value)} required />}
        </Field>
        <ErrorNote error={error} />
        <Button variant="primary" type="submit" busy={busy}>
          {mode === 'signup' ? 'Make my account' : 'Sign in'}
        </Button>
        {site.signUp && (
          <button type="button" className="text-sm font-medium text-marquee" onClick={() => setMode(mode === 'signup' ? (site.matinee ? 'matinee' : 'local') : 'signup')}>
            {mode === 'signup' ? 'I have an account' : 'New here? Make an account'}
          </button>
        )}
      </form>
    </Centered>
  )
}

function Shell({ site, member }: { site: Site; member: Member }) {
  const [account, setAccount] = useState(false)
  const tabs = [
    { to: '/', label: 'Discover', end: true },
    { to: '/requests', label: 'My requests' },
    { to: '/problems', label: 'Problems' },
  ]
  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-20 border-b border-line bg-paper/95 backdrop-blur">
        <div className="mx-auto flex max-w-6xl items-center gap-4 px-4 py-3 md:px-8">
          <Brand site={site} />
          <nav className="ml-4 hidden gap-1 sm:flex" aria-label="Sections">
            {tabs.map((t) => (
              <NavLink key={t.to} to={t.to} end={t.end} className={({ isActive }) => `rounded-md px-3 py-1.5 text-sm font-medium ${isActive ? 'bg-marquee/10 text-marquee' : 'text-muted hover:text-ink'}`}>
                {t.label}
              </NavLink>
            ))}
          </nav>
          <div className="flex-1" />
          <button onClick={() => setAccount(true)} className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-sm text-muted hover:text-ink" aria-label="Your account">
            <UserRound className="size-4" aria-hidden />
            <span className="hidden max-w-32 truncate sm:inline">{member.name}</span>
          </button>
        </div>
        <nav className="flex gap-1 overflow-x-auto px-4 pb-2 sm:hidden" aria-label="Sections">
          {tabs.map((t) => (
            <NavLink key={t.to} to={t.to} end={t.end} className={({ isActive }) => `shrink-0 rounded-md px-3 py-1.5 text-sm font-medium ${isActive ? 'bg-marquee/10 text-marquee' : 'text-muted'}`}>
              {t.label}
            </NavLink>
          ))}
        </nav>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-8 md:px-8">
        <Routes>
          <Route path="/" element={<Home site={site} member={member} />} />
          <Route path="/discover/:id" element={<RowPage />} />
          <Route path="/requests" element={<MyRequests />} />
          <Route path="/problems" element={<MyProblems />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
      {account && <Account member={member} onClose={() => setAccount(false)} />}
    </div>
  )
}

const useMine = () => useQuery({ queryKey: ['portal', 'requests'], queryFn: () => pget<Request[]>('/requests') })

function Home({ site, member }: { site: Site; member: Member }) {
  const home = useQuery({ queryKey: ['portal', 'discover'], queryFn: () => pget<DiscoverHome>('/discover'), staleTime: 5 * 60_000 })
  const [term, setTerm] = useState('')
  const [q, setQ] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setQ(term.trim()), 350)
    return () => clearTimeout(t)
  }, [term])
  return (
    <>
      <section className="mb-8 flex flex-col gap-4">
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight">Hi, {member.name}</h1>
          <p className="mt-1 whitespace-pre-line text-muted">{site.welcome || 'Find a movie or show and ask for it. You can follow it under My requests.'}</p>
        </div>
        <div className="relative max-w-xl">
          <Search className="pointer-events-none absolute left-3 top-3 size-4 text-muted" aria-hidden />
          <Input type="search" className="py-2.5 pl-9 text-base" placeholder="Find a movie or show" aria-label="Find a movie or show" value={term} onChange={(e) => setTerm(e.target.value)} />
        </div>
      </section>
      {q.length > 1 ? (
        <SearchResults q={q} />
      ) : (
        <>
          {home.isPending && <Spinner />}
          {home.error && <NotNow error={home.error} />}
          {home.data && (
            <div className="flex flex-col gap-10">
              {home.data.rows.map((r) => (
                <section key={r.id} aria-labelledby={`row-${r.id}`}>
                  <div className="mb-3 flex items-baseline justify-between gap-4">
                    <h2 id={`row-${r.id}`} className="font-display text-xl font-semibold">
                      {r.title}
                    </h2>
                    <Link to={`/discover/${r.id}`} className="flex shrink-0 items-center gap-0.5 text-sm font-medium text-marquee">
                      See all <ChevronRight className="size-4" aria-hidden />
                    </Link>
                  </div>
                  <ul className="-mx-4 flex snap-x scroll-px-4 gap-3 overflow-x-auto px-4 pb-2 md:-mx-8 md:scroll-px-8 md:px-8 [scrollbar-width:thin]">
                    {r.items.map((t) => (
                      <li key={`${t.kind}-${t.tmdbId}`} className="w-32 shrink-0 snap-start sm:w-36">
                        <Card t={t} />
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
              <Genres home={home.data} />
            </div>
          )}
        </>
      )}
    </>
  )
}

function NotNow({ error }: { error: unknown }) {
  if (error instanceof ApiError && error.status === 503) {
    return (
      <Empty title="Nothing to show yet">
        The admin hasn't finished setting this site up. Try again later.
      </Empty>
    )
  }
  return <ErrorNote error={error} />
}

function Genres({ home }: { home: DiscoverHome }) {
  const groups = [
    { title: 'Movie genres', kind: 'movie', genres: home.browse.movieGenres },
    { title: 'Show genres', kind: 'series', genres: home.browse.seriesGenres },
  ] as const
  return (
    <>
      {groups.map(
        (g) =>
          g.genres.length > 0 && (
            <section key={g.kind}>
              <h2 className="mb-3 font-display text-xl font-semibold">{g.title}</h2>
              <ul className="flex flex-wrap gap-2">
                {g.genres.map((genre) => (
                  <li key={genre.id}>
                    <Link to={`/discover/genre-${g.kind}-${genre.id}`} className="block rounded-full border border-line bg-card px-3.5 py-1.5 text-sm font-medium transition hover:border-marquee hover:text-marquee">
                      {genre.name}
                    </Link>
                  </li>
                ))}
              </ul>
            </section>
          ),
      )}
    </>
  )
}

function Grid({ items }: { items: DiscoverTitle[] }) {
  return (
    <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
      {items.map((t) => (
        <li key={`${t.kind}-${t.tmdbId}`}>
          <Card t={t} />
        </li>
      ))}
    </ul>
  )
}

function SearchResults({ q }: { q: string }) {
  const found = useQuery({ queryKey: ['portal', 'search', q], queryFn: () => pget<DiscoverTitle[]>(`/search?q=${encodeURIComponent(q)}`) })
  if (found.isPending) return <Spinner label="Looking" />
  if (found.error) return <NotNow error={found.error} />
  if (found.data.length === 0) return <p className="py-10 text-center text-sm text-muted">Nothing by that name.</p>
  const movies = found.data.filter((t) => t.kind === 'movie')
  const shows = found.data.filter((t) => t.kind === 'series')
  return (
    <div className="flex flex-col gap-10">
      {movies.length > 0 && (
        <section>
          <h2 className="mb-3 font-display text-xl font-semibold">Movies</h2>
          <Grid items={movies} />
        </section>
      )}
      {shows.length > 0 && (
        <section>
          <h2 className="mb-3 font-display text-xl font-semibold">Shows</h2>
          <Grid items={shows} />
        </section>
      )}
    </div>
  )
}

function RowPage() {
  const { id = '' } = useParams()
  const row = useInfiniteQuery({
    queryKey: ['portal', 'row', id],
    queryFn: ({ pageParam }) => pget<DiscoverPage>(`/discover/rows/${encodeURIComponent(id)}?page=${pageParam}`),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.more ? last.page + 1 : undefined),
    staleTime: 5 * 60_000,
  })
  const first = row.data?.pages[0]
  const seen = new Set<string>()
  const items = (row.data?.pages ?? []).flatMap((p) => p.items).filter((t) => {
    const k = `${t.kind}-${t.tmdbId}`
    if (seen.has(k)) return false
    seen.add(k)
    return true
  })
  return (
    <>
      <Link to="/" className="mb-2 inline-block text-sm text-muted hover:text-ink">
        ← Discover
      </Link>
      <h1 className="mb-6 font-display text-3xl font-bold tracking-tight">{first?.title ?? 'Discover'}</h1>
      {row.isPending && <Spinner />}
      {row.error && <NotNow error={row.error} />}
      <Grid items={items} />
      {row.hasNextPage && (
        <div className="mt-8 flex justify-center">
          <Button onClick={() => row.fetchNextPage()} busy={row.isFetchingNextPage}>
            Show more
          </Button>
        </div>
      )}
    </>
  )
}

// where says where a title stands for this person: in the library, on its
// way, asked for, or not here yet.
type Where = 'ready' | 'some' | 'coming' | 'asked' | 'declined' | 'none'

function whereOf(t: DiscoverTitle, mine?: Request): Where {
  if (t.status === 'available') return 'ready'
  if (t.status === 'partial') return 'some'
  if (t.status === 'wanted' || t.status === 'paused') return 'coming'
  if (mine?.status === 'pending') return 'asked'
  if (mine?.status === 'declined') return 'declined'
  return 'none'
}

const pill: Record<Exclude<Where, 'none'>, { label: string; look: string }> = {
  ready: { label: 'Ready to watch', look: 'bg-ok text-white' },
  some: { label: 'Some episodes', look: 'bg-gold text-black' },
  coming: { label: 'On its way', look: 'bg-ink/85 text-paper' },
  asked: { label: 'Requested', look: 'bg-marquee text-marquee-ink' },
  declined: { label: 'Declined', look: 'bg-ink/60 text-paper' },
}

function useMyRequest(t: DiscoverTitle) {
  const mine = useMine()
  return mine.data?.find((r) => r.kind === t.kind && r.tmdbId === t.tmdbId && r.status !== 'declined') ?? mine.data?.find((r) => r.kind === t.kind && r.tmdbId === t.tmdbId)
}

// Card is a poster: clicking it opens the title, and the button on a title
// that isn't here asks for it straight away.
function Card({ t }: { t: DiscoverTitle }) {
  const mine = useMyRequest(t)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const qc = useQueryClient()
  const where = whereOf(t, mine)
  const ask = async () => {
    setBusy(true)
    try {
      await ppost('/requests', { kind: t.kind, tmdbId: t.tmdbId })
      await qc.invalidateQueries({ queryKey: ['portal'] })
    } catch {
      setOpen(true)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="group relative">
      <button onClick={() => setOpen(true)} className="block w-full text-left" aria-label={`${t.title}${t.year ? ` (${t.year})` : ''}`}>
        <div className="relative">
          <Poster src={t.poster} title={t.title} className="transition group-hover:ring-2 group-hover:ring-marquee" />
          {where !== 'none' && (
            <span className={`absolute left-1.5 top-1.5 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-semibold shadow ${pill[where].look}`}>
              {where === 'ready' && <Check className="size-3" aria-hidden />}
              {where === 'asked' && <Clock className="size-3" aria-hidden />}
              {pill[where].label}
            </span>
          )}
        </div>
        <div className="mt-2 truncate text-sm font-medium">{t.title}</div>
        <div className="flex items-center gap-1.5 text-xs text-muted">
          {t.year ? <span>{t.year}</span> : null}
          {t.year ? <span aria-hidden>·</span> : null}
          <span>{t.kind === 'movie' ? 'Movie' : 'Show'}</span>
          {t.rating ? (
            <span className="ml-auto flex items-center gap-0.5 tabular-nums">
              <Star className="size-3" aria-hidden />
              {t.rating.toFixed(1)}
            </span>
          ) : null}
        </div>
      </button>
      {where === 'none' && (
        <button
          onClick={ask}
          disabled={busy}
          className="absolute right-1.5 top-1.5 flex size-8 items-center justify-center rounded-full bg-marquee text-marquee-ink shadow-md transition hover:scale-110 disabled:opacity-70"
          aria-label={`Request ${t.title}`}
          title={`Request ${t.title}`}
        >
          {busy ? <LoaderCircle className="size-4 animate-spin" aria-hidden /> : <Plus className="size-4.5" aria-hidden />}
        </button>
      )}
      {open && <Sheet t={t} mine={mine} where={where} onClose={() => setOpen(false)} />}
    </div>
  )
}

// Sheet is a title's card: what it's about, and asking for it, or how it's
// going, or reporting a problem with it.
function Sheet({ t, mine, where, onClose }: { t: DiscoverTitle; mine?: Request; where: Where; onClose: () => void }) {
  const qc = useQueryClient()
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [report, setReport] = useState(false)
  const ask = async () => {
    setBusy(true)
    setError(null)
    try {
      await ppost('/requests', { kind: t.kind, tmdbId: t.tmdbId, note })
      await qc.invalidateQueries({ queryKey: ['portal'] })
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  const cancel = async () => {
    if (!mine) return
    setBusy(true)
    setError(null)
    try {
      await papi('DELETE', `/requests/${mine.id}`)
      await qc.invalidateQueries({ queryKey: ['portal'] })
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  const when = t.date ? new Date(t.date + 'T00:00:00') : undefined
  const soon = when && when.getTime() > Date.now()
  return (
    <Modal title={t.title} onClose={onClose} wide>
      <div className="flex flex-col gap-5">
        {t.backdrop && <img src={t.backdrop} alt="" className="-mx-5 -mt-5 aspect-[21/9] w-[calc(100%+2.5rem)] max-w-none object-cover" />}
        <div className="flex gap-4">
          <Poster src={t.poster} title={t.title} className="hidden w-28 shrink-0 sm:block" />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-x-2 text-sm text-muted">
              <span>{t.kind === 'movie' ? 'Movie' : 'Show'}</span>
              {when && (
                <>
                  <span aria-hidden>·</span>
                  <span>
                    {soon ? (t.kind === 'movie' ? 'Out ' : 'Starts ') : t.kind === 'movie' ? 'Released ' : 'First aired '}
                    {when.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })}
                  </span>
                </>
              )}
              {t.rating ? (
                <>
                  <span aria-hidden>·</span>
                  <span className="flex items-center gap-0.5">
                    <Star className="size-3.5" aria-hidden /> {t.rating.toFixed(1)}
                  </span>
                </>
              ) : null}
            </div>
            {t.overview && <p className="mt-2 text-sm leading-relaxed">{t.overview}</p>}
          </div>
        </div>
        <ErrorNote error={error} />
        {(where === 'ready' || where === 'some') &&
          (report ? (
            <ReportForm t={t} onDone={onClose} />
          ) : (
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line px-4 py-3">
              <span className="text-sm">{where === 'ready' ? "It's in the library. Enjoy!" : 'Some of it is in the library, and the rest is on its way.'}</span>
              <Button variant="ghost" onClick={() => setReport(true)}>
                <Flag className="size-4" aria-hidden /> Report a problem
              </Button>
            </div>
          ))}
        {where === 'coming' && (
          <p className="rounded-lg border border-line px-4 py-3 text-sm">
            {mine && mine.status === 'approved' ? `${stateText(mine)}.` : "It's on its way. Nothing to ask for: it'll be in the library once it's found."}
          </p>
        )}
        {where === 'asked' && (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line px-4 py-3">
            <span className="text-sm">You asked for this. It's waiting for an admin to approve it.</span>
            <Button variant="ghost" busy={busy} onClick={cancel}>
              Take it back
            </Button>
          </div>
        )}
        {(where === 'none' || where === 'declined') && (
          <div className="flex flex-col gap-3">
            {where === 'declined' && mine && (
              <p className="rounded-lg border border-line px-4 py-3 text-sm">
                An admin declined this{mine.reply ? `: “${mine.reply}”` : '.'} You can ask again.
              </p>
            )}
            <Field label="Note for the admins" hint="Optional, such as which version you're after.">
              {(id) => <textarea id={id} rows={2} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" />}
            </Field>
            <div className="flex justify-end">
              <Button variant="primary" busy={busy} onClick={ask}>
                <Plus className="size-4" aria-hidden /> Request {t.kind === 'movie' ? 'this movie' : 'this show'}
              </Button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}

function ReportForm({ t, onDone }: { t: DiscoverTitle; onDone: () => void }) {
  const qc = useQueryClient()
  const [problem, setProblem] = useState<Problem>('wont_play')
  const [season, setSeason] = useState('')
  const [episode, setEpisode] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [sent, setSent] = useState(false)
  const send = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await ppost('/issues', { kind: t.kind, tmdbId: t.tmdbId, problem, message, season: Number(season) || 0, episode: Number(episode) || 0 })
      await qc.invalidateQueries({ queryKey: ['portal', 'issues'] })
      setSent(true)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  if (sent) {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-ok/40 px-4 py-3">
        <span className="text-sm">Thanks. The admins will take a look, and you can follow it under Problems.</span>
        <Button onClick={onDone}>Done</Button>
      </div>
    )
  }
  return (
    <form onSubmit={send} className="flex flex-col gap-3 rounded-lg border border-line p-4">
      <h3 className="font-display font-semibold">What's wrong?</h3>
      <Field label="Problem">{(id) => <Select id={id} value={problem} onChange={(v) => setProblem(v as Problem)} options={problems} />}</Field>
      {t.kind === 'series' && (
        <div className="grid grid-cols-2 gap-3">
          <Field label="Season" hint="Leave empty if it's the whole show.">
            {(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={season} onChange={(e) => setSeason(e.target.value)} />}
          </Field>
          <Field label="Episode">{(id) => <Input id={id} type="number" min={0} inputMode="numeric" value={episode} onChange={(e) => setEpisode(e.target.value)} />}</Field>
        </div>
      )}
      <Field label="Tell us more" hint={problem === 'other' ? 'Say what happened.' : 'Optional.'}>
        {(id) => <textarea id={id} rows={3} maxLength={1000} value={message} onChange={(e) => setMessage(e.target.value)} required={problem === 'other'} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" />}
      </Field>
      <ErrorNote error={error} />
      <div className="flex justify-end">
        <Button variant="primary" type="submit" busy={busy}>
          Send the report
        </Button>
      </div>
    </form>
  )
}

const stateLook: Record<Request['state'], string> = {
  pending: 'border-marquee/50 text-marquee',
  declined: 'border-line text-muted',
  searching: 'border-gold/50 text-gold',
  downloading: 'border-gold/50 text-gold',
  partial: 'border-gold/50 text-gold',
  available: 'border-ok/40 text-ok',
  paused: 'border-line text-muted',
  removed: 'border-line text-muted',
}

function MyRequests() {
  const mine = useMine()
  const qc = useQueryClient()
  const [error, setError] = useState<unknown>(null)
  // While something is downloading, keep its progress fresh.
  const busy = mine.data?.some((r) => r.state === 'downloading' || r.state === 'searching')
  useEffect(() => {
    if (!busy) return
    const t = setInterval(() => qc.invalidateQueries({ queryKey: ['portal', 'requests'] }), 15_000)
    return () => clearInterval(t)
  }, [busy, qc])
  const cancel = async (id: number) => {
    setError(null)
    try {
      await papi('DELETE', `/requests/${id}`)
      await qc.invalidateQueries({ queryKey: ['portal'] })
    } catch (err) {
      setError(err)
    }
  }
  return (
    <>
      <h1 className="mb-6 font-display text-3xl font-bold tracking-tight">My requests</h1>
      <ErrorNote error={error} />
      {mine.isPending && <Spinner />}
      {mine.error && <ErrorNote error={mine.error} />}
      {mine.data?.length === 0 && (
        <Empty icon={<Film className="size-8" />} title="Nothing asked for yet">
          Find a movie or show on{' '}
          <Link to="/" className="font-medium text-marquee">
            Discover
          </Link>{' '}
          and press <strong>+</strong> to ask for it.
        </Empty>
      )}
      <ul className="flex flex-col gap-3">
        {mine.data?.map((r) => (
          <li key={r.id} className="flex gap-4 rounded-xl border border-line bg-card p-3">
            <Poster src={r.poster} title={r.title} className="w-16 shrink-0" />
            <div className="flex min-w-0 flex-1 flex-col gap-1.5">
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="font-display font-semibold">{r.title}</span>
                <span className="text-sm text-muted">
                  {r.year ? `${r.year} · ` : ''}
                  {r.kind === 'movie' ? 'Movie' : 'Show'}
                </span>
              </div>
              <div>
                <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${stateLook[r.state]}`}>{stateText(r)}</span>
              </div>
              {r.state === 'downloading' && r.progress !== undefined && (
                <div className="h-1.5 max-w-xs overflow-hidden rounded-full bg-line" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.floor(r.progress * 100)}>
                  <div className="h-full bg-gold" style={{ width: `${Math.max(2, r.progress * 100)}%` }} />
                </div>
              )}
              {r.reply && <p className="text-sm text-muted">Admin: “{r.reply}”</p>}
              <p className="text-xs text-muted">Asked {new Date(r.createdAt).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })}</p>
            </div>
            {r.state === 'pending' && (
              <div className="self-center">
                <Button variant="ghost" onClick={() => cancel(r.id)}>
                  Take back
                </Button>
              </div>
            )}
          </li>
        ))}
      </ul>
    </>
  )
}

function MyProblems() {
  const issues = useQuery({ queryKey: ['portal', 'issues'], queryFn: () => pget<Issue[]>('/issues') })
  return (
    <>
      <h1 className="mb-1 font-display text-3xl font-bold tracking-tight">Problems</h1>
      <p className="mb-6 text-muted">Something wrong with a movie or show? Open it on Discover or find it by name, and choose Report a problem.</p>
      {issues.isPending && <Spinner />}
      {issues.error && <ErrorNote error={issues.error} />}
      {issues.data?.length === 0 && <Empty icon={<Flag className="size-8" />} title="No problems reported" />}
      <ul className="flex flex-col gap-3">
        {issues.data?.map((i) => (
          <li key={i.id} className="flex gap-4 rounded-xl border border-line bg-card p-3">
            <Poster src={i.poster} title={i.title} className="w-16 shrink-0" />
            <div className="flex min-w-0 flex-1 flex-col gap-1.5">
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="font-display font-semibold">{i.title}</span>
                {i.season ? (
                  <span className="text-sm text-muted">
                    Season {i.season}
                    {i.episode ? `, episode ${i.episode}` : ''}
                  </span>
                ) : null}
              </div>
              <div>
                <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${i.status === 'open' ? 'border-marquee/50 text-marquee' : 'border-ok/40 text-ok'}`}>
                  {i.status === 'open' ? 'Waiting for an admin' : 'Fixed'}
                </span>
              </div>
              <p className="text-sm">
                {problemLabel(i.problem)}
                {i.message ? `: ${i.message}` : ''}
              </p>
              {i.reply && <p className="text-sm text-muted">Admin: “{i.reply}”</p>}
            </div>
          </li>
        ))}
      </ul>
    </>
  )
}

function Account({ member, onClose }: { member: Member; onClose: () => void }) {
  const qc = useQueryClient()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [done, setDone] = useState(false)
  const signOut = async () => {
    await ppost('/logout')
    qc.clear()
    qc.invalidateQueries({ queryKey: ['portal'] })
  }
  const change = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await papi('PUT', '/me/password', { current, new: next })
      setDone(true)
      setCurrent('')
      setNext('')
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title="Your account" onClose={onClose}>
      <div className="flex flex-col gap-5">
        <p className="text-sm">
          Signed in as <strong>{member.name}</strong>
          {member.source === 'matinee' ? ' with your Matinee account.' : '.'}
        </p>
        {member.source === 'local' && (
          <form onSubmit={change} className="flex flex-col gap-3">
            <h3 className="font-display font-semibold">Change your password</h3>
            <Field label="Current password">{(id) => <Input id={id} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />}</Field>
            <Field label="New password" hint="At least 8 characters.">
              {(id) => <Input id={id} type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} required />}
            </Field>
            <ErrorNote error={error} />
            {done && <p className="text-sm text-ok">Changed. You're signed out everywhere else.</p>}
            <div className="flex justify-end">
              <Button type="submit" busy={busy}>
                Change password
              </Button>
            </div>
          </form>
        )}
        <div className="flex justify-end border-t border-line pt-4">
          <Button variant="ghost" onClick={signOut}>
            <LogOut className="size-4" aria-hidden /> Sign out
          </Button>
        </div>
      </div>
    </Modal>
  )
}
