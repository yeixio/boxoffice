import { useTranslation } from 'react-i18next'
import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Check, ChevronRight, File as FileIcon, Folder, FolderOpen, HardDrive, LoaderCircle, Search, Trash2, Upload, X } from 'lucide-react'
import { ApiError, del, get, patch, post, type GameLookup, type LookupArtist, type LookupAuthor, type LookupMovie, type LookupShow } from '../api'
import { bytes } from '../format'
import { Badge, Button, Card, Empty, ErrorNote, Heading, Input, Modal, Poster, Select, Spinner } from '../ui'

// ---- what the server says ----

export type ImportKind = 'movie' | 'series' | 'album' | 'ebook' | 'audiobook' | 'game'
type ImportMatch = { tmdbId?: number; mbid?: string; olKey?: string; title: string; year?: number }
export type ImportItem = {
  path: string
  name: string
  dir: boolean
  files: number
  size: number
  kind: ImportKind | ''
  title?: string
  year?: number
  season?: number
  episode?: number
  match?: ImportMatch
  part?: string
  system?: string
  game?: string
  sure: boolean
  picked?: boolean
  status: 'guessing' | 'ready' | 'importing' | 'done' | 'failed'
  problem?: string
  to?: string
}
type GameSystemInfo = { key: string; name: string; short: string; arcade?: boolean }
export type ImportState = {
  folder: string
  items: ImportItem[]
  copying: { running: boolean; done: number; total: number; current?: string; error?: string }
  running: boolean
  kinds: ImportKind[]
  systems: GameSystemInfo[]
}
type Pick = { kind?: ImportKind; tmdbId?: number; mbid?: string; olKey?: string; name?: string; year?: number; part?: string; system?: string; game?: string }
type Entry = { name: string; dir: boolean; size?: number }
type Listing = { path: string; parent: string; entries: Entry[] }

const allKinds: ImportKind[] = ['movie', 'series', 'album', 'ebook', 'audiobook', 'game']

// ready says an item can go in as it is.
export const ready = (it: ImportItem) =>
  (it.status === 'ready' || it.status === 'failed') && !it.problem && (!!it.match || (it.kind === 'game' && !!it.system))

// ---- uploads ----
//
// Uploads go one file at a time and carry on while you look at other
// pages, so they're kept outside the page.

type Upload = { id: number; name: string; size: number; sent: number; state: 'waiting' | 'sending' | 'done' | 'failed'; error?: string; file: File }
let uploads: Upload[] = []
let nextID = 1
let sending = false
const listeners = new Set<() => void>()
const changed = () => {
  uploads = [...uploads]
  listeners.forEach((l) => l())
}
const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => listeners.delete(l)
}
let onFileDone: (() => void) | undefined

// uploadName is the name a file is sent as: its path in the folder picked,
// for a folder.
export const uploadName = (f: File) => (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name

function addUploads(files: File[]) {
  for (const f of files) {
    if (uploadName(f).split('/').some((p) => p.startsWith('.'))) continue
    uploads.push({ id: nextID++, name: uploadName(f), size: f.size, sent: 0, state: 'waiting', file: f })
  }
  changed()
  void sendAll()
}

function send(u: Upload): Promise<void> {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest()
    x.open('PUT', '/api/v1/import/upload?name=' + encodeURIComponent(u.name))
    x.setRequestHeader('X-Requested-With', 'boxoffice')
    x.withCredentials = true
    x.upload.onprogress = (e) => {
      u.sent = e.loaded
      changed()
    }
    x.onload = () => {
      if (x.status < 300) return resolve()
      let msg = `Box Office answered ${x.status}`
      try {
        msg = JSON.parse(x.responseText).error ?? msg
      } catch {
        // not JSON
      }
      reject(new ApiError(x.status, msg))
    }
    x.onerror = () => reject(new Error('network'))
    x.send(u.file)
  })
}

async function sendAll() {
  if (sending) return
  sending = true
  try {
    for (;;) {
      const u = uploads.find((x) => x.state === 'waiting')
      if (!u) break
      u.state = 'sending'
      changed()
      try {
        await send(u)
        u.state = 'done'
        u.sent = u.size
      } catch (e) {
        u.state = 'failed'
        u.error = e instanceof Error ? e.message : String(e)
      }
      changed()
      onFileDone?.()
    }
  } finally {
    sending = false
  }
}

function clearUploads() {
  uploads = uploads.filter((u) => u.state === 'waiting' || u.state === 'sending')
  changed()
}

const useUploads = () => useSyncExternalStore(subscribe, () => uploads)

// ---- the page ----

export function ImportFiles() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const ups = useUploads()
  const uploading = ups.some((u) => u.state === 'waiting' || u.state === 'sending')
  const state = useQuery({
    queryKey: ['import'],
    queryFn: () => get<ImportState>('/import'),
    refetchInterval: (q) => {
      const d = q.state.data
      const busy = !!d && (d.running || d.copying.running || d.items.some((it) => it.status === 'guessing' || it.status === 'importing'))
      return busy || uploading ? 1500 : 15000
    },
  })
  useEffect(() => {
    onFileDone = () => qc.invalidateQueries({ queryKey: ['import'] })
    return () => {
      onFileDone = undefined
    }
  }, [qc])
  // Leaving the page doesn't stop an upload; closing the tab does.
  useEffect(() => {
    if (!uploading) return
    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [uploading])
  const files = useRef<HTMLInputElement>(null)
  const folder = useRef<HTMLInputElement>(null)
  const [browsing, setBrowsing] = useState(false)
  const [fixing, setFixing] = useState<ImportItem>()
  const [dragging, setDragging] = useState(false)
  const run = useMutation({
    mutationFn: (paths: string[]) => post('/import/run', { paths }),
    onSettled: () => qc.invalidateQueries({ queryKey: ['import'] }),
  })
  const clear = useMutation({
    mutationFn: () => del('/import/done'),
    onSettled: () => qc.invalidateQueries({ queryKey: ['import'] }),
  })
  const d = state.data
  const items = d?.items ?? []
  const waiting = items.filter((it) => it.status !== 'done')
  const done = items.filter((it) => it.status === 'done')
  const readyOnes = items.filter(ready)
  const busy = !!d && (d.running || d.copying.running)
  const pickFiles = (list: FileList | null) => {
    if (list?.length) addUploads([...list])
  }
  return (
    <div
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes('Files')) {
          e.preventDefault()
          setDragging(true)
        }
      }}
      onDragLeave={(e) => {
        if (e.currentTarget === e.target) setDragging(false)
      }}
      onDrop={(e) => {
        e.preventDefault()
        setDragging(false)
        pickFiles(e.dataTransfer.files)
      }}
      className={dragging ? 'rounded-xl outline-2 outline-offset-8 outline-dashed outline-marquee' : ''}
    >
      <Heading
        title={t('importFiles.title')}
        sub={t('importFiles.sub')}
        actions={
          <>
            <Button variant="primary" onClick={() => files.current?.click()}>
              <Upload className="size-4" aria-hidden /> {t('importFiles.uploadFiles')}
            </Button>
            <Button onClick={() => folder.current?.click()}>
              <FolderOpen className="size-4" aria-hidden /> {t('importFiles.uploadFolder')}
            </Button>
            <Button onClick={() => setBrowsing(true)} disabled={busy}>
              <HardDrive className="size-4" aria-hidden /> {t('importFiles.fromServer')}
            </Button>
          </>
        }
      />
      <input ref={files} type="file" multiple hidden onChange={(e) => (pickFiles(e.target.files), (e.target.value = ''))} data-testid="upload-files" />
      <input
        ref={folder}
        type="file"
        multiple
        hidden
        // Chrome, Edge, Firefox and Safari all take a folder this way.
        {...({ webkitdirectory: '' } as object)}
        onChange={(e) => (pickFiles(e.target.files), (e.target.value = ''))}
      />
      <div className="flex flex-col gap-4">
        <ErrorNote error={state.error ?? run.error} />
        <Uploads list={ups} />
        {d?.copying.running && (
          <Card className="flex flex-col gap-2">
            <div className="flex items-center gap-2 text-sm font-medium">
              <LoaderCircle className="size-4 animate-spin text-marquee" aria-hidden />
              {t('importFiles.copying', { name: d.copying.current ?? '' })}
            </div>
            <Bar done={d.copying.done} total={d.copying.total} />
            <p className="text-xs text-muted">{t('importFiles.ofSize', { done: bytes(d.copying.done), total: bytes(d.copying.total) })}</p>
          </Card>
        )}
        {d?.copying.error && <ErrorNote error={d.copying.error} />}
        {state.isPending && <Spinner />}
        {d && items.length === 0 && !uploading && !d.copying.running && (
          <Empty icon={<Upload className="size-8" />} title={t('importFiles.empty')}>
            <p className="mb-2">{t('importFiles.emptyBody')}</p>
            <p>{t('importFiles.emptyHint')}</p>
          </Empty>
        )}
        {waiting.length > 0 && (
          <Card className="p-0">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3">
              <div>
                <h2 className="font-display text-lg font-semibold">{t('importFiles.waiting', { count: waiting.length })}</h2>
                <p className="text-xs text-muted">{t('importFiles.waitingHint')}</p>
              </div>
              <Button variant="primary" busy={run.isPending || !!d?.running} disabled={!readyOnes.length || d?.copying.running} onClick={() => run.mutate(readyOnes.map((it) => it.path))}>
                <Check className="size-4" aria-hidden /> {t('importFiles.importReady', { count: readyOnes.length })}
              </Button>
            </div>
            <ul className="divide-y divide-line">
              {waiting.map((it) => (
                <Row key={it.path} it={it} systems={d?.systems ?? []} onFix={() => setFixing(it)} />
              ))}
            </ul>
          </Card>
        )}
        {done.length > 0 && (
          <Card className="p-0">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3">
              <h2 className="font-display text-lg font-semibold">{t('importFiles.imported', { count: done.length })}</h2>
              <Button variant="ghost" className="text-xs" busy={clear.isPending} onClick={() => clear.mutate()}>
                {t('importFiles.clear')}
              </Button>
            </div>
            <ul className="divide-y divide-line">
              {done.map((it) => (
                <Row key={it.path} it={it} systems={d?.systems ?? []} />
              ))}
            </ul>
          </Card>
        )}
        {d && <p className="text-xs text-muted">{t('importFiles.where', { folder: d.folder })}</p>}
      </div>
      {browsing && <ServerPicker onClose={() => setBrowsing(false)} />}
      {fixing && d && <FixItem it={fixing} kinds={d.kinds} systems={d.systems} onClose={() => setFixing(undefined)} />}
    </div>
  )
}

function Bar({ done, total }: { done: number; total: number }) {
  const pct = total > 0 ? Math.min(100, (done / total) * 100) : 0
  return (
    <div className="h-1.5 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
      <div className="h-full rounded-full bg-marquee transition-[width]" style={{ width: `${pct}%` }} />
    </div>
  )
}

function Uploads({ list }: { list: Upload[] }) {
  const { t } = useTranslation()
  if (!list.length) return null
  const total = list.reduce((n, u) => n + u.size, 0)
  const sent = list.reduce((n, u) => n + (u.state === 'done' ? u.size : u.sent), 0)
  const left = list.filter((u) => u.state === 'waiting' || u.state === 'sending').length
  const failed = list.filter((u) => u.state === 'failed')
  return (
    <Card className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-sm font-medium">
          {left ? <LoaderCircle className="size-4 animate-spin text-marquee" aria-hidden /> : <Check className="size-4 text-ok" aria-hidden />}
          {left ? t('importFiles.uploading', { count: left }) : t('importFiles.uploaded', { count: list.length - failed.length })}
        </div>
        {!left && (
          <Button variant="ghost" className="px-2 py-1 text-xs" onClick={clearUploads}>
            {t('importFiles.clear')}
          </Button>
        )}
      </div>
      <Bar done={sent} total={total} />
      <p className="text-xs text-muted">{t('importFiles.ofSize', { done: bytes(sent), total: bytes(total) })}</p>
      {failed.map((u) => (
        <p key={u.id} className="break-all text-xs text-bad">
          {u.name}: {u.error}
        </p>
      ))}
    </Card>
  )
}

// what says what Box Office thinks an item is, in a line.
export function What({ it, systems }: { it: ImportItem; systems: GameSystemInfo[] }) {
  const { t } = useTranslation()
  const m = it.match
  let text: ReactNode = null
  switch (it.kind) {
    case 'movie':
      text = m ? `${m.title}${m.year ? ` (${m.year})` : ''}` : null
      break
    case 'series':
      text = m ? `${m.title}${it.season || it.episode ? ` · ${t('importFiles.episodeCode', { season: String(it.season ?? 0).padStart(2, '0'), episode: String(it.episode ?? 0).padStart(2, '0') })}` : ''}` : null
      break
    case 'album':
    case 'ebook':
    case 'audiobook':
      text = m ? (it.part ? t('importFiles.by', { title: it.part, who: m.title }) : m.title) : null
      break
    case 'game': {
      const sys = systems.find((s) => s.key === it.system)
      text = sys ? `${sys.name}${it.title ? ` · ${it.title}` : ''}` : null
    }
  }
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-2">
      {it.kind && <Badge>{t(`importFiles.kind.${it.kind}`)}</Badge>}
      {text && <span className="min-w-0 break-words font-medium">{text}</span>}
      {it.status === 'ready' && !it.problem && !it.sure && <Badge tone="gold">{t('importFiles.check')}</Badge>}
    </span>
  )
}

function Row({ it, systems, onFix }: { it: ImportItem; systems: GameSystemInfo[]; onFix?: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const remove = useMutation({
    mutationFn: () => del('/import/item?path=' + encodeURIComponent(it.path)),
    onSettled: () => qc.invalidateQueries({ queryKey: ['import'] }),
  })
  return (
    <li className="flex flex-col gap-2 px-5 py-3 sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 gap-3">
        <div className="pt-0.5 text-muted">{it.dir ? <Folder className="size-5" aria-hidden /> : <FileIcon className="size-5" aria-hidden />}</div>
        <div className="flex min-w-0 flex-col gap-1">
          <p className="break-all text-sm">{it.name}</p>
          <p className="text-xs text-muted">{it.dir ? t('importFiles.sizeFiles', { count: it.files, size: bytes(it.size) }) : bytes(it.size)}</p>
          {it.status === 'guessing' ? (
            <span className="flex items-center gap-2 text-sm text-muted">
              <LoaderCircle className="size-4 animate-spin" aria-hidden /> {t('importFiles.guessing')}
            </span>
          ) : (
            <What it={it} systems={systems} />
          )}
          {it.status === 'importing' && (
            <span className="flex items-center gap-2 text-sm text-muted">
              <LoaderCircle className="size-4 animate-spin" aria-hidden /> {t('importFiles.importing')}
            </span>
          )}
          {it.status === 'done' && it.to && (
            <p className="break-all text-xs text-ok">
              <Check className="me-1 inline size-3.5" aria-hidden />
              {t('importFiles.wentTo', { to: it.to })}
            </p>
          )}
          {it.problem && (
            <p className={`text-xs ${it.status === 'done' ? 'text-muted' : 'text-bad'}`}>
              <AlertTriangle className="me-1 inline size-3.5" aria-hidden />
              {it.problem}
            </p>
          )}
        </div>
      </div>
      {onFix && it.status !== 'importing' && it.status !== 'guessing' && (
        <div className="flex shrink-0 gap-2 ps-8 sm:ps-0">
          <Button className="px-2.5 py-1.5 text-xs" onClick={onFix}>
            {t('importFiles.change')}
          </Button>
          <Button variant="ghost" className="px-2 py-1.5 text-xs" busy={remove.isPending} onClick={() => confirm(t('importFiles.removeAsk', { name: it.name })) && remove.mutate()} aria-label={t('importFiles.remove')}>
            <Trash2 className="size-4" aria-hidden />
          </Button>
        </div>
      )}
    </li>
  )
}

// ---- picking from the server ----

function ServerPicker({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [at, setAt] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const listing = useQuery({ queryKey: ['import-browse', at], queryFn: () => get<Listing>('/import/browse' + (at ? '?path=' + encodeURIComponent(at) : '')) })
  const copy = useMutation({
    mutationFn: () => post('/import/copy', { paths: picked }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['import'] })
      onClose()
    },
  })
  const l = listing.data
  const join = (name: string) => (l?.path === '/' ? '/' : (l?.path ?? '') + '/') + name
  const toggle = (p: string) => setPicked((x) => (x.includes(p) ? x.filter((y) => y !== p) : [...x, p]))
  return (
    <Modal title={t('importFiles.fromServer')} onClose={onClose} wide>
      <div className="flex flex-col gap-3">
        <p className="text-sm text-muted">{t('importFiles.serverHint')}</p>
        <div className="flex items-center gap-2 rounded-md border border-line bg-paper px-3 py-2 text-sm">
          <HardDrive className="size-4 shrink-0 text-muted" aria-hidden />
          <span className="min-w-0 break-all font-mono text-xs">{l?.path ?? at}</span>
        </div>
        <ErrorNote error={listing.error ?? copy.error} />
        {listing.isPending && <Spinner />}
        {l && (
          <ul className="max-h-[45vh] divide-y divide-line overflow-y-auto rounded-md border border-line">
            {l.parent && (
              <li>
                <button className="flex w-full items-center gap-2 px-3 py-2 text-start text-sm hover:bg-line/60" onClick={() => setAt(l.parent)}>
                  <FolderOpen className="size-4 text-muted" aria-hidden /> ..
                </button>
              </li>
            )}
            {l.entries.length === 0 && <li className="px-3 py-3 text-sm text-muted">{t('importFiles.emptyFolder')}</li>}
            {l.entries.map((e) => {
              const p = join(e.name)
              return (
                <li key={e.name} className="flex items-center gap-2 px-3 py-1.5 text-sm">
                  <input type="checkbox" className="size-4 accent-marquee" checked={picked.includes(p)} onChange={() => toggle(p)} aria-label={t('importFiles.pick', { name: e.name })} />
                  {e.dir ? (
                    <button className="flex min-w-0 flex-1 items-center gap-2 rounded py-1 text-start hover:text-marquee" onClick={() => setAt(p)}>
                      <Folder className="size-4 shrink-0 text-muted" aria-hidden />
                      <span className="min-w-0 [overflow-wrap:anywhere]">{e.name}</span>
                      <ChevronRight className="ms-auto size-4 shrink-0 text-muted" aria-hidden />
                    </button>
                  ) : (
                    <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 py-1" onClick={() => toggle(p)}>
                      <FileIcon className="size-4 shrink-0 text-muted" aria-hidden />
                      <span className="min-w-0 [overflow-wrap:anywhere]">{e.name}</span>
                      <span className="ms-auto shrink-0 text-xs text-muted">{bytes(e.size ?? 0)}</span>
                    </label>
                  )}
                </li>
              )
            })}
          </ul>
        )}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-xs text-muted">{t('importFiles.copyNote')}</p>
          <div className="flex gap-2">
            <Button onClick={onClose}>{t('importFiles.cancel')}</Button>
            <Button variant="primary" disabled={!picked.length} busy={copy.isPending} onClick={() => copy.mutate()}>
              {t('importFiles.copyIn', { count: picked.length })}
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  )
}

// ---- saying what something is ----

type Found = { tmdbId?: number; mbid?: string; olKey?: string; title: string; year?: number; poster?: string; note?: string }

const lookupPath: Partial<Record<ImportKind, string>> = {
  movie: '/lookup/movies',
  series: '/lookup/series',
  album: '/lookup/artists',
  ebook: '/lookup/authors',
  audiobook: '/lookup/authors',
}

function FixItem({ it, kinds, systems, onClose }: { it: ImportItem; kinds: ImportKind[]; systems: GameSystemInfo[]; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [kind, setKind] = useState<ImportKind>(it.kind || 'movie')
  const sameKind = kind === it.kind
  const [term, setTerm] = useState(sameKind ? (it.match?.title ?? it.title ?? '') : '')
  const [q, setQ] = useState(term)
  const [part, setPart] = useState(it.part ?? '')
  useEffect(() => {
    const timer = setTimeout(() => setQ(term.trim()), 400)
    return () => clearTimeout(timer)
  }, [term])
  const path = lookupPath[kind]
  const results = useQuery({
    queryKey: ['lookup', kind, q],
    queryFn: async (): Promise<Found[]> => {
      const r = await get<(LookupMovie | LookupShow | LookupArtist | LookupAuthor)[]>(`${path}?q=${encodeURIComponent(q)}`)
      return r.map((x) =>
        'key' in x
          ? { olKey: x.key, title: x.name, poster: x.photo, note: [[x.born, x.died].filter(Boolean).join('–'), x.topWork].filter(Boolean).join(', ') }
          : 'mbid' in x
            ? { mbid: x.mbid, title: x.name, note: x.disambiguation ?? [x.type, x.country].filter(Boolean).join(', ') }
            : { tmdbId: x.tmdbId, title: x.title, year: x.year, poster: x.poster, note: x.overview },
      )
    },
    enabled: !!path && q.length > 1,
  })
  const save = useMutation({
    mutationFn: (p: Pick) => patch<ImportItem>('/import/item', { path: it.path, ...p }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['import'] })
      onClose()
    },
  })
  const needsPart = kind === 'album' || kind === 'ebook' || kind === 'audiobook'
  const where = kind === 'album' ? 'MusicBrainz' : needsPart ? 'Open Library' : 'TMDB'
  const pick = (f: Found) => save.mutate({ kind, tmdbId: f.tmdbId, mbid: f.mbid, olKey: f.olKey, name: f.title, year: f.year, part: needsPart ? part.trim() : undefined })
  // The album or book's title can be changed without picking again.
  const keep = sameKind && needsPart && it.match ? it.match : undefined
  return (
    <Modal title={t('importFiles.whatIs', { name: it.name })} onClose={onClose} wide>
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap gap-1 rounded-md border border-line p-0.5" role="radiogroup" aria-label={t('importFiles.itsA')}>
          {allKinds
            .filter((k) => kinds.includes(k))
            .map((k) => (
              <button
                key={k}
                role="radio"
                aria-checked={kind === k}
                onClick={() => {
                  setKind(k)
                  if (k !== it.kind) setTerm('')
                }}
                className={`rounded px-3 py-1.5 text-sm ${kind === k ? 'bg-marquee text-marquee-ink' : 'text-muted hover:text-ink'}`}
              >
                {t(`importFiles.kind.${k}`)}
              </button>
            ))}
        </div>
        {kind !== it.kind && (
          <div className="flex items-center justify-between gap-3 rounded-md bg-line/40 px-3 py-2 text-sm">
            <span>{t('importFiles.lookAgain')}</span>
            <Button className="shrink-0 px-2.5 py-1.5 text-xs" busy={save.isPending} onClick={() => save.mutate({ kind })}>
              {t('importFiles.lookAgainButton')}
            </Button>
          </div>
        )}
        {kind === 'game' ? (
          <GameFix it={it} systems={systems} busy={save.isPending} onSave={(p) => save.mutate({ kind, ...p })} error={save.error} />
        ) : (
          <>
            {needsPart && (
              <div className="flex flex-col gap-1.5">
                <label htmlFor="import-part" className="text-sm font-medium">
                  {kind === 'album' ? t('importFiles.albumTitle') : t('importFiles.bookTitle')}
                </label>
                <Input id="import-part" value={part} onChange={(e) => setPart(e.target.value)} />
                <p className="text-xs text-muted">{kind === 'album' ? t('importFiles.albumHint') : t('importFiles.bookHint')}</p>
                {keep && (
                  <div className="flex items-center justify-between gap-3 text-sm">
                    <span className="text-muted">{t('importFiles.keepWho', { who: keep.title })}</span>
                    <Button
                      className="shrink-0 px-2.5 py-1.5 text-xs"
                      disabled={!part.trim()}
                      busy={save.isPending}
                      onClick={() => save.mutate({ kind, mbid: keep.mbid, olKey: keep.olKey, name: keep.title, part: part.trim() })}
                    >
                      {t('importFiles.save')}
                    </Button>
                  </div>
                )}
              </div>
            )}
            <div className="relative">
              <Search className="pointer-events-none absolute start-3 top-2.5 size-4 text-muted" aria-hidden />
              <Input
                className="ps-9"
                value={term}
                onChange={(e) => setTerm(e.target.value)}
                placeholder={kind === 'movie' ? t('add.moviePlaceholder') : kind === 'series' ? t('add.showPlaceholder') : kind === 'album' ? t('music.placeholder') : t('scan.authorPlaceholder')}
                aria-label={t('importFiles.find')}
              />
            </div>
            {needsPart && !part.trim() && <p className="text-xs text-muted">{t('importFiles.partFirst')}</p>}
            {results.isFetching && <Spinner label={t('scan.lookingOn', { where })} />}
            <ErrorNote error={results.error ?? save.error} />
            {results.data?.length === 0 && <p className="text-sm text-muted">{t('scan.nothingOn', { where })}</p>}
            <ul className="flex flex-col divide-y divide-line">
              {results.data?.map((r) => (
                <li key={r.tmdbId ?? r.mbid ?? r.olKey} className="flex gap-4 py-3">
                  {kind !== 'album' && <Poster src={r.poster} title={r.title} className="w-12 shrink-0" />}
                  <div className="min-w-0 flex-1">
                    <div className="font-display font-semibold">
                      {r.title} {r.year ? <span className="font-normal text-muted">({r.year})</span> : null}
                    </div>
                    <p className="line-clamp-2 text-sm text-muted">{r.note}</p>
                  </div>
                  <div className="shrink-0 self-center">
                    <Button disabled={needsPart && !part.trim()} busy={save.isPending && save.variables?.name === r.title} onClick={() => pick(r)}>
                      {t('add.pick')}
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          </>
        )}
        <div className="flex justify-end">
          <Button variant="ghost" onClick={onClose}>
            <X className="size-4" aria-hidden /> {t('importFiles.cancel')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// GameFix picks a game's system, and which game it is: found on the
// system's list, or the name it should go in as when the list hasn't got
// it or there's no list (PC games).
function GameFix({
  it,
  systems,
  busy,
  onSave,
  error,
}: {
  it: ImportItem
  systems: GameSystemInfo[]
  busy: boolean
  onSave: (p: { system: string; game?: string }) => void
  error: unknown
}) {
  const { t } = useTranslation()
  const [system, setSystem] = useState(it.system ?? '')
  const [term, setTerm] = useState(it.game ?? it.title ?? '')
  const [q, setQ] = useState(term.trim())
  useEffect(() => {
    const timer = setTimeout(() => setQ(term.trim()), 400)
    return () => clearTimeout(timer)
  }, [term])
  const sys = systems.find((s) => s.key === system)
  const found = useQuery({
    queryKey: ['import-game-lookup', system, q],
    queryFn: () => get<GameLookup[]>(`/games/lookup?${new URLSearchParams({ q, system })}`),
    enabled: !!system && q.length > 1,
  })
  const listed = (found.data ?? []).filter((l) => !l.free)
  const name = term.trim()
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1.5">
        <label htmlFor="import-system" className="text-sm font-medium">
          {t('importFiles.system')}
        </label>
        <Select id="import-system" value={system} onChange={setSystem} options={[{ value: '', label: t('importFiles.pickSystem') }, ...systems.map((s) => ({ value: s.key, label: s.name }))]} />
        <p className="text-xs text-muted">{t('importFiles.systemHint')}</p>
      </div>
      {system && (
        <div className="flex flex-col gap-1.5">
          <label htmlFor="import-game" className="text-sm font-medium">
            {t('importFiles.gameName')}
          </label>
          <div className="relative">
            <Search className="pointer-events-none absolute start-3 top-2.5 size-4 text-muted" aria-hidden />
            <Input id="import-game" className="ps-9" value={term} onChange={(e) => setTerm(e.target.value)} placeholder={t('importFiles.gamePlaceholder')} />
          </div>
          <p className="text-xs text-muted">{t('importFiles.gameHint', { system: sys?.name ?? system })}</p>
        </div>
      )}
      {found.isFetching && <Spinner label={t('games.add.looking')} />}
      <ErrorNote error={found.error ?? error} />
      {!!system && q.length > 1 && found.isSuccess && listed.length === 0 && !found.data?.some((l) => l.free) && !sys?.arcade && <p className="text-sm text-muted">{t('importFiles.noGames')}</p>}
      {listed.length > 0 && (
        <ul className="flex max-h-72 flex-col divide-y divide-line overflow-y-auto">
          {listed.flatMap((l) =>
            l.versions.slice(0, 4).map((v) => (
              <li key={v.name} className="flex items-center gap-3 py-2">
                <div className="min-w-0 flex-1">
                  <div className="break-words text-sm font-medium">{v.name}</div>
                  <div className="text-xs text-muted">{[v.year, v.maker, v.serial].filter(Boolean).join(' · ')}</div>
                </div>
                <Button className="shrink-0 px-2.5 py-1.5 text-xs" busy={busy} onClick={() => onSave({ system, game: v.name })}>
                  {t('add.pick')}
                </Button>
              </li>
            )),
          )}
        </ul>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        {!!system && !!name && !sys?.arcade && (
          <Button busy={busy} onClick={() => onSave({ system, game: name })}>
            {t('importFiles.useName', { name })}
          </Button>
        )}
        <Button variant="primary" disabled={!system} busy={busy} onClick={() => onSave({ system })}>
          {t('importFiles.save')}
        </Button>
      </div>
    </div>
  )
}
