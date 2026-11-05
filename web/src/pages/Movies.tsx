import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Film, Plus } from 'lucide-react'
import { get, type Movie, type Quality } from '../api'
import { bytes, day, runtime } from '../format'
import { qualityName, useQualities } from '../hooks'
import { SortControl, sortList, useSort, words, type SortOption } from '../sort'
import { Badge, Button, Empty, ErrorNote, Heading, Input, Poster, Spinner } from '../ui'
import { AddTitle } from './AddTitle'

const filters = {
  all: { label: 'All', test: () => true },
  missing: { label: 'Wanted', test: (m: Movie) => m.monitored && !m.file },
  have: { label: 'Have', test: (m: Movie) => !!m.file },
  unmonitored: { label: 'Not watched for', test: (m: Movie) => !m.monitored },
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
    { key: 'title', label: 'Title', value: (m) => m.sortTitle || m.title, dir: 'asc', says: words.text },
    { key: 'added', label: 'Date added', value: (m) => m.addedAt, dir: 'desc', says: words.date },
    { key: 'size', label: 'Size on disk', value: (m) => m.file?.size, dir: 'desc', says: words.size },
    { key: 'year', label: 'Year', value: (m) => m.year || undefined, dir: 'desc', says: words.date },
    { key: 'released', label: 'Release date', value: released, dir: 'desc', says: words.date },
    { key: 'rating', label: 'Rating', value: (m) => m.rating || undefined, dir: 'desc', says: words.rating },
    { key: 'quality', label: 'Quality', value: (m) => (m.file ? rank(m.file.quality) : undefined), dir: 'desc', says: words.quality },
    { key: 'runtime', label: 'Runtime', value: (m) => m.runtime || undefined, dir: 'desc', says: words.length },
    { key: 'status', label: 'Status', value: state, dir: 'asc', says: { asc: 'Wanted first', desc: 'Paused first' } },
  ]
}

const byTitle = (m: Movie) => m.sortTitle || m.title

export function Movies() {
  const movies = useQuery({ queryKey: ['movies'], queryFn: () => get<Movie[]>('/movies') })
  const [adding, setAdding] = useState(false)
  const [filter, setFilter] = useState<keyof typeof filters>('all')
  const [text, setText] = useState('')
  const qualities = useQualities()
  const sorts = useMemo(() => movieSorts(qualities.data), [qualities.data])
  const sort = useSort('movies', sorts)
  const sorted = useMemo(() => sortList(movies.data ?? [], sort.option, sort.dir, byTitle), [movies.data, sort.option, sort.dir])
  const shown = useMemo(() => {
    const t = text.trim().toLowerCase()
    return sorted.filter((m) => filters[filter].test(m) && (!t || m.title.toLowerCase().includes(t)))
  }, [sorted, filter, text])
  // Under each poster: the year, or what the list is sorted by.
  const fact = (m: Movie) => {
    switch (sort.option.key) {
      case 'added':
        return `Added ${day(m.addedAt)}`
      case 'size':
        return m.file ? bytes(m.file.size) : 'Not on disk'
      case 'released':
        return released(m) ? day(released(m)) : 'No date yet'
      case 'rating':
        return m.rating ? `★ ${m.rating.toFixed(1)}` : 'No rating'
      case 'quality':
        return m.file ? qualityName(qualities.data, m.file.quality) : 'Not on disk'
      case 'runtime':
        return runtime(m.runtime) || 'Runtime unknown'
      default:
        return m.year ? String(m.year) : ''
    }
  }

  const have = movies.data?.filter((m) => m.file).length ?? 0
  return (
    <>
      <Heading
        title="Movies"
        sub={movies.data ? `${movies.data.length} on the list, ${have} on disk` : undefined}
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus className="size-4" aria-hidden /> Add movie
          </Button>
        }
      />
      {adding && <AddTitle kind="movie" onClose={() => setAdding(false)} />}
      <ErrorNote error={movies.error} />
      {movies.isPending && <Spinner />}
      {movies.data?.length === 0 && (
        <Empty icon={<Film className="size-8" />} title="No movies yet">
          Add one, or <Link className="font-medium text-marquee" to="/import">move in from Radarr</Link> to bring your whole list across.
        </Empty>
      )}
      {!!movies.data?.length && (
        <>
          <div className="mb-5 flex flex-wrap items-center gap-x-4 gap-y-3">
            <div className="flex flex-wrap gap-2" role="group" aria-label="Show">
              {Object.entries(filters).map(([k, f]) => (
                <button
                  key={k}
                  onClick={() => setFilter(k as keyof typeof filters)}
                  aria-pressed={filter === k}
                  className={`rounded-full border px-3 py-1.5 text-sm sm:py-1 ${filter === k ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:text-ink'}`}
                >
                  {f.label}
                </button>
              ))}
            </div>
            <div className="flex w-full flex-col gap-2 sm:flex-row sm:items-center lg:ml-auto lg:w-auto">
              <Input type="search" className="sm:w-60 sm:flex-none" placeholder="Filter by title" aria-label="Filter by title" value={text} onChange={(e) => setText(e.target.value)} />
              <SortControl sort={sort} className="sm:ml-auto lg:ml-0" />
            </div>
          </div>
          {shown.length === 0 && <p className="py-10 text-center text-sm text-muted">No movies match.</p>}
          <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
            {shown.map((m) => (
              <li key={m.id}>
                <Link to={`/movies/${m.id}`} className="group block">
                  <Poster src={m.poster} title={m.title} className="transition group-hover:ring-2 group-hover:ring-marquee" />
                  <div className="mt-2 truncate font-medium">{m.title}</div>
                  <div className="flex items-center gap-2 text-xs text-muted">
                    <span className="min-w-0 truncate">{fact(m)}</span>
                    {m.file ? <Badge tone="ok">On disk</Badge> : m.monitored ? <Badge tone="gold">Wanted</Badge> : <Badge>Paused</Badge>}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  )
}
