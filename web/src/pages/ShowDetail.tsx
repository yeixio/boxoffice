import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { ArrowUpCircle, ChevronDown, ListChecks, RefreshCw, Search, Trash2 } from 'lucide-react'
import { Releases } from '../Releases'
import { del, get, patch, post, type Episode, type Profile, type Series } from '../api'
import { bytes, day, episodeCode } from '../format'
import { qualityName, useProfiles, useQualities } from '../hooks'
import { Badge, Button, Card, ErrorNote, Field, Modal, Poster, Select, Spinner, Toggle } from '../ui'

type Detail = { series: Series; episodes: Episode[] }

export function ShowDetail() {
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
  const edit = useMutation({ mutationFn: (body: Partial<Pick<Series, 'monitored' | 'profileId'>>) => patch<Detail>(`/series/${id}`, body), onSuccess: set })
  const season = useMutation({ mutationFn: ({ n, monitored }: { n: number; monitored: boolean }) => patch<Detail>(`/series/${id}/seasons/${n}`, { monitored }), onSuccess: set })
  const action = useMutation({
    mutationFn: (what: 'search' | 'refresh') => post(`/series/${id}/${what}`),
    onSuccess: (_, what) => {
      setNote(what === 'search' ? 'Looking for its wanted episodes; see Activity.' : 'Refreshing its details and episodes from TMDB.')
      setTimeout(() => qc.invalidateQueries({ queryKey: key }), 3000)
    },
  })
  const remove = useMutation({
    mutationFn: () => del(`/series/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['series'] })
      nav('/shows')
    },
  })

  if (show.isPending) return <Spinner />
  if (show.isError) return <ErrorNote error={show.error} />
  const { series: s, episodes } = show.data
  const seasons = [...s.seasons].sort((a, b) => b.number - a.number)

  return (
    <div>
      {s.backdrop && (
        <div className="-mx-4 -mt-8 mb-6 h-56 overflow-hidden sm:h-72">
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
              {s.fileCount} of {s.episodeCount} episodes on disk
            </Badge>
          </div>
          {s.overview && <p className="mt-4 max-w-3xl leading-relaxed">{s.overview}</p>}
          <div className="mt-6 flex flex-wrap gap-2">
            <Button variant="primary" busy={action.isPending && action.variables === 'search'} onClick={() => action.mutate('search')}>
              <Search className="size-4" aria-hidden /> Look for wanted episodes
            </Button>
            <Button busy={action.isPending && action.variables === 'refresh'} onClick={() => action.mutate('refresh')}>
              <RefreshCw className="size-4" aria-hidden /> Refresh details
            </Button>
            <Button variant="danger" onClick={() => setConfirmDelete(true)}>
              <Trash2 className="size-4" aria-hidden /> Remove
            </Button>
          </div>
          {note && <p className="mt-3 text-sm text-muted">{note}</p>}
          <ErrorNote error={action.error || edit.error || season.error} />
        </div>
      </div>

      <Card className="mt-8">
        <div className="grid gap-4 sm:grid-cols-2">
          <Toggle checked={s.monitored} onChange={(v) => edit.mutate({ monitored: v })} label="Watch for new episodes" />
          <Field label="Quality profile">
            {(fid) => <Select id={fid} value={s.profileId} onChange={(v) => edit.mutate({ profileId: Number(v) })} options={(profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))} />}
          </Field>
        </div>
        <div className="mt-3 text-sm">
          <span className="text-muted">Folder </span>
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
            onEpisode={(e) => set({ series: s, episodes: episodes.map((x) => (x.id === e.id ? e : x)) })}
            defaultOpen={se.number === seasons[0]?.number}
          />
        ))}
      </div>

      {confirmDelete && (
        <Modal title={`Remove ${s.title}?`} onClose={() => setConfirmDelete(false)}>
          <p className="text-sm">It comes off Box Office's list. Its files stay where they are on disk.</p>
          <ErrorNote error={remove.error} />
          <div className="mt-5 flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setConfirmDelete(false)}>
              Keep it
            </Button>
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              Remove
            </Button>
          </div>
        </Modal>
      )}
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
  defaultOpen,
}: {
  seriesId: number
  profile?: Profile
  number: number
  monitored: boolean
  episodes: Episode[]
  onMonitor: (v: boolean) => void
  onEpisode: (e: Episode) => void
  defaultOpen: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  const qualities = useQualities()
  const have = episodes.filter((e) => e.file).length
  const toggle = useMutation({ mutationFn: (e: Episode) => patch<Episode>(`/episodes/${e.id}`, { monitored: !e.monitored }), onSuccess: onEpisode })
  const search = useMutation({ mutationFn: (e: Episode) => post(`/episodes/${e.id}/search`) })
  const [releases, setReleases] = useState<Episode | 'season' | null>(null)
  const now = Date.now()
  const rank = (q: number) => qualities.data?.findIndex((x) => x.id === q) ?? -1
  // Below the profile's good-enough quality, with upgrades on.
  const wantsUpgrade = (e: Episode) => !!(e.file && profile?.upgrade && rank(e.file.quality) < rank(profile.cutoff))
  const anyAired = episodes.some((e) => e.airDate && new Date(e.airDate).getTime() <= now)
  const upgrades = episodes.filter(wantsUpgrade).length
  return (
    <Card className="p-0">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3">
        <button className="flex min-w-0 flex-1 items-center gap-3 text-left" onClick={() => setOpen(!open)} aria-expanded={open}>
          <ChevronDown className={`size-4 shrink-0 transition ${open ? '' : '-rotate-90'}`} aria-hidden />
          <span className="font-display text-lg font-semibold">{number === 0 ? 'Specials' : `Season ${number}`}</span>
          <span className="text-sm text-muted">
            {have} of {episodes.length} on disk
            {upgrades > 0 && `, ${upgrades} to upgrade`}
          </span>
        </button>
        {anyAired && (
          <Button variant="ghost" onClick={() => setReleases('season')}>
            <ListChecks className="size-4" aria-hidden /> Choose a release
          </Button>
        )}
        <Toggle checked={monitored} onChange={onMonitor} label={<span className="text-muted">Watch</span>} />
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
                  <input type="checkbox" checked={e.monitored} onChange={() => toggle.mutate(e)} aria-label={`Watch for ${episodeCode(e.season, e.number)}`} className="accent-[var(--marquee)]" />
                  <span className="w-16 font-mono text-xs text-muted">{episodeCode(e.season, e.number)}</span>
                  <span className="min-w-0 flex-1 basis-40 truncate">{e.title || 'To be announced'}</span>
                  <span className="w-28 text-muted">{day(e.airDate)}</span>
                  <span className="flex flex-wrap items-center justify-end gap-2">
                    {e.file ? (
                      <Badge tone={upgrade ? 'gold' : 'ok'}>
                        {upgrade && <ArrowUpCircle className="size-3" aria-label="Upgrade wanted" />}
                        {qualityName(qualities.data, e.file.quality)} · {bytes(e.file.size)}
                      </Badge>
                    ) : aired ? (
                      !e.monitored && <Badge>Skipped</Badge>
                    ) : (
                      <Badge tone="gold">Coming</Badge>
                    )}
                    {aired && e.monitored && (!e.file || upgrade) && (
                      <button
                        className="rounded px-1.5 py-0.5 text-xs text-marquee hover:bg-line"
                        onClick={() => search.mutate(e)}
                        title={e.file ? 'Look for a better copy and grab the best one' : 'Look for it and grab the best release'}
                      >
                        {looking ? 'Looking…' : e.file ? 'Look for an upgrade' : 'Look for it'}
                      </button>
                    )}
                    {aired && (
                      <button
                        className="flex items-center gap-1 rounded px-1.5 py-0.5 text-xs text-muted hover:bg-line hover:text-ink"
                        onClick={() => setReleases(e)}
                        aria-label={`Choose a release for ${episodeCode(e.season, e.number)}`}
                      >
                        <ListChecks className="size-3.5" aria-hidden /> Choose
                      </button>
                    )}
                  </span>
                </li>
              )
            })}
        </ul>
      )}
      {releases === 'season' && (
        <Releases path={`/series/${seriesId}/seasons/${number}/releases`} title={number === 0 ? 'Specials' : `Season ${number}`} onClose={() => setReleases(null)} />
      )}
      {releases && releases !== 'season' && (
        <Releases path={`/episodes/${releases.id}/releases`} title={episodeCode(releases.season, releases.number)} onClose={() => setReleases(null)} />
      )}
    </Card>
  )
}
