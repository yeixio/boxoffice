import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { Clapperboard, ListFilter, RefreshCw, Search, Trash2 } from 'lucide-react'
import { Releases } from '../Releases'
import { Editions } from '../Editions'
import { DeleteFiles, RemoveTitle } from '../RemoveFiles'
import { AutoUpgradeField } from '../AutoUpgrade'
import { SecondCopy, type SecondCopyEdit } from '../SecondCopy'
import { LockedNote, ReportedNote } from '../Bulk'
import { get, patch, post, type MediaFile, type Movie } from '../api'
import { bytes, day, runtime } from '../format'
import { qualityName, useProfiles, useQualities } from '../hooks'
import { pictureText } from './QualityCheck'
import { FindSubtitles, SubtitleBadges } from './Subtitles'
import { Backdrop, Badge, Button, Card, ErrorNote, Field, Poster, Select, Spinner, Toggle } from '../ui'

// FileFacts are what's known of one copy of the movie: its cut, path,
// quality, picture, length, size and when it came.
function FileFacts({ f }: { f: MediaFile }) {
  const { t } = useTranslation()
  const qualities = useQualities()
  const length = f.media && !f.media.error && f.media.duration ? Math.round(f.media.duration / 60) : 0
  return (
    <>
      {f.edition && (
        <>
          <dt className="text-muted">{t('title.edition')}</dt>
          <dd>
            <Badge tone="gold">{f.edition}</Badge>
            {f.editionFrom && <div className="text-xs text-muted">{t(`title.editionFrom.${f.editionFrom}`)}</div>}
          </dd>
        </>
      )}
      <dt className="text-muted">{t('title.path')}</dt>
      <dd className="break-all font-mono text-xs">{f.path}</dd>
      <dt className="text-muted">{t('title.quality')}</dt>
      <dd>
        {qualityName(qualities.data, f.quality)}
        {f.guessed !== undefined && <div className="text-xs text-muted">{t('qualityCheck.correctedFrom', { was: qualityName(qualities.data, f.guessed) })}</div>}
      </dd>
      {f.media && !f.media.error && (
        <>
          <dt className="text-muted">{t('qualityCheck.picture')}</dt>
          <dd>{pictureText(f.media, (n) => t('qualityCheck.bits', { bits: n }))}</dd>
        </>
      )}
      {length > 0 && (
        <>
          <dt className="text-muted">{t('title.runs')}</dt>
          <dd>{runtime(length)}</dd>
        </>
      )}
      {f.subtitles && f.subtitles.length > 0 && (
        <>
          <dt className="text-muted">{t('subtitles.title')}</dt>
          <dd>
            <SubtitleBadges subs={f.subtitles} />
          </dd>
        </>
      )}
      <dt className="text-muted">{t('title.size')}</dt>
      <dd>{bytes(f.size)}</dd>
      <dt className="text-muted">{t('title.added')}</dt>
      <dd>{day(f.addedAt)}</dd>
    </>
  )
}

export function MovieDetail() {
  const { t } = useTranslation()
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const nav = useNavigate()
  const movie = useQuery({ queryKey: ['movie', id], queryFn: () => get<Movie>(`/movies/${id}`) })
  const profiles = useProfiles()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [note, setNote] = useState('')
  const [releases, setReleases] = useState(false)
  const [editions, setEditions] = useState(false)
  const [deleteFile, setDeleteFile] = useState(false)
  const [deleteSecond, setDeleteSecond] = useState(false)

  const edit = useMutation({
    mutationFn: (body: Partial<Pick<Movie, 'monitored' | 'profileId' | 'autoUpgrade'>> & SecondCopyEdit) => patch<Movie>(`/movies/${id}`, body),
    onSuccess: (m) => {
      // The edit's answer is the movie alone; what was reported stays.
      qc.setQueryData(['movie', id], (old: Movie | undefined) => ({ ...m, reported: old?.reported }))
      qc.invalidateQueries({ queryKey: ['movies'] })
    },
  })
  const action = useMutation({
    mutationFn: (what: 'search' | 'refresh') => post(`/movies/${id}/${what}`),
    onSuccess: (_, what) => {
      setNote(what === 'search' ? t('title.lookingNow') : t('title.refreshing'))
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
    [t('title.inCinemas'), m.inCinemas],
    [t('title.digital'), m.digitalRelease],
    [t('title.onDisc'), m.physicalRelease],
  ].filter(([, d]) => d) as [string, string][]

  return (
    <div>
      <Backdrop src={m.backdrop} />
      {/* On a phone the poster is small and sits beside the title, and the
          buttons come before the overview, so they're on the first screen. */}
      <div className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-4 gap-y-4 md:grid-cols-[12rem_minmax(0,1fr)] md:gap-x-8 md:gap-y-0">
        <Poster src={m.poster} title={m.title} className="w-full self-start shadow-lg md:row-span-2" />
        <div className="self-end md:self-start">
          <h1 className="font-display text-2xl font-bold tracking-tight sm:text-4xl">
            {m.title} {m.year ? <span className="font-normal text-muted">{m.year}</span> : null}
          </h1>
          <div className="mt-2 flex flex-wrap gap-2">
            {m.file ? <Badge tone="ok">{t('common.onDisk')}</Badge> : m.monitored ? <Badge tone="gold">{t('common.wanted')}</Badge> : <Badge>{t('filter.notWatched')}</Badge>}
            {m.runtime ? <Badge>{runtime(m.runtime)}</Badge> : null}
            {m.status && <Badge>{m.status}</Badge>}
          </div>
        </div>
        <div className="col-span-2 flex flex-col md:col-span-1 md:col-start-2">
          {m.overview && <p className="max-w-3xl md:mt-4 leading-relaxed">{m.overview}</p>}
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
          {m.reported && <ReportedNote reported={m.reported} />}
          <div className="flex flex-wrap gap-2 max-md:order-first max-md:mb-4 md:mt-6">
            {!locked && (
              <>
                <Button variant="primary" busy={action.isPending && action.variables === 'search'} onClick={() => action.mutate('search')}>
                  <Search className="size-4" aria-hidden /> {t('title.lookFor')}
                </Button>
                <Button onClick={() => setReleases(true)}>
                  <ListFilter className="size-4" aria-hidden /> {t('title.choose')}
                </Button>
                <Button onClick={() => setEditions(true)}>
                  <Clapperboard className="size-4" aria-hidden /> {t('editions.look')}
                </Button>
              </>
            )}
            <Button busy={action.isPending && action.variables === 'refresh'} onClick={() => action.mutate('refresh')}>
              <RefreshCw className="size-4" aria-hidden /> {t('title.refresh')}
            </Button>
            <Button variant="danger" onClick={() => setConfirmDelete(true)}>
              <Trash2 className="size-4" aria-hidden /> {t('title.remove')}
            </Button>
          </div>
          {note && <p className="mt-3 text-sm text-muted">{note}</p>}
          {editions && <Editions id={id} title={m.title} hasFile={!!m.file} onClose={() => setEditions(false)} />}
          {releases && <Releases path={`/movies/${id}/releases`} title={m.title} onClose={() => setReleases(false)} />}
          <ErrorNote error={action.error || edit.error} />
        </div>
      </div>

      <div className="mt-8 grid gap-6 lg:grid-cols-2">
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">{t('title.handles')}</h2>
          <div className="flex flex-col gap-4">
            <Toggle checked={m.monitored} onChange={(v) => edit.mutate({ monitored: v })} label={t('title.watch')} />
            <Field label={t('add.profile')}>
              {(fid) => <Select id={fid} value={m.profileId} onChange={(v) => edit.mutate({ profileId: Number(v) })} options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))} />}
            </Field>
            {!locked && <AutoUpgradeField value={m.autoUpgrade} onChange={(v) => edit.mutate({ autoUpgrade: v as Movie['autoUpgrade'] })} upgradeOK={!!profiles.data?.find((p) => p.id === m.profileId)?.upgrade} />}
            <div className="text-sm">
              <div className="text-muted">{t('title.folder')}</div>
              <code className="break-all">{m.path}</code>
            </div>
          </div>
        </Card>
        <Card>
          <h2 className="mb-4 font-display text-lg font-semibold">{t('title.file')}</h2>
          {m.file ? (
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
              <FileFacts f={m.file} />
              {!locked && (
                <dd className="col-span-2 mt-2 flex flex-wrap items-start gap-2">
                  <Button variant="danger" onClick={() => setDeleteFile(true)}>
                    <Trash2 className="size-4" aria-hidden /> {t('title.deleteFile')}
                  </Button>
                  {[m.file, ...(m.editions ?? [])].some((f) => f.subtitles?.some((s) => s.state === 'missing' || s.state === 'waiting' || s.state === 'pending')) && (
                    <FindSubtitles path={`/movies/${m.id}/subtitles`} onFound={() => qc.invalidateQueries({ queryKey: ['movie', id] })} />
                  )}
                </dd>
              )}
            </dl>
          ) : (
            <p className="text-sm text-muted">{t('title.notOnDisk')}</p>
          )}
          {/* Other cuts kept next to the copy: not duplicates, and never
              searched for or upgraded on their own. */}
          {m.wantedEdition && <p className="mt-4 text-xs text-muted">{t('title.wantedEdition', { edition: m.wantedEdition })}</p>}
          {(m.editions?.length ?? 0) > 0 && (
            <>
              <h3 className="mb-1 mt-6 font-display font-semibold">{t('title.otherEditions')}</h3>
              <p className="mb-3 text-xs text-muted">{t('title.otherEditionsSub')}</p>
              <ul className="flex flex-col divide-y divide-line">
                {m.editions!.map((f) => (
                  <li key={f.id} className="py-3 first:pt-0">
                    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
                      <FileFacts f={f} />
                    </dl>
                  </li>
                ))}
              </ul>
            </>
          )}
        </Card>
        <SecondCopy kind="movie" title={m} have={!!m.second} profiles={profiles.data ?? []} locked={locked} onChange={(e) => edit.mutate(e)} onDelete={() => setDeleteSecond(true)} facts={m.second && <FileFacts f={m.second} />} />
      </div>

      {confirmDelete && <RemoveTitle kind="movie" id={m.id} title={m.title} onClose={() => setConfirmDelete(false)} onRemoved={removed} />}
      {deleteFile && (
        <DeleteFiles<Movie>
          path={`/movies/${m.id}/file`}
          title={t('title.filesOf', { title: m.title })}
          what="movie"
          onClose={() => setDeleteFile(false)}
          onDone={(next) => {
            qc.setQueryData(['movie', id], next)
            qc.invalidateQueries({ queryKey: ['movies'] })
            setDeleteFile(false)
          }}
        />
      )}
      {deleteSecond && (
        <DeleteFiles<Movie>
          path={`/movies/${m.id}/second`}
          title={t('secondCopy.filesOf', { title: m.title })}
          what="second"
          onClose={() => setDeleteSecond(false)}
          onDone={(next) => {
            qc.setQueryData(['movie', id], next)
            qc.invalidateQueries({ queryKey: ['movies'] })
            setDeleteSecond(false)
          }}
        />
      )}
    </div>
  )
}
