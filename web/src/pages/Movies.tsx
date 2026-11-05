import { Trans, useTranslation } from 'react-i18next'
import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { CheckSquare, Film, Lock, Plus } from 'lucide-react'
import { get, type Movie, type Quality } from '../api'
import { bytes, day, decimal, runtime } from '../format'
import { qualityName, useProfiles, useQualities } from '../hooks'
import { SortControl, sortList, useSort, words, type SortOption } from '../sort'
import { Badge, Button, Empty, ErrorNote, Heading, Input, Poster, Spinner } from '../ui'
import { AddTitle } from './AddTitle'
import { BulkBar, Pickable, useSelection } from '../Bulk'

const filters = {
  all: { label: 'filter.all', test: (_m: Movie, _l: Set<number>) => true },
  missing: { label: 'filter.wanted', test: (m: Movie, _l: Set<number>) => m.monitored && !m.file },
  have: { label: 'filter.have', test: (m: Movie, _l: Set<number>) => !!m.file },
  unmonitored: { label: 'filter.notWatched', test: (m: Movie, _l: Set<number>) => !m.monitored },
  locked: { label: 'filter.locked', test: (m: Movie, locked: Set<number>) => locked.has(m.profileId) },
} as const

// released is when a movie came out to watch at home, or failing that in
// cinemas.
function released(m: Movie): string | undefined {
  const home = [m.digitalRelease, m.physicalRelease].filter(Boolean).sort()[0]
  return home ?? m.inCinemas
}

// Wanted, then on disk, then paused: what's still to get comes first.
function state(m: Movie): number {
  return m.file ? 1 : m.monitored ? 0 : 2
}

// The sorts Radarr has, in the order people reach for them.
function movieSorts(qualities?: Quality[]): SortOption<Movie>[] {
  const rank = (id: number) => qualities?.findIndex((q) => q.id === id) ?? id
  return [
    { key: 'title', label: 'sorts.title', value: (m) => m.sortTitle || m.title, dir: 'asc', says: words.text },
    { key: 'added', label: 'sorts.added', value: (m) => m.addedAt, dir: 'desc', says: words.date },
    { key: 'size', label: 'sorts.size', value: (m) => m.file?.size, dir: 'desc', says: words.size },
    { key: 'year', label: 'sorts.year', value: (m) => m.year || undefined, dir: 'desc', says: words.date },
    { key: 'released', label: 'sorts.released', value: released, dir: 'desc', says: words.date },
    { key: 'rating', label: 'sorts.rating', value: (m) => m.rating || undefined, dir: 'desc', says: words.rating },
    { key: 'quality', label: 'sorts.quality', value: (m) => (m.file ? rank(m.file.quality) : undefined), dir: 'desc', says: words.quality },
    { key: 'runtime', label: 'sorts.runtime', value: (m) => m.runtime || undefined, dir: 'desc', says: words.length },
    { key: 'status', label: 'sorts.status', value: state, dir: 'asc', says: { asc: 'sorts.wantedFirst', desc: 'sorts.pausedFirst' } },
  ]
}

const byTitle = (m: Movie) => m.sortTitle || m.title

export function Movies() {
  const { t } = useTranslation()
  const movies = useQuery({ queryKey: ['movies'], queryFn: () => get<Movie[]>('/movies') })
  const [adding, setAdding] = useState(false)
  const sel = useSelection()
  const [filter, setFilter] = useState<keyof typeof filters>('all')
  const [text, setText] = useState('')
  const qualities = useQualities()
  const profiles = useProfiles()
  const locked = useMemo(() => new Set((profiles.data ?? []).filter((p) => p.locked).map((p) => p.id)), [profiles.data])
  const sorts = useMemo(() => movieSorts(qualities.data), [qualities.data])
  const sort = useSort('movies', sorts)
  const sorted = useMemo(() => sortList(movies.data ?? [], sort.option, sort.dir, byTitle), [movies.data, sort.option, sort.dir])
  const shown = useMemo(() => {
    const q = text.trim().toLowerCase()
    return sorted.filter((m) => filters[filter].test(m, locked) && (!q || m.title.toLowerCase().includes(q)))
  }, [sorted, filter, text, locked])
  // Under each poster: the year, or what the list is sorted by.
  const fact = (m: Movie) => {
    switch (sort.option.key) {
      case 'added':
        return t('fact.added', { date: day(m.addedAt) })
      case 'size':
        return m.file ? bytes(m.file.size) : t('fact.notOnDisk')
      case 'released':
        return released(m) ? day(released(m)) : t('fact.noDate')
      case 'rating':
        return m.rating ? t('fact.rating', { rating: decimal(m.rating) }) : t('fact.noRating')
      case 'quality':
        return m.file ? qualityName(qualities.data, m.file.quality) : t('fact.notOnDisk')
      case 'runtime':
        return runtime(m.runtime) || t('fact.noRuntime')
      default:
        return m.year ? String(m.year) : ''
    }
  }

  const have = movies.data?.filter((m) => m.file).length ?? 0
  return (
    <>
      <Heading
        title={t('nav.movies')}
        sub={movies.data ? t('movies.sub', { count: movies.data.length, have }) : undefined}
        actions={
          <>
            {!sel.on && !!movies.data?.length && (
              <Button onClick={sel.start}>
                <CheckSquare className="size-4" aria-hidden /> {t('list.select')}
              </Button>
            )}
            <Button variant="primary" onClick={() => setAdding(true)}>
              <Plus className="size-4" aria-hidden /> {t('movies.add')}
            </Button>
          </>
        }
      />
      {adding && <AddTitle kind="movie" onClose={() => setAdding(false)} />}
      <ErrorNote error={movies.error} />
      {movies.isPending && <Spinner />}
      {movies.data?.length === 0 && (
        <Empty icon={<Film className="size-8" />} title={t('movies.none')}>
          <Trans i18nKey="movies.noneBody" components={{ link: <Link className="font-medium text-marquee" to="/import" /> }} />
        </Empty>
      )}
      {!!movies.data?.length && (
        <>
          <div className="mb-5 flex flex-wrap items-center gap-x-4 gap-y-3">
            <div className="flex flex-wrap gap-2" role="group" aria-label={t('list.show')}>
              {Object.entries(filters).map(([k, f]) => (
                <button
                  key={k}
                  onClick={() => setFilter(k as keyof typeof filters)}
                  aria-pressed={filter === k}
                  className={`rounded-full border px-3 py-1.5 text-sm sm:py-1 ${filter === k ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:text-ink'}`}
                >
                  {t(f.label)}
                </button>
              ))}
            </div>
            <div className="flex w-full flex-col gap-2 sm:flex-row sm:items-center lg:ms-auto lg:w-auto">
              <Input type="search" className="sm:w-60 sm:flex-none" placeholder={t('list.filterTitle')} aria-label={t('list.filterTitle')} value={text} onChange={(e) => setText(e.target.value)} />
              <SortControl sort={sort} className="sm:ms-auto lg:ms-0" />
            </div>
          </div>
          {shown.length === 0 && <p className="py-10 text-center text-sm text-muted">{t('movies.noMatch')}</p>}
          <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
            {shown.map((m) => {
              const tile = (
                <>
                  <Poster src={m.poster} title={m.title} className="transition group-hover:ring-2 group-hover:ring-marquee" />
                  <div className="mt-2 truncate font-medium">{m.title}</div>
                  <div className="flex items-center gap-2 text-xs text-muted">
                    <span className="min-w-0 truncate">{fact(m)}</span>
                    {locked.has(m.profileId) ? (
                      <Badge>
                        <Lock className="size-3" aria-label={t('filter.locked')} />
                      </Badge>
                    ) : m.file ? (
                      <Badge tone="ok">{t('common.onDisk')}</Badge>
                    ) : m.monitored ? (
                      <Badge tone="gold">{t('common.wanted')}</Badge>
                    ) : (
                      <Badge>{t('common.paused')}</Badge>
                    )}
                  </div>
                </>
              )
              return (
                <li key={m.id}>
                  {sel.on ? (
                    <Pickable sel={sel} id={m.id} title={m.title}>
                      {tile}
                    </Pickable>
                  ) : (
                    <Link to={`/movies/${m.id}`} className="group block">
                      {tile}
                    </Link>
                  )}
                </li>
              )
            })}
          </ul>
          {sel.on && <BulkBar kind="movies" sel={sel} shown={shown.map((m) => m.id)} />}
        </>
      )}
    </>
  )
}
