import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { Music as MusicIcon, Plus, Search } from 'lucide-react'
import { get, post, type AlbumTypes, type Artist, type ArtistDetail, type LookupArtist } from '../api'
import { day } from '../format'
import { useRootFolders } from '../hooks'
import { SortControl, sortList, useSort, words, type SortOption } from '../sort'
import { Badge, Button, Empty, ErrorNote, Field, Heading, Input, Modal, Select, Spinner, Toggle } from '../ui'

const artistSorts: SortOption<Artist>[] = [
  { key: 'name', label: 'Name', value: (a) => a.sortName || a.name, dir: 'asc', says: words.text },
  { key: 'added', label: 'Date added', value: (a) => a.addedAt, dir: 'desc', says: words.date },
  { key: 'wanted', label: 'Albums wanted', value: (a) => a.wantedAlbums, dir: 'desc', says: { asc: 'Fewest first', desc: 'Most first' } },
]

const byName = (a: Artist) => a.sortName || a.name

// Music lists the artists Box Office follows.
export function Music() {
  const artists = useQuery({ queryKey: ['artists'], queryFn: () => get<Artist[]>('/artists') })
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState('')
  const sort = useSort('artists', artistSorts)
  const shown = useMemo(() => {
    const t = text.trim().toLowerCase()
    return sortList(artists.data ?? [], sort.option, sort.dir, byName).filter((a) => !t || a.name.toLowerCase().includes(t))
  }, [artists.data, sort.option, sort.dir, text])
  const wanted = artists.data?.reduce((n, a) => n + a.wantedAlbums, 0) ?? 0
  return (
    <>
      <Heading
        title="Music"
        sub={artists.data ? `${artists.data.length} ${artists.data.length === 1 ? 'artist' : 'artists'}, ${wanted} ${wanted === 1 ? 'album' : 'albums'} wanted` : undefined}
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus className="size-4" aria-hidden /> Add artist
          </Button>
        }
      />
      {adding && <AddArtist onClose={() => setAdding(false)} />}
      <ErrorNote error={artists.error} />
      {artists.isPending && <Spinner />}
      {artists.data?.length === 0 && (
        <Empty icon={<MusicIcon className="size-8" />} title="No artists yet">
          Add an artist and Box Office lists their albums from MusicBrainz. Studio albums are wanted to start with; you can pick EPs, singles and live albums too.
        </Empty>
      )}
      {!!artists.data?.length && (
        <>
          <div className="mb-5 flex flex-col gap-2 sm:flex-row sm:items-center">
            <Input type="search" className="sm:w-60 sm:flex-none" placeholder="Filter by name" aria-label="Filter by name" value={text} onChange={(e) => setText(e.target.value)} />
            <SortControl sort={sort} className="sm:ml-auto" />
          </div>
          {shown.length === 0 && <p className="py-10 text-center text-sm text-muted">No artists match.</p>}
          <ul className="flex flex-col divide-y divide-line">
            {shown.map((a) => (
              <li key={a.id}>
                <Link to={`/music/${a.id}`} className="flex items-center gap-3 py-3 hover:text-marquee">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-full bg-line font-display font-semibold text-muted" aria-hidden>
                    {a.name.slice(0, 1).toUpperCase()}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{a.name}</span>
                    <span className="block truncate text-xs text-muted">
                      {sort.option.key === 'added' ? `Added ${day(a.addedAt)}` : `${a.albums} ${a.albums === 1 ? 'release' : 'releases'}`}
                      {a.disambiguation && ` · ${a.disambiguation}`}
                    </span>
                  </span>
                  {a.monitored ? a.wantedAlbums > 0 ? <Badge tone="gold">{a.wantedAlbums} wanted</Badge> : <Badge>Nothing wanted</Badge> : <Badge>Paused</Badge>}
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  )
}

function describe(a: LookupArtist): string {
  const years = a.began ? `${a.began.slice(0, 4)}${a.ended ? `–${a.ended.slice(0, 4)}` : ''}` : ''
  return [a.type, a.country, years, a.disambiguation].filter(Boolean).join(' · ')
}

// AddArtist finds an artist on MusicBrainz and adds them.
function AddArtist({ onClose }: { onClose: () => void }) {
  const [term, setTerm] = useState('')
  const [q, setQ] = useState('')
  const [picked, setPicked] = useState<LookupArtist | null>(null)
  useEffect(() => {
    const t = setTimeout(() => setQ(term.trim()), 400)
    return () => clearTimeout(t)
  }, [term])
  const results = useQuery({ queryKey: ['lookup', 'artists', q], queryFn: () => get<LookupArtist[]>(`/lookup/artists?q=${encodeURIComponent(q)}`), enabled: q.length > 1 })
  return (
    <Modal title={picked ? `Add ${picked.name}` : 'Add an artist'} onClose={onClose} wide>
      {picked ? (
        <AddOptions artist={picked} onBack={() => setPicked(null)} onDone={onClose} />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted" aria-hidden />
            <Input autoFocus className="pl-9" placeholder="Artist name, or a MusicBrainz id" value={term} onChange={(e) => setTerm(e.target.value)} />
          </div>
          {results.isFetching && <Spinner label="Looking on MusicBrainz" />}
          <ErrorNote error={results.error} />
          {results.data?.length === 0 && <p className="text-sm text-muted">MusicBrainz has nobody by that name.</p>}
          <ul className="flex flex-col divide-y divide-line">
            {results.data?.map((a) => (
              <li key={a.mbid} className="flex items-center gap-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="font-display font-semibold">{a.name}</div>
                  <p className="truncate text-sm text-muted">{describe(a)}</p>
                </div>
                {a.id ? (
                  <Link className="shrink-0 text-sm font-medium text-marquee" to={`/music/${a.id}`} onClick={onClose}>
                    On the list
                  </Link>
                ) : (
                  <Button onClick={() => setPicked(a)}>Pick</Button>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </Modal>
  )
}

export const albumTypeLabels: Record<string, string> = {
  Album: 'Studio albums',
  EP: 'EPs',
  Single: 'Singles',
  Live: 'Live albums',
  Compilation: 'Compilations',
  Soundtrack: 'Soundtracks',
  Remix: 'Remixes',
  Other: 'Everything else',
}

export function useAlbumTypes() {
  return useQuery({ queryKey: ['albumtypes'], queryFn: () => get<AlbumTypes>('/artists/albumtypes'), staleTime: Infinity })
}

// AlbumTypePicker ticks the kinds of album an artist is watched for.
export function AlbumTypePicker({ value, onChange, disabled }: { value: string[]; onChange: (v: string[]) => void; disabled?: boolean }) {
  const types = useAlbumTypes()
  return (
    <div className="flex flex-wrap gap-2" role="group" aria-label="Kinds of album">
      {types.data?.types.map((t) => {
        const on = value.includes(t)
        return (
          <button
            key={t}
            type="button"
            disabled={disabled}
            aria-pressed={on}
            onClick={() => onChange(on ? value.filter((v) => v !== t) : [...value, t])}
            className={`rounded-full border px-3 py-1.5 text-sm sm:py-1 ${on ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:text-ink'}`}
          >
            {albumTypeLabels[t] ?? t}
          </button>
        )
      })}
    </div>
  )
}

function AddOptions({ artist, onBack, onDone }: { artist: LookupArtist; onBack: () => void; onDone: () => void }) {
  const folders = useRootFolders('music')
  const types = useAlbumTypes()
  const qc = useQueryClient()
  const nav = useNavigate()
  const [root, setRoot] = useState('')
  const [monitored, setMonitored] = useState(true)
  const [albumTypes, setAlbumTypes] = useState<string[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    if (!root && folders.data?.length) setRoot(folders.data[0].path)
  }, [folders.data, root])
  if (folders.isPending || types.isPending) return <Spinner />
  if (!folders.data?.length) {
    return (
      <p className="text-sm">
        Add a music root folder under{' '}
        <Link to="/settings" className="font-medium text-marquee" onClick={onDone}>
          Settings
        </Link>{' '}
        first, so Box Office knows where the music goes.
      </p>
    )
  }
  const picked = albumTypes ?? types.data?.defaults ?? []
  const add = async () => {
    setBusy(true)
    setError(null)
    try {
      const added = await post<ArtistDetail>('/artists', { mbid: artist.mbid, rootFolder: root, monitored, albumTypes: picked })
      await qc.invalidateQueries({ queryKey: ['artists'] })
      onDone()
      nav(`/music/${added.id}`)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex flex-col gap-4">
      <Field label="Root folder">{(id) => <Select id={id} value={root} onChange={setRoot} options={folders.data.map((f) => ({ value: f.path, label: f.path }))} />}</Field>
      <Field label="Which albums to get" hint="You can switch any album on or off later from the artist's page.">
        {() => <AlbumTypePicker value={picked} onChange={setAlbumTypes} />}
      </Field>
      <Toggle checked={monitored} onChange={setMonitored} label="Watch for new albums of these kinds" />
      <p className="text-sm text-muted">Box Office looks the albums up on MusicBrainz, which takes a few seconds for artists with a lot of them.</p>
      <ErrorNote error={error} />
      <div className="flex justify-between">
        <Button variant="ghost" onClick={onBack}>
          Back
        </Button>
        <Button variant="primary" busy={busy} disabled={picked.length === 0 && monitored} onClick={add}>
          Add artist
        </Button>
      </div>
    </div>
  )
}
