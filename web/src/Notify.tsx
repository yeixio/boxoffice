// What Box Office's own pages and the request site share for installing
// as an app and turning notifications on for this device.

import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Download } from 'lucide-react'
import { Blocked, subscribe, unsubscribe, useInstall, type PushBlocker, type usePush } from './push'
import { Button, ErrorNote, Toggle } from './ui'

// Install offers to install the site, or says how it's done on this device.
export function Install() {
  const { t } = useTranslation('common')
  const { state, install } = useInstall()
  if (state === 'offer')
    return (
      <div>
        <Button onClick={() => void install()}>
          <Download className="size-4" aria-hidden /> {t('push.install')}
        </Button>
      </div>
    )
  return <p className="text-sm text-muted">{t(state === 'installed' ? 'push.installed' : state === 'apple' ? 'push.installApple' : 'push.installMenu')}</p>
}

// Why says why this device can't get notifications, and what to do about it.
export function Why({ why }: { why: PushBlocker }) {
  const { t } = useTranslation('common')
  return (
    <p role="note" className="rounded-md border border-line px-3 py-2 text-sm text-muted">
      {t(`push.why.${why}`)}
    </p>
  )
}

// ThisDevice is the switch for this device's notifications. push is this
// browser's subscription (usePush), which the page may show too. on says whether
// the server has this device (given its endpoint); save and remove tell the
// server; test asks it to send one. children show while it's on.
export function ThisDevice({
  push,
  publicKey,
  on,
  save,
  remove,
  test,
  children,
}: {
  push: ReturnType<typeof usePush>
  publicKey: string
  on: (endpoint: string) => boolean
  save: (sub: PushSubscriptionJSON) => Promise<unknown>
  remove: (endpoint: string) => Promise<unknown>
  test: (endpoint: string) => Promise<unknown>
  children?: ReactNode
}) {
  const { t } = useTranslation('common')
  const { endpoint, why, refresh } = push
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [refused, setRefused] = useState(false)
  if (why && why !== 'blocked') return <Why why={why} />
  const isOn = !!endpoint && on(endpoint)
  const flip = async (next: boolean) => {
    setBusy(true)
    setError(null)
    setSent(false)
    setRefused(false)
    try {
      if (next) await save(await subscribe(publicKey))
      else {
        const was = (await unsubscribe()) || endpoint || ''
        if (was) await remove(was)
      }
    } catch (err) {
      if (err instanceof Blocked) setRefused(true)
      else setError(err)
    } finally {
      await refresh()
      setBusy(false)
    }
  }
  const send = async () => {
    if (!endpoint) return
    setTesting(true)
    setError(null)
    setSent(false)
    try {
      await test(endpoint)
      setSent(true)
    } catch (err) {
      setError(err)
      await refresh()
    } finally {
      setTesting(false)
    }
  }
  return (
    <div className="flex flex-col gap-3">
      <Toggle checked={isOn} onChange={(v) => void flip(v)} disabled={busy || endpoint === undefined} label={t('push.thisDevice')} />
      {(why === 'blocked' || refused) && !isOn && <Why why="blocked" />}
      {isOn && children}
      <ErrorNote error={error} />
      {isOn && (
        <div className="flex flex-wrap items-center gap-3">
          <Button busy={testing} onClick={() => void send()}>
            {t('push.test')}
          </Button>
          {sent && (
            <span role="status" className="text-sm text-ok">
              {t('push.testSent')}
            </span>
          )}
        </div>
      )}
    </div>
  )
}
