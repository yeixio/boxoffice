// The Box Office API, as the web UI uses it. The session cookie signs in;
// every change goes as JSON, which the server requires from a browser.

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: { Accept: 'application/json', 'X-Requested-With': 'boxoffice' }, credentials: 'same-origin' }
  if (body instanceof Blob) {
    init.body = body
  } else if (body !== undefined) {
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  const res = await fetch('/api/v1' + path, init)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: unknown = undefined
  try {
    data = text ? JSON.parse(text) : undefined
  } catch {
    // not JSON
  }
  if (!res.ok) {
    const msg = (data as { error?: string } | undefined)?.error ?? `Box Office answered ${res.status}`
    throw new ApiError(res.status, msg)
  }
  return data as T
}

export const get = <T>(path: string) => api<T>('GET', path)
export const post = <T>(path: string, body?: unknown) => api<T>('POST', path, body ?? {})
export const put = <T>(path: string, body: unknown) => api<T>('PUT', path, body)
export const patch = <T>(path: string, body: unknown) => api<T>('PATCH', path, body)
export const del = (path: string) => api<void>('DELETE', path)

// ---- types, as the server sends them ----

export type User = { id: number; username: string }
// movedIn is set once an admin has finished moving in from the old apps.
export type Status = { setup: boolean; version: string; user?: User; movedIn?: boolean }

export type Quality = { id: number; name: string; source: string; resolution: number }
export type Profile = { id: number; name: string; allowed: number[]; cutoff: number; upgrade: boolean }
export type RootFolder = { id: number; kind: 'movie' | 'series'; path: string; freeSpace: number; accessible: boolean }

export type MediaFile = { id: number; path: string; size: number; quality: number; addedAt: string }

export type Movie = {
  id: number
  tmdbId: number
  imdbId?: string
  title: string
  year?: number
  overview?: string
  poster?: string
  backdrop?: string
  runtime?: number
  status?: string
  inCinemas?: string
  digitalRelease?: string
  physicalRelease?: string
  monitored: boolean
  profileId: number
  rootFolder: string
  path: string
  addedAt: string
  file?: MediaFile
}

export type Season = { number: number; monitored: boolean }
export type Series = {
  id: number
  tmdbId: number
  tvdbId?: number
  title: string
  year?: number
  overview?: string
  poster?: string
  backdrop?: string
  status?: string
  network?: string
  runtime?: number
  monitored: boolean
  profileId: number
  rootFolder: string
  path: string
  addedAt: string
  seasons: Season[]
  episodeCount: number
  fileCount: number
}
export type Episode = {
  id: number
  seriesId: number
  season: number
  number: number
  title?: string
  overview?: string
  airDate?: string
  monitored: boolean
  file?: MediaFile
}

export type LookupMovie = Omit<Movie, 'id' | 'monitored' | 'profileId' | 'rootFolder' | 'path' | 'addedAt'> & { id?: number }
export type LookupShow = {
  id?: number
  tmdbId: number
  tvdbId?: number
  title: string
  year?: number
  overview?: string
  poster?: string
  network?: string
  status?: string
  seasons?: { number: number; episodes: number }[]
}

export type Upcoming = {
  kind: 'movie' | 'episode'
  release: 'cinemas' | 'digital' | 'physical' | 'airs'
  date: string
  title: string
  year?: number
  movieId?: number
  seriesId?: number
  show?: string
  season?: number
  episode?: number
  poster?: string
  hasFile: boolean
}

export type Job = { id: number; kind: string; subject: string; status: string; message?: string; createdAt: string; startedAt?: string; finishedAt?: string }

export type Settings = { apiKey: string; apiKeyFromEnv: boolean; hasTmdbKey: boolean; tmdbKeyFromEnv: boolean }

export type BackupRemote = {
  kind: '' | 'folder' | 's3'
  folder?: string
  endpoint?: string
  region?: string
  bucket?: string
  prefix?: string
  accessKey?: string
  hasSecret?: boolean
  pathStyle?: boolean
}
export type BackupSettings = { everyHours: number; keep: number; hasPassphrase: boolean; remote: BackupRemote }
export type Backup = { name: string; reason: string; size: number; createdAt: string; encrypted: boolean; local: boolean; remote: boolean }
export type Backups = { settings: BackupSettings; backups: Backup[]; dir: string; remoteError?: string }

export type PathMap = { from: string; to: string }
export type MigrationApp = 'radarr' | 'sonarr' | 'nzbget'
export type MigrationRequest = { app: MigrationApp; url: string; apiKey?: string; username?: string; password?: string; pathMap: PathMap[] }
export type Plan = {
  token: string
  app: MigrationApp
  appVersion: string
  url: string
  profiles: { legacyId: number; name: string; action: 'create' | 'match'; localId?: number; qualities: string[] }[]
  rootFolders: { legacyPath: string; path: string; exists: boolean; action: 'add' | 'have' }[]
  titles: { total: number; new: number; already: number; skipped: number }
  files: { total: number; missing: number }
  sample: { title: string; year?: number; path: string; hasFile: boolean }[]
  settings: {
    indexers: number
    downloadClients: number
    notifications: number
    importLists: number
    blocklist: number
    usenetServers: number
    categories: number
    sections: string[]
    hiddenSecrets: number
  }
  servers?: { name: string; host: string; port: number; tls: boolean; connections: number; level: number; hasPassword: boolean }[]
  warnings: string[]
  expiresAt: string
  pathMap: PathMap[]
  pathMapFound: boolean
}
export type Scan = {
  id: string
  app: MigrationApp
  url: string
  status: 'running' | 'ready' | 'failed'
  step: string
  done: number
  total: number
  error?: string
  plan?: Plan
  started: string
  finished?: string
}
export type Step = { name: string; status: 'waiting' | 'running' | 'done' | 'skipped' | 'failed'; message?: string }
export type Progress = {
  id: number
  app: MigrationApp
  status: 'running' | 'done' | 'failed'
  steps: Step[]
  imported: { total: number; new: number; already: number; skipped: number }
  files: number
  warnings: string[]
  backups: string[]
  started: string
  finished?: string
  matinee?: string
}
export type SwitchResult = { app: string; before: string; after: string; rootFolder?: string; qualityProfileId?: number }

export type Indexer = { id: number; name: string; protocol: string; kind: string; url: string; apiPath?: string; hasApiKey: boolean; categories: number[]; enableRss: boolean; enableSearch: boolean; priority: number; source?: string; missingSecret: boolean }
export type DownloadClient = { id: number; name: string; kind: string; protocol: string; host: string; port: number; useTls: boolean; urlBase?: string; username?: string; category?: string; priority: number; hasPassword: boolean; enabled: boolean; source?: string; missingSecret: boolean }
export type UsenetServer = { id: number; name: string; host: string; port: number; useTls: boolean; username?: string; hasPassword: boolean; connections: number; level: number; optional: boolean; retention: number; enabled: boolean; source?: string }
export type Connections = { indexers: Indexer[]; downloadClients: DownloadClient[]; usenetServers: UsenetServer[]; blocklist: number }

export type SystemInfo = { version: string; database: string; dataDir: string; go: string; os: string; startedAt: string; movies: number; series: number }

// A problem Box Office found in itself, and how to fix it.
export type HealthIssue = { id: string; level: 'error' | 'warning' | 'notice'; title: string; detail: string; steps: string[]; link?: { to: string; label: string } }
export type Health = { issues: HealthIssue[]; checked: string }

// ---- downloads ----

export type DownloadStatus = 'queued' | 'downloading' | 'paused' | 'verifying' | 'repairing' | 'unpacking' | 'importing' | 'completed' | 'failed'
export type Download = {
  id: number
  name: string
  status: DownloadStatus
  kind?: 'movie' | 'series'
  titleId?: number
  quality: number
  indexer?: string
  source?: string
  size: number
  doneBytes: number
  failedBytes: number
  folder?: string
  message?: string
  importedTo?: string
  // Where its files were moved when they looked unsafe.
  quarantined?: string
  // Its files are still there to check and unpack again.
  canReprocess?: boolean
  // It's done, and waiting to be moved into the library.
  canImport?: boolean
  // How far checking, repairing or unpacking has got.
  stepDone?: number
  stepTotal?: number
  addedAt: string
  startedAt?: string
  finishedAt?: string
}
export type DownloadSettings = { enabled: boolean; paused: boolean; speedLimit: number; import: boolean }
export type Downloads = {
  settings: DownloadSettings
  queue: Download[]
  history: Download[]
  speed: number
  servers?: { id: number; name: string; active: number; max: number; error?: string }[]
  newsServers: number
  folder: { path: string; freeSpace: number; problem?: string }
}

export type Candidate = {
  key: string
  title: string
  size: number
  publishedAt?: string
  grabs?: number
  indexer: string
  protocol: string
  quality: number
  accepted: boolean
  rejections?: string[]
  infoUrl?: string
}
export type ReleaseResults = { releases: Candidate[]; errors?: string[]; searched: number }
export type IndexerCaps = { categories: { id: number; name: string; subs?: { id: number; name: string }[] }[]; movieSearch: boolean; tvSearch: boolean }
