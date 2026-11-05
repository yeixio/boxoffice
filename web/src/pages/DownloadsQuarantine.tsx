import { Trans, useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ListChecks, ShieldAlert, ShieldOff, Trash2, X } from 'lucide-react'
import { get, post, type QuarantineItem, type Quarantine as QuarantineData } from '../api'
import { bytes, when } from '../format'
import { Button, ErrorNote, Modal } from '../ui'

type Result = { done: number; failed: { path: string; error: string }[] }

// Quarantine lists the releases held because they didn't look safe, with
// why, and lets them be deleted or (with care) released, one at a time or
// several at once. It shows only when something is held.
export function Quarantine() {
  const { t } = useTranslation()
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
      setNote(r.failed.length ? t('quarantine.couldnt', { count: r.failed.length, which: r.failed.slice(0, 3).map((f) => `${f.path.split('/').pop()} (${f.error})`).join('; ') }) : '')
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
        <h2 className="me-auto flex items-center gap-2 font-display text-xl font-semibold">
          <ShieldAlert className="size-5 text-gold" aria-hidden /> {t('quarantine.title')}
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
              {chosen.length > 0 ? t('quarantine.selected', { count: chosen.length }) : t('quarantine.selectAll')}
            </label>
            <Button variant="ghost" onClick={() => (setPicking(false), setPicked(new Set()))}>
              {t('bulk.done')}
            </Button>
          </>
        ) : (
          <>
            <Button variant="ghost" onClick={() => setPicking(true)}>
              <ListChecks className="size-4" aria-hidden /> {t('list.select')}
            </Button>
            <Button variant="ghost" className="text-bad" disabled={free.length === 0} onClick={() => setAsking({ what: 'delete', items: free })}>
              <Trash2 className="size-4" aria-hidden /> {t('quarantine.deleteAll')}
            </Button>
          </>
        )}
      </div>
      <p className="mb-3 text-sm text-muted">
        {t('quarantine.held', { count: items.length, size: bytes(q.data!.size) })}{' '}
        {q.data!.days > 0 ? (
          t('quarantine.deletedAfter', { count: q.data!.days })
        ) : (
          <Trans i18nKey="quarantine.stay" components={{ link: <Link to="/settings#downloads" className="underline" /> }} />
        )}
      </p>
      <ul className="divide-y divide-line rounded-xl border border-line bg-card">
        {items.map((it) => (
          <li key={it.path} className={`flex flex-wrap items-start gap-3 px-4 py-3 ${picked.has(it.path) ? 'bg-marquee/5' : ''}`}>
            {picking && (
              <label className="-m-2 flex shrink-0 cursor-pointer items-center p-2">
                <input type="checkbox" className="size-5 accent-[var(--marquee)]" disabled={it.busy} checked={picked.has(it.path)} onChange={() => toggle(it.path)} aria-label={t('quarantine.selectName', { name: it.name })} />
              </label>
            )}
            <div className="min-w-0 flex-1 basis-56" onClick={picking && !it.busy ? () => toggle(it.path) : undefined}>
              <div className="break-words text-sm font-medium">{it.download?.name ?? it.name}</div>
              <div className="mt-0.5 text-xs text-muted">
                {bytes(it.size)} · {t('quarantine.fileCount', { count: it.files })} · {when(it.since)}
              </div>
              <p className="mt-1 break-words text-xs text-bad">{it.reason || t('quarantine.noReason')}</p>
            </div>
            {!picking && (
              <div className="flex shrink-0 gap-1">
                <button onClick={() => setAsking({ what: 'release', items: [it] })} disabled={it.busy} title={t('quarantine.release')} aria-label={t('quarantine.release')} className="rounded-md p-2 text-muted hover:bg-line hover:text-ink disabled:opacity-50">
                  <ShieldOff className="size-4" />
                </button>
                <button onClick={() => setAsking({ what: 'delete', items: [it] })} disabled={it.busy} title={t('quarantine.delete')} aria-label={t('quarantine.delete')} className="rounded-md p-2 text-muted hover:bg-line hover:text-bad disabled:opacity-50">
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
            <span className="me-auto text-sm font-medium sm:me-2">{t('quarantine.selected', { count: chosen.length })}</span>
            <button onClick={() => (setPicking(false), setPicked(new Set()))} title={t('quarantine.stop')} aria-label={t('quarantine.stop')} className="rounded-md p-2 text-muted hover:bg-line hover:text-ink sm:order-last">
              <X className="size-5" />
            </button>
            <div className="flex basis-full flex-wrap gap-2 sm:flex-1 sm:basis-auto">
              <Button variant="danger" className="px-3 py-1.5" onClick={() => setAsking({ what: 'delete', items: chosen })}>
                <Trash2 className="size-4" aria-hidden /> {t('quarantine.delete')}
              </Button>
              <Button className="px-3 py-1.5" onClick={() => setAsking({ what: 'release', items: chosen })}>
                <ShieldOff className="size-4" aria-hidden /> {t('quarantine.releaseShort')}
              </Button>
            </div>
          </div>
        </div>
      )}
      {picking && chosen.length > 0 && <div className="h-36 sm:h-20" aria-hidden />}
      {asking && (
        <Modal title={asking.what === 'delete' ? t('quarantine.deleteTitle', { count: asking.items.length }) : t('quarantine.releaseTitle', { count: asking.items.length })} onClose={() => setAsking(undefined)}>
          <ul className="mb-4 max-h-48 overflow-y-auto text-sm">
            {asking.items.map((it) => (
              <li key={it.path} className="truncate" title={it.name}>
                {it.download?.name ?? it.name}
              </li>
            ))}
          </ul>
          <p className="text-sm text-muted">
            {asking.what === 'delete'
              ? t('quarantine.deleteBody', { size: bytes(asking.items.reduce((n, i) => n + i.size, 0)) })
              : t('quarantine.releaseBody')}
          </p>
          <ErrorNote error={run.error} />
          <div className="mt-5 flex flex-wrap justify-end gap-2">
            <Button onClick={() => setAsking(undefined)}>{t('quarantine.keep')}</Button>
            <Button variant="danger" busy={run.isPending} onClick={() => run.mutate({ what: asking.what, paths: asking.items.map((i) => i.path) })}>
              {asking.what === 'delete' ? <Trash2 className="size-4" aria-hidden /> : <ShieldOff className="size-4" aria-hidden />}
              {asking.what === 'delete' ? t('quarantine.delete') : t('quarantine.releaseShort')}
            </Button>
          </div>
        </Modal>
      )}
    </section>
  )
}
