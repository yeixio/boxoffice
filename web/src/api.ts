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

// FileMedia is what the quality check found inside a file.
export type FileMedia = {
  width: number
  height: number
  videoCodec?: string
  bitDepth?: number
  hdr?: string
  audioCodec?: string
  channels?: number
  bitrate?: number
  duration?: number
  error?: string
}
// guessed is the quality a file had before the quality check corrected it.
// edition is the cut a movie's copy is, from its name or, with
// editionByLength, a guess from how long it runs.
export type MediaFile = {
  id: number
  path: string
  size: number
  quality: number
  addedAt: string
  media?: FileMedia
  guessed?: number
  edition?: string
  // Where the edition was told from; length is a guess.
  editionFrom?: 'name' | 'release' | 'file' | 'length'
  editionByLength?: boolean
  // Its subtitles in each language wanted, while subtitles are on.
  subtitles?: FileSubtitle[]
}

// A file's subtitles in one language (Settings → Subtitles): inside it,
// beside it, downloaded, looked for and not found yet, waiting on a
// provider, or not looked at yet.
export type SubtitleState = 'embedded' | 'on_disk' | 'fetched' | 'missing' | 'waiting' | 'pending'
export type FileSubtitle = {
  language: string
  state: SubtitleState
  provider?: string
  release?: string
  matchesFile?: boolean
  sdh?: boolean
  tries?: number
  note?: string
  checkedAt?: string
  nextAt?: string
}

export type SubtitleSettings = {
  config: {
    on: boolean
    languages: string[]
    preferSdh: boolean
    dailyLimit: number
    podnapisi: boolean
    openSubtitles: { username: string; hasApiKey: boolean; hasPassword: boolean }
  }
  status: {
    running: boolean
    done: number
    error?: string
    ffprobe: boolean
    counts: { files: number; complete: number; missing: number; waiting: number; pending: number; fetched: number }
    today: { looked: number; limit: number; providers: Record<string, { searches: number; downloads: number; usedUp?: boolean }> }
    recent: { kind: 'movie' | 'series'; titleId: number; title: string; season?: number; episode?: number; language: string; provider: string; at: string }[]
  }
  languages: { code: string; name: string }[]
  suggested: string[]
}

export type QualityCheckState = {
  status: { running: boolean; done: number; ffprobe: boolean; error?: string; files: number; checked: number; unreadable: number; unknown: number }
  corrections: { fileId: number; kind: 'movie' | 'series'; titleId: number; title: string; path: string; was: number; now: number; media: FileMedia; at: string }[]
  more: number
}

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
  // Other cuts kept next to its copy.
  editions?: MediaFile[]
  // The edition its copy should be, once one was asked for in its place.
  wantedEdition?: string
  reported?: Reported
}

// A problem someone reported in Matinee with a movie's or an episode's
// copy: Box Office blocked the bad copy's release and is getting another.
export type Reported = {
  kind: 'movie' | 'episode'
  ownerId: number
  problem: 'wrong' | 'language' | 'video' | 'audio'
  // The language (ISO 639-1) its sound should be in, for language.
  language?: string
  reportedBy?: string
  note?: string
  updatedAt: string
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
  settings: { enabled: boolean; perDay: number; rss: boolean; rssMinutes: number; upgrades: boolean; upgradesPerMonth: number; replaceSameQuality: boolean; propers: boolean; minAgeMinutes: number; regrabsPerDay: number }
  missing: number
  upgrades: number
  upgradesThisMonth: number
  paused?: string
  rssPaused?: string
  cap?: string
  last?: Job
  lastRss?: { at: string; releases: number; grabbed?: string[]; problems?: string[]; skipped?: string }
  // New releases held back until they're old enough to grab by themselves.
  held?: { key: string; title: string; dueAt: string; heldAt: string }[]
  // Replacements of copies on disk grabbed by Box Office itself today, and
  // those waiting for a day with room (why: upgrade, fix or replace).
  regrabsToday: number
  regrabsWaiting?: { key: string; subject: string; why: 'upgrade' | 'fix' | 'replace'; release: string; since: string }[]
}
export type Job = { id: number; kind: string; subject: string; status: string; message?: string; createdAt: string; startedAt?: string; finishedAt?: string }

// ShowDefaults say which seasons a new show watches when nobody picks.
export type ShowDefaults = { monitor: string; recent: number }

// A kind of media beyond movies and TV that an admin can turn on.
export type MediaTypeKey = 'music' | 'audiobook' | 'ebook' | 'comic' | 'game'
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

// What the Restart button needs to know first.
export type RestartInfo = { downloads: number; how: 'kubernetes' | 'self'; available: boolean }

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
  // The run before this one, from the log on disk; crash is the first line
  // of its crash, when it crashed.
  previousRun?: { lines: number; started?: number; ended?: number; crash?: string }
}

export type WebhookKind = 'discord' | 'ntfy' | 'homeassistant' | 'json'
export type Toggle = { on: boolean }
export type RateRule = { on: boolean; count: number; minutes: number }
export type AlertRules = { disk: { on: boolean; percent: number }; folder: Toggle; errors: RateRule; downloads: RateRule; restart: Toggle; backup: Toggle; problems: Toggle }
export type AlertEmail = { on: boolean; host: string; port: number; security: 'starttls' | 'tls' | 'none'; username: string; password?: string; from: string; to: string; hasPassword?: boolean }
export type AlertWebhook = { id: string; kind: WebhookKind; name?: string; url?: string; token?: string; hint?: string; hasToken?: boolean }
export type AlertSettings = { email: AlertEmail; webhooks: AlertWebhook[]; rules: AlertRules; firing: Alert[]; history: Alert[]; links: boolean; forwarded: boolean }
export type MetricsSettings = { on: boolean; token?: string; path: string }

// ---- e-readers ----

export type EreaderKind = 'kindle' | 'pocketbook' | 'other'
export type Ereader = { id: string; name: string; kind: EreaderKind; address: string; auto: boolean }
export type EreaderMail = { sameAsAlerts: boolean; host: string; port: number; security: 'starttls' | 'tls' | 'none'; username: string; password?: string; from: string; hasPassword?: boolean }
export type EreaderSend = { id: number; deviceId: string; deviceName: string; bookId?: number; title: string; auto: boolean; error?: string; at: string }
export type EreaderSettings = {
  mail: EreaderMail
  devices: Ereader[]
  alertMail: { ready: boolean; host?: string; from?: string }
  from: string
  canConvert: boolean
  history: EreaderSend[]
}

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
  // Added by hand for no title: what to file it as once it's downloaded
  // ("auto" works it out).
  fileAs?: string
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
// The speed schedule: times of the week with their own speed. Days are
// 0 (Sunday) to 6, times "HH:MM" on the server's clock, limits in KB/s.
export type SpeedMode = 'full' | 'limit' | 'pause'
export type SpeedRange = { days: number[]; from: string; to: string; mode: SpeedMode; limit: number }
export type SpeedSchedule = { enabled: boolean; ranges: SpeedRange[] }
export type SpeedStep = { mode: SpeedMode; limit: number; source: 'setting' | 'schedule' | 'override'; range: number }
export type SpeedNow = SpeedStep & {
  until?: string
  next?: SpeedStep
  override?: { mode: SpeedMode; limit: number; until?: string }
  // The server's time zone and its clock now, which the schedule's times are on.
  zone: string
  clock: string
}
export type Downloads = {
  settings: DownloadSettings
  schedule: SpeedSchedule
  speedNow: SpeedNow
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
  // How many finished downloads there are of each type and each state,
  // for the history's filters.
  historyTypes?: Record<string, number>
  historyStates?: Record<string, number>
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
  // Set when it's a fixed re-release of the copy there (PROPER, REPACK,
  // REAL, v2), taken in its place at its quality.
  fix?: string
  // Too new for Box Office to grab by itself until then; a grab by hand doesn't wait.
  newUntil?: string
}
export type ReleaseResults = { releases: Candidate[]; errors?: string[]; searched: number }
export type IndexerCaps = { categories: { id: number; name: string; subs?: { id: number; name: string }[] }[]; movieSearch: boolean; tvSearch: boolean }

// What deleting files from the library takes. folder is set when a whole
// title's folder goes; files names up to 200 of them, biggest first.
export type DeletePlan = { folder?: string; files: { path: string; size: number }[]; count: number; size: number; problem?: string }
// Removing a movie or a show: its folder, and its downloads that are cancelled.
export type TitleRemoval = DeletePlan & { downloads?: number }
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
  // linked: it went in with the title it matched, kept in another folder.
  linked?: boolean
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
// LookupPerson is an actor or other person found on TMDB; the row
// person-{tmdbId} is what they're in.
export type LookupPerson = { tmdbId: number; name: string; photo?: string; department?: string; knownFor: string[] }
export type DiscoverRow = { id: string; title: string; because?: string; person?: LookupPerson; items: DiscoverTitle[]; more?: boolean }
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
  // A picture of the author, from Open Library or Wikimedia Commons.
  photo?: string
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
  // The series or collection it's part of, and its place in it ("1",
  // "1-3" for an omnibus, or none).
  series?: string
  seriesPosition?: string
  cover?: string
  // The copies in the library; an audiobook's is its folder.
  ebookFile?: BookFile
  audiobookFile?: BookFile
  authorName?: string
}
export type AuthorDetail = Author & { bookList: Book[] }
export type LookupAuthor = { key: string; name: string; born?: string; died?: string; topWork?: string; works?: number; photo?: string; id?: number }

// ---- problem files ----

export type ProblemKind = 'duplicate' | 'editions' | 'parts' | 'disc_image' | 'untracked' | 'missing' | 'naming' | 'unknown' | 'not_listed' | 'leftover' | 'empty'
export type ProblemAction = 'rename' | 'set_aside' | 'keep_one' | 'keep_editions' | 'record' | 'forget' | 'convert' | 'join' | 'remove_dirs'
export type ProblemMedia = { duration: number; width?: number; height?: number; video?: string; hdr?: string; audio?: string[]; subtitles?: number; title?: string }
// name is the file's name as it was meant to be, when it's garbled on
// disk; count, for a folder, how many files are in it.
export type ProblemFile = { path: string; size: number; dir?: boolean; count?: number; name?: string; recorded?: boolean; episode?: string; media?: ProblemMedia; edition?: string; byLength?: boolean }
export type ProblemMove = { from: string; to: string; folder?: boolean }
export type LibProblem = {
  id: string
  kind: ProblemKind
  root: string
  title?: { kind: 'movie' | 'series'; id: number; name: string; year?: number; locked?: boolean }
  detail?: string
  copies?: boolean
  files: ProblemFile[]
  fix?: { action: ProblemAction; moves?: ProblemMove[]; paths?: string[]; keep?: string; output?: string; blocked?: 'read_only' | 'locked' | 'output_exists' }
}
export type ProblemRoot = { path: string; kind: 'movie' | 'series'; readable: boolean; writable: boolean; files: number }
export type ProblemFixFile = { role: 'aside' | 'moved' | 'made' | 'record' | 'forgot' | 'removed_dir'; src: string; dest: string; size: number }
export type ProblemFix = {
  id: number
  action: ProblemAction
  // The kind of problem it fixed, so the page can say why.
  kind: ProblemKind | ''
  detail?: string
  title: string
  root: string
  state: 'waiting' | 'done' | 'undone'
  createdAt: string
  decidedAt?: string
  files: ProblemFixFile[]
}
export type ProblemsState = {
  status: {
    looking: boolean
    done: number
    total: number
    lookedAt?: string
    next?: string
    error?: string
    ffmpeg: boolean
    fixing?: { id: string; action: ProblemAction; progress: number }
    queued: string[]
    failed: { id: string; error: string }[]
    // A batch of set-aside originals being put back or deleted for good,
    // and the ones the last batch couldn't.
    deciding?: { action: 'put_back' | 'delete'; done: number; total: number; freed: number }
    undecided: { id: number; title: string; error: string }[]
  }
  report: { roots: ProblemRoot[]; problems: LibProblem[] } | null
  fixes: ProblemFix[]
}

// Games: each system's folder in the games root folders, and the games in
// it, checked against MAME's list or No-Intro's.
export type GameSystem = {
  id: number
  system: string
  name: string
  short: string
  maker: string
  arcade: boolean
  // hash: games are checked byte for byte; otherwise they're known by the
  // serial, title ID or name in them (discs, downloads, PC games).
  hash: boolean
  path: string
  rootFolder: string
  locked: boolean
  format?: string
  list?: string
  support: number
  note?: string
  addedAt: string
  checkedAt?: string
  games: number
  groups: number
  problems: number
}
export type GameStatus = 'complete' | 'identified' | 'missing' | 'wrong' | 'bios' | 'unknown' | 'unreadable'
export type GameProblem = { kind: string; name: string }
export type Game = {
  id: number
  systemId: number
  name: string
  title: string
  parent?: string
  parentTitle?: string
  year?: string
  maker?: string
  players?: number
  genre?: string
  path: string
  inParent?: boolean
  size: number
  crc?: string
  serial?: string
  status: GameStatus
  problems: GameProblem[]
  addedAt: string
}
export type GameGroup = { game: Game; versions: number; problems: number }
export type GamePage = { system: GameSystem; groups: GameGroup[]; total: number; all: number; genres: string[]; decades: string[] }
export type GameFile = { path: string; size: number; parent?: boolean }
export type GameDetail = Game & { system: GameSystem; files: GameFile[]; versions: Game[] }
export type GameCheck = {
  running: boolean
  step?: 'finding' | 'list' | 'reading' | 'checking'
  system?: string
  done: number
  total: number
  systems: number
  games: number
  unsure: string[]
  error?: string
  started?: string
  finished?: string
}
export type GameSettings = { mameVersion: string; newest?: string; versions: string[] }
// A system Box Office knows, wanted games come from its list.
export type GameSystemInfo = { key: string; name: string; short: string; maker: string; arcade: boolean; hash: boolean }
// One version of a game on a system's list (No-Intro's or MAME's).
export type CatalogGame = {
  system: string
  name: string
  title: string
  tags?: string[]
  year?: string
  maker?: string
  genre?: string
  parent?: string
  crc?: string
  serial?: string
  size?: number
}
// free: the system has no list, so this is the name asked for.
export type GameLookup = { system: string; title: string; best: CatalogGame; versions: CatalogGame[]; free?: boolean; onDisk: boolean; wantId?: number; wantName?: string }
export type GameWant = {
  id: number
  system: string
  name: string
  title: string
  year?: string
  maker?: string
  crc?: string
  size?: number
  gotAt?: string
  addedAt: string
  systemName: string
  systemShort: string
  arcade: boolean
  searched?: string
  misses?: number
  download?: Download
}
