import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { CheckSquare, Lock, Plus, Tv } from 'lucide-react'
import { get, type Profile, type Series } from '../api'
import { bytes, day, relativeDay, runtime } from '../format'
import { useProfiles } from '../hooks'
import { SortControl, sortList, useSort, words, type SortOption } from '../sort'
import { Badge, Button, Empty, ErrorNote, Heading, Input, Poster, Spinner } from '../ui'
import { AddTitle } from './AddTitle'
import { BulkBar, Pickable, useSelection } from '../Bulk'

// airing groups a show's status, as TMDB or Sonarr word it, the way
// people think of it: still on, not started yet, or over.
export function airing(status?: string): 'Continuing' | 'Upcoming' | 'Ended' | undefined {
  const s = status?.toLowerCase() ?? ''
  if (!s) return undefined
  if (s.includes('ended') || s.includes('cancel')) return 'Ended'
  if (s.includes('production') || s.includes('planned') || s.includes('upcoming') || s.includes('pilot')) return 'Upcoming'
  return 'Continuing'
}
const airingOrder = { Continuing: 0, Upcoming: 1, Ended: 2 }

const done = (s: Series) => (s.episodeCount ? s.fileCount / s.episodeCount : undefined)

// The sorts Sonarr has, in the order people reach for them.
function showSorts(profiles?: Profile[]): SortOption<Series>[] {
  return [
    { key: 'title', label: 'Title', value: (s) => s.sortTitle || s.title, dir: 'asc', says: words.text },
    { key: 'added', label: 'Date added', value: (s) => s.addedAt, dir: 'desc', says: words.date },
    { key: 'size', label: 'Size on disk', value: (s) => s.sizeOnDisk || undefined, dir: 'desc', says: words.size },
    { key: 'year', label: 'Year', value: (s) => s.year || undefined, dir: 'desc', says: words.date },
    { key: 'lastAired', label: 'Latest episode', value: (s) => s.lastAired, dir: 'desc', says: words.date },
    { key: 'nextAiring', label: 'Next episode', value: (s) => s.nextAiring, dir: 'asc', says: words.soon },
    { key: 'rating', label: 'Rating', value: (s) => s.rating || undefined, dir: 'desc', says: words.rating },
    { key: 'episodes', label: 'Episodes on disk', value: done, dir: 'desc', says: words.percent },
    { key: 'profile', label: 'Quality profile', value: (s) => profiles?.find((p) => p.id === s.profileId)?.name, dir: 'asc', says: words.text },
    { key: 'runtime', label: 'Runtime', value: (s) => s.runtime || undefined, dir: 'desc', says: words.length },
    { key: 'network', label: 'Network', value: (s) => s.network, dir: 'asc', says: words.text },
    { key: 'status', label: 'Status', value: (s) => airingOrder[airing(s.status) ?? 'Continuing'], dir: 'asc', says: { asc: 'Continuing first', desc: 'Ended first' } },
  ]
}

const byTitle = (s: Series) => s.sortTitle || s.title

export function Shows() {
  const shows = useQuery({ queryKey: ['series'], queryFn: () => get<Series[]>('/series') })
  const [adding, setAdding] = useState(false)
  const sel = useSelection()
  const [text, setText] = useState('')
  const profiles = useProfiles()
  const locked = useMemo(() => new Set((profiles.data ?? []).filter((p) => p.locked).map((p) => p.id)), [profiles.data])
  const sorts = useMemo(() => showSorts(profiles.data), [profiles.data])
  const sort = useSort('shows', sorts)
  const shown = useMemo(() => {
    const t = text.trim().toLowerCase()
    const sorted = sortList(shows.data ?? [], sort.option, sort.dir, byTitle)
    return sorted.filter((s) => !t || s.title.toLowerCase().includes(t))
  }, [shows.data, text, sort.option, sort.dir])
  // Under each poster: how many episodes it has, or what it's sorted by.
  const fact = (s: Series) => {
    switch (sort.option.key) {
      case 'added':
        return `Added ${day(s.addedAt)}`
      case 'size':
        return s.sizeOnDisk ? bytes(s.sizeOnDisk) : 'Nothing on disk'
      case 'year':
        return s.year ? String(s.year) : 'Year unknown'
      case 'lastAired':
        return s.lastAired ? `Last aired ${day(s.lastAired)}` : 'Not aired yet'
      case 'nextAiring':
        return s.nextAiring ? `Next: ${relativeDay(s.nextAiring)}` : 'Nothing coming up'
      case 'rating':
        return s.rating ? `★ ${s.rating.toFixed(1)}` : 'No rating'
      case 'profile':
        return profiles.data?.find((p) => p.id === s.profileId)?.name ?? ''
      case 'runtime':
        return runtime(s.runtime) || 'Runtime unknown'
      case 'network':
        return s.network || 'Network unknown'
      case 'status':
        return airing(s.status) ?? 'Status unknown'
      default:
        return `${s.fileCount}/${s.episodeCount} episodes`
    }
  }
  return (
    <>
      <Heading
        title="Shows"
        sub={shows.data ? `${shows.data.length} on the list` : undefined}
        actions={
          <>
            {!sel.on && !!shows.data?.length && (
              <Button onClick={sel.start}>
                <CheckSquare className="size-4" aria-hidden /> Select
              </Button>
            )}
            <Button variant="primary" onClick={() => setAdding(true)}>
              <Plus className="size-4" aria-hidden /> Add show
            </Button>
          </>
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
          <div className="mb-5 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-end">
            <Input type="search" className="sm:w-60 sm:flex-none" placeholder="Filter by title" aria-label="Filter by title" value={text} onChange={(e) => setText(e.target.value)} />
            <SortControl sort={sort} />
          </div>
          {shown.length === 0 && <p className="py-10 text-center text-sm text-muted">No shows match.</p>}
          <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
            {shown.map((s) => {
              const pct = s.episodeCount ? Math.round((100 * s.fileCount) / s.episodeCount) : 0
              const tile = (
                <>
                    <Poster src={s.poster} title={s.title} className="transition group-hover:ring-2 group-hover:ring-marquee" />
                    <div className="mt-2 h-1 overflow-hidden rounded bg-line" title={`${s.fileCount} of ${s.episodeCount} episodes on disk`}>
                      <div className="h-full bg-ok" style={{ width: `${pct}%` }} />
                    </div>
                    <div className="mt-1.5 truncate font-medium">{s.title}</div>
                    <div className="flex items-center gap-2 text-xs text-muted">
                      <span className="min-w-0 truncate">{fact(s)}</span>
                      {locked.has(s.profileId) && (
                        <Badge>
                          <Lock className="size-3" aria-label="Do not touch" />
                        </Badge>
                      )}
                      {!s.monitored && <Badge>Paused</Badge>}
                    </div>
                  </>
              )
              return (
                <li key={s.id}>
                  {sel.on ? (
                    <Pickable sel={sel} id={s.id} title={s.title}>
                      {tile}
                    </Pickable>
                  ) : (
                    <Link to={`/shows/${s.id}`} className="group block">
                      {tile}
                    </Link>
                  )}
                </li>
              )
            })}
          </ul>
          {sel.on && <BulkBar kind="series" sel={sel} shown={shown.map((s) => s.id)} />}
        </>
      )}
    </>
  )
}
