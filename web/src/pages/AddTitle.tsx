import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { Search } from 'lucide-react'
import { get, post, type LookupMovie, type LookupShow, type Movie, type Series } from '../api'
import { monitorOptions, pickProfile, rememberProfile, useProfiles, useRootFolders, useShowDefaults } from '../hooks'
import { Button, ErrorNote, Field, Input, Modal, Poster, Select, Spinner, Toggle } from '../ui'

type Kind = 'movie' | 'series'

// AddTitle finds a movie or show on TMDB and adds it to the list.
export function AddTitle({ kind, onClose }: { kind: Kind; onClose: () => void }) {
  const [term, setTerm] = useState('')
  const [q, setQ] = useState('')
  const [picked, setPicked] = useState<LookupMovie | LookupShow | null>(null)
  useEffect(() => {
    const t = setTimeout(() => setQ(term.trim()), 350)
    return () => clearTimeout(t)
  }, [term])
  const path = kind === 'movie' ? '/lookup/movies' : '/lookup/series'
  const results = useQuery({
    queryKey: ['lookup', kind, q],
    queryFn: () => get<(LookupMovie | LookupShow)[]>(`${path}?q=${encodeURIComponent(q)}`),
    enabled: q.length > 1,
  })
  const what = kind === 'movie' ? 'movie' : 'show'
  return (
    <Modal title={picked ? `Add ${picked.title}` : `Add a ${what}`} onClose={onClose} wide>
      {picked ? (
        <AddOptions kind={kind} title={picked} onBack={() => setPicked(null)} onDone={onClose} />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted" aria-hidden />
            <Input autoFocus className="pl-9" placeholder={kind === 'movie' ? 'Title, "Title 1999", tmdb:603 or an IMDb id' : 'Title, tmdb:95396 or tvdb:371980'} value={term} onChange={(e) => setTerm(e.target.value)} />
          </div>
          {results.isFetching && <Spinner label="Looking" />}
          <ErrorNote error={results.error} />
          {results.data?.length === 0 && <p className="text-sm text-muted">TMDB has nothing by that name.</p>}
          <ul className="flex flex-col divide-y divide-line">
            {results.data?.map((r) => (
              <li key={r.tmdbId} className="flex gap-4 py-3">
                <Poster src={r.poster} title={r.title} className="w-16 shrink-0" />
                <div className="min-w-0 flex-1">
                  <div className="font-display font-semibold">
                    {r.title} {r.year ? <span className="font-normal text-muted">({r.year})</span> : null}
                  </div>
                  <p className="line-clamp-2 text-sm text-muted">{r.overview}</p>
                </div>
                <div className="shrink-0 self-center">
                  {r.id ? (
                    <Link className="text-sm font-medium text-marquee" to={`/${kind === 'movie' ? 'movies' : 'shows'}/${r.id}`} onClick={onClose}>
                      On the list
                    </Link>
                  ) : (
                    <Button onClick={() => setPicked(r)}>Pick</Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Modal>
  )
}

// AddOptions are the choices for adding a title. onAdded, when given, is
// called with the new title instead of opening its page.
export function AddOptions({
  kind,
  title,
  onBack,
  onDone,
  onAdded,
}: {
  kind: Kind
  title: { tmdbId: number }
  onBack?: () => void
  onDone: () => void
  onAdded?: (added: Movie | Series) => void
}) {
  const profiles = useProfiles()
  const folders = useRootFolders(kind)
  const qc = useQueryClient()
  const nav = useNavigate()
  const [profileId, setProfileId] = useState(0)
  const [root, setRoot] = useState('')
  const [monitored, setMonitored] = useState(true)
  const [search, setSearch] = useState(true)
  const defaults = useShowDefaults()
  const [monitor, setMonitor] = useState('')
  const [recent, setRecent] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    if (!profileId && profiles.data?.length) setProfileId(pickProfile(profiles.data, kind)?.id ?? 0)
  }, [profiles.data, profileId, kind])
  useEffect(() => {
    if (!root && folders.data?.length) setRoot(folders.data[0].path)
  }, [folders.data, root])
  useEffect(() => {
    if (!monitor && defaults.data) {
      setMonitor(defaults.data.monitor)
      setRecent(String(defaults.data.recent))
    }
  }, [defaults.data, monitor])

  if (profiles.isPending || folders.isPending || (kind === 'series' && defaults.isPending)) return <Spinner />
  if (!folders.data?.length) {
    return (
      <p className="text-sm">
        Add a {kind === 'movie' ? 'movie' : 'TV'} root folder under{' '}
        <Link to="/settings" className="font-medium text-marquee" onClick={onDone}>
          Settings
        </Link>{' '}
        first, so Box Office knows where the files go.
      </p>
    )
  }
  const seasons = Math.max(1, Math.round(Number(recent)) || defaults.data?.recent || 3)
  const add = async () => {
    setBusy(true)
    setError(null)
    try {
      const body = { tmdbId: title.tmdbId, profileId, rootFolder: root, monitored, search, ...(kind === 'series' ? { monitor, recent: seasons } : {}) }
      const added = await post<Movie | Series>(kind === 'movie' ? '/movies' : '/series', body)
      rememberProfile(kind, profileId)
      await qc.invalidateQueries({ queryKey: [kind === 'movie' ? 'movies' : 'series'] })
      qc.invalidateQueries({ queryKey: ['discover'] })
      if (onAdded) {
        onAdded(added)
        return
      }
      onDone()
      nav(`/${kind === 'movie' ? 'movies' : 'shows'}/${added.id}`)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Quality profile">{(id) => <Select id={id} value={profileId} onChange={(v) => setProfileId(Number(v))} options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))} />}</Field>
        <Field label="Root folder">{(id) => <Select id={id} value={root} onChange={setRoot} options={folders.data.map((f) => ({ value: f.path, label: f.path }))} />}</Field>
        {kind === 'series' && (
          <Field label="Which episodes to get" hint="Specials are left out. You can switch any season on or off later from the show's page.">
            {(id) => <Select id={id} value={monitor} onChange={setMonitor} options={monitorOptions(seasons)} />}
          </Field>
        )}
        {kind === 'series' && monitor === 'recent' && (
          <Field label="How many seasons" hint="Counts seasons that have started airing. A show with this many or fewer gets every season.">
            {(id) => <Input id={id} type="number" min={1} max={99} value={recent} onChange={(e) => setRecent(e.target.value)} className="max-w-32" />}
          </Field>
        )}
      </div>
      <Toggle checked={monitored} onChange={setMonitored} label="Watch for it, and get upgrades as the profile allows" />
      <Toggle checked={search} onChange={setSearch} label="Start looking now" />
      <ErrorNote error={error} />
      <div className="flex justify-between">
        {onBack ? (
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
        ) : (
          <span />
        )}
        <Button variant="primary" busy={busy} onClick={add}>
          Add {kind === 'movie' ? 'movie' : 'show'}
        </Button>
      </div>
    </div>
  )
}
