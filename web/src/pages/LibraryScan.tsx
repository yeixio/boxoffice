import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleAlert, ScanSearch, Search } from 'lucide-react'
import { del, get, post, type LibScan, type LookupArtist, type LookupAuthor, type LookupMovie, type LookupShow, type RootFolder, type RootKind, type ScanFound, type ScanMatch } from '../api'
import { bytes, count } from '../format'
import { monitorOptions, useProfiles, useShowDefaults } from '../hooks'
import { Badge, Button, ErrorNote, Field, Input, Modal, Poster, Select, Spinner, Toggle } from '../ui'

// Kinds of root folder a scan can add from: the ones Box Office has a list
// for.
export const scannable = (kind: RootKind) => kind === 'movie' || kind === 'series' || kind === 'music' || isBooks(kind)

// Ebook and audiobook root folders hold a folder per author.
const isBooks = (kind: RootKind) => kind === 'ebook' || kind === 'audiobook'
// Kinds matched somewhere other than TMDB, with no quality profile.
const notTMDB = (kind: RootKind) => kind === 'music' || isBooks(kind)

const running = (s?: LibScan | null) => s?.status === 'scanning' || s?.status === 'adding'

export function useLibScan(rootId: number) {
  return useQuery({
    queryKey: ['libscan', rootId],
    queryFn: () => get<LibScan | null>(`/rootfolders/${rootId}/scan`),
    refetchInterval: (q) => (running(q.state.data) ? 1500 : false),
  })
}

const noun = (kind: RootKind, n: number) => {
  const one = kind === 'movie' ? 'movie' : kind === 'series' ? 'show' : isBooks(kind) ? 'author' : 'artist'
  return n === 1 ? one : one + 's'
}

// addable says whether a found folder can go on the list as picked.
const addable = (f: ScanFound, pick?: ScanMatch) => !f.added && !!(pick ?? f.match) && !(pick ? false : f.have)

// ScanButton is a root folder's Scan button, and what its last scan found.
export function ScanButton({ folder }: { folder: RootFolder }) {
  const qc = useQueryClient()
  const scan = useLibScan(folder.id)
  const [open, setOpen] = useState(false)
  const start = useMutation({
    mutationFn: () => post<LibScan>(`/rootfolders/${folder.id}/scan`),
    onSuccess: (s) => {
      qc.setQueryData(['libscan', folder.id], s)
      setOpen(true)
    },
  })
  const s = scan.data
  const waiting = s?.found.filter((f) => addable(f)).length ?? 0
  return (
    <>
      {s && running(s) ? (
        <button className="text-xs font-medium text-marquee" onClick={() => setOpen(true)}>
          {s.status === 'scanning' ? 'Scanning' : 'Adding'}
          {s.total ? ` ${s.done}/${s.total}` : '…'}
        </button>
      ) : s && (s.status === 'ready' || s.status === 'done') && waiting > 0 ? (
        <button className="text-xs font-medium text-marquee" onClick={() => setOpen(true)}>
          {count(waiting, 'to review', 'to review')}
        </button>
      ) : null}
      <Button variant="ghost" className="px-2 py-1 text-xs" busy={start.isPending} disabled={running(s)} onClick={() => start.mutate()} title="Look for titles on disk that aren't on the list yet">
        <ScanSearch className="size-4" aria-hidden /> Scan
      </Button>
      {start.error && (
        <span role="alert" className="text-xs text-bad">
          {start.error instanceof Error ? start.error.message : String(start.error)}
        </span>
      )}
      {open && s && <ScanReview folder={folder} onClose={() => setOpen(false)} onRescan={() => start.mutate()} />}
    </>
  )
}

function ScanReview({ folder, onClose, onRescan }: { folder: RootFolder; onClose: () => void; onRescan: () => void }) {
  const qc = useQueryClient()
  const scan = useLibScan(folder.id)
  const s = scan.data
  const kind = folder.kind
  const profiles = useProfiles()
  const defaults = useShowDefaults()
  const [picks, setPicks] = useState<Record<string, ScanMatch>>({})
  const [chosen, setChosen] = useState<Record<string, boolean>>({})
  const [changing, setChanging] = useState<string | null>(null)
  const [profileId, setProfileId] = useState(0)
  const [monitored, setMonitored] = useState(true)
  const [monitor, setMonitor] = useState('')
  useEffect(() => {
    if (!profileId && profiles.data?.length) setProfileId(profiles.data[0].id)
  }, [profiles.data, profileId])
  useEffect(() => {
    if (!monitor && defaults.data) setMonitor(defaults.data.monitor)
  }, [defaults.data, monitor])

  // Sure matches are ticked to start with; ones to check aren't.
  const ticked = (f: ScanFound) => (f.path in chosen ? chosen[f.path] : f.sure && !f.note) && addable(f, picks[f.path])
  const groups = useMemo(() => {
    const found = s?.found ?? []
    return {
      toAdd: found.filter((f) => !f.added && (f.match || picks[f.path]) && (!f.have || picks[f.path])),
      unmatched: found.filter((f) => !f.added && !f.match && !picks[f.path]),
      have: found.filter((f) => !f.added && f.have && !picks[f.path]),
      added: found.filter((f) => f.added),
    }
  }, [s, picks])
  const selected = groups.toAdd.filter(ticked)

  const add = useMutation({
    mutationFn: () =>
      post<LibScan>(`/rootfolders/${folder.id}/scan/add`, {
        picks: selected.map((f) => {
          const m = picks[f.path] ?? f.match!
          return { path: f.path, tmdbId: m.tmdbId, mbid: m.mbid, olKey: m.olKey }
        }),
        profileId,
        monitored,
        ...(kind === 'series' ? { monitor } : {}),
      }),
    onSuccess: (r) => {
      qc.setQueryData(['libscan', folder.id], r)
      setPicks({})
      setChosen({})
    },
  })
  const dismiss = useMutation({
    mutationFn: () => del(`/rootfolders/${folder.id}/scan`),
    onSuccess: () => {
      qc.setQueryData(['libscan', folder.id], null)
      onClose()
    },
  })
  useEffect(() => {
    if (s?.status === 'done') {
      qc.invalidateQueries({ queryKey: [kind === 'movie' ? 'movies' : kind === 'series' ? 'series' : isBooks(kind) ? 'authors' : 'artists'] })
    }
  }, [s?.status, kind, qc])

  const title = `Scan for ${kind === 'movie' ? 'movies' : kind === 'series' ? 'shows' : isBooks(kind) ? 'authors' : 'artists'}`
  if (!s) return <Modal title={title} onClose={onClose} wide>{scan.isPending ? <Spinner /> : <p className="text-sm text-muted">There's no scan of this folder now.</p>}</Modal>
  return (
    <Modal title={title} onClose={onClose} wide>
      <div className="flex flex-col gap-5">
        {running(s) ? (
          <Progress scan={s} />
        ) : s.status === 'failed' ? (
          <>
            <ErrorNote error={s.error} />
            <div className="flex justify-end">
              <Button onClick={onRescan}>Try again</Button>
            </div>
          </>
        ) : (
          <>
            <Summary scan={s} />
            {s.status === 'done' && (s.added > 0 || s.notAdded > 0) && (
              <p className="rounded-md border border-ok/40 bg-ok/5 px-3 py-2 text-sm">
                Added {count(s.added, noun(kind, 1), noun(kind, 2))} to the list.
                {s.notAdded > 0 && ` ${count(s.notAdded, "folder couldn't", "folders couldn't")} be added; each says why below.`}
              </p>
            )}
            {groups.toAdd.length > 0 && (
              <section>
                <h3 className="mb-1 font-display font-semibold">Not on the list yet</h3>
                <p className="mb-2 text-xs text-muted">Ticked ones look right. Check the ones marked “check this” and change any that are wrong.</p>
                <ul className="flex flex-col divide-y divide-line">
                  {groups.toAdd.map((f) => (
                    <FoundRow
                      key={f.path}
                      kind={kind}
                      found={f}
                      pick={picks[f.path]}
                      checked={ticked(f)}
                      onCheck={(v) => setChosen((c) => ({ ...c, [f.path]: v }))}
                      onChange={() => setChanging(f.path)}
                    />
                  ))}
                </ul>
              </section>
            )}
            {groups.unmatched.length > 0 && (
              <section>
                <h3 className="mb-1 font-display font-semibold">Couldn't tell what these are</h3>
                <ul className="flex flex-col divide-y divide-line">
                  {groups.unmatched.map((f) => (
                    <li key={f.path} className="flex flex-wrap items-center gap-3 py-2 text-sm">
                      <div className="min-w-0 flex-1">
                        <code className="break-all">{f.name}</code>
                        <p className="text-xs text-muted">{[f.problem, f.note, contents(kind, f)].filter(Boolean).join(' · ')}</p>
                      </div>
                      <Button className="px-2 py-1 text-xs" onClick={() => setChanging(f.path)}>
                        <Search className="size-3.5" aria-hidden /> Find it
                      </Button>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {groups.have.length > 0 && (
              <section>
                <h3 className="mb-1 font-display font-semibold">On the list in another folder</h3>
                <p className="mb-2 text-xs text-muted">These match a title Box Office already has somewhere else, so they're left alone. Change the match if one is a different title.</p>
                <ul className="flex flex-col divide-y divide-line">
                  {groups.have.map((f) => (
                    <li key={f.path} className="flex flex-wrap items-center gap-3 py-2 text-sm">
                      <div className="min-w-0 flex-1">
                        <code className="break-all">{f.name}</code>
                        <p className="text-xs text-muted">
                          {f.have!.title} is at <code className="break-all">{f.have!.path}</code>
                        </p>
                      </div>
                      <Button variant="ghost" className="px-2 py-1 text-xs" onClick={() => setChanging(f.path)}>
                        Change
                      </Button>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {groups.added.length > 0 && (
              <section>
                <h3 className="mb-1 font-display font-semibold">Added</h3>
                <ul className="flex flex-col gap-1 text-sm">
                  {groups.added.map((f) => (
                    <li key={f.path}>
                      <Link className="text-marquee" to={`/${kind === 'movie' ? 'movies' : kind === 'series' ? 'shows' : kind === 'ebook' ? 'books' : kind === 'audiobook' ? 'audiobooks' : 'music'}/${f.added}`} onClick={onClose}>
                        {(picks[f.path] ?? f.match)?.title ?? f.name}
                      </Link>{' '}
                      <span className="text-xs text-muted">from {f.name}</span>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {selected.length > 0 && (
              <div className="grid gap-4 rounded-lg border border-line p-4 sm:grid-cols-2">
                {!notTMDB(kind) && (
                  <Field label="Quality profile">
                    {(id) => <Select id={id} value={profileId} onChange={(v) => setProfileId(Number(v))} options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))} />}
                  </Field>
                )}
                {kind === 'series' && (
                  <Field label="Which episodes to watch for">
                    {(id) => <Select id={id} value={monitor} onChange={setMonitor} options={monitorOptions(defaults.data?.recent ?? 3)} />}
                  </Field>
                )}
                <div className="sm:col-span-2">
                  <Toggle
                    checked={monitored}
                    onChange={setMonitored}
                    label={kind === 'music' ? 'Watch for their new albums' : kind === 'ebook' ? 'Watch for their new books as ebooks' : kind === 'audiobook' ? 'Watch for their new books as audiobooks' : kind === 'series' ? 'Watch for missing and new episodes, and upgrades' : 'Watch for upgrades as the profile allows'}
                  />
                </div>
              </div>
            )}
            <ErrorNote error={add.error || dismiss.error} />
            <p className="text-xs text-muted">Box Office leaves the files where they are. Nothing is moved, renamed or deleted, and nothing is searched for until you ask.
              {isBooks(kind) && ' Books already in the folder aren’t marked as wanted, so they aren’t fetched again; pick any others you want on the author’s page.'}
            </p>
            <div className="flex flex-wrap justify-between gap-2">
              <div className="flex gap-2">
                <Button variant="ghost" busy={dismiss.isPending} onClick={() => dismiss.mutate()}>
                  Done
                </Button>
                <Button variant="ghost" onClick={onRescan}>
                  Scan again
                </Button>
              </div>
              <Button variant="primary" busy={add.isPending} disabled={selected.length === 0 || (!notTMDB(kind) && !profileId)} onClick={() => add.mutate()}>
                Add {selected.length ? count(selected.length, noun(kind, 1), noun(kind, 2)) : ''}
              </Button>
            </div>
          </>
        )}
      </div>
      {changing && s && (
        <ChangeMatch
          kind={kind}
          found={s.found.find((f) => f.path === changing)!}
          onClose={() => setChanging(null)}
          onPick={(m) => {
            setPicks((p) => ({ ...p, [changing]: m }))
            setChosen((c) => ({ ...c, [changing]: true }))
            setChanging(null)
          }}
        />
      )}
    </Modal>
  )
}

function Progress({ scan }: { scan: LibScan }) {
  const pct = scan.total ? Math.round((scan.done / scan.total) * 100) : 0
  return (
    <div className="flex flex-col gap-2" role="status">
      <p className="text-sm">
        {scan.step || 'Starting'}
        {scan.total ? ` (${scan.done} of ${scan.total})` : '…'}
      </p>
      <div className="h-2 overflow-hidden rounded-full bg-line">
        <div className="h-full bg-marquee transition-all" style={{ width: `${scan.total ? pct : 5}%` }} />
      </div>
      <p className="text-xs text-muted">This runs on the server, so you can close this and come back. Nothing on disk is changed.</p>
    </div>
  )
}

function Summary({ scan }: { scan: LibScan }) {
  const kind = scan.kind
  const left = scan.found.filter((f) => !f.added).length
  return (
    <div className="text-sm">
      <p>
        Box Office looked through {count(scan.folders, 'folder', 'folders')} in <code className="break-all">{scan.root}</code>.{' '}
        {scan.listed > 0 && `${count(scan.listed, 'is', 'are')} on the list already. `}
        {left === 0 ? `Everything else is on the list too.` : `${count(left, "isn't", "aren't")} yet.`}
      </p>
      {scan.elsewhere > 0 && (
        <p className="mt-1 text-xs text-muted">
          {count(scan.elsewhere, 'folder is', 'folders are')} named like a {noun(kind, 1)} on the list that's kept at another path (the same library mounted in two places, say), so {scan.elsewhere === 1 ? "it's" : "they're"} left out.
        </p>
      )}
      {scan.loose > 0 && (
        <p className="mt-1 flex items-start gap-1 text-xs text-muted">
          <CircleAlert className="mt-px size-3.5 shrink-0" aria-hidden />
          {count(scan.loose, 'file lies', 'files lie')} in the root folder itself, outside any {noun(kind, 1)}'s folder. Put each in a folder named for it to add it.
        </p>
      )}
    </div>
  )
}

function contents(kind: RootKind, f: ScanFound) {
  if (f.files === 0) return ''
  if (kind === 'music') return `${count(f.files, 'track', 'tracks')}, ${bytes(f.size)}`
  if (isBooks(kind)) return `${count(f.files, 'file', 'files')}, ${bytes(f.size)}`
  if (kind === 'series') return `${count(f.episodes ?? 0, 'episode', 'episodes')}, ${bytes(f.size)}`
  return `${count(f.files, 'video', 'videos')}, ${bytes(f.size)}`
}

function FoundRow({ kind, found: f, pick, checked, onCheck, onChange }: { kind: RootKind; found: ScanFound; pick?: ScanMatch; checked: boolean; onCheck: (v: boolean) => void; onChange: () => void }) {
  const m = (pick ?? f.match)!
  const sure = pick ? true : f.sure
  return (
    <li className="flex gap-3 py-3">
      <input type="checkbox" className="mt-1 size-4 shrink-0 accent-[var(--color-marquee)]" checked={checked} onChange={(e) => onCheck(e.target.checked)} aria-label={`Add ${f.name}`} />
      {kind !== 'music' && <Poster src={m.poster} title={m.title} className="w-12 shrink-0" />}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-display font-semibold">
            {m.title} {m.year ? <span className="font-normal text-muted">({m.year})</span> : null}
          </span>
          {m.disambiguation && <span className="text-xs text-muted">{m.disambiguation}</span>}
          {!sure && <Badge tone="gold">check this</Badge>}
          {f.problem && <Badge tone="bad">not added</Badge>}
        </div>
        <p className="text-xs text-muted">
          <code className="break-all">{f.name}</code>
          {contents(kind, f) && ` · ${contents(kind, f)}`}
        </p>
        {f.note && <p className="text-xs text-gold">{f.note[0].toUpperCase() + f.note.slice(1)}.</p>}
        {f.problem && <p className="text-xs text-bad">{f.problem[0].toUpperCase() + f.problem.slice(1)}.</p>}
      </div>
      <Button variant="ghost" className="shrink-0 self-start px-2 py-1 text-xs" onClick={onChange}>
        Change
      </Button>
    </li>
  )
}

// ChangeMatch searches for what a folder really is.
function ChangeMatch({ kind, found, onClose, onPick }: { kind: RootKind; found: ScanFound; onClose: () => void; onPick: (m: ScanMatch) => void }) {
  const [term, setTerm] = useState(found.guess.title + (found.guess.year && !notTMDB(kind) ? ' ' + found.guess.year : ''))
  const [q, setQ] = useState(term)
  useEffect(() => {
    const t = setTimeout(() => setQ(term.trim()), 400)
    return () => clearTimeout(t)
  }, [term])
  const path = kind === 'movie' ? '/lookup/movies' : kind === 'series' ? '/lookup/series' : isBooks(kind) ? '/lookup/authors' : '/lookup/artists'
  const results = useQuery({
    queryKey: ['lookup', kind, q],
    queryFn: async (): Promise<(ScanMatch & { id?: number })[]> => {
      const r = await get<(LookupMovie | LookupShow | LookupArtist | LookupAuthor)[]>(`${path}?q=${encodeURIComponent(q)}`)
      return r.map((x) =>
        'key' in x
          ? { olKey: x.key, title: x.name, poster: x.photo, disambiguation: [[x.born, x.died].filter(Boolean).join('–'), x.topWork].filter(Boolean).join(', '), id: x.id }
          : 'mbid' in x
          ? { mbid: x.mbid, title: x.name, disambiguation: x.disambiguation ?? [x.type, x.country].filter(Boolean).join(', '), id: x.id }
          : { tmdbId: x.tmdbId, title: x.title, year: x.year, poster: x.poster, overview: x.overview, id: x.id },
      )
    },
    enabled: q.length > 1,
  })
  const where = kind === 'music' ? 'MusicBrainz' : isBooks(kind) ? 'Open Library' : 'TMDB'
  return (
    <Modal title={`What is ${found.name}?`} onClose={onClose} wide>
      <div className="flex flex-col gap-4">
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted" aria-hidden />
          <Input
            autoFocus
            className="pl-9"
            value={term}
            onChange={(e) => setTerm(e.target.value)}
            placeholder={kind === 'movie' ? 'Title, "Title 1999", tmdb:603 or an IMDb id' : kind === 'series' ? 'Title, tmdb:95396 or tvdb:371980' : isBooks(kind) ? 'Author name, or an Open Library id like OL23919A' : 'Artist name, or a MusicBrainz id'}
          />
        </div>
        {results.isFetching && <Spinner label={`Looking on ${where}`} />}
        <ErrorNote error={results.error} />
        {results.data?.length === 0 && <p className="text-sm text-muted">{where} has nothing by that name.</p>}
        <ul className="flex flex-col divide-y divide-line">
          {results.data?.map((r) => (
            <li key={r.tmdbId ?? r.mbid ?? r.olKey} className="flex gap-4 py-3">
              {kind !== 'music' && <Poster src={r.poster} title={r.title} className="w-12 shrink-0" />}
              <div className="min-w-0 flex-1">
                <div className="font-display font-semibold">
                  {r.title} {r.year ? <span className="font-normal text-muted">({r.year})</span> : null}
                </div>
                <p className="line-clamp-2 text-sm text-muted">{r.overview ?? r.disambiguation}</p>
              </div>
              <div className="shrink-0 self-center">{r.id ? <span className="text-sm text-muted">On the list</span> : <Button onClick={() => onPick(r)}>Pick</Button>}</div>
            </li>
          ))}
        </ul>
      </div>
    </Modal>
  )
}
