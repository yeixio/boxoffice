import { Trans, useTranslation } from 'react-i18next'
import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { RefreshCw, Trash2 } from 'lucide-react'
import { del, get, patch, post, type Album, type ArtistDetail as Detail } from '../api'
import { Badge, Button, Card, ErrorNote, Modal, Spinner, Toggle } from '../ui'
import { AlbumTypePicker, albumTypeLabels } from './Music'

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

  if (artist.isPending) return <Spinner />
  if (artist.isError) return <ErrorNote error={artist.error} />
  const a = artist.data
  return (
    <div>
      <h1 className="font-display text-4xl font-bold tracking-tight">{a.name}</h1>
      <div className="mt-2 flex flex-wrap gap-2">
        {a.monitored ? <Badge tone="gold">{t('music.wanted', { count: a.wantedAlbums })}</Badge> : <Badge>{t('common.paused')}</Badge>}
        {a.type && <Badge>{a.type}</Badge>}
        {a.country && <Badge>{a.country}</Badge>}
        {a.disambiguation && <Badge>{a.disambiguation}</Badge>}
      </div>
      <p className="mt-3 text-sm text-muted">
        <Trans i18nKey="artist.filesGo" values={{ path: a.path }} components={{ code: <code className="break-all" /> }} />
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
              <li key={b.id}>
                <Cover src={b.cover} title={b.title} />
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
