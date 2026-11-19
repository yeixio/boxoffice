import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { get, put } from '../api'
import { browserZone } from '../format'
import { formatLocale } from '../i18n'
import { Button, Card, ErrorNote, Field, Select } from '../ui'

type TimeZoneState = { zone: string; saved: string; server: string; fromEnv: boolean; now: string }

// The time on Box Office's clock, written the way this language writes
// times: now is that clock's own time with its offset, so its hour and
// minute are read as they stand.
function wallClock(now: string): string {
  const [h, m] = [Number(now.slice(11, 13)), Number(now.slice(14, 16))]
  return new Intl.DateTimeFormat(formatLocale(), { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' }).format(new Date(Date.UTC(2026, 0, 1, h, m)))
}

function zones(): string[] {
  try {
    return ['UTC', ...Intl.supportedValuesOf('timeZone').filter((z) => z !== 'UTC')]
  } catch {
    return ['UTC']
  }
}

// TimeZoneCard picks the zone Box Office's clock is in. It offers this
// browser's when that's a different one, since a server in a container
// usually thinks it's in UTC.
export function TimeZoneCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const state = useQuery({ queryKey: ['timezone'], queryFn: () => get<TimeZoneState>('/timezone'), refetchInterval: 60_000 })
  const pick = useMutation({
    mutationFn: (zone: string) => put<TimeZoneState>('/timezone', { zone }),
    onSuccess: (v) => {
      qc.setQueryData(['timezone'], v)
      // Schedules, limits and messages are all on this clock.
      void qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== 'timezone' && q.queryKey[0] !== 'status' })
    },
  })
  const v = state.data
  const mine = browserZone()
  const list = zones()
  // A zone saved before this browser knew it is still listed.
  const options = [
    { value: '', label: t('settings.zone.server', { zone: v?.server ?? '' }) },
    ...(v?.saved && !list.includes(v.saved) ? [v.saved] : []).concat(list).map((z) => ({ value: z, label: z.replaceAll('_', ' ') })),
  ]
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.zone.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('settings.zone.hint')}</p>
      <ErrorNote error={state.error ?? pick.error} />
      {v && (
        <div className="flex flex-col gap-3">
          <Field label={t('settings.zone.title')} hint={t('settings.zone.now', { time: wallClock(v.now), zone: v.zone.replaceAll('_', ' ') })}>
            {(id) => <Select id={id} value={v.saved} onChange={(z) => pick.mutate(z)} options={options} />}
          </Field>
          {mine && mine !== v.zone && (
            <div className="flex flex-wrap items-center gap-3 rounded-md border border-line px-3 py-2 text-sm">
              <span className="min-w-0 flex-1">{t('settings.zone.differs', { zone: mine.replaceAll('_', ' ') })}</span>
              <Button busy={pick.isPending} onClick={() => pick.mutate(mine)}>
                {t('settings.zone.use', { zone: mine.replaceAll('_', ' ') })}
              </Button>
            </div>
          )}
        </div>
      )}
    </Card>
  )
}
