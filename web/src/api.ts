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

export type User = { id: number; username: string; language?: string }
// movedIn is set once an admin has finished moving in from the old apps.
export type Status = { setup: boolean; version: string; user?: User; movedIn?: boolean }

export type Quality = { id: number; name: string; source: string; resolution: number }
// locked is the Do not touch profile, which is never picked for a new title on its own.
export type Profile = { id: number; name: string; allowed: number[]; cutoff: number; upgrade: boolean; locked?: boolean }
export type RootFolder = { id: number; kind: RootKind; path: string; freeSpace: number; accessible: boolean }

export type MediaFile = { id: number; path: string; size: number; quality: number; addedAt: string }

export type Movie = {
  id: number
  tmdbId: number
  imdbId?: string
  title: string
  sortTitle?: string
  year?: number
  overview?: string
  poster?: string
  backdrop?: string
  runtime?: number
  status?: string
  inCinemas?: string
  digitalRelease?: string
  physicalRelease?: string
  // Out of 10, from TMDB or brought from Radarr.
  rating?: number
  monitored: boolean
  profileId: number
  autoUpgrade?: '' | 'on' | 'off'
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
  autoUpgrade?: '' | 'on' | 'off'
  rootFolder: string
  path: string
  addedAt: string
  seasons: Season[]
  episodeCount: number
  fileCount: number
  sortTitle?: string
  rating?: number
  sizeOnDisk: number
  lastAired?: string
  nextAiring?: string
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
  backdrop?: string
  rating?: number
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

export type AutoSearch = {
  settings: { enabled: boolean; perDay: number; rss: boolean; rssMinutes: number; upgrades: boolean; upgradesPerMonth: number }
  missing: number
  upgrades: number
  upgradesThisMonth: number
  paused?: string
  rssPaused?: string
  cap?: string
  last?: Job
  lastRss?: { at: string; releases: number; grabbed?: string[]; problems?: string[]; skipped?: string }
}
export type Job = { id: number; kind: string; subject: string; status: string; message?: string; createdAt: string; startedAt?: string; finishedAt?: string }

// ShowDefaults say which seasons a new show watches when nobody picks.
export type ShowDefaults = { monitor: string; recent: number }

// A kind of media beyond movies and TV that an admin can turn on.
export type MediaTypeKey = 'music' | 'audiobook' | 'ebook' | 'comic'
export type MediaType = { key: MediaTypeKey; name: string; summary: string; replaces?: string; enabled: boolean; suggested: string[] }
export type RootKind = 'movie' | 'series' | MediaTypeKey
export type Settings = { apiKey: string; apiKeyFromEnv: boolean; hasTmdbKey: boolean; tmdbKeyFromEnv: boolean; tmdbKeyBuiltIn: boolean; mediaTypes: MediaType[] }

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
export type MigrationApp = 'radarr' | 'sonarr' | 'lidarr' | 'readarr' | 'nzbget'
export type MigrationRequest = { app: MigrationApp; url: string; apiKey?: string; username?: string; password?: string; pathMap: PathMap[] }
export type Plan = {
  token: string
  app: MigrationApp
  appVersion: string
  url: string
  profiles: { legacyId: number; name: string; action: 'create' | 'match'; localId?: number; qualities: string[] }[]
  rootFolders: { legacyPath: string; path: string; exists: boolean; action: 'add' | 'have'; kind?: 'ebook' | 'audiobook' }[]
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

export type Indexer = { id: number; name: string; protocol: string; kind: string; url: string; apiPath?: string; hasApiKey: boolean; categories: number[]; enableRss: boolean; enableSearch: boolean; priority: number; source?: string; missingSecret: boolean; dailyQueries: number; dailyGrabs: number }
export type IndexerState = {
  resting: boolean
  restingUntil?: string
  failures: number
  lastError?: string
  rssSyncedAt?: string
  today: { queries: number; grabs: number; auto: number }
  queryLimit?: number
  grabLimit?: number
  limitsReported: boolean
}
export type QuotaState = { used: number; left: number; problem?: string }
export type DownloadClient = { id: number; name: string; kind: string; protocol: string; host: string; port: number; useTls: boolean; urlBase?: string; username?: string; category?: string; priority: number; hasPassword: boolean; enabled: boolean; source?: string; missingSecret: boolean }
export type UsenetServer = { id: number; name: string; host: string; port: number; useTls: boolean; username?: string; hasPassword: boolean; connections: number; level: number; optional: boolean; retention: number; enabled: boolean; source?: string; quota: number; quotaSince?: string; expires?: string }
export type Connections = {
  indexers: Indexer[]
  downloadClients: DownloadClient[]
  usenetServers: UsenetServer[]
  blocklist: number
  indexerStates: Record<number, IndexerState>
  quotas: Record<number, QuotaState>
}

export type SystemInfo = { version: string; database: string; dataDir: string; go: string; os: string; startedAt: string; movies: number; series: number }

// A problem Box Office found in itself, and how to fix it.
export type HealthIssue = { id: string; level: 'error' | 'warning' | 'notice'; title: string; detail: string; steps: string[]; link?: { to: string; label: string } }
export type Health = { issues: HealthIssue[]; checked: string }

// ---- server monitoring (Health → Server, Log, Alerts) ----

export type MonitorPoint = { t: number; cpu: number; hostCpu?: number; mem: number; out: number; req: number; serverErrors: number; problems: number; downloads: number; downloadRate: number }
export type MonitorSystem = {
  os: string
  arch: string
  goVersion: string
  hostname?: string
  pid: number
  started: number
  scope: 'group' | 'process'
  cores: number
  cpuLimit?: number
  cpu: number
  hostCpu?: number
  load?: number[]
  memTotal?: number
  memAvailable?: number
  memory: number
  memLimit?: number
  goHeap: number
  goroutines: number
  bytesOut: number
  requests: number
  serverErrors: number
  warnings: number
  errors: number
  history: MonitorPoint[]
}
export type LogAttr = { key: string; value: string }
export type LogLevel = 'debug' | 'info' | 'warn' | 'error'
export type LogLine = { seq: number; time: number; level: LogLevel; message: string; attrs?: LogAttr[] }
export type Problem = { level: 'warn' | 'error'; message: string; detail?: string; count: number; first: number; last: number; attrs?: LogAttr[] }
export type MonitorDisk = { total: number; free: number; reachable: boolean; uses: { use: 'data' | 'downloads' | 'backups' | 'movies' | 'tv'; path: string }[] }
export type Alert = { id: string; rule: string; key: string; level: 'info' | 'warning' | 'critical'; title: string; message: string; time: number; resolved?: boolean; sent: number; failed?: string[] }
export type Monitor = {
  version: string
  database: string
  databaseMs: number
  databaseError?: string
  debug: boolean
  debugForced?: boolean
  system: MonitorSystem
  disks: MonitorDisk[]
  downloads: Partial<Record<DownloadStatus, number>>
  downloadFailures: number
  jobs: { queued: number; running: number; failed: number }
  problems: Problem[]
  alerts: Alert[]
  logLines: number
  logsSince?: number
}

export type WebhookKind = 'discord' | 'ntfy' | 'homeassistant' | 'json'
export type Toggle = { on: boolean }
export type RateRule = { on: boolean; count: number; minutes: number }
export type AlertRules = { disk: { on: boolean; percent: number }; folder: Toggle; errors: RateRule; downloads: RateRule; restart: Toggle; backup: Toggle }
export type AlertEmail = { on: boolean; host: string; port: number; security: 'starttls' | 'tls' | 'none'; username: string; password?: string; from: string; to: string; hasPassword?: boolean }
export type AlertWebhook = { id: string; kind: WebhookKind; name?: string; url?: string; token?: string; hint?: string; hasToken?: boolean }
export type AlertSettings = { email: AlertEmail; webhooks: AlertWebhook[]; rules: AlertRules; firing: Alert[]; history: Alert[]; links: boolean; forwarded: boolean }
export type MetricsSettings = { on: boolean; token?: string; path: string }

// ---- downloads ----

export type DownloadStatus = 'queued' | 'downloading' | 'paused' | 'downloaded' | 'verifying' | 'repairing' | 'unpacking' | 'importing' | 'completed' | 'failed'
// What a download or a file is for: a movie, a show, or a book in one
// format.
export type TitleKind = 'movie' | 'series' | 'ebook' | 'audiobook'

export type Download = {
  id: number
  name: string
  status: DownloadStatus
  kind?: TitleKind
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
  // It failed because too much is gone from the news servers to repair.
  missing?: boolean
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
export type DownloadSettings = { enabled: boolean; paused: boolean; speedLimit: number; import: boolean; deleteFailed: boolean; quarantineDays: number }
// One release held in the quarantine folder.
export type QuarantineItem = {
  path: string
  name: string
  size: number
  files: number
  since: string
  reason?: string
  download?: { id: number; name: string; status: string; kind?: TitleKind; titleId?: number }
  busy?: boolean
}
export type Quarantine = { items: QuarantineItem[]; size: number; days: number }
export type Downloads = {
  settings: DownloadSettings
  queue: Download[]
  history: Download[]
  speed: number
  servers?: { id: number; name: string; active: number; max: number; error?: string }[]
  newsServers: number
  folder: { path: string; freeSpace: number; problem?: string }
  cap: DataCap
  waiting?: string
  // How many finished downloads there are, of which history is a page.
  historyTotal?: number
  // Failures for articles gone from the news servers left out of the history.
  historyHidden?: number
}
// The downloads folder's file browser.
export type DownloadFile = {
  name: string
  // Relative to the downloads folder, with forward slashes.
  path: string
  dir: boolean
  link?: boolean
  size: number
  files?: number
  modified: string
  quarantined?: boolean
  // A download is still using it, and busyWith is what it's doing.
  busy?: boolean
  busyWith?: DownloadStatus
  download?: { id: number; name: string; status: DownloadStatus; kind?: TitleKind; titleId?: number }
  moving?: boolean
  moved?: string
  video?: boolean
}
export type DownloadFiles = {
  root: string
  listing: { path: string; entries: DownloadFile[]; size: number; quarantine: number; moves: { path: string; running: boolean; message?: string }[] }
  disk?: { total: number; free: number }
  problem?: string
}
// The monthly data cap, and how much of it is used.
export type DataCapSettings = { enabled: boolean; gb: number; upgradesAt: number; missingAt: number; resetDay: number }
export type DataCap = DataCapSettings & { used: number; limit: number; since: string; resets: string; level: '' | 'upgrades' | 'missing' | 'full' }

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
  pack?: boolean
  episodes?: number[]
  // A book release's format, as its name gives it: EPUB, M4B.
  format?: string
}
export type ReleaseResults = { releases: Candidate[]; errors?: string[]; searched: number }
export type IndexerCaps = { categories: { id: number; name: string; subs?: { id: number; name: string }[] }[]; movieSearch: boolean; tvSearch: boolean }

// What deleting files from the library takes. folder is set when a whole
// title's folder goes; files names up to 200 of them, biggest first.
export type DeletePlan = { folder?: string; files: { path: string; size: number }[]; count: number; size: number; problem?: string }
// Deleting a movie's, a season's or an episode's files, keeping the title.
export type FileRemoval = DeletePlan & { releases: { name: string; indexer?: string }[]; watched: boolean }
export type RecycleFolder = { root: string; folder: string; count: number; size: number; oldest?: string }
export type Recycle = { folders: RecycleFolder[]; keepDays: number }

// ---- music ----

export type Artist = {
  id: number
  mbid: string
  name: string
  sortName: string
  type?: string
  country?: string
  disambiguation?: string
  monitored: boolean
  albumTypes: string[]
  rootFolder: string
  path: string
  addedAt: string
  refreshedAt?: string
  albums: number
  wantedAlbums: number
  // How many of its albums are in the library.
  haveAlbums: number
}
// An album's copy in the library is its folder, with the size of its tracks.
export type AlbumFile = { id: number; path: string; size: number; addedAt: string }
export type Album = { id: number; artistId: number; mbid: string; title: string; type: string; secondaryTypes: string[]; released?: string; monitored: boolean; cover: string; file?: AlbumFile }
// Which of FLAC and MP3 albums are wanted in.
export type MusicQuality = 'flac-first' | 'flac' | 'mp3'
export type MusicSettings = { quality: MusicQuality; qualities: MusicQuality[] }
export type ArtistDetail = Artist & { albumList: Album[] }
export type LookupArtist = { mbid: string; name: string; sortName: string; type?: string; country?: string; disambiguation?: string; began?: string; ended?: string; id?: number }
export type AlbumTypes = { types: string[]; defaults: string[] }

// A root folder scan: folders on disk that aren't on the list, matched to
// TMDB or MusicBrainz and ready to add where they are.
export type ScanMatch = { tmdbId?: number; mbid?: string; olKey?: string; title: string; year?: number; poster?: string; overview?: string; disambiguation?: string }
export type ScanFound = {
  path: string
  name: string
  files: number
  size: number
  episodes?: number
  guess: { title: string; year?: number }
  match?: ScanMatch
  sure: boolean
  have?: { id: number; title: string; path: string }
  note?: string
  problem?: string
  added?: number
}
export type LibScan = {
  rootId: number
  root: string
  kind: RootKind
  status: 'scanning' | 'ready' | 'adding' | 'done' | 'failed'
  step: string
  done: number
  total: number
  error?: string
  folders: number
  listed: number
  elsewhere: number
  loose: number
  found: ScanFound[]
  added: number
  notAdded: number
  started: string
  finished?: string
}

// ---- discover ----

// Where a title from TMDB stands here: on disk, partly on disk (a show),
// wanted, or on the list but paused. None means it's not on the list.
export type DiscoverStatus = 'available' | 'partial' | 'wanted' | 'paused'
export type DiscoverKind = 'movie' | 'series'
export type DiscoverTitle = {
  kind: DiscoverKind
  tmdbId: number
  title: string
  year?: number
  overview?: string
  poster?: string
  backdrop?: string
  rating?: number
  date?: string
  status?: DiscoverStatus
  id?: number
}
export type DiscoverRow = { id: string; title: string; because?: string; items: DiscoverTitle[]; more?: boolean }
export type DiscoverPage = DiscoverRow & { page: number; pages: number }
export type Genre = { id: number; name: string }
export type Network = { id: number; name: string; logo?: string }
export type DiscoverHome = { rows: DiscoverRow[]; browse: { movieGenres: Genre[]; seriesGenres: Genre[]; networks: Network[] } }

// ---- books ----

export type BookFormat = 'ebook' | 'audiobook'
export type Author = {
  id: number
  olKey: string
  name: string
  sortName: string
  born?: string
  died?: string
  bio?: string
  newEbooks: boolean
  newAudiobooks: boolean
  // Do not touch: no searches, downloads or file changes for their books.
  locked: boolean
  addedAt: string
  books: number
  wantedEbooks: number
  wantedAudiobooks: number
  haveEbooks: number
  haveAudiobooks: number
}
export type BookFile = { id: number; path: string; size: number; addedAt: string }
export type Book = {
  id: number
  authorId: number
  olKey: string
  title: string
  subtitle?: string
  year?: number
  editions: number
  wantEbook: boolean
  wantAudiobook: boolean
  cover?: string
  // The copies in the library; an audiobook's is its folder.
  ebookFile?: BookFile
  audiobookFile?: BookFile
  authorName?: string
}
export type AuthorDetail = Author & { bookList: Book[] }
export type LookupAuthor = { key: string; name: string; born?: string; died?: string; topWork?: string; works?: number; photo?: string; id?: number }
