import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { Music as MusicIcon, Plus, Search } from 'lucide-react'
import { get, post, put, type AlbumTypes, type Artist, type ArtistDetail, type LookupArtist, type MusicSettings } from '../api'
import { day } from '../format'
import { useRootFolders } from '../hooks'
import { SortControl, sortList, useSort, words, type SortOption } from '../sort'
import { Badge, Button, Empty, ErrorNote, Field, Heading, Input, Modal, Select, Spinner, Toggle } from '../ui'

const artistSorts: SortOption<Artist>[] = [
  { key: 'name', label: 'sorts.name', value: (a) => a.sortName || a.name, dir: 'asc', says: words.text },
  { key: 'added', label: 'sorts.added', value: (a) => a.addedAt, dir: 'desc', says: words.date },
  { key: 'wanted', label: 'sorts.albumsWanted', value: (a) => a.wantedAlbums, dir: 'desc', says: words.count },
]

const byName = (a: Artist) => a.sortName || a.name

// Music lists the artists Box Office follows.
export function Music() {
  const { t } = useTranslation()
  const artists = useQuery({ queryKey: ['artists'], queryFn: () => get<Artist[]>('/artists') })
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState('')
  const sort = useSort('artists', artistSorts)
  const shown = useMemo(() => {
    const q = text.trim().toLowerCase()
    return sortList(artists.data ?? [], sort.option, sort.dir, byName).filter((a) => !q || a.name.toLowerCase().includes(q))
  }, [artists.data, sort.option, sort.dir, text])
  const wanted = artists.data?.reduce((n, a) => n + a.wantedAlbums, 0) ?? 0
  return (
    <>
      <Heading
        title={t('nav.music')}
        sub={artists.data ? t('music.sub', { artists: t('music.artists', { count: artists.data.length }), albums: t('music.albums', { count: wanted }) }) : undefined}
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus className="size-4" aria-hidden /> {t('music.add')}
          </Button>
        }
      />
      {adding && <AddArtist onClose={() => setAdding(false)} />}
      <QualityPicker />
      <ErrorNote error={artists.error} />
      {artists.isPending && <Spinner />}
      {artists.data?.length === 0 && (
        <Empty icon={<MusicIcon className="size-8" />} title={t('music.none')}>
          {t('music.noneBody')}
        </Empty>
      )}
      {!!artists.data?.length && (
        <>
          <div className="mb-5 flex flex-col gap-2 sm:flex-row sm:items-center">
            <Input type="search" className="sm:w-60 sm:flex-none" placeholder={t('list.filterName')} aria-label={t('list.filterName')} value={text} onChange={(e) => setText(e.target.value)} />
            <SortControl sort={sort} className="sm:ms-auto" />
          </div>
          {shown.length === 0 && <p className="py-10 text-center text-sm text-muted">{t('music.noMatch')}</p>}
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
                      {sort.option.key === 'added' ? t('fact.added', { date: day(a.addedAt) }) : t('music.releases', { count: a.albums })}
                      {a.disambiguation && ` · ${a.disambiguation}`}
                    </span>
                  </span>
                  {a.monitored ? a.wantedAlbums > 0 ? <Badge tone="gold">{t('music.wanted', { count: a.wantedAlbums })}</Badge> : <Badge>{t('music.nothingWanted')}</Badge> : <Badge>{t('common.paused')}</Badge>}
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  )
}

// QualityPicker says which of FLAC and MP3 Box Office looks for.
function QualityPicker() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const set = useQuery({ queryKey: ['music-settings'], queryFn: () => get<MusicSettings>('/music/settings') })
  const save = useMutation({
    mutationFn: (quality: string) => put<MusicSettings>('/music/settings', { quality }),
    onSuccess: (v) => qc.setQueryData(['music-settings'], v),
  })
  if (!set.data) return null
  return (
    <div className="mb-6 max-w-md">
      <Field label={t('music.quality')} hint={t('music.qualityHint')}>
        {(id) => (
          <Select id={id} value={set.data.quality} onChange={(q) => save.mutate(q)} options={set.data.qualities.map((q) => ({ value: q, label: t(`music.qualities.${q}`) }))} />
        )}
      </Field>
      <ErrorNote error={save.error} />
    </div>
  )
}

function describe(a: LookupArtist): string {
  const years = a.began ? `${a.began.slice(0, 4)}${a.ended ? `–${a.ended.slice(0, 4)}` : ''}` : ''
  return [a.type, a.country, years, a.disambiguation].filter(Boolean).join(' · ')
}

// AddArtist finds an artist on MusicBrainz and adds them.
function AddArtist({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const [term, setTerm] = useState('')
  const [q, setQ] = useState('')
  const [picked, setPicked] = useState<LookupArtist | null>(null)
  useEffect(() => {
    const timer = setTimeout(() => setQ(term.trim()), 400)
    return () => clearTimeout(timer)
  }, [term])
  const results = useQuery({ queryKey: ['lookup', 'artists', q], queryFn: () => get<LookupArtist[]>(`/lookup/artists?q=${encodeURIComponent(q)}`), enabled: q.length > 1 })
  return (
    <Modal title={picked ? t('add.title', { title: picked.name }) : t('music.addAn')} onClose={onClose} wide>
      {picked ? (
        <AddOptions artist={picked} onBack={() => setPicked(null)} onDone={onClose} />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="relative">
            <Search className="pointer-events-none absolute start-3 top-2.5 size-4 text-muted" aria-hidden />
            <Input autoFocus className="ps-9" placeholder={t('music.placeholder')} value={term} onChange={(e) => setTerm(e.target.value)} />
          </div>
          {results.isFetching && <Spinner label={t('music.looking')} />}
          <ErrorNote error={results.error} />
          {results.data?.length === 0 && <p className="text-sm text-muted">{t('music.nobody')}</p>}
          <ul className="flex flex-col divide-y divide-line">
            {results.data?.map((a) => (
              <li key={a.mbid} className="flex items-center gap-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="font-display font-semibold">{a.name}</div>
                  <p className="truncate text-sm text-muted">{describe(a)}</p>
                </div>
                {a.id ? (
                  <Link className="shrink-0 text-sm font-medium text-marquee" to={`/music/${a.id}`} onClick={onClose}>
                    {t('add.onList')}
                  </Link>
                ) : (
                  <Button onClick={() => setPicked(a)}>{t('add.pick')}</Button>
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
  Album: 'albumTypes.Album',
  EP: 'albumTypes.EP',
  Single: 'albumTypes.Single',
  Live: 'albumTypes.Live',
  Compilation: 'albumTypes.Compilation',
  Soundtrack: 'albumTypes.Soundtrack',
  Remix: 'albumTypes.Remix',
  Other: 'albumTypes.Other',
}

export function useAlbumTypes() {
  return useQuery({ queryKey: ['albumtypes'], queryFn: () => get<AlbumTypes>('/artists/albumtypes'), staleTime: Infinity })
}

// AlbumTypePicker ticks the kinds of album an artist is watched for.
export function AlbumTypePicker({ value, onChange, disabled }: { value: string[]; onChange: (v: string[]) => void; disabled?: boolean }) {
  const { t } = useTranslation()
  const types = useAlbumTypes()
  return (
    <div className="flex flex-wrap gap-2" role="group" aria-label={t('music.kinds')}>
      {types.data?.types.map((type) => {
        const on = value.includes(type)
        return (
          <button
            key={type}
            type="button"
            disabled={disabled}
            aria-pressed={on}
            onClick={() => onChange(on ? value.filter((v) => v !== type) : [...value, type])}
            className={`rounded-full border px-3 py-1.5 text-sm sm:py-1 ${on ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:text-ink'}`}
          >
            {albumTypeLabels[type] ? t(albumTypeLabels[type]) : type}
          </button>
        )
      })}
    </div>
  )
}

function AddOptions({ artist, onBack, onDone }: { artist: LookupArtist; onBack: () => void; onDone: () => void }) {
  const { t } = useTranslation()
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
        <Trans i18nKey="music.noRoot" components={{ link: <Link to="/settings" className="font-medium text-marquee" onClick={onDone} /> }} />
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
      <Field label={t('add.root')}>{(id) => <Select id={id} value={root} onChange={setRoot} options={folders.data.map((f) => ({ value: f.path, label: f.path }))} />}</Field>
      <Field label={t('music.which')} hint={t('music.whichHint')}>
        {() => <AlbumTypePicker value={picked} onChange={setAlbumTypes} />}
      </Field>
      <Toggle checked={monitored} onChange={setMonitored} label={t('music.watch')} />
      <p className="text-sm text-muted">{t('music.lookup')}</p>
      <ErrorNote error={error} />
      <div className="flex justify-between">
        <Button variant="ghost" onClick={onBack}>
          {t('common.back')}
        </Button>
        <Button variant="primary" busy={busy} disabled={picked.length === 0 && monitored} onClick={add}>
          {t('music.add')}
        </Button>
      </div>
    </div>
  )
}
