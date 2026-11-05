import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { get, type AutoSearch } from './api'
import { Field, Select } from './ui'

// AutoUpgradeField picks whether Box Office looks for upgrades of one
// title by itself: as Settings says, always, or never.
export function AutoUpgradeField({ value, onChange, upgradeOK }: { value?: string; onChange: (v: string) => void; upgradeOK: boolean }) {
  const { t } = useTranslation()
  const a = useQuery({ queryKey: ['search-auto'], queryFn: () => get<AutoSearch>('/search/auto') })
  const on = a.data?.settings.upgrades
  const follow = on === undefined ? t('autoUpgrade.follow') : on ? t('autoUpgrade.followOn') : t('autoUpgrade.followOff')
  return (
    <Field
      label={t('autoUpgrade.label')}
      hint={
        upgradeOK
          ? t('autoUpgrade.hint')
          : t('autoUpgrade.noUpgrades')
      }
    >
      {(id) => (
        <Select
          id={id}
          value={value ?? ''}
          onChange={(v) => onChange(String(v))}
          options={[
            { value: '', label: follow },
            { value: 'on', label: t('autoUpgrade.on') },
            { value: 'off', label: t('autoUpgrade.off') },
          ]}
        />
      )}
    </Field>
  )
}
