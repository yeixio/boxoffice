import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { Search } from 'lucide-react'
import { get, post, type LookupMovie, type LookupShow, type Movie, type Series } from '../api'
import { useProfiles, useRootFolders } from '../hooks'
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

function AddOptions({ kind, title, onBack, onDone }: { kind: Kind; title: LookupMovie | LookupShow; onBack: () => void; onDone: () => void }) {
  const profiles = useProfiles()
  const folders = useRootFolders(kind)
  const qc = useQueryClient()
  const nav = useNavigate()
  const [profileId, setProfileId] = useState(0)
  const [root, setRoot] = useState('')
  const [monitored, setMonitored] = useState(true)
  const [search, setSearch] = useState(true)
  const [monitor, setMonitor] = useState('all')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    if (!profileId && profiles.data?.length) setProfileId(profiles.data[0].id)
  }, [profiles.data, profileId])
  useEffect(() => {
    if (!root && folders.data?.length) setRoot(folders.data[0].path)
  }, [folders.data, root])

  if (profiles.isPending || folders.isPending) return <Spinner />
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
  const add = async () => {
    setBusy(true)
    setError(null)
    try {
      const body = { tmdbId: title.tmdbId, profileId, rootFolder: root, monitored, search, ...(kind === 'series' ? { monitor } : {}) }
      const added = await post<Movie | Series>(kind === 'movie' ? '/movies' : '/series', body)
      await qc.invalidateQueries({ queryKey: [kind === 'movie' ? 'movies' : 'series'] })
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
          <Field label="Which episodes to get">
            {(id) => (
              <Select
                id={id}
                value={monitor}
                onChange={setMonitor}
                options={[
                  { value: 'all', label: 'All episodes' },
                  { value: 'future', label: 'Only ones not out yet' },
                  { value: 'latest', label: 'The latest season' },
                  { value: 'none', label: 'None for now' },
                ]}
              />
            )}
          </Field>
        )}
      </div>
      <Toggle checked={monitored} onChange={setMonitored} label="Watch for it, and get upgrades as the profile allows" />
      <Toggle checked={search} onChange={setSearch} label="Start looking now" />
      <ErrorNote error={error} />
      <div className="flex justify-between">
        <Button variant="ghost" onClick={onBack}>
          Back
        </Button>
        <Button variant="primary" busy={busy} onClick={add}>
          Add {kind === 'movie' ? 'movie' : 'show'}
        </Button>
      </div>
    </div>
  )
}
