import { useTranslation } from 'react-i18next'
import { Trash2 } from 'lucide-react'
import type { ReactNode } from 'react'
import type { Movie, Profile } from './api'
import { useRootFolders } from './hooks'
import { Button, Card, Field, Select } from './ui'

// What a movie's second copy can be changed to.
export type SecondCopyEdit = { secondProfileId?: number; secondRoot?: string }

// SecondCopy is a movie's second copy: another copy of it kept at a
// different quality (a 1080p one next to the 4K one, say), with its own
// quality profile, next to the first copy or in another movie folder.
// facts shows the copy that's there.
export function SecondCopy({
  movie: m,
  profiles,
  locked,
  onChange,
  onDelete,
  facts,
}: {
  movie: Movie
  profiles: Profile[]
  locked: boolean
  onChange: (e: SecondCopyEdit) => void
  onDelete: () => void
  facts?: ReactNode
}) {
  const { t } = useTranslation()
  const roots = useRootFolders('movie')
  const on = !!m.secondProfileId
  // Any profile but the first copy's, and never Do not touch.
  const choices = profiles.filter((p) => !p.locked && (p.id !== m.profileId || p.id === m.secondProfileId))
  const others = (roots.data ?? []).filter((r) => r.path !== m.rootFolder)
  if (locked && !on && !m.second) return null
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('secondCopy.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('secondCopy.hint')}</p>
      {!locked && (
        <div className="flex flex-col gap-4">
          <Field label={t('secondCopy.profile')}>
            {(id) => (
              <Select
                id={id}
                value={m.secondProfileId ?? 0}
                onChange={(v) => onChange({ secondProfileId: Number(v) })}
                options={[{ value: 0, label: t('secondCopy.off') }, ...choices.map((p) => ({ value: p.id, label: p.name }))]}
              />
            )}
          </Field>
          {on && !m.second && (others.length > 0 || !!m.secondRoot) && (
            <Field label={t('secondCopy.where')}>
              {(id) => (
                <Select
                  id={id}
                  value={m.secondRoot ?? ''}
                  onChange={(v) => onChange({ secondRoot: String(v) })}
                  options={[{ value: '', label: t('secondCopy.nextToFirst') }, ...others.map((r) => ({ value: r.path, label: r.path }))]}
                />
              )}
            </Field>
          )}
        </div>
      )}
      {m.second ? (
        <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          {facts}
          {!on && <dd className="col-span-2 text-xs text-muted">{t('secondCopy.stopped')}</dd>}
          {!locked && (
            <dd className="col-span-2 mt-2">
              <Button variant="danger" onClick={onDelete}>
                <Trash2 className="size-4" aria-hidden /> {t('secondCopy.delete')}
              </Button>
            </dd>
          )}
        </dl>
      ) : (
        on && <p className="mt-4 text-sm text-muted">{m.monitored ? t('secondCopy.looking') : t('secondCopy.notWatched')}</p>
      )}
    </Card>
  )
}
