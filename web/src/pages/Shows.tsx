import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Plus, Tv } from 'lucide-react'
import { get, type Series } from '../api'
import { Badge, Button, Empty, ErrorNote, Heading, Input, Poster, Spinner } from '../ui'
import { AddTitle } from './AddTitle'

export function Shows() {
  const shows = useQuery({ queryKey: ['series'], queryFn: () => get<Series[]>('/series') })
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState('')
  const shown = useMemo(() => {
    const t = text.trim().toLowerCase()
    return (shows.data ?? []).filter((s) => !t || s.title.toLowerCase().includes(t))
  }, [shows.data, text])
  return (
    <>
      <Heading
        title="Shows"
        sub={shows.data ? `${shows.data.length} on the list` : undefined}
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus className="size-4" aria-hidden /> Add show
          </Button>
        }
      />
      {adding && <AddTitle kind="series" onClose={() => setAdding(false)} />}
      <ErrorNote error={shows.error} />
      {shows.isPending && <Spinner />}
      {shows.data?.length === 0 && (
        <Empty icon={<Tv className="size-8" />} title="No shows yet">
          Add one, or <Link className="font-medium text-marquee" to="/import">move in from Sonarr</Link> to bring your whole list across.
        </Empty>
      )}
      {!!shows.data?.length && (
        <>
          <div className="mb-5 flex">
            <Input className="ml-auto max-w-xs" placeholder="Filter by title" value={text} onChange={(e) => setText(e.target.value)} />
          </div>
          <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
            {shown.map((s) => {
              const pct = s.episodeCount ? Math.round((100 * s.fileCount) / s.episodeCount) : 0
              return (
                <li key={s.id}>
                  <Link to={`/shows/${s.id}`} className="group block">
                    <Poster src={s.poster} title={s.title} className="transition group-hover:ring-2 group-hover:ring-marquee" />
                    <div className="mt-2 h-1 overflow-hidden rounded bg-line" title={`${s.fileCount} of ${s.episodeCount} episodes on disk`}>
                      <div className="h-full bg-ok" style={{ width: `${pct}%` }} />
                    </div>
                    <div className="mt-1.5 truncate font-medium">{s.title}</div>
                    <div className="flex items-center gap-2 text-xs text-muted">
                      {s.fileCount}/{s.episodeCount} episodes
                      {!s.monitored && <Badge>Paused</Badge>}
                    </div>
                  </Link>
                </li>
              )
            })}
          </ul>
        </>
      )}
    </>
  )
}
