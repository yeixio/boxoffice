import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ArrowDownToLine, ArrowUpToLine, FolderInput, PackageOpen, Pause, Play, RotateCcw, Trash2, X } from 'lucide-react'
import { post, type Download } from '../api'
import { Button, ErrorNote, Modal, Toggle } from '../ui'

export type BulkList = 'queue' | 'history'
export type BulkAction = 'pause' | 'resume' | 'retry' | 'import' | 'reprocess' | 'top' | 'bottom' | 'remove'

type BulkResult = { done: number; failed: { id: number; error: string }[] }

const working = (d: Download) => ['verifying', 'repairing', 'unpacking', 'importing'].includes(d.status)

// Which of the picked downloads each action applies to. A button only
// shows when it applies to at least one, and only acts on those.
const applies: Record<BulkList, [BulkAction, string, (d: Download) => boolean][]> = {
  queue: [
    ['pause', 'Pause', (d) => d.status === 'queued' || d.status === 'downloading'],
    ['resume', 'Resume', (d) => d.status === 'paused'],
    ['top', 'To the top', (d) => d.status === 'queued' || d.status === 'paused'],
    ['bottom', 'To the bottom', (d) => d.status === 'queued' || d.status === 'paused'],
    ['remove', 'Remove', (d) => !working(d)],
  ],
  history: [
    ['import', 'Move into library', (d) => d.status === 'completed' && !!d.canImport],
    ['reprocess', 'Unpack again', (d) => !!d.canReprocess],
    ['retry', 'Download again', (d) => d.status !== 'completed' && !d.quarantined],
    ['remove', 'Remove', () => true],
  ],
}

const icons: Record<BulkAction, React.ReactNode> = {
  pause: <Pause className="size-4" aria-hidden />,
  resume: <Play className="size-4" aria-hidden />,
  top: <ArrowUpToLine className="size-4" aria-hidden />,
  bottom: <ArrowDownToLine className="size-4" aria-hidden />,
  remove: <Trash2 className="size-4" aria-hidden />,
  import: <FolderInput className="size-4" aria-hidden />,
  reprocess: <PackageOpen className="size-4" aria-hidden />,
  retry: <RotateCcw className="size-4" aria-hidden />,
}

// SelectAll is the checkbox in a section's header while picking.
export function SelectAll({ items, picked, onChange }: { items: Download[]; picked: Set<number>; onChange: (ids: Set<number>) => void }) {
  const n = items.filter((d) => picked.has(d.id)).length
  return (
    <label className="flex min-h-9 cursor-pointer items-center gap-2 text-sm">
      <input
        type="checkbox"
        className="size-5 accent-[var(--marquee)]"
        checked={n > 0 && n === items.length}
        ref={(el) => {
          if (el) el.indeterminate = n > 0 && n < items.length
        }}
        onChange={(e) => onChange(new Set(e.target.checked ? items.map((d) => d.id) : []))}
      />
      {n > 0 ? `${n} selected` : 'Select all'}
    </label>
  )
}

// PickBox is a row's checkbox while picking.
export function PickBox({ d, picked, onToggle }: { d: Download; picked: boolean; onToggle: () => void }) {
  return (
    <label className="-m-2 flex shrink-0 cursor-pointer items-center p-2">
      <input type="checkbox" className="size-5 accent-[var(--marquee)]" checked={picked} onChange={onToggle} aria-label={`Select ${d.name}`} />
    </label>
  )
}

// BulkBar sits at the bottom of the screen while downloads are picked,
// with what can be done to them.
export function BulkBar({ list, items, picked, onDone }: { list: BulkList; items: Download[]; picked: Set<number>; onDone: () => void }) {
  const qc = useQueryClient()
  const [removing, setRemoving] = useState(false)
  const [note, setNote] = useState('')
  const chosen = items.filter((d) => picked.has(d.id))
  const run = useMutation({
    mutationFn: ({ action, ids, deleteFiles }: { action: BulkAction; ids: number[]; deleteFiles?: boolean }) =>
      post<BulkResult>('/downloads/bulk', { action, ids, deleteFiles: !!deleteFiles }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ['downloads'] })
      setRemoving(false)
      if (r.failed.length === 0) {
        setNote('')
        onDone()
        return
      }
      const name = (id: number) => items.find((d) => d.id === id)?.name ?? `#${id}`
      setNote(
        `${r.done > 0 ? `${r.done} done. ` : ''}${r.failed.length} couldn't: ` +
          r.failed
            .slice(0, 3)
            .map((f) => `${name(f.id)} (${f.error})`)
            .join('; ') +
          (r.failed.length > 3 ? `, and ${r.failed.length - 3} more` : ''),
      )
    },
  })
  if (chosen.length === 0) return null
  // Downloads are listed in the order they're shown, so moving several to
  // the top or bottom keeps them in that order.
  const ids = (ok: (d: Download) => boolean) => chosen.filter(ok).map((d) => d.id)
  return (
    <div className="fixed inset-x-0 bottom-0 z-30 border-t border-line bg-card/95 shadow-[0_-4px_16px_rgba(0,0,0,0.25)] backdrop-blur" style={{ paddingBottom: 'env(safe-area-inset-bottom)' }}>
      <div className="mx-auto max-w-7xl px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="mr-auto text-sm font-medium sm:mr-2">{chosen.length} selected</span>
          <button onClick={onDone} title="Stop selecting" aria-label="Stop selecting" className="rounded-md p-2 text-muted hover:bg-line hover:text-ink sm:order-last">
            <X className="size-5" />
          </button>
          <div className="flex basis-full flex-wrap gap-2 sm:flex-1 sm:basis-auto">
            {applies[list].map(([action, label, ok]) => {
              const n = ids(ok).length
              if (n === 0) return null
              const busy = run.isPending && run.variables?.action === action
              return (
                <Button
                  key={action}
                  variant={action === 'remove' ? 'danger' : 'plain'}
                  className="whitespace-nowrap px-3 py-1.5"
                  busy={busy}
                  disabled={run.isPending}
                  onClick={() => (action === 'remove' ? setRemoving(true) : run.mutate({ action, ids: ids(ok) }))}
                >
                  {!busy && icons[action]}
                  {label}
                  {n < chosen.length && <span className="text-muted">({n})</span>}
                </Button>
              )
            })}
          </div>
        </div>
        {note && <p className="mt-2 break-words text-xs text-bad">{note}</p>}
        <ErrorNote error={run.error} />
      </div>
      {removing && (
        <BulkRemove
          items={chosen.filter(applies[list].find(([a]) => a === 'remove')![2])}
          busy={run.isPending}
          error={run.error}
          onRemove={(ids, deleteFiles) => run.mutate({ action: 'remove', ids, deleteFiles })}
          onClose={() => setRemoving(false)}
        />
      )}
    </div>
  )
}

function BulkRemove({ items, busy, error, onRemove, onClose }: { items: Download[]; busy: boolean; error: Error | null; onRemove: (ids: number[], deleteFiles: boolean) => void; onClose: () => void }) {
  const withFiles = items.filter((d) => d.folder && !d.quarantined)
  const held = items.filter((d) => d.quarantined).length
  // As for one: files go by default when there's nothing in them to keep,
  // that is when it failed or is already in the library.
  const [files, setFiles] = useState(withFiles.length > 0 && withFiles.every((d) => d.status !== 'completed' || !!d.importedTo))
  return (
    <Modal title={`Remove ${items.length} download${items.length === 1 ? '' : 's'}`} onClose={onClose}>
      <ul className="mb-4 max-h-48 overflow-y-auto text-sm">
        {items.map((d) => (
          <li key={d.id} className="truncate" title={d.name}>
            {d.name}
          </li>
        ))}
      </ul>
      {withFiles.length > 0 && (
        <Toggle
          checked={files}
          onChange={setFiles}
          label={
            <>
              Also delete their files in the downloads folder
              <span className="block text-xs text-muted">Files already moved into your library are never touched.</span>
            </>
          }
        />
      )}
      {held > 0 && <p className="mt-3 text-sm text-muted">Files in quarantine stay there; delete them under Quarantine below.</p>}
      <ErrorNote error={error} />
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>Keep them</Button>
        <Button
          variant="danger"
          busy={busy}
          onClick={() =>
            onRemove(
              items.map((d) => d.id),
              files,
            )
          }
        >
          Remove
        </Button>
      </div>
    </Modal>
  )
}
