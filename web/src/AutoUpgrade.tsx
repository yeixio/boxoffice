import { useQuery } from '@tanstack/react-query'
import { get, type AutoSearch } from './api'
import { Field, Select } from './ui'

// AutoUpgradeField picks whether Box Office looks for upgrades of one
// title by itself: as Settings says, always, or never.
export function AutoUpgradeField({ value, onChange, upgradeOK }: { value?: string; onChange: (v: string) => void; upgradeOK: boolean }) {
  const a = useQuery({ queryKey: ['search-auto'], queryFn: () => get<AutoSearch>('/search/auto') })
  const on = a.data?.settings.upgrades
  const follow = on === undefined ? 'As in Settings' : `As in Settings (${on ? 'on' : 'off'})`
  return (
    <Field
      label="Automatic upgrades"
      hint={
        upgradeOK
          ? 'Whether RSS sync and the library patrol look for a better copy by themselves. Looking for an upgrade from this page works either way.'
          : "Its quality profile doesn't take upgrades, so there's nothing to look for until you pick one that does."
      }
    >
      {(id) => (
        <Select
          id={id}
          value={value ?? ''}
          onChange={(v) => onChange(String(v))}
          options={[
            { value: '', label: follow },
            { value: 'on', label: 'On for this title' },
            { value: 'off', label: 'Off for this title' },
          ]}
        />
      )}
    </Field>
  )
}
