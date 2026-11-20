import { useTranslation } from 'react-i18next'
import { episodeCode } from './format'
import { Field, Select } from './ui'

// How many episodes ahead the picker offers; a show set to another number
// through the API keeps it.
const choices = [3, 5, 10, 20]

// What Matinee has said about a show with space saver on.
export type SpaceSaverWatched = { reported: boolean; season: number; episode: number }

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
