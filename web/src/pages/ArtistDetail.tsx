import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Navigate, useNavigate, useParams } from 'react-router-dom'
import { CircleCheck, ListFilter, RefreshCw, Search, Trash2 } from 'lucide-react'
import { del, get, patch, post, type Album, type ArtistDetail as Detail } from '../api'
import { bytes } from '../format'
import { Releases } from '../Releases'
import { Badge, Button, Card, ErrorNote, Modal, Spinner, Toggle } from '../ui'
import { AlbumTypePicker, albumTypeLabels } from './Music'

// out says whether an album is out by today, as Box Office only looks for
// albums that are.
const out = (b: Album) => !!b.released && b.released <= new Date().toISOString().slice(0, 10)

// kindOf is the heading an album is listed under.
function kindOf(a: Album): string {
  if (a.secondaryTypes.length) return a.secondaryTypes[0]
  return ['Album', 'EP', 'Single'].includes(a.type) ? a.type : 'Other'
}

const order = ['Album', 'EP', 'Single', 'Live', 'Compilation', 'Soundtrack', 'Remix']

export function ArtistDetail() {
  const { t } = useTranslation()
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const nav = useNavigate()
  const artist = useQuery({ queryKey: ['artist', id], queryFn: () => get<Detail>(`/artists/${id}`) })
  const [removing, setRemoving] = useState(false)
  const [open, setOpen] = useState<number>()
  const [releasesFor, setReleasesFor] = useState<Album>()
  const set = (a: Detail) => {
    qc.setQueryData(['artist', id], a)
    qc.invalidateQueries({ queryKey: ['artists'] })
  }
  const edit = useMutation({ mutationFn: (body: { monitored?: boolean; albumTypes?: string[] }) => patch<Detail>(`/artists/${id}`, body), onSuccess: set })
  const refresh = useMutation({ mutationFn: () => post<Detail>(`/artists/${id}/refresh`), onSuccess: set })
  const album = useMutation({
    mutationFn: (v: { id: number; monitored: boolean }) => patch<Album>(`/albums/${v.id}`, { monitored: v.monitored }),
    onSuccess: (b) => {
      qc.setQueryData<Detail>(['artist', id], (a) => a && { ...a, albumList: a.albumList.map((x) => (x.id === b.id ? b : x)), wantedAlbums: a.wantedAlbums + (b.monitored ? 1 : -1) })
      qc.invalidateQueries({ queryKey: ['artists'] })
    },
  })
  const remove = useMutation({
    mutationFn: () => del(`/artists/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['artists'] })
      nav('/music')
    },
  })
  const groups = useMemo(() => {
    const by = new Map<string, Album[]>()
    for (const b of artist.data?.albumList ?? []) {
      const k = kindOf(b)
      by.set(k, [...(by.get(k) ?? []), b])
    }
    return [...by.entries()].sort(([a], [b]) => (order.indexOf(a) + 1 || 99) - (order.indexOf(b) + 1 || 99))
  }, [artist.data])
  // A link to one album (from Downloads, say) opens it.
  useEffect(() => {
    const m = /^#album-(\d+)$/.exec(window.location.hash)
    if (m && artist.data?.albumList.some((b) => b.id === Number(m[1]))) setOpen(Number(m[1]))
  }, [artist.data])

  if (artist.isPending) return <Spinner />
  if (artist.isError) return <ErrorNote error={artist.error} />
  const a = artist.data
  const opened = a.albumList.find((b) => b.id === open)
  return (
    <div>
      <h1 className="font-display text-4xl font-bold tracking-tight">{a.name}</h1>
      <div className="mt-2 flex flex-wrap gap-2">
        {a.monitored ? <Badge tone="gold">{t('music.wanted', { count: a.wantedAlbums })}</Badge> : <Badge>{t('common.paused')}</Badge>}
        {a.haveAlbums > 0 && <Badge tone="ok">{t('books.onDiskCount', { count: a.haveAlbums })}</Badge>}
        {a.type && <Badge>{a.type}</Badge>}
        {a.country && <Badge>{a.country}</Badge>}
        {a.disambiguation && <Badge>{a.disambiguation}</Badge>}
      </div>
      <p className="mt-3 text-sm text-muted">
        <Trans i18nKey="artist.filesAt" values={{ path: a.path }} components={{ code: <code className="break-all" /> }} />
      </p>
      <div className="mt-5 flex flex-wrap gap-2">
        <Button busy={refresh.isPending} onClick={() => refresh.mutate()}>
          <RefreshCw className="size-4" aria-hidden /> {t('artist.refresh')}
        </Button>
        <Button variant="ghost" onClick={() => setRemoving(true)}>
          <Trash2 className="size-4" aria-hidden /> {t('title.remove')}
        </Button>
      </div>
      <ErrorNote error={refresh.error ?? edit.error ?? album.error} />

      <Card className="mt-6">
        <div className="flex flex-col gap-4">
          <Toggle checked={a.monitored} disabled={edit.isPending} onChange={(monitored) => edit.mutate({ monitored })} label={t('artist.watch')} />
          <div>
            <p className="mb-2 text-sm font-medium">{t('artist.kinds')}</p>
            <AlbumTypePicker value={a.albumTypes} disabled={edit.isPending} onChange={(albumTypes) => edit.mutate({ albumTypes })} />
            <p className="mt-2 text-xs text-muted">{t('artist.kindsHint')}</p>
          </div>
        </div>
      </Card>

      {groups.length === 0 && <p className="mt-6 text-sm text-muted">{t('artist.noAlbums', { name: a.name })}</p>}
      {groups.map(([kind, albums]) => (
        <section key={kind} className="mt-8">
          <h2 className="mb-3 font-display text-lg font-semibold">
            {albumTypeLabels[kind] ? t(albumTypeLabels[kind]) : kind} <span className="font-normal text-muted">{albums.length}</span>
          </h2>
          <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
            {albums.map((b) => (
              <li key={b.id} id={`album-${b.id}`}>
                <button type="button" className="relative block w-full text-start" onClick={() => setOpen(b.id)} aria-label={t('artist.open', { title: b.title })}>
                  <Cover src={b.cover} title={b.title} />
                  {b.file && (
                    <span className="absolute top-2 end-2 rounded-full bg-ok p-1 text-white shadow" title={t('common.onDisk')}>
                      <CircleCheck className="size-4" aria-label={t('common.onDisk')} />
                    </span>
                  )}
                </button>
                <div className="mt-2 truncate font-medium" title={b.title}>
                  {b.title}
                </div>
                <div className="flex items-center justify-between gap-2 text-xs text-muted">
                  <span>{b.released?.slice(0, 4) || t('artist.noDate')}</span>
                  <button
                    type="button"
                    disabled={album.isPending}
                    aria-pressed={b.monitored}
                    aria-label={b.monitored ? t('artist.stopWanting', { title: b.title }) : t('artist.want', { title: b.title })}
                    onClick={() => album.mutate({ id: b.id, monitored: !b.monitored })}
                    className={`rounded-full border px-2 py-1 sm:py-0.5 ${b.monitored ? 'border-gold text-ink' : 'border-line hover:text-ink'}`}
                  >
                    {b.monitored ? t('common.wanted') : t('artist.notWanted')}
                  </button>
                </div>
              </li>
            ))}
          </ul>
        </section>
      ))}

      {opened && (
        <AlbumDialog
          album={opened}
          busy={album.isPending}
          onWant={(monitored) => album.mutate({ id: opened.id, monitored })}
          onReleases={() => {
            setOpen(undefined)
            setReleasesFor(opened)
          }}
          onClose={() => setOpen(undefined)}
        />
      )}
      {releasesFor && <Releases path={`/albums/${releasesFor.id}/releases`} title={releasesFor.title} onClose={() => setReleasesFor(undefined)} />}

      {removing && (
        <Modal title={t('artist.removeTitle', { name: a.name })} onClose={() => setRemoving(false)}>
          <p className="text-sm">{t('artist.removeBody', { name: a.name })}</p>
          <ErrorNote error={remove.error} />
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setRemoving(false)}>
              {t('artist.keep')}
            </Button>
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              {t('title.remove')}
            </Button>
          </div>
        </Modal>
      )}
    </div>
  )
}

// AlbumDialog is one album: whether it's in the library, and looking for
// it.
function AlbumDialog({
  album: b,
  busy,
  onWant,
  onReleases,
  onClose,
}: {
  album: Album
  busy: boolean
  onWant: (wanted: boolean) => void
  onReleases: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const search = useMutation({
    mutationFn: () => post(`/albums/${b.id}/search`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  })
  return (
    <Modal title={b.title} onClose={onClose}>
      <div className="flex gap-4">
        <div className="w-24 shrink-0 sm:w-28">
          <Cover src={b.cover} title={b.title} />
        </div>
        <div className="min-w-0 flex-1 text-sm">
          <p className="text-muted">{[b.released?.slice(0, 4), albumTypeLabels[kindOf(b)] ? t(albumTypeLabels[kindOf(b)]) : b.type].filter(Boolean).join(' · ')}</p>
          <div className="mt-3 flex flex-wrap gap-2">
            {b.file ? <Badge tone="ok">{t('common.onDisk')}</Badge> : b.monitored ? <Badge tone="gold">{t('common.wanted')}</Badge> : <Badge>{t('artist.notWanted')}</Badge>}
          </div>
          {b.file && (
            <p className="mt-2 break-all text-xs text-muted">
              {b.file.path} · {bytes(b.file.size)}
            </p>
          )}
        </div>
      </div>
      <div className="mt-5 flex flex-col gap-4">
        <Toggle checked={b.monitored} disabled={busy} onChange={onWant} label={t('artist.wantIt')} />
        <div className="flex flex-wrap gap-2">
          <Button variant="primary" busy={search.isPending} disabled={search.isSuccess} onClick={() => search.mutate()}>
            <Search className="size-4" aria-hidden /> {search.isSuccess ? t('books.lookingFor') : t('books.lookFor')}
          </Button>
          <Button onClick={onReleases}>
            <ListFilter className="size-4" aria-hidden /> {t('title.choose')}
          </Button>
        </div>
        {search.isSuccess && <p className="text-sm text-muted">{t('artist.searching')}</p>}
        {!b.file && b.monitored && !search.isSuccess && <p className="text-sm text-muted">{out(b) ? t('artist.alsoLooks') : t('artist.notOut')}</p>}
        <ErrorNote error={search.error} />
      </div>
    </Modal>
  )
}

// AlbumLink opens an album's artist page at the album, from a link that
// only knows the album (a download's, say).
export function AlbumLink() {
  const id = Number(useParams().id)
  const album = useQuery({ queryKey: ['album', id], queryFn: () => get<Album>(`/albums/${id}`) })
  if (album.isPending) return <Spinner />
  if (album.isError) return <ErrorNote error={album.error} />
  return <Navigate to={`/music/${album.data.artistId}#album-${id}`} replace />
}

// Cover is an album's square front cover, or its title when there's none.
function Cover({ src, title }: { src: string; title: string }) {
  const [failed, setFailed] = useState(false)
  return (
    <div className="aspect-square overflow-hidden rounded-lg bg-line">
      {!failed ? (
        <img src={src} alt="" loading="lazy" className="size-full object-cover" onError={() => setFailed(true)} />
      ) : (
        <div className="flex size-full items-center justify-center p-3 text-center font-display text-sm text-muted">{title}</div>
      )}
    </div>
  )
}
