// The request site's API, under /api/portal. It signs in with its own
// cookie, which never opens the admin API.

import i18n, { formatLocale } from '../i18n'
import { ApiError, type DiscoverHome, type DiscoverKind, type DiscoverPage, type DiscoverTitle } from '../api'

export async function papi<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: { Accept: 'application/json', 'X-Requested-With': 'boxoffice' }, credentials: 'same-origin' }
  if (body !== undefined) {
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  const res = await fetch('/api/portal' + path, init)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: unknown = undefined
  try {
    data = text ? JSON.parse(text) : undefined
  } catch {
    // not JSON
  }
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string } | undefined)?.error ?? `The server answered ${res.status}`)
  return data as T
}

export const pget = <T>(path: string) => papi<T>('GET', path)
export const ppost = <T>(path: string, body?: unknown) => papi<T>('POST', path, body ?? {})

export type Member = { id: number; username: string; displayName: string; name: string; source: 'local' | 'matinee'; language?: string }

export type Site = {
  enabled: boolean
  name: string
  welcome: string
  accent: string
  hasLogo: boolean
  localSignIn: boolean
  signUp: boolean
  matinee: boolean
  member?: Member
}

export type RequestState = 'pending' | 'declined' | 'searching' | 'downloading' | 'partial' | 'available' | 'paused' | 'removed'

export type Request = {
  id: number
  memberId: number
  member?: string
  kind: DiscoverKind
  tmdbId: number
  title: string
  year?: number
  poster?: string
  status: 'pending' | 'approved' | 'declined'
  note?: string
  reply?: string
  createdAt: string
  decidedAt?: string
  state: RequestState
  progress?: number
  titleId?: number
}

export type Problem = 'wont_play' | 'wrong_title' | 'picture' | 'sound' | 'subtitles' | 'other'

const problemKinds: Problem[] = ['wont_play', 'wrong_title', 'picture', 'sound', 'subtitles', 'other']

// problems are the kinds of problem someone can report, in the page's language.
export const problems = (): { value: Problem; label: string }[] => problemKinds.map((value) => ({ value, label: problemLabel(value) }))

export const problemLabel = (p: string) => i18n.t(`request:problems.${problemKinds.includes(p as Problem) ? p : 'other'}`)

export type Issue = {
  id: number
  memberId: number
  member?: string
  kind: DiscoverKind
  tmdbId: number
  title: string
  year?: number
  poster?: string
  season?: number
  episode?: number
  problem: Problem
  message?: string
  status: 'open' | 'resolved'
  reply?: string
  createdAt: string
  resolvedAt?: string
  titleId?: number
}

export type { DiscoverHome, DiscoverPage, DiscoverTitle }

// stateText is what's happening with a request, in a few words.
export function stateText(r: Pick<Request, 'state' | 'progress' | 'kind'>): string {
  if (r.state === 'downloading' && r.progress)
    return i18n.t('request:state.downloadingAt', { percent: new Intl.NumberFormat(formatLocale(), { style: 'percent' }).format(Math.floor(r.progress * 100) / 100) })
  return i18n.t(`request:state.${r.state}`)
}
