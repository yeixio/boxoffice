// Installing the site as an app, and push notifications for this device.
// The server sends them through the browser maker's push service, and the
// service worker (public/sw.js) shows them.

import { useCallback, useEffect, useState } from 'react'

// ---- installing ----

type InstallPrompt = Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }> }

// Chrome and Edge say once, as the page loads, that they could install it;
// the offer is kept for the Install button.
let offer: InstallPrompt | null = null
const watching = new Set<() => void>()
const changed = () => watching.forEach((fn) => fn())

if (typeof window !== 'undefined') {
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault()
    offer = e as InstallPrompt
    changed()
  })
  window.addEventListener('appinstalled', () => {
    offer = null
    changed()
  })
}

// installed says whether this is the installed app, not a browser tab.
export const installed = () =>
  typeof window !== 'undefined' && (window.matchMedia?.('(display-mode: standalone)').matches || (navigator as { standalone?: boolean }).standalone === true)

// onApple says whether this is an iPhone or iPad, where installing is done
// from Safari's Share menu and notifications need the installed app. An
// iPad says it's a Mac, but a Mac has no touch screen.
export const onApple = (nav: Pick<Navigator, 'userAgent' | 'platform' | 'maxTouchPoints'> = navigator) =>
  /iPhone|iPad|iPod/.test(nav.userAgent) || (nav.platform === 'MacIntel' && nav.maxTouchPoints > 1)

export type InstallState = 'installed' | 'offer' | 'apple' | 'menu'

// useInstall says how this device installs the site, and gives the button
// its action when the browser offered one.
export function useInstall(): { state: InstallState; install: () => Promise<void> } {
  const [, tick] = useState(0)
  useEffect(() => {
    const fn = () => tick((n) => n + 1)
    watching.add(fn)
    return () => void watching.delete(fn)
  }, [])
  const install = useCallback(async () => {
    const o = offer
    if (!o) return
    await o.prompt()
    await o.userChoice
    // An offer can be used once.
    offer = null
    changed()
  }, [])
  return { state: installed() ? 'installed' : offer ? 'offer' : onApple() ? 'apple' : 'menu', install }
}

// ---- notifications ----

// Why this device can't get notifications, when it can't:
// insecure: the page wasn't opened over https;
// apple: an iPhone or iPad, where it has to be installed first;
// unsupported: the browser has no push;
// blocked: notifications were refused for this site in the browser.
export type PushBlocker = 'insecure' | 'apple' | 'unsupported' | 'blocked'

type Env = { secure: boolean; worker: boolean; push: boolean; notification: boolean; permission: string; apple: boolean; installed: boolean }

// blocker works out why not, from what the browser has. The order matters:
// each one is the first thing the person would have to fix.
export function blocker(env: Env): PushBlocker | null {
  if (!env.secure) return 'insecure'
  if (env.apple && !env.installed && !(env.worker && env.push && env.notification)) return 'apple'
  if (!env.worker || !env.push || !env.notification) return 'unsupported'
  if (env.permission === 'denied') return 'blocked'
  return null
}

export const pushBlocker = (): PushBlocker | null =>
  blocker({
    secure: window.isSecureContext,
    worker: 'serviceWorker' in navigator,
    push: 'PushManager' in window,
    notification: 'Notification' in window,
    permission: 'Notification' in window ? Notification.permission : 'default',
    apple: onApple(),
    installed: installed(),
  })

// keyBytes turns the server's key (base64url) into the bytes browsers take.
export function keyBytes(key: string): Uint8Array<ArrayBuffer> {
  const raw = atob(key.replaceAll('-', '+').replaceAll('_', '/'))
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

const sameKey = (have: ArrayBuffer | null | undefined, want: Uint8Array) => {
  if (!have) return false
  const a = new Uint8Array(have)
  return a.length === want.length && a.every((b, i) => b === want[i])
}

// Blocked is the person saying no to the browser's question.
export class Blocked extends Error {}

// subscription is this browser's push subscription, if it has one.
export async function subscription(): Promise<PushSubscription | null> {
  if (pushBlocker() !== null && pushBlocker() !== 'blocked') return null
  const reg = await navigator.serviceWorker.getRegistration('/')
  return (await reg?.pushManager.getSubscription()) ?? null
}

// subscribe asks the browser for a subscription to the server's key. The
// browser asks the person first, the first time.
export async function subscribe(publicKey: string): Promise<PushSubscriptionJSON> {
  if ((await Notification.requestPermission()) !== 'granted') throw new Blocked()
  await navigator.serviceWorker.register('/sw.js', { scope: '/' })
  const reg = await navigator.serviceWorker.ready
  const key = keyBytes(publicKey)
  let sub = await reg.pushManager.getSubscription()
  // One made for another server's key (a restored or rebuilt Box Office)
  // can't be sent to any more.
  if (sub && !sameKey(sub.options.applicationServerKey, key)) {
    await sub.unsubscribe()
    sub = null
  }
  sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key })
  return sub.toJSON()
}

// unsubscribe drops this browser's subscription, and gives the endpoint it
// had so the server can forget it.
export async function unsubscribe(): Promise<string> {
  const sub = await subscription()
  if (!sub) return ''
  await sub.unsubscribe()
  return sub.endpoint
}

// usePush follows this browser's subscription: its endpoint ('' for none,
// undefined while looking), and why it can't have one.
export function usePush(): { endpoint: string | undefined; why: PushBlocker | null; refresh: () => Promise<void> } {
  const [endpoint, setEndpoint] = useState<string | undefined>(undefined)
  const [why, setWhy] = useState<PushBlocker | null>(() => pushBlocker())
  const refresh = useCallback(async () => {
    setWhy(pushBlocker())
    try {
      setEndpoint((await subscription())?.endpoint ?? '')
    } catch {
      setEndpoint('')
    }
  }, [])
  useEffect(() => void refresh(), [refresh])
  return { endpoint, why, refresh }
}
