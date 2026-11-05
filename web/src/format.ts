// Small helpers for showing values.

export function bytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  const v = n / 1024 ** i
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export function day(iso?: string): string {
  if (!iso) return ''
  return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

export function when(iso?: string): string {
  if (!iso) return ''
  return new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

export function episodeCode(season: number, episode: number): string {
  return `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`
}

// relativeDay says "Today", "Tomorrow", a weekday this week, or the date.
export function relativeDay(iso: string, now = new Date()): string {
  const d = new Date(iso)
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diff = Math.round((start(d) - start(now)) / 86400000)
  if (diff === 0) return 'Today'
  if (diff === 1) return 'Tomorrow'
  if (diff === -1) return 'Yesterday'
  if (diff > 1 && diff < 7) return d.toLocaleDateString(undefined, { weekday: 'long' })
  return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
}

export const appName: Record<string, string> = { radarr: 'Radarr', sonarr: 'Sonarr', nzbget: 'NZBGet' }

// count says "1 file" or "2 files".
export function count(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}

// runtime says "48 min" or "2 h 16 min".
export function runtime(minutes?: number): string {
  if (!minutes) return ''
  if (minutes < 60) return `${minutes} min`
  const m = minutes % 60
  return m ? `${Math.floor(minutes / 60)} h ${m} min` : `${minutes / 60} h`
}

// gb is a data amount the way internet plans and Usenet blocks count it,
// in 1000s: 2.1 TB, 450 GB.
export function gb(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 GB'
  if (n >= 1e12) return `${(n / 1e12).toFixed(n >= 1e13 ? 0 : 1)} TB`
  if (n >= 1e9) return `${(n / 1e9).toFixed(n >= 1e11 ? 0 : 1)} GB`
  return `${Math.round(n / 1e6)} MB`
}
