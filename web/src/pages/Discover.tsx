import { formatLocale } from '../i18n'
import { decimal } from '../format'
import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useState } from 'react'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { Check, ChevronRight, Compass, LoaderCircle, Plus, Search, Star, User } from 'lucide-react'
import {
  ApiError,
  get,
  post,
  type DiscoverHome,
  type DiscoverKind,
  type DiscoverPage,
  type DiscoverRow,
  type DiscoverStatus,
  type DiscoverTitle,
  type LookupMovie,
  type LookupPerson,
  type LookupShow,
} from '../api'
import { pickProfile, useProfiles } from '../hooks'
import { Button, Empty, ErrorNote, Heading, Input, Modal, Poster, Spinner } from '../ui'
import { AddOptions } from './AddTitle'

// Discover is the home page: what's trending, popular and coming soon on
// TMDB, titles like the ones in the library, and genres and networks to
// browse. Every poster says whether the title is here already, and ones
// that aren't can be added in one click with the usual profile, root
// folder and seasons, or with other choices from the title's card.

const pageOf = (kind: DiscoverKind, id: number) => `/${kind === 'movie' ? 'movies' : 'shows'}/${id}`

export function Discover() {
  const { t } = useTranslation()
  const home = useQuery({ queryKey: ['discover', 'home'], queryFn: () => get<DiscoverHome>('/discover'), staleTime: 5 * 60_000 })
  const [term, setTerm] = useState('')
  const [q, setQ] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setQ(term.trim()), 350)
    return () => clearTimeout(timer)
  }, [term])
  return (
    <>
      <Heading
        title={t('nav.discover')}
        sub={t('discover.sub')}
        actions={
          <div className="relative w-full sm:w-72">
            <Search className="pointer-events-none absolute start-3 top-2.5 size-4 text-muted" aria-hidden />
            <Input type="search" className="ps-9" placeholder={t('discover.find')} aria-label={t('discover.find')} value={term} onChange={(e) => setTerm(e.target.value)} />
          </div>
        }
      />
      {q.length > 1 ? (
        <SearchResults q={q} />
      ) : (
        <>
          {home.isPending && <Spinner />}
          {home.error && <NoTMDB error={home.error} />}
          {home.data && (
            <div className="flex flex-col gap-10">
              {home.data.rows.map((r) => (
                <Row key={r.id} row={r} />
              ))}
              <Browse browse={home.data.browse} />
            </div>
          )}
        </>
      )}
    </>
  )
}

// NoTMDB explains a failure, with the way to a key when that's the trouble.
function NoTMDB({ error }: { error: unknown }) {
  const { t } = useTranslation()
  if (error instanceof ApiError && error.status === 503) {
    return (
      <Empty icon={<Compass className="size-8" />} title={t('discover.needsTmdb')}>
        <Trans i18nKey="discover.needsTmdbBody" components={{ link: <Link to="/settings#tmdb" className="font-medium text-marquee" /> }} />
      </Empty>
    )
  }
  return <ErrorNote error={error} />
}

function Row({ row }: { row: DiscoverRow }) {
  const { t } = useTranslation()
  return (
    <section aria-labelledby={`row-${row.id}`}>
      <div className="mb-3 flex items-baseline justify-between gap-4">
        <h2 id={`row-${row.id}`} className="font-display text-xl font-semibold">
          {row.title}
        </h2>
        <Link to={`/discover/${row.id}`} className="flex shrink-0 items-center gap-0.5 text-sm font-medium text-marquee">
          {t('discover.seeAll')} <ChevronRight className="size-4 rtl:rotate-180" aria-hidden />
        </Link>
      </div>
      <ul className="-mx-4 flex snap-x scroll-px-4 gap-3 overflow-x-auto px-4 pb-2 md:-mx-8 md:scroll-px-8 md:px-8 [scrollbar-width:thin]">
        {row.items.map((title) => (
          <li key={`${title.kind}-${title.tmdbId}`} className="w-32 shrink-0 snap-start sm:w-36">
            <TitleCard t={title} />
          </li>
        ))}
      </ul>
    </section>
  )
}

const statusLook: Record<DiscoverStatus, { label: string; look: string }> = {
  available: { label: 'common.onDisk', look: 'bg-ok text-white' },
  partial: { label: 'discover.someOnDisk', look: 'bg-gold text-black' },
  wanted: { label: 'common.wanted', look: 'bg-marquee text-marquee-ink' },
  paused: { label: 'common.paused', look: 'bg-ink/85 text-paper' },
}

function StatusPill({ status }: { status?: DiscoverStatus }) {
  const { t } = useTranslation()
  const s = status ? statusLook[status] : { label: 'add.onList', look: 'bg-ink/85 text-paper' }
  return (
    <span className={`absolute start-1.5 top-1.5 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-semibold shadow ${s.look}`}>
      {status === 'available' && <Check className="size-3" aria-hidden />}
      {t(s.label)}
    </span>
  )
}

// TitleCard is a poster: clicking it opens the title's card, and the
// button on it adds the title straight away.
function TitleCard({ t }: { t: DiscoverTitle }) {
  const [open, setOpen] = useState(false)
  const [added, setAdded] = useState<{ id: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const { t: say } = useTranslation()
  const [error, setError] = useState<unknown>(null)
  const profiles = useProfiles()
  const qc = useQueryClient()
  const id = t.id ?? added?.id
  const status = t.status ?? (added ? 'wanted' : undefined)
  const what = t.kind === 'movie' ? say('discover.movie') : say('discover.show')
  const quickAdd = async () => {
    setBusy(true)
    setError(null)
    try {
      const body = { tmdbId: t.tmdbId, profileId: pickProfile(profiles.data, t.kind)?.id ?? 0, monitored: true, search: true }
      const a = await post<{ id: number }>(t.kind === 'movie' ? '/movies' : '/series', body)
      setAdded(a)
      qc.invalidateQueries({ queryKey: [t.kind === 'movie' ? 'movies' : 'series'] })
      qc.invalidateQueries({ queryKey: ['discover'] })
    } catch (err) {
      // Something to choose first, such as a root folder: the card says what.
      setError(err)
      setOpen(true)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="group relative">
      <button onClick={() => setOpen(true)} className="block w-full text-start" aria-label={`${t.title}${t.year ? ` (${t.year})` : ''}`}>
        <div className="relative">
          <Poster src={t.poster} title={t.title} className="transition group-hover:ring-2 group-hover:ring-marquee" />
          {id ? <StatusPill status={status} /> : null}
        </div>
        <div className="mt-2 truncate text-sm font-medium">{t.title}</div>
        <div className="flex items-center gap-1.5 text-xs text-muted">
          {t.year ? <span>{t.year}</span> : null}
          {t.year ? <span aria-hidden>·</span> : null}
          <span>{what}</span>
          {t.rating ? (
            <span className="ms-auto flex items-center gap-0.5 tabular-nums">
              <Star className="size-3" aria-hidden />
              {decimal(t.rating)}
            </span>
          ) : null}
        </div>
      </button>
      {!id && (
        <button
          onClick={quickAdd}
          disabled={busy || profiles.isPending}
          className="absolute end-1.5 top-1.5 flex size-8 items-center justify-center rounded-full bg-marquee text-marquee-ink shadow-md transition hover:scale-110 disabled:opacity-70"
          aria-label={say('discover.add', { title: t.title })}
          title={say('discover.addAndLook', { title: t.title })}
        >
          {busy ? <LoaderCircle className="size-4 animate-spin" aria-hidden /> : <Plus className="size-4.5" aria-hidden />}
        </button>
      )}
      {open && <TitleSheet t={t} id={id} status={status} error={error} onAdded={(a) => setAdded(a)} onClose={() => setOpen(false)} />}
    </div>
  )
}

// TitleSheet is a title's card: what it's about, and adding it with a
// choice of profile, folder and seasons, or the way to its page here.
function TitleSheet({
  t,
  id,
  status,
  error,
  onAdded,
  onClose,
}: {
  t: DiscoverTitle
  id?: number
  status?: DiscoverStatus
  error?: unknown
  onAdded: (a: { id: number }) => void
  onClose: () => void
}) {
  const { t: say } = useTranslation()
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
              <span>{t.kind === 'movie' ? say('discover.movie') : say('discover.show')}</span>
              {when && (
                <>
                  <span aria-hidden>·</span>
                  <span>
                    {say(soon ? (t.kind === 'movie' ? 'discover.out' : 'discover.starts') : t.kind === 'movie' ? 'discover.released' : 'discover.firstAired', {
                      date: when.toLocaleDateString(formatLocale(), { day: 'numeric', month: 'short', year: 'numeric' }),
                    })}
                  </span>
                </>
              )}
              {t.rating ? (
                <>
                  <span aria-hidden>·</span>
                  <span className="flex items-center gap-0.5">
                    <Star className="size-3.5" aria-hidden /> {decimal(t.rating)}
                  </span>
                </>
              ) : null}
            </div>
            {t.overview && <p className="mt-2 text-sm leading-relaxed">{t.overview}</p>}
          </div>
        </div>
        {id ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line px-4 py-3">
            <span className="text-sm">
              {status === 'available'
                ? say('discover.inLibrary')
                : status === 'partial'
                  ? say('discover.someInLibrary')
                  : status === 'paused'
                    ? say('discover.notWatched')
                    : say('discover.looking')}
            </span>
            <Link to={pageOf(t.kind, id)} className="text-sm font-medium text-marquee" onClick={onClose}>
              {t.kind === 'movie' ? say('discover.openMovie') : say('discover.openShow')}
            </Link>
          </div>
        ) : (
          <>
            <ErrorNote error={error} />
            <AddOptions
              kind={t.kind}
              title={t}
              onDone={onClose}
              onAdded={(a) => {
                onAdded(a)
                onClose()
              }}
            />
          </>
        )}
      </div>
    </Modal>
  )
}

function Browse({ browse }: { browse: DiscoverHome['browse'] }) {
  const { t } = useTranslation()
  const groups = [
    { title: t('discover.movieGenres'), kind: 'movie', genres: browse.movieGenres },
    { title: t('discover.showGenres'), kind: 'series', genres: browse.seriesGenres },
  ] as const
  return (
    <>
      {browse.networks.length > 0 && (
        <section aria-labelledby="networks">
          <h2 id="networks" className="mb-3 font-display text-xl font-semibold">
            {t('discover.networks')}
          </h2>
          <ul className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-8">
            {browse.networks.map((n) => (
              <li key={n.id}>
                <Link
                  to={`/discover/network-${n.id}`}
                  className="flex h-16 items-center justify-center rounded-lg border border-line bg-white px-3 transition hover:ring-2 hover:ring-marquee"
                  title={n.name}
                >
                  {n.logo ? <img src={n.logo} alt={n.name} loading="lazy" className="max-h-8 max-w-full object-contain" /> : <span className="font-display text-sm font-semibold text-black">{n.name}</span>}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
      {groups.map(
        (g) =>
          g.genres.length > 0 && (
            <section key={g.kind} aria-labelledby={`genres-${g.kind}`}>
              <h2 id={`genres-${g.kind}`} className="mb-3 font-display text-xl font-semibold">
                {g.title}
              </h2>
              <ul className="flex flex-wrap gap-2">
                {g.genres.map((genre) => (
                  <li key={genre.id}>
                    <Link
                      to={`/discover/genre-${g.kind}-${genre.id}`}
                      className="block rounded-full border border-line bg-card px-3.5 py-1.5 text-sm font-medium transition hover:border-marquee hover:text-marquee"
                    >
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
      {items.map((title) => (
        <li key={`${title.kind}-${title.tmdbId}`}>
          <TitleCard t={title} />
        </li>
      ))}
    </ul>
  )
}

// Face is a person's photo in a circle, or an outline when TMDB has none.
function Face({ src, className = 'size-16' }: { src?: string; className?: string }) {
  const [failed, setFailed] = useState('')
  return (
    <div className={`flex shrink-0 items-center justify-center overflow-hidden rounded-full bg-line text-muted ${className}`}>
      {src && failed !== src ? <img src={src} alt="" loading="lazy" className="size-full object-cover" onError={() => setFailed(src)} /> : <User className="size-1/2" aria-hidden />}
    </div>
  )
}

// People are actors and others whose names match a search; each leads to
// everything they're in.
function People({ people }: { people: LookupPerson[] }) {
  const { t } = useTranslation()
  return (
    <section>
      <h2 className="mb-3 font-display text-xl font-semibold">{t('discover.people')}</h2>
      <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {people.slice(0, 6).map((p) => (
          <li key={p.tmdbId}>
            <Link
              to={`/discover/person-${p.tmdbId}`}
              className="flex items-center gap-3 rounded-xl border border-line bg-card p-3 transition hover:border-marquee"
            >
              <Face src={p.photo} className="size-14" />
              <div className="min-w-0">
                <div className="truncate font-medium">{p.name}</div>
                {p.knownFor.length > 0 && <div className="line-clamp-2 text-xs text-muted">{t('discover.knownFor', { titles: p.knownFor.join(', ') })}</div>}
              </div>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )
}

// SearchResults finds movies and shows by name, both at once, and the
// people the search names, such as an actor.
function SearchResults({ q }: { q: string }) {
  const lookup = (kind: DiscoverKind) =>
    get<(LookupMovie | LookupShow)[]>(`/lookup/${kind === 'movie' ? 'movies' : 'series'}?q=${encodeURIComponent(q)}`).then((rs) =>
      rs.map((r): DiscoverTitle => ({ kind, tmdbId: r.tmdbId, title: r.title, year: r.year, overview: r.overview, poster: r.poster, backdrop: r.backdrop, rating: r.rating, id: r.id })),
    )
  const { t } = useTranslation()
  const movies = useQuery({ queryKey: ['lookup', 'movie', q], queryFn: () => lookup('movie') })
  const shows = useQuery({ queryKey: ['lookup', 'series', q], queryFn: () => lookup('series') })
  // Finding no one is no reason to say the search failed.
  const people = useQuery({ queryKey: ['lookup', 'people', q], queryFn: () => get<LookupPerson[]>(`/lookup/people?q=${encodeURIComponent(q)}`).catch(() => []) })
  if (movies.isPending || shows.isPending || people.isPending) return <Spinner label={t('common.looking')} />
  const err = movies.error ?? shows.error
  if (err) return <NoTMDB error={err} />
  const all = [...(movies.data ?? []), ...(shows.data ?? [])]
  if (all.length === 0 && !people.data?.length) return <p className="py-10 text-center text-sm text-muted">{t('add.nothing')}</p>
  return (
    <div className="flex flex-col gap-10">
      {!!people.data?.length && <People people={people.data} />}
      {!!movies.data?.length && (
        <section>
          <h2 className="mb-3 font-display text-xl font-semibold">{t('nav.movies')}</h2>
          <Grid items={movies.data} />
        </section>
      )}
      {!!shows.data?.length && (
        <section>
          <h2 className="mb-3 font-display text-xl font-semibold">{t('nav.shows')}</h2>
          <Grid items={shows.data} />
        </section>
      )}
    </div>
  )
}

// DiscoverRowPage is every title of one row, a page at a time.
export function DiscoverRowPage() {
  const { t } = useTranslation()
  const { id = '' } = useParams()
  const row = useInfiniteQuery({
    queryKey: ['discover', 'row', id],
    queryFn: ({ pageParam }) => get<DiscoverPage>(`/discover/rows/${encodeURIComponent(id)}?page=${pageParam}`),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.more ? last.page + 1 : undefined),
    staleTime: 5 * 60_000,
  })
  const first = row.data?.pages[0]
  const seen = new Set<string>()
  const items = (row.data?.pages ?? [])
    .flatMap((p) => p.items)
    .filter((title) => {
      const k = `${title.kind}-${title.tmdbId}`
      if (seen.has(k)) return false
      seen.add(k)
      return true
    })
  return (
    <>
      <Link to="/discover" className="mb-2 inline-block text-sm text-muted hover:text-ink">
        {t('discover.back')}
      </Link>
      {first?.person ? (
        <div className="mb-6 flex items-center gap-4">
          <Face src={first.person.photo} className="size-20" />
          <h1 className="font-display text-3xl font-bold tracking-tight">{first.person.name}</h1>
        </div>
      ) : (
        <Heading title={first?.title ?? t('nav.discover')} />
      )}
      {row.isPending && <Spinner />}
      {row.error && <NoTMDB error={row.error} />}
      {first && items.length === 0 && <p className="py-10 text-center text-sm text-muted">{t('discover.nothingHere')}</p>}
      <Grid items={items} />
      {row.hasNextPage && (
        <div className="mt-8 flex justify-center">
          <Button onClick={() => row.fetchNextPage()} busy={row.isFetchingNextPage}>
            {t('discover.showMore')}
          </Button>
        </div>
      )}
    </>
  )
}
