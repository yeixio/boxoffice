// The download order card in Settings: which downloads go first, by why
// they were grabbed and what they are. Saving re-sorts the waiting
// downloads at once; those moved to the top or bottom by hand stay put.
import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { put, type Downloads as DownloadsData, type QueuePriority } from '../api'
import { Button, Card, ErrorNote, Field, Input, Toggle } from '../ui'
import { useDownloads } from './Downloads'

export const defaultOrder = [
  'new-episodes',
  'new-movies',
  'backlog-episodes',
  'backlog-movies',
  'books',
  'music',
  'games',
  'replacements',
  'fixes',
  'second-copies',
  'upgrades',
  'other',
]

// move swaps group i with the one above (-1) or below (+1).
export function move(order: string[], i: number, by: -1 | 1) {
  const j = i + by
  if (j < 0 || j >= order.length) return order
  const out = [...order]
  ;[out[i], out[j]] = [out[j], out[i]]
  return out
}

function IconButton({ label, onClick, disabled, children }: { label: string; onClick: () => void; disabled?: boolean; children: React.ReactNode }) {
  return (
    <button type="button" onClick={onClick} disabled={disabled} title={label} aria-label={label} className="rounded-md p-2 text-muted hover:bg-line hover:text-ink disabled:pointer-events-none disabled:opacity-40">
      {children}
    </button>
  )
}

export function DownloadOrderCard() {
  const qc = useQueryClient()
  const { t } = useTranslation()
  const d = useDownloads()
  const [draft, setDraft] = useState<QueuePriority | null>(null)
  const [days, setDays] = useState<string | null>(null)
  const save = useMutation({
    mutationFn: (p: QueuePriority) => put<DownloadsData>('/downloads/priority', p),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['downloads'] })
      setDraft(null)
      setDays(null)
    },
  })
  if (!d.data?.priority) return null
  const saved = d.data.priority
  const p = draft ?? saved
  const edit = (next: Partial<QueuePriority>) => setDraft({ ...p, ...next })
  const changed = draft !== null && JSON.stringify(draft) !== JSON.stringify(saved)
  const isDefault = JSON.stringify(p.order) === JSON.stringify(defaultOrder)
  // How many are waiting in each group, so it's clear what moves.
  const waiting: Record<string, number> = {}
  for (const x of d.data.queue) waiting[x.group] = (waiting[x.group] ?? 0) + 1
  return (
    <Card>
      <h2 id="download-order" className="mb-1 scroll-mt-24 font-display text-lg font-semibold">
        {t('order.title')}
      </h2>
      <p className="mb-4 text-sm text-muted">
        {t('order.sub')} {t('order.searches')}
      </p>
      <div className="flex flex-col gap-4">
        <ol className="flex flex-col gap-1.5">
          {p.order.map((g, i) => (
            <li key={g} className="flex items-center gap-2 rounded-lg border border-line px-3 py-1.5">
              <span className="w-6 shrink-0 text-end text-sm tabular-nums text-muted">{i + 1}</span>
              <span className="min-w-0 flex-1 text-sm">
                {t(`order.groups.${g}`)}
                {waiting[g] ? <span className="ms-2 text-xs text-muted">{t('order.waiting', { count: waiting[g] })}</span> : null}
              </span>
              <IconButton label={t('order.up')} disabled={i === 0} onClick={() => edit({ order: move(p.order, i, -1) })}>
                <ArrowUp className="size-4" />
              </IconButton>
              <IconButton label={t('order.down')} disabled={i === p.order.length - 1} onClick={() => edit({ order: move(p.order, i, 1) })}>
                <ArrowDown className="size-4" />
              </IconButton>
            </li>
          ))}
        </ol>
        {!isDefault && (
          <div>
            <Button variant="ghost" onClick={() => edit({ order: defaultOrder })}>
              {t('order.reset')}
            </Button>
          </div>
        )}
        <Field label={t('order.newDays')} hint={t('order.newDaysHint')}>
          {(id) => (
            <Input
              id={id}
              type="number"
              min={1}
              max={365}
              className="w-28"
              value={days ?? String(p.newDays)}
              onChange={(e) => {
                setDays(e.target.value)
                const n = Number(e.target.value)
                if (Number.isInteger(n) && n >= 1 && n <= 365) edit({ newDays: n })
              }}
            />
          )}
        </Field>
        <Toggle
          checked={p.preempt}
          onChange={(v) => edit({ preempt: v })}
          label={
            <>
              {t('order.preempt')}
              <span className="block text-xs text-muted">{t('order.preemptHint')}</span>
            </>
          }
        />
        {changed && (
          <div className="flex flex-wrap gap-2">
            <Button variant="primary" busy={save.isPending} onClick={() => save.mutate(p)}>
              {t('common.save')}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setDraft(null)
                setDays(null)
                save.reset()
              }}
            >
              {t('common.cancel')}
            </Button>
          </div>
        )}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}
