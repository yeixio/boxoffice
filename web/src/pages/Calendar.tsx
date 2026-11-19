import { useTranslation } from 'react-i18next'
import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { CalendarDays, CalendarPlus, ChevronLeft, ChevronRight } from 'lucide-react'
import { get, post, type Upcoming } from '../api'
import { episodeCode, relativeDay } from '../format'
import { formatLocale } from '../i18n'
import { Badge, Button, Empty, ErrorNote, Heading, Input, Modal, Poster, Spinner } from '../ui'

const releaseLabel = { cinemas: 'calendar.cinemas', digital: 'calendar.digital', physical: 'calendar.physical', airs: 'calendar.airs' }

// Release and air dates are days, sent as midnight UTC. dayOf is that day
// on the viewer's calendar, so a date doesn't slip to the day before west
// of Greenwich.
const dayKey = (u: Upcoming) => u.date.slice(0, 10)
function dayOf(key: string): Date {
  const [y, m, d] = key.split('-').map(Number)
  return new Date(y, m - 1, d)
}
const keyOf = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n)

// weekStart is the day weeks start on where the viewer is: 0 for Sunday, 1
// for Monday.
export function weekStart(locale: string): number {
  try {
    const l = new Intl.Locale(locale) as Intl.Locale & { weekInfo?: { firstDay: number }; getWeekInfo?: () => { firstDay: number } }
    const first = (l.getWeekInfo?.() ?? l.weekInfo)?.firstDay
    if (first) return first % 7
  } catch {
    // an odd locale: Monday
  }
  return 1
}

// monthWeeks is the days a month's grid shows: whole weeks, from the week
// its first day is in to the week its last day is in.
export function monthWeeks(year: number, month: number, first: number): Date[] {
  const start = new Date(year, month, 1)
  const lead = (start.getDay() - first + 7) % 7
  const days = new Date(year, month + 1, 0).getDate()
  const cells = Math.ceil((lead + days) / 7) * 7
  return Array.from({ length: cells }, (_, i) => new Date(year, month, 1 - lead + i))
}

function byDay(list: Upcoming[]): Map<string, Upcoming[]> {
  const days = new Map<string, Upcoming[]>()
  for (const u of list) days.set(dayKey(u), [...(days.get(dayKey(u)) ?? []), u])
  return days
}

export function Calendar() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const month = params.get('view') === 'month'
  const [feed, setFeed] = useState(false)
  const view = (v: 'list' | 'month') => setParams(v === 'month' ? { view: 'month' } : {}, { replace: true })
  return (
    <>
      <Heading
        title={t('nav.calendar')}
        sub={t('calendar.sub')}
        actions={
          <Button onClick={() => setFeed(true)}>
            <CalendarPlus className="size-4" aria-hidden /> {t('calendar.subscribe')}
          </Button>
        }
      />
      <div className="mb-5 flex gap-2" role="group" aria-label={t('calendar.view')}>
        {(['list', 'month'] as const).map((v) => (
          <button
            key={v}
            onClick={() => view(v)}
            aria-pressed={month === (v === 'month')}
            className={`rounded-full border px-3 py-1.5 text-sm sm:py-1 ${month === (v === 'month') ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:text-ink'}`}
          >
            {t(v === 'list' ? 'calendar.list' : 'calendar.month')}
          </button>
        ))}
      </div>
      {month ? <Month /> : <List />}
      {feed && <Feed onClose={() => setFeed(false)} />}
    </>
  )
}

// List is the next six weeks, a day at a time, and on asking the week
// before today with whether each release arrived.
function List() {
  const { t } = useTranslation()
  const [back, setBack] = useState(false)
  const today = new Date()
  const from = addDays(today, back ? -10 : -3)
  const to = addDays(today, 42)
  const cal = useQuery({ queryKey: ['calendar', keyOf(from), keyOf(to)], queryFn: () => get<Upcoming[]>(`/calendar?start=${keyOf(from)}&end=${keyOf(to)}`) })
  const days = useMemo(() => byDay(cal.data ?? []), [cal.data])
  return (
    <>
      <ErrorNote error={cal.error} />
      {cal.isPending && <Spinner />}
      {!back && (
        <Button variant="ghost" className="mb-4" onClick={() => setBack(true)}>
          {t('calendar.lastWeek')}
        </Button>
      )}
      {cal.data?.length === 0 && (
        <Empty icon={<CalendarDays className="size-8" />} title={t('calendar.nothing')}>
          {t('calendar.nothingBody')}
        </Empty>
      )}
      <div className="flex flex-col gap-8">
        {[...days.entries()].map(([k, items]) => (
          <section key={k}>
            <h2 className="mb-3 font-display text-lg font-semibold">{relativeDay(dayOf(k).toISOString())}</h2>
            <Items items={items} />
          </section>
        ))}
      </div>
    </>
  )
}

function Items({ items }: { items: Upcoming[] }) {
  const { t } = useTranslation()
  const past = (u: Upcoming) => dayOf(dayKey(u)).getTime() < new Date().setHours(0, 0, 0, 0)
  return (
    <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {items.map((u, i) => (
        <li key={i}>
          <Link to={u.kind === 'movie' ? `/movies/${u.movieId}` : `/shows/${u.seriesId}`} className="flex gap-3 rounded-xl border border-line bg-card p-3 hover:border-ink/40">
            <Poster src={u.poster} title={u.show || u.title} className="w-12 shrink-0" />
            <div className="min-w-0">
              <div className="truncate font-medium">{u.kind === 'episode' ? u.show : u.title}</div>
              <div className="truncate text-sm text-muted">{u.kind === 'episode' ? `${episodeCode(u.season ?? 0, u.episode ?? 0)} · ${u.title}` : t(releaseLabel[u.release])}</div>
              <div className="mt-1">{u.hasFile ? <Badge tone="ok">{t('common.onDisk')}</Badge> : past(u) ? <Badge>{t('calendar.notYet')}</Badge> : <Badge tone="gold">{t('common.wanted')}</Badge>}</div>
            </div>
          </Link>
        </li>
      ))}
    </ul>
  )
}

// Month is one month as a grid of days. Picking a day lists what's on it
// underneath.
function Month() {
  const { t } = useTranslation()
  const locale = formatLocale()
  const now = new Date()
  const [at, setAt] = useState({ year: now.getFullYear(), month: now.getMonth() })
  const [picked, setPicked] = useState(keyOf(now))
  const cells = useMemo(() => monthWeeks(at.year, at.month, weekStart(locale)), [at, locale])
  const from = keyOf(cells[0])
  const to = keyOf(addDays(cells[cells.length - 1], 1))
  const cal = useQuery({ queryKey: ['calendar', from, to], queryFn: () => get<Upcoming[]>(`/calendar?start=${from}&end=${to}`) })
  const days = useMemo(() => byDay(cal.data ?? []), [cal.data])
  const step = (n: number) => {
    const d = new Date(at.year, at.month + n, 1)
    setAt({ year: d.getFullYear(), month: d.getMonth() })
    setPicked(d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() ? keyOf(now) : keyOf(d))
  }
  const title = new Date(at.year, at.month, 1).toLocaleDateString(locale, { month: 'long', year: 'numeric' })
  const weekday = (d: Date, width: 'short' | 'narrow') => d.toLocaleDateString(locale, { weekday: width })
  const full = (d: Date) => d.toLocaleDateString(locale, { weekday: 'long', month: 'long', day: 'numeric' })
  const today = keyOf(now)
  const items = days.get(picked) ?? []
  return (
    <>
      <div className="mb-3 flex items-center gap-2">
        <Button variant="ghost" aria-label={t('calendar.prev')} title={t('calendar.prev')} onClick={() => step(-1)}>
          <ChevronLeft className="size-4 rtl:rotate-180" aria-hidden />
        </Button>
        <h2 className="min-w-0 flex-1 text-center font-display text-lg font-semibold capitalize sm:flex-none sm:px-2" aria-live="polite">
          {title}
        </h2>
        <Button variant="ghost" aria-label={t('calendar.next')} title={t('calendar.next')} onClick={() => step(1)}>
          <ChevronRight className="size-4 rtl:rotate-180" aria-hidden />
        </Button>
        {cal.isFetching && <Spinner />}
      </div>
      <ErrorNote error={cal.error} />
      <div className="grid grid-cols-7 overflow-hidden rounded-xl border border-line bg-line gap-px text-sm">
        {cells.slice(0, 7).map((d) => (
          <div key={d.getDay()} className="bg-card py-1.5 text-center text-xs font-medium text-muted">
            <span className="sm:hidden">{weekday(d, 'narrow')}</span>
            <span className="hidden sm:inline">{weekday(d, 'short')}</span>
          </div>
        ))}
        {cells.map((d) => {
          const k = keyOf(d)
          const list = days.get(k) ?? []
          const inMonth = d.getMonth() === at.month
          return (
            <button
              key={k}
              type="button"
              onClick={() => setPicked(k)}
              aria-pressed={picked === k}
              aria-label={list.length ? `${full(d)}: ${list.length}` : full(d)}
              className={`flex min-h-14 flex-col items-stretch gap-0.5 p-1 text-start sm:min-h-24 sm:p-1.5 ${inMonth ? 'bg-card' : 'bg-paper text-muted'} ${picked === k ? 'ring-2 ring-inset ring-marquee' : 'hover:bg-paper'}`}
            >
              <span className={`self-start rounded-full px-1.5 text-xs ${k === today ? 'bg-ink font-semibold text-paper' : ''}`}>{d.getDate()}</span>
              {/* Phones get dots; wider screens get the names. */}
              {list.length > 0 && (
                <span className="flex flex-wrap gap-0.5 sm:hidden" aria-hidden>
                  {list.slice(0, 6).map((u, i) => (
                    <span key={i} className={`size-1.5 rounded-full ${u.hasFile ? 'bg-ok' : 'bg-gold'}`} />
                  ))}
                </span>
              )}
              <span className="hidden min-w-0 flex-col gap-0.5 sm:flex" aria-hidden>
                {list.slice(0, 3).map((u, i) => (
                  <span key={i} className="flex min-w-0 items-center gap-1 text-xs">
                    <span className={`size-1.5 shrink-0 rounded-full ${u.hasFile ? 'bg-ok' : 'bg-gold'}`} />
                    <span className="truncate">{u.kind === 'episode' ? u.show : u.title}</span>
                  </span>
                ))}
                {list.length > 3 && <span className="text-xs text-muted">+{list.length - 3}</span>}
              </span>
            </button>
          )
        })}
      </div>
      <section className="mt-6" aria-live="polite">
        <h2 className="mb-3 font-display text-lg font-semibold">{relativeDay(dayOf(picked).toISOString())}</h2>
        {items.length ? <Items items={items} /> : <p className="text-sm text-muted">{t('calendar.nothingDay')}</p>}
      </section>
    </>
  )
}

// Feed is the calendar's address for a phone's or computer's calendar to
// subscribe to.
function Feed({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const feed = useQuery({ queryKey: ['calendar-feed'], queryFn: () => get<{ path: string }>('/calendar/feed') })
  const renew = useMutation({ mutationFn: () => post<{ path: string }>('/calendar/feed/renew'), onSuccess: (d) => qc.setQueryData(['calendar-feed'], d) })
  const [copied, setCopied] = useState(false)
  const address = feed.data ? window.location.origin + feed.data.path : ''
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(address)
      setCopied(true)
    } catch {
      // no clipboard here: the address is in the box to copy by hand
    }
  }
  return (
    <Modal title={t('calendar.feedTitle')} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <p className="text-sm">{t('calendar.feedBody')}</p>
        {feed.isPending && <Spinner />}
        <ErrorNote error={feed.error ?? renew.error} />
        {address && (
          <div className="flex gap-2">
            <Input readOnly value={address} aria-label={t('calendar.feedTitle')} onFocus={(e) => e.target.select()} className="min-w-0 flex-1 font-mono text-xs" />
            <Button onClick={copy}>{copied ? t('common.copied') : t('common.copy')}</Button>
          </div>
        )}
        <p className="text-sm text-muted">{t('calendar.feedSafe')}</p>
        <div className="flex justify-end">
          <Button
            variant="ghost"
            busy={renew.isPending}
            onClick={() => {
              setCopied(false)
              renew.mutate()
            }}
          >
            {t('calendar.renew')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
