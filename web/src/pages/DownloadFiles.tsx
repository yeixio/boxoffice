import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronRight, Download as DownloadIcon, File, FileVideo, Folder, FolderInput, HardDrive, Link2, LoaderCircle, RefreshCw, Search, ShieldAlert, ShieldOff, Trash2, X } from 'lucide-react'
import { get, post, type Book, type DownloadFile, type DownloadFiles as FilesData, type Movie, type Series, type Settings as SettingsData, type TitleKind } from '../api'
import { bytes, count, titleLink, when } from '../format'
import { Badge, Button, ErrorNote, Input, Modal } from '../ui'

const QUARANTINE = '_quarantine'

function useFiles(path: string) {
  return useQuery({
    queryKey: ['download-files', path],
    queryFn: () => get<FilesData>('/downloads/files?path=' + encodeURIComponent(path)),
    refetchInterval: (q) => (q.state.data?.listing.moves.some((m) => m.running) ? 3000 : 15000),
  })
}

// DownloadFiles is a file browser for the downloads folder: what's in it,
// how much room it takes, and deleting, releasing from quarantine, moving
// into the library and saving a copy. It never reaches outside the
// downloads folder; the server makes sure of that too.
export function DownloadFiles() {
  const qc = useQueryClient()
  const [path, setPath] = useState('')
  const [dismissed, setDismissed] = useState<string[]>([])
  const files = useFiles(path)
  const [deleting, setDeleting] = useState<DownloadFile>()
  const [releasing, setReleasing] = useState<DownloadFile>()
  const [importing, setImporting] = useState<DownloadFile>()
  const refresh = () => qc.invalidateQueries({ queryKey: ['download-files'] })
  const data = files.data
  const crumbs = path ? path.split('/') : []
  const notes = (data?.listing.moves ?? []).filter((m) => m.running || (m.message && !dismissed.includes(m.path + m.message)))
  return (
    <section className="mt-8">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h2 className="mr-auto font-display text-xl font-semibold">Files</h2>
        <Button variant="ghost" onClick={refresh} disabled={files.isFetching} aria-label="Refresh the file list">
          <RefreshCw className={`size-4 ${files.isFetching ? 'animate-spin' : ''}`} aria-hidden /> Refresh
        </Button>
      </div>
      {data && <Usage data={data} />}
      {data?.problem && (
        <p role="alert" className="mb-3 rounded-md border border-bad/40 bg-bad/5 px-3 py-2 text-sm text-bad">
          {data.problem} Until that's fixed, files can be looked at but not changed.
        </p>
      )}
      {notes.length > 0 && (
        <ul className="mb-3 flex flex-col gap-2">
          {notes.map((m) => (
            <li key={m.path} className={`flex items-start gap-2 rounded-md border px-3 py-2 text-sm ${m.running ? 'border-line' : m.message?.startsWith('Moved') ? 'border-ok/40 bg-ok/5' : 'border-bad/40 bg-bad/5 text-bad'}`}>
              {m.running ? <LoaderCircle className="mt-0.5 size-4 shrink-0 animate-spin" aria-hidden /> : <FolderInput className="mt-0.5 size-4 shrink-0" aria-hidden />}
              <span className="min-w-0 flex-1 break-words">{m.running ? `Moving ${m.path} into the library…` : `${m.path}: ${m.message}`}</span>
              {!m.running && (
                <button className="rounded p-0.5 hover:bg-line" aria-label="Dismiss" onClick={() => setDismissed((d) => [...d, m.path + m.message])}>
                  <X className="size-4" />
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      <div className="rounded-xl border border-line bg-card">
        <nav aria-label="Folder" className="flex flex-wrap items-center gap-1 border-b border-line px-4 py-2.5 text-sm">
          <button className={`flex items-center gap-1.5 rounded px-1 ${path ? 'text-marquee hover:underline' : 'font-medium'}`} onClick={() => setPath('')}>
            <HardDrive className="size-4" aria-hidden /> Downloads
          </button>
          {crumbs.map((c, i) => {
            const to = crumbs.slice(0, i + 1).join('/')
            const last = i === crumbs.length - 1
            return (
              <span key={to} className="flex min-w-0 items-center gap-1">
                <ChevronRight className="size-3.5 shrink-0 text-muted" aria-hidden />
                <button className={`min-w-0 break-all rounded px-1 text-left ${last ? 'font-medium' : 'text-marquee hover:underline'}`} onClick={() => setPath(to)} aria-current={last ? 'page' : undefined}>
                  {c === QUARANTINE ? 'Quarantine' : c}
                </button>
              </span>
            )
          })}
        </nav>
        {(path === QUARANTINE || path.startsWith(QUARANTINE + '/')) && (
          <p className="flex gap-2 border-b border-line bg-gold/5 px-4 py-2.5 text-xs text-muted">
            <ShieldAlert className="size-4 shrink-0 text-gold" aria-hidden />
            Downloads that didn't look safe. Box Office won't open, download or move these into the library, and nothing here can run. Delete them, or release one only if you're sure it's fine.
          </p>
        )}
        {files.isPending ? (
          <p className="flex items-center gap-2 px-4 py-6 text-sm text-muted">
            <LoaderCircle className="size-4 animate-spin" aria-hidden /> Reading the downloads folder…
          </p>
        ) : files.isError ? (
          <div className="p-4">
            <ErrorNote error={files.error} />
            {path && (
              <Button className="mt-3" onClick={() => setPath('')}>
                Back to Downloads
              </Button>
            )}
          </div>
        ) : data!.listing.entries.length === 0 ? (
          <p className="px-4 py-6 text-sm text-muted">{path ? 'This folder is empty.' : 'The downloads folder is empty.'}</p>
        ) : (
          <ul className="divide-y divide-line">
            {data!.listing.entries.map((f) => (
              <Row
                key={f.path}
                f={f}
                locked={!!data!.problem}
                onOpen={() => setPath(f.path)}
                onDelete={() => setDeleting(f)}
                onRelease={() => setReleasing(f)}
                onImport={() => setImporting(f)}
              />
            ))}
          </ul>
        )}
      </div>
      {deleting && <DeleteDialog f={deleting} onClose={() => setDeleting(undefined)} />}
      {releasing && <ReleaseDialog f={releasing} onClose={() => setReleasing(undefined)} />}
      {importing && <ImportDialog f={importing} onClose={() => setImporting(undefined)} />}
    </section>
  )
}

// Usage shows the disk the downloads folder is on: how much downloads and
// quarantine take of it, and what's free.
function Usage({ data }: { data: FilesData }) {
  const { disk, listing } = data
  const total = disk?.total ?? 0
  const pct = (n: number) => (total > 0 ? Math.max(n > 0 ? 0.5 : 0, Math.min(100, (n / total) * 100)) : 0)
  const other = total > 0 ? Math.max(0, total - (disk?.free ?? 0) - listing.size) : 0
  return (
    <div className="mb-3 rounded-xl border border-line bg-card px-4 py-3">
      {total > 0 && (
        <div className="mb-2 flex h-2.5 overflow-hidden rounded-full bg-line" role="img" aria-label={`${bytes(total - (disk?.free ?? 0))} of ${bytes(total)} used`}>
          <div className="h-full bg-marquee" style={{ width: `${pct(listing.size - listing.quarantine)}%` }} />
          <div className="h-full bg-gold" style={{ width: `${pct(listing.quarantine)}%` }} />
          <div className="h-full bg-muted/40" style={{ width: `${pct(other)}%` }} />
        </div>
      )}
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-full bg-marquee" aria-hidden /> Downloads {bytes(listing.size - listing.quarantine)}
        </span>
        {listing.quarantine > 0 && (
          <span className="flex items-center gap-1.5">
            <span className="size-2 rounded-full bg-gold" aria-hidden /> Quarantine {bytes(listing.quarantine)}
          </span>
        )}
        {total > 0 && (
          <span className="flex items-center gap-1.5">
            <span className="size-2 rounded-full bg-muted/40" aria-hidden /> Other files {bytes(other)}
          </span>
        )}
        {disk && <span>{bytes(disk.free)} free{total > 0 && ` of ${bytes(total)}`}</span>}
        <span className="basis-full break-all sm:ml-auto sm:basis-auto">{data.root}</span>
      </div>
    </div>
  )
}

// What a download using a file or folder is doing, said on it.
const busyName: Record<string, string> = {
  queued: 'Waiting to download',
  downloading: 'Downloading',
  paused: 'Download paused',
  verifying: 'Checking',
  repairing: 'Repairing',
  unpacking: 'Unpacking',
  importing: 'Moving into library',
}

function Row({ f, locked, onOpen, onDelete, onRelease, onImport }: { f: DownloadFile; locked: boolean; onOpen: () => void; onDelete: () => void; onRelease: () => void; onImport: () => void }) {
  const isQuarantine = f.path === QUARANTINE
  const releasable = f.quarantined && f.path.split('/').length === 2
  const canChange = !locked && !f.busy && !f.moving && !isQuarantine
  const icon = isQuarantine ? (
    <ShieldAlert className="size-5 text-gold" aria-hidden />
  ) : f.link ? (
    <Link2 className="size-5 text-muted" aria-hidden />
  ) : f.dir ? (
    <Folder className={`size-5 ${f.quarantined ? 'text-gold' : 'text-marquee'}`} aria-hidden />
  ) : f.video ? (
    <FileVideo className="size-5 text-muted" aria-hidden />
  ) : (
    <File className="size-5 text-muted" aria-hidden />
  )
  const name = isQuarantine ? 'Quarantine' : f.name
  // The downloader is still using it: it's greyed out, says what's going
  // on, and offers nothing until the download is done.
  const inUse = !!f.busy || !!f.moving
  const label = f.moving ? busyName.importing : (busyName[f.busyWith ?? ''] ?? 'In use by a download')
  return (
    <li
      className={`flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 ${inUse ? 'opacity-55' : ''}`}
      aria-disabled={inUse || undefined}
      title={inUse ? `${label}. You can change this once the download is done.` : undefined}
    >
      <span className="shrink-0">{icon}</span>
      <div className="min-w-0 flex-1 basis-48">
        {f.dir && !inUse ? (
          <button onClick={onOpen} className="break-all text-left text-sm font-medium hover:text-marquee hover:underline">
            {name}
          </button>
        ) : (
          <span className="break-all text-sm">{name}</span>
        )}
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
          {!f.link && <span>{bytes(f.size)}</span>}
          {f.dir && !f.link && <span>{count(f.files ?? 0, 'file', 'files')}</span>}
          {f.link && <span>Link (not followed)</span>}
          <span>{when(f.modified)}</span>
          {f.quarantined && f.path.split('/').length === 2 && <Badge tone="gold">Quarantined</Badge>}
          {inUse && (
            <Badge tone={f.busyWith === 'downloading' ? 'marquee' : f.busyWith === 'queued' || f.busyWith === 'paused' ? 'plain' : 'gold'}>
              {f.busyWith !== 'queued' && f.busyWith !== 'paused' && <LoaderCircle className="size-3 animate-spin" aria-hidden />}
              {label}
            </Badge>
          )}
          {(() => {
            const t = titleLink(f.download?.kind, f.download?.titleId)
            return t ? (
              <Link to={t.to} className="text-marquee hover:underline">
                {t.label}
              </Link>
            ) : null
          })()}
        </div>
      </div>
      {!isQuarantine && !inUse && (
        <div className="ml-auto flex shrink-0 gap-1">
          {!f.quarantined && !f.link && (f.dir || f.video) && (
            <IconButton label="Move into library" disabled={!canChange} onClick={onImport}>
              <FolderInput className="size-4" />
            </IconButton>
          )}
          {releasable && (
            <IconButton label="Release from quarantine" disabled={!canChange} onClick={onRelease}>
              <ShieldOff className="size-4" />
            </IconButton>
          )}
          {!f.dir && !f.link && !f.quarantined && (
            <a
              href={`/api/v1/downloads/files/get?path=${encodeURIComponent(f.path)}`}
              download={f.name}
              title="Download"
              aria-label="Download"
              className="rounded-md p-2 text-muted hover:bg-line hover:text-ink"
            >
              <DownloadIcon className="size-4" />
            </a>
          )}
          <IconButton label="Delete" disabled={!canChange} onClick={onDelete}>
            <Trash2 className="size-4" />
          </IconButton>
        </div>
      )}
    </li>
  )
}

function IconButton({ label, onClick, disabled, children }: { label: string; onClick: () => void; disabled?: boolean; children: React.ReactNode }) {
  return (
    <button onClick={onClick} disabled={disabled} title={label} aria-label={label} className="rounded-md p-2 text-muted hover:bg-line hover:text-ink disabled:pointer-events-none disabled:opacity-40">
      {children}
    </button>
  )
}

function useFileAction<T = unknown>(what: 'delete' | 'release' | 'import', onClose: () => void) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { path: string; kind?: string; titleId?: number }) => post<T>(`/downloads/files/${what}`, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['download-files'] })
      qc.invalidateQueries({ queryKey: ['downloads'] })
      onClose()
    },
  })
}

function DeleteDialog({ f, onClose }: { f: DownloadFile; onClose: () => void }) {
  const del = useFileAction('delete', onClose)
  return (
    <Modal title={f.dir ? 'Delete folder' : 'Delete file'} onClose={onClose}>
      <p className="mb-2 break-all text-sm font-medium">{f.name}</p>
      <p className="mb-4 text-sm text-muted">
        {f.dir ? `This deletes the folder and everything in it: ${count(f.files ?? 0, 'file', 'files')}, ${bytes(f.size)}.` : `This deletes ${bytes(f.size)}.`} It can't be undone. Only the downloads folder is touched; your library isn't.
      </p>
      <ErrorNote error={del.error} />
      <div className="mt-5 flex flex-wrap justify-end gap-2">
        <Button onClick={onClose}>Keep it</Button>
        <Button variant="danger" busy={del.isPending} onClick={() => del.mutate({ path: f.path })}>
          <Trash2 className="size-4" aria-hidden /> Delete
        </Button>
      </div>
    </Modal>
  )
}

function ReleaseDialog({ f, onClose }: { f: DownloadFile; onClose: () => void }) {
  const release = useFileAction<{ path: string }>('release', onClose)
  return (
    <Modal title="Release from quarantine" onClose={onClose}>
      <p className="mb-2 break-all text-sm font-medium">{f.name}</p>
      <p className="mb-2 text-sm text-muted">
        Box Office quarantined this because it didn't look safe: it may hold a program pretending to be a video, or no video at all. Its files can't be run, but releasing it moves it back into the downloads folder, where it can be downloaded or moved into your library.
      </p>
      <p className="mb-4 text-sm text-muted">Only release it if you know what's in it. Moving into the library still only takes video and subtitle files.</p>
      <ErrorNote error={release.error} />
      <div className="mt-5 flex flex-wrap justify-end gap-2">
        <Button onClick={onClose}>Keep it in quarantine</Button>
        <Button variant="danger" busy={release.isPending} onClick={() => release.mutate({ path: f.path })}>
          <ShieldOff className="size-4" aria-hidden /> Release
        </Button>
      </div>
    </Modal>
  )
}

type Pick = { kind: TitleKind; id: number; title: string; year?: number; by?: string }

const kindNames: Record<TitleKind, { label: string; find: string; from: string }> = {
  movie: { label: 'Movie', find: 'Find the movie', from: 'Movies' },
  series: { label: 'Show', find: 'Find the show', from: 'Shows' },
  ebook: { label: 'Ebook', find: 'Find the book', from: 'Books' },
  audiobook: { label: 'Audiobook', find: 'Find the book', from: 'Audiobooks' },
}

const words = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()
    .split(' ')
    .filter(Boolean)

// guess scores how well a title matches a release name: the share of the
// title's words found in order at the start of the name, plus its year.
export function guess(name: string, title: string, year?: number): number {
  const n = words(name)
  const t = words(title)
  if (t.length === 0) return 0
  let i = 0
  while (i < t.length && n[i] === t[i]) i++
  let score = i / t.length
  if (i === t.length) score += 1
  if (year && n.includes(String(year))) score += 0.5
  return score
}

function ImportDialog({ f, onClose }: { f: DownloadFile; onClose: () => void }) {
  const qc = useQueryClient()
  const movies = useQuery({ queryKey: ['movies'], queryFn: () => get<Movie[]>('/movies') })
  const shows = useQuery({ queryKey: ['series'], queryFn: () => get<Series[]>('/series') })
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => get<SettingsData>('/settings') })
  const kinds = (['movie', 'series', 'ebook', 'audiobook'] as const).filter((k) => k === 'movie' || k === 'series' || !!settings.data?.mediaTypes.find((t) => t.key === k)?.enabled)
  const bookKind = kinds.includes('ebook') || kinds.includes('audiobook')
  const books = useQuery({ queryKey: ['books', 'all'], queryFn: () => get<Book[]>('/books'), enabled: bookKind })
  const looksLikeShow = /\bS\d{1,2}(E\d{1,3})?\b|\b\d{1,2}x\d{2}\b/i.test(f.name)
  const [kind, setKind] = useState<TitleKind>(f.download?.kind ?? (looksLikeShow ? 'series' : 'movie'))
  const [query, setQuery] = useState('')
  const [picked, setPicked] = useState<Pick>()
  const move = useMutation({
    mutationFn: (p: Pick) => post('/downloads/files/import', { path: f.path, kind: p.kind, titleId: p.id }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['download-files'] })
      qc.invalidateQueries({ queryKey: ['downloads'] })
      onClose()
    },
  })
  const all: Pick[] = useMemo(() => {
    switch (kind) {
      case 'movie':
        return (movies.data ?? []).map((m) => ({ kind, id: m.id, title: m.title, year: m.year }))
      case 'series':
        return (shows.data ?? []).map((s) => ({ kind, id: s.id, title: s.title, year: s.year }))
      default:
        return (books.data ?? []).map((b) => ({ kind, id: b.id, title: b.title, year: b.year, by: b.authorName }))
    }
  }, [kind, movies.data, shows.data, books.data])
  // The download's own title first, then the best guesses from the name,
  // or what's typed.
  const list = useMemo(() => {
    const q = words(query).join(' ')
    const scored = all
      .map((p) => {
        // Book releases usually lead with the author.
        const named = (title: string) => Math.max(guess(f.name, title, p.year), p.by ? guess(f.name, `${p.by} ${title}`, p.year) : 0)
        let s = q ? (words(p.title).join(' ').includes(q) ? 1 + guess(q, p.title) : 0) : named(p.title)
        if (f.download?.kind === p.kind && f.download.titleId === p.id) s += 10
        return { p, s }
      })
      .filter((x) => x.s > 0.5 || (q && x.s > 0))
    scored.sort((a, b) => b.s - a.s || a.p.title.localeCompare(b.p.title))
    return scored.slice(0, 8).map((x) => x.p)
  }, [all, query, f.name, f.download])
  const loading = kind === 'movie' ? movies.isPending : kind === 'series' ? shows.isPending : books.isPending
  const first = list[0]
  const firstScore = first ? Math.max(guess(f.name, first.title, first.year), first.by ? guess(f.name, `${first.by} ${first.title}`, first.year) : 0) : 0
  const auto = !query && first && ((f.download?.kind === kind && f.download.titleId === first.id) || firstScore >= 1.5) ? first : undefined
  const isBook = kind === 'ebook' || kind === 'audiobook'
  const choice = picked?.kind === kind ? picked : auto
  return (
    <Modal title="Move into library" onClose={onClose}>
      <p className="mb-1 break-all text-sm font-medium">{f.name}</p>
      <p className="mb-4 text-xs text-muted">
        {f.dir ? `${count(f.files ?? 0, 'file', 'files')}, ${bytes(f.size)}. ` : `${bytes(f.size)}. `}
        {kind === 'ebook'
          ? "Box Office moves the best ebook file (EPUB first) into the author's folder and names it for you"
          : kind === 'audiobook'
            ? "Box Office moves the audio files, in order, into the book's folder in the author's folder"
            : "Box Office moves the main video (and its subtitles) into the title's folder and names it for you"}
        {f.dir ? ', then deletes what’s left of the folder' : ''}. A copy already there is set aside, not deleted.
      </p>
      <div className="mb-3 inline-flex rounded-md border border-line p-0.5" role="radiogroup" aria-label="It's a">
        {kinds.map((k) => (
          <button
            key={k}
            role="radio"
            aria-checked={kind === k}
            onClick={() => setKind(k)}
            className={`rounded px-3 py-1.5 text-sm ${kind === k ? 'bg-marquee text-marquee-ink' : 'text-muted hover:text-ink'}`}
          >
            {kindNames[k].label}
          </button>
        ))}
      </div>
      <div className="relative mb-2">
        <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted" aria-hidden />
        <Input className="pl-9" placeholder={kindNames[kind].find} value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Find the title" />
      </div>
      {loading ? (
        <p className="py-3 text-sm text-muted">Loading your library…</p>
      ) : list.length === 0 ? (
        <p className="py-3 text-sm text-muted">
          {query ? 'Nothing in your library matches.' : 'Type to find it.'} It has to be in your library first; add it from {kindNames[kind].from}{isBook ? ', where its author is' : ''}.
        </p>
      ) : (
        <ul className="mb-2 max-h-64 overflow-y-auto rounded-md border border-line" role="listbox" aria-label="Titles">
          {list.map((p) => {
            const on = choice?.id === p.id && choice.kind === p.kind
            return (
              <li key={p.id}>
                <button
                  role="option"
                  aria-selected={on}
                  onClick={() => setPicked(p)}
                  className={`flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm ${on ? 'bg-marquee/10 font-medium text-marquee' : 'hover:bg-line/60'}`}
                >
                  <span className="min-w-0 break-words">
                    {p.title}
                    {p.by && <span className="block text-xs font-normal text-muted">{p.by}</span>}
                  </span>
                  {p.year ? <span className="shrink-0 text-xs text-muted">{p.year}</span> : null}
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {kind === 'series' && <p className="mb-2 text-xs text-muted">Each video goes to the episode its name says, like S01E02.</p>}
      <ErrorNote error={move.error} />
      <div className="mt-5 flex flex-wrap justify-end gap-2">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!choice} busy={move.isPending} onClick={() => choice && move.mutate(choice)}>
          <FolderInput className="size-4" aria-hidden /> {choice ? `Move into ${choice.title}` : 'Move into library'}
        </Button>
      </div>
    </Modal>
  )
}
