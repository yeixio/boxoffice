import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { CalendarDays } from 'lucide-react'
import { get, type Upcoming } from '../api'
import { episodeCode, relativeDay } from '../format'
import { Badge, Empty, ErrorNote, Heading, Poster, Spinner } from '../ui'

const releaseLabel = { cinemas: 'calendar.cinemas', digital: 'calendar.digital', physical: 'calendar.physical', airs: 'calendar.airs' }

export function Calendar() {
  const { t } = useTranslation()
  const from = new Date()
  from.setDate(from.getDate() - 3)
  const to = new Date(from)
  to.setDate(to.getDate() + 45)
  const iso = (d: Date) => d.toISOString().slice(0, 10)
  const cal = useQuery({ queryKey: ['calendar', iso(from)], queryFn: () => get<Upcoming[]>(`/calendar?start=${iso(from)}&end=${iso(to)}`) })

  const days = new Map<string, Upcoming[]>()
  for (const u of cal.data ?? []) {
    const k = new Date(u.date).toDateString()
    days.set(k, [...(days.get(k) ?? []), u])
  }
  return (
    <>
      <Heading title={t('nav.calendar')} sub={t('calendar.sub')} />
      <ErrorNote error={cal.error} />
      {cal.isPending && <Spinner />}
      {cal.data?.length === 0 && <Empty icon={<CalendarDays className="size-8" />} title={t('calendar.nothing')}>{t('calendar.nothingBody')}</Empty>}
      <div className="flex flex-col gap-8">
        {[...days.entries()].map(([k, items]) => (
          <section key={k}>
            <h2 className="mb-3 font-display text-lg font-semibold">{relativeDay(items[0].date)}</h2>
            <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {items.map((u, i) => (
                <li key={i}>
                  <Link to={u.kind === 'movie' ? `/movies/${u.movieId}` : `/shows/${u.seriesId}`} className="flex gap-3 rounded-xl border border-line bg-card p-3 hover:border-ink/40">
                    <Poster src={u.poster} title={u.show || u.title} className="w-12 shrink-0" />
                    <div className="min-w-0">
                      <div className="truncate font-medium">{u.kind === 'episode' ? u.show : u.title}</div>
                      <div className="truncate text-sm text-muted">
                        {u.kind === 'episode' ? `${episodeCode(u.season ?? 0, u.episode ?? 0)} · ${u.title}` : t(releaseLabel[u.release])}
                      </div>
                      <div className="mt-1">{u.hasFile ? <Badge tone="ok">{t('common.onDisk')}</Badge> : <Badge tone="gold">{t('common.wanted')}</Badge>}</div>
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </>
  )
}
