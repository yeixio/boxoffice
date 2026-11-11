import i18n from '../i18n'
import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleAlert, ScanSearch, Search } from 'lucide-react'
import { del, get, post, type LibScan, type LookupArtist, type LookupAuthor, type LookupMovie, type LookupShow, type RootFolder, type RootKind, type ScanFound, type ScanMatch } from '../api'
import { bytes } from '../format'
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

// nounOf is what a kind of root folder holds, for the catalog's scan.n and scan.title keys.
const nounOf = (kind: RootKind) => (kind === 'movie' ? 'movie' : kind === 'series' ? 'show' : isBooks(kind) ? 'author' : 'artist')
const things = (kind: RootKind, n: number) => i18n.t(`scan.n.${nounOf(kind)}`, { count: n })

// addable says whether a found folder can go on the list as picked.
const addable = (f: ScanFound, pick?: ScanMatch) => !f.added && !!(pick ?? f.match) && !(pick ? false : f.have)

// ScanButton is a root folder's Scan button, and what its last scan found.
export function ScanButton({ folder }: { folder: RootFolder }) {
  const { t } = useTranslation()
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
          {s.status === 'scanning' ? t('scan.scanning') : t('scan.adding')}
          {s.total ? ` ${s.done}/${s.total}` : '…'}
        </button>
      ) : s && (s.status === 'ready' || s.status === 'done') && waiting > 0 ? (
        <button className="text-xs font-medium text-marquee" onClick={() => setOpen(true)}>
          {t('scan.toReview', { count: waiting })}
        </button>
      ) : null}
      <Button variant="ghost" className="px-2 py-1 text-xs" busy={start.isPending} disabled={running(s)} onClick={() => start.mutate()} title={t('scan.scanHint')}>
        <ScanSearch className="size-4" aria-hidden /> {t('scan.scan')}
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
  const { t } = useTranslation()
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
  // A folder holding more of a title kept in another folder goes in with it.
  const link = useMutation({
    mutationFn: (path: string) => post<LibScan>(`/rootfolders/${folder.id}/scan/link`, { path }),
    onSuccess: (r) => {
      qc.setQueryData(['libscan', folder.id], r)
      qc.invalidateQueries({ queryKey: ['problems'] })
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

  const title = t(`scan.title.${nounOf(kind)}`)
  if (!s) return <Modal title={title} onClose={onClose} wide>{scan.isPending ? <Spinner /> : <p className="text-sm text-muted">{t('scan.noScan')}</p>}</Modal>
  return (
    <Modal title={title} onClose={onClose} wide>
      <div className="flex flex-col gap-5">
        {running(s) ? (
          <Progress scan={s} />
        ) : s.status === 'failed' ? (
          <>
            <ErrorNote error={s.error} />
            <div className="flex justify-end">
              <Button onClick={onRescan}>{t('tray.tryAgain')}</Button>
            </div>
          </>
        ) : (
          <>
            <Summary scan={s} />
            {s.status === 'done' && (s.added > 0 || s.notAdded > 0) && (
              <p className="rounded-md border border-ok/40 bg-ok/5 px-3 py-2 text-sm">
                {t('scan.added', { what: things(kind, s.added) })}
                {s.notAdded > 0 && ` ${t('scan.notAdded', { count: s.notAdded })}`}
              </p>
            )}
            {groups.toAdd.length > 0 && (
              <section>
                <h3 className="mb-1 font-display font-semibold">{t('scan.notListed')}</h3>
                <p className="mb-2 text-xs text-muted">{t('scan.notListedHint')}</p>
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
                <h3 className="mb-1 font-display font-semibold">{t('scan.unmatched')}</h3>
                <ul className="flex flex-col divide-y divide-line">
                  {groups.unmatched.map((f) => (
                    <li key={f.path} className="flex flex-wrap items-center gap-3 py-2 text-sm">
                      <div className="min-w-0 flex-1">
                        <code className="break-all">{f.name}</code>
                        <p className="text-xs text-muted">{[f.problem, f.note, contents(kind, f)].filter(Boolean).join(' · ')}</p>
                      </div>
                      <Button className="px-2 py-1 text-xs" onClick={() => setChanging(f.path)}>
                        <Search className="size-3.5" aria-hidden /> {t('scan.findIt')}
                      </Button>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {groups.have.length > 0 && (
              <section>
                <h3 className="mb-1 font-display font-semibold">{t('scan.elsewhereTitle')}</h3>
                <p className="mb-2 text-xs text-muted">{t(kind === 'movie' || kind === 'series' ? 'scan.elsewhereFilesHint' : 'scan.elsewhereHint')}</p>
                <ul className="flex flex-col divide-y divide-line">
                  {groups.have.map((f) => (
                    <li key={f.path} className="flex flex-wrap items-center gap-3 py-2 text-sm">
                      <div className="min-w-0 flex-1 basis-60">
                        <code className="break-all">{f.name}</code>
                        <p className="text-xs text-muted">
                          <Trans i18nKey="scan.isAt" values={{ title: f.have!.title, path: f.have!.path }} components={{ code: <code className="break-all" /> }} />
                        </p>
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        {(kind === 'movie' || kind === 'series') && (
                          <Button className="px-2 py-1 text-xs" busy={link.isPending && link.variables === f.path} disabled={link.isPending} onClick={() => link.mutate(f.path)}>
                            {t('scan.addFilesTo', { title: f.have!.title })}
                          </Button>
                        )}
                        <Button variant="ghost" className="px-2 py-1 text-xs" onClick={() => setChanging(f.path)}>
                          {t('scan.change')}
                        </Button>
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {groups.added.length > 0 && (
              <section>
                <h3 className="mb-1 font-display font-semibold">{t('scan.addedHeading')}</h3>
                <ul className="flex flex-col gap-1 text-sm">
                  {groups.added.map((f) => (
                    f.linked ? (
                      <li key={f.path}>
                        <Trans
                          i18nKey="scan.linked"
                          values={{ name: f.name, title: f.have?.title ?? '' }}
                          components={{ code: <code className="break-all" />, problems: <Link className="text-marquee" to="/problems" onClick={onClose} /> }}
                        />
                      </li>
                    ) : (
                    <li key={f.path}>
                      <Link className="text-marquee" to={`/${kind === 'movie' ? 'movies' : kind === 'series' ? 'shows' : kind === 'ebook' ? 'books' : kind === 'audiobook' ? 'audiobooks' : 'music'}/${f.added}`} onClick={onClose}>
                        {(picks[f.path] ?? f.match)?.title ?? f.name}
                      </Link>{' '}
                      <span className="text-xs text-muted">{t('scan.from', { name: f.name })}</span>
                    </li>
                    )
                  ))}
                </ul>
              </section>
            )}
            {selected.length > 0 && (
              <div className="grid gap-4 rounded-lg border border-line p-4 sm:grid-cols-2">
                {!notTMDB(kind) && (
                  <Field label={t('add.profile')}>
                    {(id) => <Select id={id} value={profileId} onChange={(v) => setProfileId(Number(v))} options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))} />}
                  </Field>
                )}
                {kind === 'series' && (
                  <Field label={t('scan.whichEpisodes')}>
                    {(id) => <Select id={id} value={monitor} onChange={setMonitor} options={monitorOptions(defaults.data?.recent ?? 3)} />}
                  </Field>
                )}
                <div className="sm:col-span-2">
                  <Toggle
                    checked={monitored}
                    onChange={setMonitored}
                    label={t(`scan.watch.${kind === 'music' || kind === 'ebook' || kind === 'audiobook' || kind === 'series' ? kind : 'movie'}`)}
                  />
                </div>
              </div>
            )}
            <ErrorNote error={add.error || link.error || dismiss.error} />
            <p className="text-xs text-muted">
              {t('scan.leaves')}
              {isBooks(kind) && ` ${t('scan.booksNote')}`}
            </p>
            <div className="flex flex-wrap justify-between gap-2">
              <div className="flex gap-2">
                <Button variant="ghost" busy={dismiss.isPending} onClick={() => dismiss.mutate()}>
                  {t('bulk.done')}
                </Button>
                <Button variant="ghost" onClick={onRescan}>
                  {t('scan.again')}
                </Button>
              </div>
              <Button variant="primary" busy={add.isPending} disabled={selected.length === 0 || (!notTMDB(kind) && !profileId)} onClick={() => add.mutate()}>
                {selected.length ? t('scan.addWhat', { what: things(kind, selected.length) }) : t('scan.add')}
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
  const { t } = useTranslation()
  const pct = scan.total ? Math.round((scan.done / scan.total) * 100) : 0
  return (
    <div className="flex flex-col gap-2" role="status">
      <p className="text-sm">
        {scan.step || t('scan.starting')}
        {scan.total ? ` ${t('scan.progress', { done: scan.done, total: scan.total })}` : '…'}
      </p>
      <div className="h-2 overflow-hidden rounded-full bg-line">
        <div className="h-full bg-marquee transition-all" style={{ width: `${scan.total ? pct : 5}%` }} />
      </div>
      <p className="text-xs text-muted">{t('scan.background')}</p>
    </div>
  )
}

function Summary({ scan }: { scan: LibScan }) {
  const { t } = useTranslation()
  const left = scan.found.filter((f) => !f.added).length
  return (
    <div className="text-sm">
      <p>
        <Trans i18nKey="scan.lookedThrough" values={{ count: scan.folders, root: scan.root }} components={{ code: <code className="break-all" /> }} />{' '}
        {scan.listed > 0 && `${t('scan.listed', { count: scan.listed })} `}
        {left === 0 ? t('scan.allListed') : t('scan.left', { count: left })}
      </p>
      {scan.elsewhere > 0 && (
        <p className="mt-1 text-xs text-muted">
          {t('scan.elsewhere', { count: scan.elsewhere })}
        </p>
      )}
      {scan.loose > 0 && (
        <p className="mt-1 flex items-start gap-1 text-xs text-muted">
          <CircleAlert className="mt-px size-3.5 shrink-0" aria-hidden />
          {t('scan.loose', { count: scan.loose })}
        </p>
      )}
    </div>
  )
}

function contents(kind: RootKind, f: ScanFound) {
  if (f.files === 0) return ''
  const size = bytes(f.size)
  if (kind === 'music') return i18n.t('scan.tracks', { count: f.files, size })
  if (isBooks(kind)) return i18n.t('scan.files', { count: f.files, size })
  if (kind === 'series') return i18n.t('scan.episodes', { count: f.episodes ?? 0, size })
  return i18n.t('scan.videos', { count: f.files, size })
}

function FoundRow({ kind, found: f, pick, checked, onCheck, onChange }: { kind: RootKind; found: ScanFound; pick?: ScanMatch; checked: boolean; onCheck: (v: boolean) => void; onChange: () => void }) {
  const { t } = useTranslation()
  const m = (pick ?? f.match)!
  const sure = pick ? true : f.sure
  return (
    <li className="flex gap-3 py-3">
      <input type="checkbox" className="mt-1 size-4 shrink-0 accent-[var(--color-marquee)]" checked={checked} onChange={(e) => onCheck(e.target.checked)} aria-label={t('scan.addName', { name: f.name })} />
      {kind !== 'music' && <Poster src={m.poster} title={m.title} className="w-12 shrink-0" />}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-display font-semibold">
            {m.title} {m.year ? <span className="font-normal text-muted">({m.year})</span> : null}
          </span>
          {m.disambiguation && <span className="text-xs text-muted">{m.disambiguation}</span>}
          {!sure && <Badge tone="gold">{t('scan.check')}</Badge>}
          {f.problem && <Badge tone="bad">{t('scan.notAddedBadge')}</Badge>}
        </div>
        <p className="text-xs text-muted">
          <code className="break-all">{f.name}</code>
          {contents(kind, f) && ` · ${contents(kind, f)}`}
        </p>
        {f.note && <p className="text-xs text-gold">{f.note[0].toUpperCase() + f.note.slice(1)}.</p>}
        {f.problem && <p className="text-xs text-bad">{f.problem[0].toUpperCase() + f.problem.slice(1)}.</p>}
      </div>
      <Button variant="ghost" className="shrink-0 self-start px-2 py-1 text-xs" onClick={onChange}>
        {t('scan.change')}
      </Button>
    </li>
  )
}

// ChangeMatch searches for what a folder really is.
function ChangeMatch({ kind, found, onClose, onPick }: { kind: RootKind; found: ScanFound; onClose: () => void; onPick: (m: ScanMatch) => void }) {
  const { t } = useTranslation()
  const [term, setTerm] = useState(found.guess.title + (found.guess.year && !notTMDB(kind) ? ' ' + found.guess.year : ''))
  const [q, setQ] = useState(term)
  useEffect(() => {
    const timer = setTimeout(() => setQ(term.trim()), 400)
    return () => clearTimeout(timer)
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
    <Modal title={t('scan.what', { name: found.name })} onClose={onClose} wide>
      <div className="flex flex-col gap-4">
        <div className="relative">
          <Search className="pointer-events-none absolute start-3 top-2.5 size-4 text-muted" aria-hidden />
          <Input
            autoFocus
            className="ps-9"
            value={term}
            onChange={(e) => setTerm(e.target.value)}
            placeholder={kind === 'movie' ? t('add.moviePlaceholder') : kind === 'series' ? t('add.showPlaceholder') : isBooks(kind) ? t('scan.authorPlaceholder') : t('music.placeholder')}
          />
        </div>
        {results.isFetching && <Spinner label={t('scan.lookingOn', { where })} />}
        <ErrorNote error={results.error} />
        {results.data?.length === 0 && <p className="text-sm text-muted">{t('scan.nothingOn', { where })}</p>}
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
              <div className="shrink-0 self-center">{r.id ? <span className="text-sm text-muted">{t('add.onList')}</span> : <Button onClick={() => onPick(r)}>{t('add.pick')}</Button>}</div>
            </li>
          ))}
        </ul>
      </div>
    </Modal>
  )
}
