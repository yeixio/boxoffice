import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Bell, Check, LoaderCircle, X } from 'lucide-react'
import { del, get, type Progress, type Scan } from './api'
import { appName } from './format'

// Moves finished this long ago still show, so their result can be seen.
const recent = 30 * 60 * 1000

// An item is something running in the background, or just finished.
type Item = { key: string; state: 'running' | 'ready' | 'done' | 'failed'; title: string; detail?: string; done?: number; total?: number; to: string; action: string; dismiss?: () => void }

function scanItem(s: Scan, dismiss: () => void): Item {
  const name = appName[s.app] ?? s.app
  const to = `/import?scan=${s.id}`
  switch (s.status) {
    case 'ready':
      return { key: s.id, state: 'ready', title: `${name} is ready to look over`, detail: 'Nothing has changed yet.', to, action: 'Look it over', dismiss }
    case 'failed':
      return { key: s.id, state: 'failed', title: `Couldn't look over ${name}`, detail: s.error, to: '/import', action: 'Try again', dismiss }
    default:
      return { key: s.id, state: 'running', title: `Looking over ${name}`, detail: s.step, done: s.done, total: s.total, to, action: 'Open' }
  }
}

function moveItem(m: Progress): Item {
  const name = appName[m.app] ?? m.app
  const to = `/import?migration=${m.id}`
  const step = m.steps?.find((s) => s.status === 'running')
  if (m.status === 'running') return { key: `m${m.id}`, state: 'running', title: `Moving in from ${name}`, detail: step?.message || step?.name, to, action: 'See progress' }
  if (m.status === 'failed') return { key: `m${m.id}`, state: 'failed', title: `Moving in from ${name} stopped`, detail: `${name} is as it was.`, to, action: 'See why' }
  return { key: `m${m.id}`, state: 'done', title: `Moved in from ${name}`, to, action: 'See it' }
}

// useBackground lists what's running in the background: move-in scans and
// moves. It checks often while something runs and rarely otherwise.
export function useBackground() {
  const qc = useQueryClient()
  const scans = useQuery({
    queryKey: ['scans'],
    queryFn: () => get<Scan[]>('/migrations/scans'),
    refetchInterval: (q) => (q.state.data?.some((s) => s.status === 'running') ? 1500 : 20_000),
  })
  const moves = useQuery({
    queryKey: ['migrations'],
    queryFn: () => get<Progress[]>('/migrations'),
    refetchInterval: (q) => (q.state.data?.some((m) => m.status === 'running') ? 1500 : 20_000),
  })
  const dismiss = (id: string) => async () => {
    await del(`/migrations/scans/${id}`)
    qc.invalidateQueries({ queryKey: ['scans'] })
  }
  const now = Date.now()
  const items: Item[] = [
    ...(scans.data ?? []).map((s) => scanItem(s, dismiss(s.id))),
    ...(moves.data ?? []).filter((m) => m.status === 'running' || now - Date.parse(m.finished ?? m.started) < recent).map(moveItem),
  ]
  return items
}

function ItemIcon({ state }: { state: Item['state'] }) {
  if (state === 'running') return <LoaderCircle className="mt-0.5 size-4 shrink-0 animate-spin text-marquee" aria-label="running" />
  if (state === 'failed') return <X className="mt-0.5 size-4 shrink-0 text-bad" aria-label="failed" />
  return <Check className="mt-0.5 size-4 shrink-0 text-ok" aria-label="ready" />
}

export function ItemRow({ item, onGo }: { item: Item; onGo?: () => void }) {
  return (
    <li className="flex gap-3 px-4 py-3">
      <ItemIcon state={item.state} />
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{item.title}</div>
        {item.detail && <div className={`text-xs ${item.state === 'failed' ? 'text-bad' : 'text-muted'}`}>{item.detail}</div>}
        {item.state === 'running' && item.total ? (
          <div className="mt-1.5">
            <div className="h-1.5 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuemin={0} aria-valuemax={item.total} aria-valuenow={item.done}>
              <div className="h-full bg-marquee transition-all" style={{ width: `${Math.round(((item.done ?? 0) / item.total) * 100)}%` }} />
            </div>
            <div className="mt-0.5 text-xs text-muted">
              {(item.done ?? 0).toLocaleString()} of {item.total.toLocaleString()}
            </div>
          </div>
        ) : null}
        {item.action && (
          <Link to={item.to} onClick={onGo} className="mt-1 inline-block text-xs font-medium text-marquee">
            {item.action}
          </Link>
        )}
      </div>
      {item.dismiss && (
        <button onClick={item.dismiss} aria-label="Dismiss" className="self-start text-muted hover:text-ink">
          <X className="size-4" />
        </button>
      )}
    </li>
  )
}

// Tray is the bell in the sidebar, or in the header on a phone: what's
// running in the background, and a note when something finishes, wherever
// you are in Box Office.
export function Tray({ side }: { side?: boolean }) {
  const items = useBackground()
  const [open, setOpen] = useState(false)
  const [toasts, setToasts] = useState<Item[]>([])
  const seen = useRef<Map<string, Item['state']> | null>(null)
  const box = useRef<HTMLDivElement>(null)

  // A note for each item that has just finished, but not for ones that had
  // finished before the page was opened.
  useEffect(() => {
    const before = seen.current
    const now = new Map(items.map((i) => [i.key, i.state]))
    if (before) {
      const finished = items.filter((i) => i.state !== 'running' && before.get(i.key) === 'running')
      if (finished.length) setToasts((t) => [...t, ...finished])
    }
    seen.current = now
  }, [items])
  useEffect(() => {
    if (!toasts.length) return
    const t = setTimeout(() => setToasts((ts) => ts.slice(1)), 10_000)
    return () => clearTimeout(t)
  }, [toasts])
  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])

  const running = items.filter((i) => i.state === 'running').length
  const waiting = items.some((i) => i.state === 'ready')
  return (
    <>
      <div className="relative" ref={box}>
        <button
          onClick={() => setOpen(!open)}
          className="relative flex items-center gap-1.5 rounded-md px-2 py-1.5 text-sm text-muted hover:text-ink"
          aria-label={running ? `${running} running in the background` : 'Background activity'}
          aria-expanded={open}
        >
          {running ? <LoaderCircle className="size-4 animate-spin text-marquee" aria-hidden /> : <Bell className="size-4" aria-hidden />}
          {running > 0 && <span className="text-xs font-medium text-marquee">{running}</span>}
          {waiting && <span className="absolute right-1 top-1 size-2 rounded-full bg-marquee" aria-hidden />}
        </button>
        {open && (
          <div
            className={`fixed inset-x-4 top-14 z-30 overflow-hidden rounded-xl border border-line bg-card shadow-lg sm:absolute sm:inset-x-auto sm:w-80 ${
              side ? 'sm:left-full sm:top-0 sm:ml-2' : 'sm:right-0 sm:top-full sm:mt-2 md:left-0 md:right-auto'
            }`}
          >
            <div className="border-b border-line px-4 py-2.5 text-sm font-semibold">In the background</div>
            {items.length ? (
              <ul className="max-h-96 divide-y divide-line overflow-y-auto">
                {items.map((i) => (
                  <ItemRow key={i.key} item={i} onGo={() => setOpen(false)} />
                ))}
              </ul>
            ) : (
              <p className="px-4 py-4 text-sm text-muted">Nothing running. Looking over or moving in from an app shows here, so you can carry on elsewhere meanwhile.</p>
            )}
          </div>
        )}
      </div>
      {toasts.length > 0 &&
        createPortal(
        <div className="fixed bottom-4 right-4 z-40 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" aria-live="polite">
          {toasts.map((t) => (
            <ul key={t.key} className="overflow-hidden rounded-xl border border-line bg-card shadow-lg">
              <ItemRow item={{ ...t, dismiss: () => setToasts((ts) => ts.filter((x) => x.key !== t.key)) }} onGo={() => setToasts((ts) => ts.filter((x) => x.key !== t.key))} />
            </ul>
          ))}
        </div>,
        // Not inside the header, whose blurred backdrop would hold it.
        document.body,
      )}
    </>
  )
}
