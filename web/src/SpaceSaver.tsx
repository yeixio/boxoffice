import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { get, put } from './api'
import { bytes, episodeCode } from './format'
import { Button, Card, ErrorNote, Field, Modal, Select, Spinner, Toggle } from './ui'

// How many episodes ahead the picker offers; a show set to another number
// through the API keeps it.
const choices = [3, 5, 10, 20]

// What Matinee has said about a show with space saver on.
// allSeason and allEpisode are the latest episode everyone watching has
// finished.
export type SpaceSaverWatched = { reported: boolean; season: number; episode: number; allSeason?: number; allEpisode?: number }

// What turning on removing watched episodes would move to the recycle
// folder now, for one show or for every show that follows Settings.
export type WatchedRemoval = {
  shows: number
  episodes: number
  files: number
  size: number
  reported: boolean
  stale: boolean
  problem?: string
  keepDays: number
  dailyCap: number
}

// What the person was shown, for the server to hold the removal to.
export type WatchedShown = { expectFiles: number; expectSize: number }

// SpaceSaverField picks a show's space saver: fetch only the next few
// episodes past the latest one anyone has watched, or every wanted one.
export function SpaceSaverField({ value, onChange, watched }: { value: number; onChange: (n: number) => void; watched?: SpaceSaverWatched }) {
  const { t } = useTranslation()
  const options = [...new Set([...choices, ...(value > 0 ? [value] : [])])].sort((a, b) => a - b)
  // Where the show stands, once it's on.
  const state = !watched ? '' : !watched.reported ? t('spaceSaver.noReport') : watched.season > 0 ? t('spaceSaver.upTo', { episode: episodeCode(watched.season, watched.episode) }) : t('spaceSaver.notStarted')
  return (
    <Field label={t('spaceSaver.label')} hint={[t('spaceSaver.hint'), state].filter(Boolean).join(' ')}>
      {(id) => (
        <Select
          id={id}
          value={value}
          onChange={(v) => onChange(Number(v))}
          options={[{ value: 0, label: t('spaceSaver.off') }, ...options.map((n) => ({ value: n, label: t('spaceSaver.next', { n }) }))]}
        />
      )}
    </Field>
  )
}

// ConfirmRemoveWatched says what turning on removing watched episodes
// moves to the recycle folder now, and turns it on only for that: the
// server refuses if the disk has changed since.
export function ConfirmRemoveWatched({ path, title, busy, error, onClose, onConfirm }: { path: string; title?: string; busy: boolean; error: unknown; onClose: () => void; onConfirm: (shown: WatchedShown) => void }) {
  const { t } = useTranslation()
  const plan = useQuery({ queryKey: ['watched-removal', path], queryFn: () => get<WatchedRemoval>(path), gcTime: 0, staleTime: 0 })
  const p = plan.data
  return (
    <Modal title={t('spaceSaver.remove.confirmTitle')} onClose={onClose}>
      {plan.isPending ? (
        <Spinner />
      ) : !p ? (
        <ErrorNote error={plan.error} />
      ) : (
        <div className="flex flex-col gap-3 text-sm">
          {title && <p className="font-medium">{title}</p>}
          {!p.reported || p.stale ? (
            <p>{t('spaceSaver.remove.stale')}</p>
          ) : p.problem ? (
            <p>{p.problem}</p>
          ) : p.files === 0 ? (
            <p>{t('spaceSaver.remove.nothingNow')}</p>
          ) : (
            <p className="rounded-lg border border-bad/40 bg-bad/5 p-3">
              {!title && `${t('spaceSaver.remove.shows', { shows: p.shows })} `}
              {t('spaceSaver.remove.goesNow', { episodes: p.episodes, files: p.files, size: bytes(p.size) })}
            </p>
          )}
          <p className="text-muted">{t('spaceSaver.remove.later', { n: p.dailyCap })}</p>
          <p className="text-muted">{p.keepDays > 0 ? t('spaceSaver.remove.emptied', { days: p.keepDays }) : t('spaceSaver.remove.kept')}</p>
        </div>
      )}
      <ErrorNote error={error} />
      <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button variant="ghost" onClick={onClose}>
          {t('common.cancel')}
        </Button>
        <Button variant="danger" busy={busy} disabled={!p} onClick={() => p && onConfirm({ expectFiles: p.files, expectSize: p.size })}>
          {t('common.turnOn')}
        </Button>
      </div>
    </Modal>
  )
}

// RemoveWatchedField picks whether a show's watched episodes are removed:
// as Settings says, always, or never. onTurnOn is asked instead of
// onChange when the pick would start removing them, so the page can show
// what goes first.
export function RemoveWatchedField({ value, settings, watched, onChange, onTurnOn }: { value: string; settings: boolean; watched?: SpaceSaverWatched; onChange: (v: string) => void; onTurnOn: (v: string) => void }) {
  const { t } = useTranslation()
  const on = (v: string) => v === 'on' || (v === '' && settings)
  const state = !on(value) || !watched ? '' : !watched.reported ? t('spaceSaver.remove.stale') : watched.allSeason ? t('spaceSaver.remove.upTo', { episode: episodeCode(watched.allSeason, watched.allEpisode ?? 0) }) : t('spaceSaver.remove.notYet')
  return (
    <Field label={t('spaceSaver.remove.label')} hint={[t('spaceSaver.remove.hint'), state].filter(Boolean).join(' ')}>
      {(id) => (
        <Select
          id={id}
          value={value}
          onChange={(v) => (on(v) && !on(value) ? onTurnOn(v) : onChange(v))}
          options={[
            { value: '', label: settings ? t('autoUpgrade.followOn') : t('autoUpgrade.followOff') },
            { value: 'on', label: t('spaceSaver.remove.on') },
            { value: 'off', label: t('spaceSaver.remove.off') },
          ]}
        />
      )}
    </Field>
  )
}

type SpaceSaverSettings = { removeWatched: boolean; showsOn: number; showsOff: number; reported: boolean; stale: boolean; dailyCap: number }

// SpaceSaverCard is Settings → Library → Space saver: whether watched
// episodes are removed from every show that doesn't have its own choice.
export function SpaceSaverCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const saver = useQuery({ queryKey: ['space-saver'], queryFn: () => get<SpaceSaverSettings>('/space-saver') })
  const [confirm, setConfirm] = useState(false)
  const save = useMutation({
    mutationFn: (body: { removeWatched: boolean } & Partial<WatchedShown>) => put<SpaceSaverSettings>('/space-saver', body),
    onSuccess: (d) => {
      qc.setQueryData(['space-saver'], d)
      qc.invalidateQueries({ queryKey: ['show'] })
      qc.invalidateQueries({ queryKey: ['recycle'] })
      setConfirm(false)
    },
  })
  const d = saver.data
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('spaceSaver.label')}</h2>
      <p className="mb-4 text-sm text-muted">{t('spaceSaver.remove.allHint')}</p>
      {saver.isPending ? (
        <Spinner />
      ) : !d ? (
        <ErrorNote error={saver.error} />
      ) : (
        <div className="flex flex-col gap-3">
          <Toggle checked={d.removeWatched} disabled={save.isPending} onChange={(v) => (v ? setConfirm(true) : save.mutate({ removeWatched: false }))} label={t('spaceSaver.remove.all')} />
          {d.removeWatched && (!d.reported || d.stale) && <p className="text-sm text-muted">{t('spaceSaver.remove.stale')}</p>}
          {d.showsOn + d.showsOff > 0 && <p className="text-sm text-muted">{t('spaceSaver.remove.own', { on: d.showsOn, off: d.showsOff })}</p>}
          {!confirm && <ErrorNote error={save.error} />}
        </div>
      )}
      {confirm && (
        <ConfirmRemoveWatched
          path="/space-saver/removal"
          busy={save.isPending}
          error={save.error}
          onClose={() => {
            setConfirm(false)
            save.reset()
          }}
          onConfirm={(shown) => save.mutate({ removeWatched: true, ...shown })}
        />
      )}
    </Card>
  )
}
