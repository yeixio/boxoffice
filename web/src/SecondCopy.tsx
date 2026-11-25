import { useTranslation } from 'react-i18next'
import { Trash2 } from 'lucide-react'
import type { ReactNode } from 'react'
import type { Movie, Profile } from './api'
import { useRootFolders } from './hooks'
import { Button, Card, Field, Select } from './ui'

// What a title's second copy can be changed to.
export type SecondCopyEdit = { secondProfileId?: number; secondRoot?: string }

// SecondCopy is a movie's second copy, or a show's second copies: another
// copy kept at a different quality (a 1080p one next to the 4K one, say),
// with its own quality profile, next to the first copy or in another
// library folder of the same kind. have says whether one is on disk;
// facts shows what's there (a movie's file, or how many of a show's
// episodes have one).
export function SecondCopy({
  kind,
  title: m,
  have,
  profiles,
  locked,
  onChange,
  onDelete,
  facts,
}: {
  kind: 'movie' | 'series'
  title: Pick<Movie, 'profileId' | 'rootFolder' | 'monitored' | 'secondProfileId' | 'secondRoot'>
  have: boolean
  profiles: Profile[]
  locked: boolean
  onChange: (e: SecondCopyEdit) => void
  onDelete: () => void
  facts?: ReactNode
}) {
  const { t } = useTranslation()
  const roots = useRootFolders(kind)
  const show = kind === 'series'
  const on = !!m.secondProfileId
  // Any profile but the first copy's, and never Do not touch.
  const choices = profiles.filter((p) => !p.locked && (p.id !== m.profileId || p.id === m.secondProfileId))
  const others = (roots.data ?? []).filter((r) => r.path !== m.rootFolder)
  if (locked && !on && !have) return null
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{show ? t('secondCopy.showTitle') : t('secondCopy.title')}</h2>
      <p className="mb-4 text-sm text-muted">{show ? t('secondCopy.showHint') : t('secondCopy.hint')}</p>
      {!locked && (
        <div className="flex flex-col gap-4">
          <Field label={show ? t('secondCopy.showProfile') : t('secondCopy.profile')}>
            {(id) => (
              <Select
                id={id}
                value={m.secondProfileId ?? 0}
                onChange={(v) => onChange({ secondProfileId: Number(v) })}
                options={[{ value: 0, label: show ? t('secondCopy.showOff') : t('secondCopy.off') }, ...choices.map((p) => ({ value: p.id, label: p.name }))]}
              />
            )}
          </Field>
          {on && !have && (others.length > 0 || !!m.secondRoot) && (
            <Field label={show ? t('secondCopy.showWhere') : t('secondCopy.where')}>
              {(id) => (
                <Select
                  id={id}
                  value={m.secondRoot ?? ''}
                  onChange={(v) => onChange({ secondRoot: String(v) })}
                  options={[{ value: '', label: show ? t('secondCopy.showNextToFirst') : t('secondCopy.nextToFirst') }, ...others.map((r) => ({ value: r.path, label: r.path }))]}
                />
              )}
            </Field>
          )}
        </div>
      )}
      {have ? (
        <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          {show ? <dd className="col-span-2">{facts}</dd> : facts}
          {!on && <dd className="col-span-2 text-xs text-muted">{show ? t('secondCopy.showStopped') : t('secondCopy.stopped')}</dd>}
          {!locked && (
            <dd className="col-span-2 mt-2">
              <Button variant="danger" onClick={onDelete}>
                <Trash2 className="size-4" aria-hidden /> {show ? t('secondCopy.showDelete') : t('secondCopy.delete')}
              </Button>
            </dd>
          )}
        </dl>
      ) : (
        on && !show && <p className="mt-4 text-sm text-muted">{m.monitored ? t('secondCopy.looking') : t('secondCopy.notWatched')}</p>
      )}
      {!have && on && show && <p className="mt-4 text-sm text-muted">{facts}</p>}
    </Card>
  )
}
