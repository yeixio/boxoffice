// The request site's API, under /api/portal. It signs in with its own
// cookie, which never opens the admin API.

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

export type Member = { id: number; username: string; displayName: string; name: string; source: 'local' | 'matinee' }

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

export const problems: { value: Problem; label: string }[] = [
  { value: 'wont_play', label: "It won't play" },
  { value: 'wrong_title', label: "It's the wrong movie or episode" },
  { value: 'picture', label: 'The picture is bad' },
  { value: 'sound', label: 'The sound is wrong or out of sync' },
  { value: 'subtitles', label: 'Subtitles are missing or wrong' },
  { value: 'other', label: 'Something else' },
]

export const problemLabel = (p: string) => problems.find((x) => x.value === p)?.label ?? 'Something else'

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
  switch (r.state) {
    case 'pending':
      return 'Waiting for approval'
    case 'declined':
      return 'Declined'
    case 'searching':
      return 'Approved, looking for it'
    case 'downloading':
      return r.progress ? `Downloading, ${Math.floor(r.progress * 100)}%` : 'Downloading'
    case 'partial':
      return 'Some episodes are ready'
    case 'available':
      return 'Ready to watch'
    case 'paused':
      return 'Approved, on hold'
    case 'removed':
      return 'Removed from the library'
  }
}
