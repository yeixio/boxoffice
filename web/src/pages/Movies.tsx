import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Film, Plus } from 'lucide-react'
import { get, type Movie } from '../api'
import { Badge, Button, Empty, ErrorNote, Heading, Input, Poster, Spinner } from '../ui'
import { AddTitle } from './AddTitle'

const filters = {
  all: { label: 'All', test: () => true },
  missing: { label: 'Wanted', test: (m: Movie) => m.monitored && !m.file },
  have: { label: 'Have', test: (m: Movie) => !!m.file },
  unmonitored: { label: 'Not watched for', test: (m: Movie) => !m.monitored },
} as const

export function Movies() {
  const movies = useQuery({ queryKey: ['movies'], queryFn: () => get<Movie[]>('/movies') })
  const [adding, setAdding] = useState(false)
  const [filter, setFilter] = useState<keyof typeof filters>('all')
  const [text, setText] = useState('')
  const shown = useMemo(() => {
    const t = text.trim().toLowerCase()
    return (movies.data ?? []).filter((m) => filters[filter].test(m) && (!t || m.title.toLowerCase().includes(t)))
  }, [movies.data, filter, text])

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
          <div className="mb-5 flex flex-wrap items-center gap-2">
            {Object.entries(filters).map(([k, f]) => (
              <button
                key={k}
                onClick={() => setFilter(k as keyof typeof filters)}
                className={`rounded-full border px-3 py-1 text-sm ${filter === k ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:text-ink'}`}
              >
                {f.label}
              </button>
            ))}
            <Input className="ml-auto max-w-xs" placeholder="Filter by title" value={text} onChange={(e) => setText(e.target.value)} />
          </div>
          <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
            {shown.map((m) => (
              <li key={m.id}>
                <Link to={`/movies/${m.id}`} className="group block">
                  <Poster src={m.poster} title={m.title} className="transition group-hover:ring-2 group-hover:ring-marquee" />
                  <div className="mt-2 truncate font-medium">{m.title}</div>
                  <div className="flex items-center gap-2 text-xs text-muted">
                    {m.year}
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
