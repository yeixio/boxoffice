import { Trans, useTranslation } from 'react-i18next'
import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { CheckSquare, Lock, Plus, Tv } from 'lucide-react'
import { get, type Profile, type Series } from '../api'
import { bytes, day, decimal, relativeDay, runtime } from '../format'
import { useProfiles } from '../hooks'
import { SortControl, sortList, useSort, words, type SortOption } from '../sort'
import { Badge, Button, Empty, ErrorNote, Heading, Input, Poster, Spinner } from '../ui'
import { AddTitle } from './AddTitle'
import { BulkBar, Pickable, useSelection } from '../Bulk'

// airing groups a show's status, as TMDB or Sonarr word it, the way
// people think of it: still on, not started yet, or over. Shown as
// t(`airing.${airing(status)}`).
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
    { key: 'title', label: 'sorts.title', value: (s) => s.sortTitle || s.title, dir: 'asc', says: words.text },
    { key: 'added', label: 'sorts.added', value: (s) => s.addedAt, dir: 'desc', says: words.date },
    { key: 'size', label: 'sorts.size', value: (s) => s.sizeOnDisk || undefined, dir: 'desc', says: words.size },
    { key: 'year', label: 'sorts.year', value: (s) => s.year || undefined, dir: 'desc', says: words.date },
    { key: 'lastAired', label: 'sorts.lastAired', value: (s) => s.lastAired, dir: 'desc', says: words.date },
    { key: 'nextAiring', label: 'sorts.nextAiring', value: (s) => s.nextAiring, dir: 'asc', says: words.soon },
    { key: 'rating', label: 'sorts.rating', value: (s) => s.rating || undefined, dir: 'desc', says: words.rating },
    { key: 'episodes', label: 'sorts.episodes', value: done, dir: 'desc', says: words.percent },
    { key: 'profile', label: 'sorts.profile', value: (s) => profiles?.find((p) => p.id === s.profileId)?.name, dir: 'asc', says: words.text },
    { key: 'runtime', label: 'sorts.runtime', value: (s) => s.runtime || undefined, dir: 'desc', says: words.length },
    { key: 'network', label: 'sorts.network', value: (s) => s.network, dir: 'asc', says: words.text },
    { key: 'status', label: 'sorts.status', value: (s) => airingOrder[airing(s.status) ?? 'Continuing'], dir: 'asc', says: { asc: 'sorts.continuingFirst', desc: 'sorts.endedFirst' } },
  ]
}

const byTitle = (s: Series) => s.sortTitle || s.title

export function Shows() {
  const { t } = useTranslation()
  const shows = useQuery({ queryKey: ['series'], queryFn: () => get<Series[]>('/series') })
  const [adding, setAdding] = useState(false)
  const sel = useSelection()
  const [text, setText] = useState('')
  const profiles = useProfiles()
  const locked = useMemo(() => new Set((profiles.data ?? []).filter((p) => p.locked).map((p) => p.id)), [profiles.data])
  const sorts = useMemo(() => showSorts(profiles.data), [profiles.data])
  const sort = useSort('shows', sorts)
  const shown = useMemo(() => {
    const q = text.trim().toLowerCase()
    const sorted = sortList(shows.data ?? [], sort.option, sort.dir, byTitle)
    return sorted.filter((s) => !q || s.title.toLowerCase().includes(q))
  }, [shows.data, text, sort.option, sort.dir])
  // Under each poster: how many episodes it has, or what it's sorted by.
  const fact = (s: Series) => {
    switch (sort.option.key) {
      case 'added':
        return t('fact.added', { date: day(s.addedAt) })
      case 'size':
        return s.sizeOnDisk ? bytes(s.sizeOnDisk) : t('fact.nothingOnDisk')
      case 'year':
        return s.year ? String(s.year) : t('fact.noYear')
      case 'lastAired':
        return s.lastAired ? t('fact.lastAired', { date: day(s.lastAired) }) : t('fact.notAired')
      case 'nextAiring':
        return s.nextAiring ? t('fact.next', { day: relativeDay(s.nextAiring) }) : t('fact.nothingComing')
      case 'rating':
        return s.rating ? t('fact.rating', { rating: decimal(s.rating) }) : t('fact.noRating')
      case 'profile':
        return profiles.data?.find((p) => p.id === s.profileId)?.name ?? ''
      case 'runtime':
        return runtime(s.runtime) || t('fact.noRuntime')
      case 'network':
        return s.network || t('fact.noNetwork')
      case 'status':
        return airing(s.status) ? t(`airing.${airing(s.status)}`) : t('fact.noStatus')
      default:
        return t('fact.episodes', { have: s.fileCount, count: s.episodeCount })
    }
  }
  return (
    <>
      <Heading
        title={t('nav.shows')}
        sub={shows.data ? t('shows.sub', { count: shows.data.length }) : undefined}
        actions={
          <>
            {!sel.on && !!shows.data?.length && (
              <Button onClick={sel.start}>
                <CheckSquare className="size-4" aria-hidden /> {t('list.select')}
              </Button>
            )}
            <Button variant="primary" onClick={() => setAdding(true)}>
              <Plus className="size-4" aria-hidden /> {t('shows.add')}
            </Button>
          </>
        }
      />
      {adding && <AddTitle kind="series" onClose={() => setAdding(false)} />}
      <ErrorNote error={shows.error} />
      {shows.isPending && <Spinner />}
      {shows.data?.length === 0 && (
        <Empty icon={<Tv className="size-8" />} title={t('shows.none')}>
          <Trans i18nKey="shows.noneBody" components={{ link: <Link className="font-medium text-marquee" to="/import" /> }} />
        </Empty>
      )}
      {!!shows.data?.length && (
        <>
          <div className="mb-5 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-end">
            <Input type="search" className="sm:w-60 sm:flex-none" placeholder={t('list.filterTitle')} aria-label={t('list.filterTitle')} value={text} onChange={(e) => setText(e.target.value)} />
            <SortControl sort={sort} />
          </div>
          {shown.length === 0 && <p className="py-10 text-center text-sm text-muted">{t('shows.noMatch')}</p>}
          <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
            {shown.map((s) => {
              const pct = s.episodeCount ? Math.round((100 * s.fileCount) / s.episodeCount) : 0
              const tile = (
                <>
                    <Poster src={s.poster} title={s.title} className="transition group-hover:ring-2 group-hover:ring-marquee" />
                    <div className="mt-2 h-1 overflow-hidden rounded bg-line" title={t('shows.progress', { have: s.fileCount, count: s.episodeCount })}>
                      <div className="h-full bg-ok" style={{ width: `${pct}%` }} />
                    </div>
                    <div className="mt-1.5 truncate font-medium">{s.title}</div>
                    <div className="flex items-center gap-2 text-xs text-muted">
                      <span className="min-w-0 truncate">{fact(s)}</span>
                      {locked.has(s.profileId) && (
                        <Badge>
                          <Lock className="size-3" aria-label={t('filter.locked')} />
                        </Badge>
                      )}
                      {!s.monitored && <Badge>{t('common.paused')}</Badge>}
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
