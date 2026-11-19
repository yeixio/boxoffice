import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { del, get, post } from '../api'
import { Install, ThisDevice } from '../Notify'
import { usePush } from '../push'
import { Badge, Button, Card, ErrorNote, Toggle } from '../ui'

type Device = { id: number; endpoint: string; topics: string[]; name: string; createdAt: string }
type PushInfo = { publicKey: string; topics: string[]; devices: Device[] }

// PhoneCard installs Box Office as an app, and picks what this device is
// told about while Box Office isn't open.
export function PhoneCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const info = useQuery({ queryKey: ['push'], queryFn: () => get<PushInfo>('/push') })
  const push = usePush()
  const endpoint = push.endpoint
  const reload = () => qc.invalidateQueries({ queryKey: ['push'] })
  const remove = useMutation({ mutationFn: (id: number) => del(`/push/devices/${id}`), onSettled: reload })
  const v = info.data
  const mine = v?.devices.find((d) => d.endpoint === endpoint)
  const retopic = useMutation({
    // The same device again: the server keeps it and changes what it hears about.
    mutationFn: async (topics: string[]) => {
      const reg = await navigator.serviceWorker.getRegistration('/')
      const sub = await reg?.pushManager.getSubscription()
      if (sub) await post('/push/devices', { ...sub.toJSON(), topics })
    },
    onSettled: reload,
  })
  const byEndpoint = (e: string) => v?.devices.find((d) => d.endpoint === e)
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('phone.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('phone.sub')}</p>
      <ErrorNote error={info.error ?? remove.error ?? retopic.error} />
      <div className="flex flex-col gap-4">
        {push.why !== 'apple' && <Install />}
        {v && (
          <ThisDevice
            push={push}
            publicKey={v.publicKey}
            on={(e) => !!byEndpoint(e)}
            save={(sub) => post('/push/devices', { ...sub, topics: v.topics }).then(reload)}
            remove={async (e) => {
              const d = byEndpoint(e)
              if (d) await del(`/push/devices/${d.id}`)
              await reload()
            }}
            test={async (e) => {
              const d = byEndpoint(e)
              if (d) await post(`/push/devices/${d.id}/test`).finally(reload)
            }}
          >
            {mine && (
              <fieldset className="flex flex-col gap-2 ps-12" disabled={retopic.isPending}>
                <legend className="sr-only">{t('phone.about')}</legend>
                {v.topics.map((topic) => (
                  <Toggle
                    key={topic}
                    checked={mine.topics.includes(topic)}
                    onChange={(on) => retopic.mutate(on ? [...mine.topics, topic] : mine.topics.filter((x) => x !== topic))}
                    label={t(`phone.topics.${topic}`)}
                  />
                ))}
              </fieldset>
            )}
          </ThisDevice>
        )}
        {v && v.devices.some((d) => d.id !== mine?.id) && (
          <div>
            <h3 className="mb-2 text-sm font-medium">{t('phone.devices')}</h3>
            <ul className="divide-y divide-line rounded-md border border-line">
              {v.devices.map((d) => (
                <li key={d.id} className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
                  <span className="min-w-0 flex-1 truncate">{d.name || t('phone.unnamed')}</span>
                  {d.id === mine?.id ? (
                    <Badge tone="ok">{t('phone.thisOne')}</Badge>
                  ) : (
                    <Button variant="ghost" busy={remove.isPending && remove.variables === d.id} onClick={() => remove.mutate(d.id)}>
                      {t('common.remove')}
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </Card>
  )
}
