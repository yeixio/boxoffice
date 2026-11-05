// Dialogs for taking things out of the library: removing a movie or a show
// (and, if asked, deleting its folder), and deleting a movie's, a season's
// or an episode's files while the title stays on the list.
//
// Deleting is for good: it frees the space. So the dialogs always say
// exactly what goes (files, size), deleting files is never ticked by
// itself, and the server only deletes what it was shown (expectFiles and
// expectSize); if the disk changed since, nothing is deleted.

import { useState, type ReactNode } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { AlertTriangle, ChevronDown, Trash2 } from 'lucide-react'
import { api, get, type DeletePlan, type FileRemoval } from './api'
import { bytes, count } from './format'
import { Button, ErrorNote, Modal, Spinner } from './ui'

function expect(p: DeletePlan) {
  return `expectFiles=${p.count}&expectSize=${p.size}`
}

export function Check({ checked, onChange, disabled, children, hint }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; children: ReactNode; hint?: ReactNode }) {
  return (
    <label className={`flex items-start gap-3 rounded-lg border border-line p-3 ${disabled ? 'opacity-60' : 'cursor-pointer hover:border-ink/30'}`}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} className="mt-0.5 size-5 shrink-0 accent-[var(--marquee)]" />
      <span className="min-w-0 text-sm">
        <span className="font-medium">{children}</span>
        {hint && <span className="mt-0.5 block text-muted">{hint}</span>}
      </span>
    </label>
  )
}

// FileList says what a plan deletes, with the files a tap away.
export function FileList({ plan }: { plan: DeletePlan }) {
  const [open, setOpen] = useState(false)
  if (plan.count === 0) return null
  return (
    <div className="rounded-lg border border-bad/40 bg-bad/5 p-3 text-sm">
      <p className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-bad" aria-hidden />
        <span>
          {plan.folder ? (
            <>
              Deletes the folder <code className="break-all">{plan.folder}</code> and everything in it: <strong>{count(plan.count, 'file', 'files')}</strong>, <strong>{bytes(plan.size)}</strong>.
            </>
          ) : (
            <>
              Deletes <strong>{count(plan.count, 'file', 'files')}</strong>, <strong>{bytes(plan.size)}</strong>.
            </>
          )}{' '}
          This frees {bytes(plan.size)} and can't be undone: deleted files don't go to the recycle folder.
        </span>
      </p>
      <button type="button" className="mt-2 flex items-center gap-1 text-xs text-muted hover:text-ink" onClick={() => setOpen(!open)} aria-expanded={open}>
        <ChevronDown className={`size-3.5 transition ${open ? '' : '-rotate-90'}`} aria-hidden /> {open ? 'Hide' : 'Show'} the files
      </button>
      {open && (
        <ul className="mt-2 max-h-48 overflow-y-auto rounded border border-line bg-card text-xs">
          {plan.files.map((f) => (
            <li key={f.path} className="flex gap-3 border-b border-line px-2 py-1.5 last:border-0">
              <span className="min-w-0 flex-1 break-all font-mono">{plan.folder ? f.path.slice(plan.folder.length + 1) : f.path.split('/').pop()}</span>
              <span className="shrink-0 text-muted">{bytes(f.size)}</span>
            </li>
          ))}
          {plan.count > plan.files.length && <li className="px-2 py-1.5 text-muted">and {count(plan.count - plan.files.length, 'more file', 'more files')}</li>}
        </ul>
      )}
    </div>
  )
}

function Actions({ children }: { children: ReactNode }) {
  return <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">{children}</div>
}

// RemoveTitle removes a movie or a show from the list, and deletes its
// folder only when that's ticked.
export function RemoveTitle({ kind, id, title, onClose, onRemoved }: { kind: 'movie' | 'series'; id: number; title: string; onClose: () => void; onRemoved: () => void }) {
  const base = kind === 'movie' ? `/movies/${id}` : `/series/${id}`
  const plan = useQuery({ queryKey: ['removal', base], queryFn: () => get<DeletePlan>(`${base}/removal`), gcTime: 0, staleTime: 0 })
  const [deleteFiles, setDeleteFiles] = useState(false)
  const remove = useMutation({
    mutationFn: () => api<void>('DELETE', deleteFiles && plan.data ? `${base}?deleteFiles=true&${expect(plan.data)}` : base),
    onSuccess: onRemoved,
    onError: () => plan.refetch(),
  })
  const p = plan.data
  const canDelete = !!p && p.count > 0 && !p.problem
  return (
    <Modal title={`Remove ${title}?`} onClose={onClose}>
      <p className="text-sm">
        It comes off Box Office's list{kind === 'movie' ? " and Matinee's requests" : ''}, and Box Office stops looking for it.
        {deleteFiles ? '' : ' Its files stay where they are on disk.'}
      </p>
      <div className="mt-4 flex flex-col gap-3">
        {plan.isPending ? (
          <Spinner label="Checking its folder" />
        ) : plan.isError ? (
          <ErrorNote error={plan.error} />
        ) : (
          <Check
            checked={deleteFiles}
            onChange={setDeleteFiles}
            disabled={!canDelete}
            hint={
              p!.problem
                ? `Box Office can't delete them: ${p!.problem}.`
                : p!.count === 0
                  ? 'There are no files on disk for it.'
                  : `${count(p!.count, 'file', 'files')}, ${bytes(p!.size)}, in ${p!.folder}`
            }
          >
            Also delete its files from disk
          </Check>
        )}
        {deleteFiles && p && <FileList plan={p} />}
      </div>
      <ErrorNote error={remove.error} />
      <Actions>
        <Button variant="ghost" onClick={onClose}>
          Keep it
        </Button>
        <Button variant="danger" busy={remove.isPending} disabled={plan.isPending} onClick={() => remove.mutate()}>
          <Trash2 className="size-4" aria-hidden /> {deleteFiles && p ? `Remove and delete ${bytes(p.size)}` : 'Remove from list'}
        </Button>
      </Actions>
    </Modal>
  )
}

// DeleteFiles deletes a movie's, a season's or an episode's files; the
// title stays on the list. path is the files' API path, such as
// /episodes/7/file or /series/3/seasons/2/files.
export function DeleteFiles<T>({ path, title, what, onClose, onDone }: { path: string; title: string; what: 'movie' | 'season' | 'episode'; onClose: () => void; onDone: (r: T) => void }) {
  const plan = useQuery({ queryKey: ['removal', path], queryFn: () => get<FileRemoval>(`${path}/removal`), gcTime: 0, staleTime: 0 })
  const [unmonitor, setUnmonitor] = useState(true)
  const [block, setBlock] = useState(false)
  const p = plan.data
  const del = useMutation({
    mutationFn: () => api<T>('DELETE', `${path}?${expect(p!)}&unmonitor=${unmonitor && !!p?.watched}&block=${block}`),
    onSuccess: onDone,
    onError: () => plan.refetch(),
  })
  const them = what === 'season' ? 'them' : 'it'
  const they = what === 'season' ? 'they' : 'it'
  return (
    <Modal title={`Delete ${title} from disk?`} onClose={onClose}>
      {plan.isPending ? (
        <Spinner label="Checking the files" />
      ) : plan.isError ? (
        <ErrorNote error={plan.error} />
      ) : p!.count === 0 ? (
        <p className="text-sm">There are no files on disk for it.</p>
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-sm">
            {what === 'season' ? 'The episodes stay on the list, without files.' : what === 'episode' ? 'The episode stays on the list, without a file.' : 'The movie stays on the list, without a file.'}{' '}
            Subtitles, artwork and other files named after {what === 'season' ? 'each episode' : 'it'} go too.
          </p>
          {p!.problem ? <p className="text-sm text-bad">Box Office can't delete {them}: {p!.problem}.</p> : <FileList plan={p!} />}
          {p!.watched && (
            <Check checked={unmonitor} onChange={setUnmonitor} hint={`Otherwise Box Office will look for ${them} and download ${them} again.`}>
              Stop watching for {them}
            </Check>
          )}
          {p!.releases.length > 0 && (
            <Check
              checked={block}
              onChange={setBlock}
              hint={p!.releases.map((r) => (
                <code key={r.name} className="block break-all text-xs">
                  {r.name}
                </code>
              ))}
            >
              Block the {p!.releases.length === 1 ? 'release' : 'releases'} {they} came from, so {p!.releases.length === 1 ? 'it isn’t' : 'they aren’t'} grabbed again
            </Check>
          )}
        </div>
      )}
      <ErrorNote error={del.error} />
      <Actions>
        <Button variant="ghost" onClick={onClose}>
          Keep {them}
        </Button>
        {p && p.count > 0 && (
          <Button variant="danger" busy={del.isPending} disabled={!!p.problem} onClick={() => del.mutate()}>
            <Trash2 className="size-4" aria-hidden /> Delete {bytes(p.size)}
          </Button>
        )}
      </Actions>
    </Modal>
  )
}
