import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { ListFilter, RefreshCw, Search, Trash2 } from 'lucide-react'
import { Releases } from '../Releases'
import { DeleteFiles, RemoveTitle } from '../RemoveFiles'
import { LockedNote } from '../Bulk'
import { get, patch, post, type Movie } from '../api'
import { bytes, day } from '../format'
import { qualityName, useProfiles, useQualities } from '../hooks'
import { Badge, Button, Card, ErrorNote, Field, Poster, Select, Spinner, Toggle } from '../ui'

export function MovieDetail() {
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const nav = useNavigate()
  const movie = useQuery({ queryKey: ['movie', id], queryFn: () => get<Movie>(`/movies/${id}`) })
  const profiles = useProfiles()
  const qualities = useQualities()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [note, setNote] = useState('')
  const [releases, setReleases] = useState(false)
  const [deleteFile, setDeleteFile] = useState(false)

  const edit = useMutation({
    mutationFn: (body: Partial<Pick<Movie, 'monitored' | 'profileId'>>) => patch<Movie>(`/movies/${id}`, body),
    onSuccess: (m) => {
      qc.setQueryData(['movie', id], m)
      qc.invalidateQueries({ queryKey: ['movies'] })
    },
  })
  const action = useMutation({
    mutationFn: (what: 'search' | 'refresh') => post(`/movies/${id}/${what}`),
    onSuccess: (_, what) => {
      setNote(what === 'search' ? 'Looking for it now; see Activity.' : 'Refreshing its details from TMDB.')
      setTimeout(() => qc.invalidateQueries({ queryKey: ['movie', id] }), 2500)
    },
  })
  const removed = () => {
    qc.invalidateQueries({ queryKey: ['movies'] })
    nav('/movies')
  }

  if (movie.isPending) return <Spinner />
  if (movie.isError) return <ErrorNote error={movie.error} />
  const m = movie.data
  const locked = !!profiles.data?.find((p) => p.id === m.profileId)?.locked
  const dates = [
    ['In cinemas', m.inCinemas],
    ['Digital', m.digitalRelease],
    ['On disc', m.physicalRelease],
  ].filter(([, d]) => d) as [string, string][]

  return (
    <div>
      {m.backdrop && (
        <div className="-mx-4 -mt-8 mb-6 h-56 overflow-hidden sm:h-72">
          <img src={m.backdrop} alt="" className="size-full object-cover opacity-80 [mask-image:linear-gradient(to_bottom,black_40%,transparent)]" />
        </div>
      )}
      <div className="flex flex-col gap-8 md:flex-row">
        <Poster src={m.poster} title={m.title} className="w-48 shrink-0 self-start shadow-lg" />
        <div className="flex-1">
          <h1 className="font-display text-4xl font-bold tracking-tight">
            {m.title} {m.year ? <span className="font-normal text-muted">{m.year}</span> : null}
          </h1>
          <div className="mt-2 flex flex-wrap gap-2">
            {m.file ? <Badge tone="ok">On disk</Badge> : m.monitored ? <Badge tone="gold">Wanted</Badge> : <Badge>Not watched for</Badge>}
            {m.runtime ? <Badge>{m.runtime} min</Badge> : null}
            {m.status && <Badge>{m.status}</Badge>}
          </div>
          {m.overview && <p className="mt-4 max-w-3xl leading-relaxed">{m.overview}</p>}
          {dates.length > 0 && (
            <dl className="mt-4 flex flex-wrap gap-6 text-sm">
              {dates.map(([label, d]) => (
                <div key={label}>
                  <dt className="text-muted">{label}</dt>
                  <dd className="font-medium">{day(d)}</dd>
                </div>
              ))}
            </dl>
          )}
          {locked && <LockedNote />}
          <div className="mt-6 flex flex-wrap gap-2">
            {!locked && (
              <>
                <Button variant="primary" busy={action.isPending && action.variables === 'search'} onClick={() => action.mutate('search')}>
                  <Search className="size-4" aria-hidden /> Look for it
                </Button>
                <Button onClick={() => setReleases(true)}>
                  <ListFilter className="size-4" aria-hidden /> Choose a release
                </Button>
              </>
            )}
            <Button busy={action.isPending && action.variables === 'refresh'} onClick={() => action.mutate('refresh')}>
              <RefreshCw className="size-4" aria-hidden /> Refresh details
            </Button>
            <Button variant="danger" onClick={() => setConfirmDelete(true)}>
              <Trash2 className="size-4" aria-hidden /> Remove
            </Button>
          </div>
          {note && <p className="mt-3 text-sm text-muted">{note}</p>}
          {releases && <Releases path={`/movies/${id}/releases`} title={m.title} onClose={() => setReleases(false)} />}
          <ErrorNote error={action.error || edit.error} />
        </div>
      </div>

      <div className="mt-8 grid gap-6 lg:grid-cols-2">
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">How Box Office handles it</h2>
          <div className="flex flex-col gap-4">
            <Toggle checked={m.monitored} onChange={(v) => edit.mutate({ monitored: v })} label="Watch for it, and get upgrades as the profile allows" />
            <Field label="Quality profile">
              {(fid) => <Select id={fid} value={m.profileId} onChange={(v) => edit.mutate({ profileId: Number(v) })} options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))} />}
            </Field>
            <div className="text-sm">
              <div className="text-muted">Folder</div>
              <code className="break-all">{m.path}</code>
            </div>
          </div>
        </Card>
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">File</h2>
          {m.file ? (
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
              <dt className="text-muted">Path</dt>
              <dd className="break-all font-mono text-xs">{m.file.path}</dd>
              <dt className="text-muted">Quality</dt>
              <dd>{qualityName(qualities.data, m.file.quality)}</dd>
              <dt className="text-muted">Size</dt>
              <dd>{bytes(m.file.size)}</dd>
              <dt className="text-muted">Added</dt>
              <dd>{day(m.file.addedAt)}</dd>
              {!locked && (
                <dd className="col-span-2 mt-2">
                  <Button variant="danger" onClick={() => setDeleteFile(true)}>
                    <Trash2 className="size-4" aria-hidden /> Delete file from disk
                  </Button>
                </dd>
              )}
            </dl>
          ) : (
            <p className="text-sm text-muted">Not on disk yet.</p>
          )}
        </Card>
      </div>

      {confirmDelete && <RemoveTitle kind="movie" id={m.id} title={m.title} onClose={() => setConfirmDelete(false)} onRemoved={removed} />}
      {deleteFile && (
        <DeleteFiles<Movie>
          path={`/movies/${m.id}/file`}
          title={`${m.title}'s file`}
          what="movie"
          onClose={() => setDeleteFile(false)}
          onDone={(next) => {
            qc.setQueryData(['movie', id], next)
            qc.invalidateQueries({ queryKey: ['movies'] })
            setDeleteFile(false)
          }}
        />
      )}
    </div>
  )
}
