import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { ArrowUpCircle, ChevronDown, ListChecks, RefreshCw, Search, Trash2 } from 'lucide-react'
import { AutoUpgradeField } from '../AutoUpgrade'
import { LockedNote, ReportedNote } from '../Bulk'
import { Releases } from '../Releases'
import { DeleteFiles, RemoveTitle } from '../RemoveFiles'
import { get, patch, post, type Episode, type Profile, type Reported, type Series } from '../api'
import { bytes, day, episodeCode } from '../format'
import { qualityName, useProfiles, useQualities } from '../hooks'
import { pictureText } from './QualityCheck'
import { Badge, Button, Card, ErrorNote, Field, Poster, Select, Spinner, Toggle } from '../ui'

type Detail = { series: Series; episodes: Episode[]; reported?: Reported[] }

export function ShowDetail() {
  const { t } = useTranslation()
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const nav = useNavigate()
  const key = ['show', id]
  const show = useQuery({ queryKey: key, queryFn: () => get<Detail>(`/series/${id}`) })
  const profiles = useProfiles()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [note, setNote] = useState('')
  const set = (d: Detail) => {
    qc.setQueryData(key, d)
    qc.invalidateQueries({ queryKey: ['series'] })
  }
  const edit = useMutation({ mutationFn: (body: Partial<Pick<Series, 'monitored' | 'profileId' | 'autoUpgrade'>>) => patch<Detail>(`/series/${id}`, body), onSuccess: set })
  const season = useMutation({ mutationFn: ({ n, monitored }: { n: number; monitored: boolean }) => patch<Detail>(`/series/${id}/seasons/${n}`, { monitored }), onSuccess: set })
  const action = useMutation({
    mutationFn: (what: 'search' | 'refresh') => post(`/series/${id}/${what}`),
    onSuccess: (_, what) => {
      setNote(what === 'search' ? t('show.lookingNow') : t('show.refreshing'))
      setTimeout(() => qc.invalidateQueries({ queryKey: key }), 3000)
    },
  })
  const removed = () => {
    qc.invalidateQueries({ queryKey: ['series'] })
    nav('/shows')
  }

  if (show.isPending) return <Spinner />
  if (show.isError) return <ErrorNote error={show.error} />
  const { series: s, episodes } = show.data
  const seasons = [...s.seasons].sort((a, b) => b.number - a.number)
  const locked = !!profiles.data?.find((p) => p.id === s.profileId)?.locked

  return (
    <div>
      {s.backdrop && (
        <div className="-mx-4 -mt-8 mb-6 h-56 md:-mx-8 overflow-hidden sm:h-72">
          <img src={s.backdrop} alt="" className="size-full object-cover opacity-80 [mask-image:linear-gradient(to_bottom,black_40%,transparent)]" />
        </div>
      )}
      <div className="flex flex-col gap-8 md:flex-row">
        <Poster src={s.poster} title={s.title} className="w-48 shrink-0 self-start shadow-lg" />
        <div className="flex-1">
          <h1 className="font-display text-4xl font-bold tracking-tight">
            {s.title} {s.year ? <span className="font-normal text-muted">{s.year}</span> : null}
          </h1>
          <div className="mt-2 flex flex-wrap gap-2">
            {s.network && <Badge>{s.network}</Badge>}
            {s.status && <Badge>{s.status}</Badge>}
            <Badge tone={s.fileCount === s.episodeCount && s.episodeCount > 0 ? 'ok' : 'gold'}>
              {t('shows.progress', { have: s.fileCount, count: s.episodeCount })}
            </Badge>
          </div>
          {s.overview && <p className="mt-4 max-w-3xl leading-relaxed">{s.overview}</p>}
          {locked && <LockedNote />}
          {show.data.reported?.map((r) => {
            const e = episodes.find((x) => x.id === r.ownerId)
            return <ReportedNote key={r.ownerId} reported={r} episode={e ? episodeCode(e.season, e.number) : undefined} />
          })}
          <div className="mt-6 flex flex-wrap gap-2">
            {!locked && (
              <Button variant="primary" busy={action.isPending && action.variables === 'search'} onClick={() => action.mutate('search')}>
                <Search className="size-4" aria-hidden /> {t('show.lookWanted')}
              </Button>
            )}
            <Button busy={action.isPending && action.variables === 'refresh'} onClick={() => action.mutate('refresh')}>
              <RefreshCw className="size-4" aria-hidden /> {t('title.refresh')}
            </Button>
            <Button variant="danger" onClick={() => setConfirmDelete(true)}>
              <Trash2 className="size-4" aria-hidden /> {t('title.remove')}
            </Button>
          </div>
          {note && <p className="mt-3 text-sm text-muted">{note}</p>}
          <ErrorNote error={action.error || edit.error || season.error} />
        </div>
      </div>

      <Card className="mt-8">
        <div className="grid gap-4 sm:grid-cols-2">
          <Toggle checked={s.monitored} onChange={(v) => edit.mutate({ monitored: v })} label={t('show.watchNew')} />
          <Field label={t('add.profile')}>
            {(fid) => <Select id={fid} value={s.profileId} onChange={(v) => edit.mutate({ profileId: Number(v) })} options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))} />}
          </Field>
          {!locked && <AutoUpgradeField value={s.autoUpgrade} onChange={(v) => edit.mutate({ autoUpgrade: v as Series['autoUpgrade'] })} upgradeOK={!!profiles.data?.find((p) => p.id === s.profileId)?.upgrade} />}
        </div>
        <div className="mt-3 text-sm">
          <span className="text-muted">{t('title.folder')} </span>
          <code className="break-all">{s.path}</code>
        </div>
      </Card>

      <div className="mt-6 flex flex-col gap-3">
        {seasons.map((se) => (
          <SeasonBlock
            key={se.number}
            seriesId={s.id}
            profile={profiles.data?.find((p) => p.id === s.profileId)}
            number={se.number}
            monitored={se.monitored}
            episodes={episodes.filter((e) => e.season === se.number)}
            onMonitor={(v) => season.mutate({ n: se.number, monitored: v })}
            onEpisode={(e) => set({ ...show.data, series: s, episodes: episodes.map((x) => (x.id === e.id ? e : x)) })}
            onShow={set}
            showTitle={s.title}
            defaultOpen={se.number === seasons[0]?.number}
          />
        ))}
      </div>

      {confirmDelete && <RemoveTitle kind="series" id={s.id} title={s.title} onClose={() => setConfirmDelete(false)} onRemoved={removed} />}
    </div>
  )
}

function SeasonBlock({
  seriesId,
  profile,
  number,
  monitored,
  episodes,
  onMonitor,
  onEpisode,
  onShow,
  showTitle,
  defaultOpen,
}: {
  seriesId: number
  profile?: Profile
  number: number
  monitored: boolean
  episodes: Episode[]
  onMonitor: (v: boolean) => void
  onEpisode: (e: Episode) => void
  onShow: (d: Detail) => void
  showTitle: string
  defaultOpen: boolean
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(defaultOpen)
  const qualities = useQualities()
  const have = episodes.filter((e) => e.file).length
  const toggle = useMutation({ mutationFn: (e: Episode) => patch<Episode>(`/episodes/${e.id}`, { monitored: !e.monitored }), onSuccess: onEpisode })
  const search = useMutation({ mutationFn: (e: Episode) => post(`/episodes/${e.id}/search`) })
  const [releases, setReleases] = useState<Episode | 'season' | null>(null)
  const [deleting, setDeleting] = useState<Episode | 'season' | null>(null)
  const seasonName = number === 0 ? t('show.specials') : t('show.season', { number })
  const now = Date.now()
  const rank = (q: number) => qualities.data?.findIndex((x) => x.id === q) ?? -1
  // What the quality check found inside an episode's file, on hover.
  const fileTitle = (f: NonNullable<Episode['file']>) =>
    [
      f.media && !f.media.error ? pictureText(f.media, (n) => t('qualityCheck.bits', { bits: n })) : '',
      f.guessed !== undefined ? t('qualityCheck.correctedFrom', { was: qualityName(qualities.data, f.guessed) }) : '',
    ]
      .filter(Boolean)
      .join('\n') || undefined
  // Below the profile's good-enough quality, with upgrades on.
  const wantsUpgrade = (e: Episode) => !!(e.file && profile?.upgrade && rank(e.file.quality) < rank(profile.cutoff))
  const anyAired = episodes.some((e) => e.airDate && new Date(e.airDate).getTime() <= now)
  const upgrades = episodes.filter(wantsUpgrade).length
  const locked = !!profile?.locked
  return (
    <Card className="p-0">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3">
        <button className="flex min-w-0 flex-1 basis-full flex-wrap items-center gap-x-3 text-start sm:basis-0" onClick={() => setOpen(!open)} aria-expanded={open}>
          <ChevronDown className={`size-4 shrink-0 transition ${open ? '' : '-rotate-90'}`} aria-hidden />
          <span className="whitespace-nowrap font-display text-lg font-semibold">{seasonName}</span>
          <span className="text-sm text-muted">
            {t('show.seasonHave', { have, count: episodes.length })}
            {upgrades > 0 && t('show.toUpgrade', { count: upgrades })}
          </span>
        </button>
        {anyAired && !locked && (
          <Button variant="ghost" onClick={() => setReleases('season')}>
            <ListChecks className="size-4" aria-hidden /> {t('title.choose')}
          </Button>
        )}
        {have > 0 && !locked && (
          <Button variant="ghost" onClick={() => setDeleting('season')} aria-label={t('show.deleteSeason', { season: seasonName })} title={t('show.deleteItsFiles')}>
            <Trash2 className="size-4" aria-hidden /> <span className="sm:hidden">{t('show.deleteFiles')}</span>
          </Button>
        )}
        <Toggle checked={monitored} onChange={onMonitor} label={<span className="text-muted">{t('show.watch')}</span>} />
      </div>
      {open && (
        <ul className="divide-y divide-line border-t border-line">
          {[...episodes]
            .sort((a, b) => b.number - a.number)
            .map((e) => {
              const aired = e.airDate && new Date(e.airDate).getTime() <= now
              const upgrade = wantsUpgrade(e)
              const looking = search.isSuccess && search.variables?.id === e.id
              return (
                <li key={e.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-2.5 text-sm">
                  <input type="checkbox" checked={e.monitored} onChange={() => toggle.mutate(e)} aria-label={t('show.watchEpisode', { episode: episodeCode(e.season, e.number) })} className="accent-[var(--marquee)]" />
                  <span className="w-16 font-mono text-xs text-muted">{episodeCode(e.season, e.number)}</span>
                  <span className="min-w-0 flex-1 basis-40 truncate">{e.title || t('show.tba')}</span>
                  <span className="w-28 text-muted">{day(e.airDate)}</span>
                  <span className="ms-auto flex flex-wrap items-center justify-end gap-2">
                    {e.file ? (
                      <span title={fileTitle(e.file)}>
                        <Badge tone={upgrade ? 'gold' : 'ok'}>
                          {upgrade && <ArrowUpCircle className="size-3" aria-label={t('show.upgradeWanted')} />}
                          {qualityName(qualities.data, e.file.quality)} · {bytes(e.file.size)}
                        </Badge>
                      </span>
                    ) : aired ? (
                      !e.monitored && <Badge>{t('show.skipped')}</Badge>
                    ) : (
                      <Badge tone="gold">{t('show.coming')}</Badge>
                    )}
                    {!locked && aired && e.monitored && (!e.file || upgrade) && (
                      <button
                        className="rounded px-1.5 py-0.5 text-xs text-marquee hover:bg-line"
                        onClick={() => search.mutate(e)}
                        title={e.file ? t('show.betterCopy') : t('show.grabBest')}
                      >
                        {looking ? t('common.looking') : e.file ? t('show.lookUpgrade') : t('title.lookFor')}
                      </button>
                    )}
                    {aired && !locked && (
                      <button
                        className="flex items-center gap-1 rounded px-1.5 py-0.5 text-xs text-muted hover:bg-line hover:text-ink"
                        onClick={() => setReleases(e)}
                        aria-label={t('show.chooseFor', { episode: episodeCode(e.season, e.number) })}
                      >
                        <ListChecks className="size-3.5" aria-hidden /> {t('show.choose')}
                      </button>
                    )}
                    {e.file && !locked && (
                      <button
                        className="rounded p-1 text-muted hover:bg-line hover:text-bad"
                        onClick={() => setDeleting(e)}
                        aria-label={t('show.deleteEpisode', { episode: episodeCode(e.season, e.number) })}
                        title={t('show.deleteItsFile')}
                      >
                        <Trash2 className="size-3.5" aria-hidden />
                      </button>
                    )}
                  </span>
                </li>
              )
            })}
        </ul>
      )}
      {releases === 'season' && (
        <Releases path={`/series/${seriesId}/seasons/${number}/releases`} title={seasonName} onClose={() => setReleases(null)} />
      )}
      {deleting === 'season' && (
        <DeleteFiles<Detail>
          path={`/series/${seriesId}/seasons/${number}/files`}
          title={t('show.seasonOf', { show: showTitle, season: seasonName })}
          what="season"
          onClose={() => setDeleting(null)}
          onDone={(d) => {
            onShow(d)
            setDeleting(null)
          }}
        />
      )}
      {deleting && deleting !== 'season' && (
        <DeleteFiles<Episode>
          path={`/episodes/${deleting.id}/file`}
          title={t('show.seasonOf', { show: showTitle, season: episodeCode(deleting.season, deleting.number) })}
          what="episode"
          onClose={() => setDeleting(null)}
          onDone={(e) => {
            onEpisode(e)
            setDeleting(null)
          }}
        />
      )}
      {releases && releases !== 'season' && (
        <Releases path={`/episodes/${releases.id}/releases`} title={episodeCode(releases.season, releases.number)} onClose={() => setReleases(null)} />
      )}
    </Card>
  )
}
