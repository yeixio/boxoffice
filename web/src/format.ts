// Small helpers for showing values, in the page's language.
import i18n, { formatLocale } from './i18n'

const number = (v: number, digits = 0) =>
  new Intl.NumberFormat(formatLocale(), { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(v)

const sizeUnits = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const

// size says "1.5 GB" the language's way: 1,5 GB, or 1.5 Go in French.
function size(value: number, unit: (typeof sizeUnits)[number], digits = 0): string {
  return i18n.t(`common:size.${unit}`, { value: number(value, digits) })
}

export function bytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return size(0, 'B')
  const i = Math.min(sizeUnits.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  const v = n / 1024 ** i
  return size(v, sizeUnits[i], v >= 100 || i === 0 ? 0 : 1)
}

export function day(iso?: string): string {
  if (!iso) return ''
  return new Date(iso).toLocaleDateString(formatLocale(), { year: 'numeric', month: 'short', day: 'numeric' })
}

export function when(iso?: string): string {
  if (!iso) return ''
  return new Date(iso).toLocaleString(formatLocale(), { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

export function episodeCode(season: number, episode: number): string {
  return `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`
}

const capitalize = (s: string) => s.charAt(0).toLocaleUpperCase(formatLocale()) + s.slice(1)

// relativeDay says "Today", "Tomorrow", a weekday this week, or the date.
export function relativeDay(iso: string, now = new Date()): string {
  const d = new Date(iso)
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diff = Math.round((start(d) - start(now)) / 86400000)
  if (diff >= -1 && diff <= 1) return capitalize(new Intl.RelativeTimeFormat(formatLocale(), { numeric: 'auto' }).format(diff, 'day'))
  if (diff > 1 && diff < 7) return capitalize(d.toLocaleDateString(formatLocale(), { weekday: 'long' }))
  return d.toLocaleDateString(formatLocale(), { weekday: 'short', month: 'short', day: 'numeric' })
}

export const appName: Record<string, string> = { radarr: 'Radarr', sonarr: 'Sonarr', nzbget: 'NZBGet' }

// runtime says "48 min" or "2 h 16 min".
export function runtime(minutes?: number): string {
  if (!minutes) return ''
  if (minutes < 60) return i18n.t('common:duration.minutes', { count: minutes })
  const m = minutes % 60
  const h = Math.floor(minutes / 60)
  return m ? i18n.t('common:duration.hoursMinutes', { hours: h, minutes: m }) : i18n.t('common:duration.hours', { hours: h })
}

// gb is a data amount the way internet plans and Usenet blocks count it,
// in 1000s: 2.1 TB, 450 GB.
export function gb(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return size(0, 'GB')
  if (n >= 1e12) return size(n / 1e12, 'TB', n >= 1e13 ? 0 : 1)
  if (n >= 1e9) return size(n / 1e9, 'GB', n >= 1e11 ? 0 : 1)
  return size(Math.round(n / 1e6), 'MB')
}

// decimal says 7.3 the language's way: 7,3 in German.
export function decimal(n: number, digits = 1): string {
  return number(n, digits)
}

// titleLink is where a download's or a file's title is shown, and what to
// call it.
export function titleLink(kind?: string, id?: number): { to: string; label: string } | null {
  if (!kind || !id) return null
  switch (kind) {
    case 'movie':
      return { to: `/movies/${id}`, label: i18n.t('web:discover.movie') }
    case 'series':
      return { to: `/shows/${id}`, label: i18n.t('web:discover.show') }
    case 'ebook':
      return { to: `/books/book/${id}`, label: i18n.t('web:kind.ebook') }
    case 'audiobook':
      return { to: `/audiobooks/book/${id}`, label: i18n.t('web:kind.audiobook') }
  }
  return null
}
