import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ListChecks, ShieldAlert, ShieldOff, Trash2, X } from 'lucide-react'
import { get, post, type QuarantineItem, type Quarantine as QuarantineData } from '../api'
import { bytes, count, when } from '../format'
import { Button, ErrorNote, Modal } from '../ui'

type Result = { done: number; failed: { path: string; error: string }[] }

// Quarantine lists the releases held because they didn't look safe, with
// why, and lets them be deleted or (with care) released, one at a time or
// several at once. It shows only when something is held.
export function Quarantine() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['quarantine'], queryFn: () => get<QuarantineData>('/downloads/quarantine'), refetchInterval: 15000 })
  const [picking, setPicking] = useState(false)
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [asking, setAsking] = useState<{ what: 'delete' | 'release'; items: QuarantineItem[] }>()
  const [note, setNote] = useState('')
  const run = useMutation({
    mutationFn: ({ what, paths }: { what: 'delete' | 'release'; paths: string[] }) => post<Result>(`/downloads/quarantine/${what}`, { paths }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ['quarantine'] })
      qc.invalidateQueries({ queryKey: ['downloads'] })
      qc.invalidateQueries({ queryKey: ['download-files'] })
      setAsking(undefined)
      setPicked(new Set())
      setNote(r.failed.length ? `${r.failed.length} couldn't: ${r.failed.slice(0, 3).map((f) => `${f.path.split('/').pop()} (${f.error})`).join('; ')}` : '')
      if (!r.failed.length) setPicking(false)
    },
  })
  const items = q.data?.items ?? []
  if (items.length === 0) return null
  const free = items.filter((i) => !i.busy)
  const chosen = items.filter((i) => picked.has(i.path) && !i.busy)
  const toggle = (p: string) =>
    setPicked((s) => {
      const n = new Set(s)
      if (n.has(p)) n.delete(p)
      else n.add(p)
      return n
    })
  return (
    <section className="mt-8">
      <div className="mb-1 flex flex-wrap items-center gap-2">
        <h2 className="mr-auto flex items-center gap-2 font-display text-xl font-semibold">
          <ShieldAlert className="size-5 text-gold" aria-hidden /> Quarantine
        </h2>
        {picking ? (
          <>
            <label className="flex min-h-9 cursor-pointer items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="size-5 accent-[var(--marquee)]"
                checked={chosen.length > 0 && chosen.length === free.length}
                ref={(el) => {
                  if (el) el.indeterminate = chosen.length > 0 && chosen.length < free.length
                }}
                onChange={(e) => setPicked(new Set(e.target.checked ? free.map((i) => i.path) : []))}
              />
              {chosen.length > 0 ? `${chosen.length} selected` : 'Select all'}
            </label>
            <Button variant="ghost" onClick={() => (setPicking(false), setPicked(new Set()))}>
              Done
            </Button>
          </>
        ) : (
          <>
            <Button variant="ghost" onClick={() => setPicking(true)}>
              <ListChecks className="size-4" aria-hidden /> Select
            </Button>
            <Button variant="ghost" className="text-bad" disabled={free.length === 0} onClick={() => setAsking({ what: 'delete', items: free })}>
              <Trash2 className="size-4" aria-hidden /> Delete all
            </Button>
          </>
        )}
      </div>
      <p className="mb-3 text-sm text-muted">
        {count(items.length, 'release', 'releases')}, {bytes(q.data!.size)}. Box Office held these because they didn't look safe, and won't open them or move them into the library.{' '}
        {q.data!.days > 0 ? (
          <>They're deleted after {count(q.data!.days, 'day', 'days')}.</>
        ) : (
          <>
            They stay until you delete them; you can have them deleted after a number of days in <Link to="/settings#downloads" className="underline">Settings</Link>.
          </>
        )}
      </p>
      <ul className="divide-y divide-line rounded-xl border border-line bg-card">
        {items.map((it) => (
          <li key={it.path} className={`flex flex-wrap items-start gap-3 px-4 py-3 ${picked.has(it.path) ? 'bg-marquee/5' : ''}`}>
            {picking && (
              <label className="-m-2 flex shrink-0 cursor-pointer items-center p-2">
                <input type="checkbox" className="size-5 accent-[var(--marquee)]" disabled={it.busy} checked={picked.has(it.path)} onChange={() => toggle(it.path)} aria-label={`Select ${it.name}`} />
              </label>
            )}
            <div className="min-w-0 flex-1 basis-56" onClick={picking && !it.busy ? () => toggle(it.path) : undefined}>
              <div className="break-words text-sm font-medium">{it.download?.name ?? it.name}</div>
              <div className="mt-0.5 text-xs text-muted">
                {bytes(it.size)} · {count(it.files, 'file', 'files')} · {when(it.since)}
              </div>
              <p className="mt-1 break-words text-xs text-bad">{it.reason || "Box Office doesn't know why any more: the download it came from was cleared from the history."}</p>
            </div>
            {!picking && (
              <div className="flex shrink-0 gap-1">
                <button onClick={() => setAsking({ what: 'release', items: [it] })} disabled={it.busy} title="Release from quarantine" aria-label="Release from quarantine" className="rounded-md p-2 text-muted hover:bg-line hover:text-ink disabled:opacity-50">
                  <ShieldOff className="size-4" />
                </button>
                <button onClick={() => setAsking({ what: 'delete', items: [it] })} disabled={it.busy} title="Delete" aria-label="Delete" className="rounded-md p-2 text-muted hover:bg-line hover:text-bad disabled:opacity-50">
                  <Trash2 className="size-4" />
                </button>
              </div>
            )}
          </li>
        ))}
      </ul>
      {note && <p className="mt-2 break-words text-xs text-bad">{note}</p>}
      {picking && chosen.length > 0 && (
        <div className="fixed inset-x-0 bottom-0 z-30 border-t border-line bg-card/95 shadow-[0_-4px_16px_rgba(0,0,0,0.25)] backdrop-blur" style={{ paddingBottom: 'env(safe-area-inset-bottom)' }}>
          <div className="mx-auto flex max-w-7xl flex-wrap items-center gap-2 px-4 py-3">
            <span className="mr-auto text-sm font-medium sm:mr-2">{chosen.length} selected</span>
            <button onClick={() => (setPicking(false), setPicked(new Set()))} title="Stop selecting" aria-label="Stop selecting" className="rounded-md p-2 text-muted hover:bg-line hover:text-ink sm:order-last">
              <X className="size-5" />
            </button>
            <div className="flex basis-full flex-wrap gap-2 sm:flex-1 sm:basis-auto">
              <Button variant="danger" className="px-3 py-1.5" onClick={() => setAsking({ what: 'delete', items: chosen })}>
                <Trash2 className="size-4" aria-hidden /> Delete
              </Button>
              <Button className="px-3 py-1.5" onClick={() => setAsking({ what: 'release', items: chosen })}>
                <ShieldOff className="size-4" aria-hidden /> Release
              </Button>
            </div>
          </div>
        </div>
      )}
      {picking && chosen.length > 0 && <div className="h-36 sm:h-20" aria-hidden />}
      {asking && (
        <Modal title={asking.what === 'delete' ? `Delete ${count(asking.items.length, 'release', 'releases')} from quarantine` : `Release ${count(asking.items.length, 'release', 'releases')}`} onClose={() => setAsking(undefined)}>
          <ul className="mb-4 max-h-48 overflow-y-auto text-sm">
            {asking.items.map((it) => (
              <li key={it.path} className="truncate" title={it.name}>
                {it.download?.name ?? it.name}
              </li>
            ))}
          </ul>
          <p className="text-sm text-muted">
            {asking.what === 'delete'
              ? `Their files (${bytes(asking.items.reduce((n, i) => n + i.size, 0))}) are deleted for good. Downloads in the history that they came from stay, and say so.`
              : "Releasing moves them back into the downloads folder, where they can be downloaded or moved into your library. They may hold a program pretending to be a video; their files can't be run, but only release them if you know what's in them."}
          </p>
          <ErrorNote error={run.error} />
          <div className="mt-5 flex flex-wrap justify-end gap-2">
            <Button onClick={() => setAsking(undefined)}>Keep them in quarantine</Button>
            <Button variant="danger" busy={run.isPending} onClick={() => run.mutate({ what: asking.what, paths: asking.items.map((i) => i.path) })}>
              {asking.what === 'delete' ? <Trash2 className="size-4" aria-hidden /> : <ShieldOff className="size-4" aria-hidden />}
              {asking.what === 'delete' ? 'Delete' : 'Release'}
            </Button>
          </div>
        </Modal>
      )}
    </section>
  )
}
