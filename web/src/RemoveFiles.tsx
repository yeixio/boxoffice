// Dialogs for taking things out of the library: removing a movie or a show
// (and, if asked, deleting its folder), and deleting a movie's, a season's
// or an episode's files while the title stays on the list.
//
// Deleting is for good: it frees the space. So the dialogs always say
// exactly what goes (files, size), deleting files is never ticked by
// itself, and the server only deletes what it was shown (expectFiles and
// expectSize); if the disk changed since, nothing is deleted.

import { Trans, useTranslation } from 'react-i18next'
import { useState, type ReactNode } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { AlertTriangle, ChevronDown, Trash2 } from 'lucide-react'
import { api, get, type DeletePlan, type FileRemoval, type TitleRemoval } from './api'
import { bytes } from './format'
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
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  if (plan.count === 0) return null
  return (
    <div className="rounded-lg border border-bad/40 bg-bad/5 p-3 text-sm">
      <p className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-bad" aria-hidden />
        <span>
          {plan.folder ? (
            <Trans i18nKey="remove.deletesFolder" values={{ folder: plan.folder, count: plan.count, size: bytes(plan.size) }} components={{ code: <code className="break-all" />, strong: <strong /> }} />
          ) : (
            <Trans i18nKey="remove.deletes" values={{ count: plan.count, size: bytes(plan.size) }} components={{ strong: <strong /> }} />
          )}{' '}
          {t('remove.forGood', { size: bytes(plan.size) })}
        </span>
      </p>
      <button type="button" className="mt-2 flex items-center gap-1 text-xs text-muted hover:text-ink" onClick={() => setOpen(!open)} aria-expanded={open}>
        <ChevronDown className={`size-3.5 transition ${open ? '' : '-rotate-90'}`} aria-hidden /> {open ? t('remove.hideFiles') : t('remove.showFiles')}
      </button>
      {open && (
        <ul className="mt-2 max-h-48 overflow-y-auto rounded border border-line bg-card text-xs">
          {plan.files.map((f) => (
            <li key={f.path} className="flex gap-3 border-b border-line px-2 py-1.5 last:border-0">
              <span className="min-w-0 flex-1 break-all font-mono">{plan.folder ? f.path.slice(plan.folder.length + 1) : f.path.split('/').pop()}</span>
              <span className="shrink-0 text-muted">{bytes(f.size)}</span>
            </li>
          ))}
          {plan.count > plan.files.length && <li className="px-2 py-1.5 text-muted">{t('remove.more', { count: plan.count - plan.files.length })}</li>}
        </ul>
      )}
    </div>
  )
}

function Actions({ children }: { children: ReactNode }) {
  return <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">{children}</div>
}

// RemoveTitle removes a movie or a show from the list, cancelling its
// downloads, and deletes its folder only when that's ticked.
export function RemoveTitle({ kind, id, title, onClose, onRemoved }: { kind: 'movie' | 'series'; id: number; title: string; onClose: () => void; onRemoved: () => void }) {
  const { t } = useTranslation()
  const base = kind === 'movie' ? `/movies/${id}` : `/series/${id}`
  const plan = useQuery({ queryKey: ['removal', base], queryFn: () => get<TitleRemoval>(`${base}/removal`), gcTime: 0, staleTime: 0 })
  const [deleteFiles, setDeleteFiles] = useState(false)
  const remove = useMutation({
    mutationFn: () => api<void>('DELETE', deleteFiles && plan.data ? `${base}?deleteFiles=true&${expect(plan.data)}` : base),
    onSuccess: onRemoved,
    onError: () => plan.refetch(),
  })
  const p = plan.data
  const canDelete = !!p && p.count > 0 && !p.problem
  return (
    <Modal title={t('remove.title', { title })} onClose={onClose}>
      <p className="text-sm">
        {kind === 'movie' ? t('remove.offListMovie') : t('remove.offList')}
        {deleteFiles ? '' : ` ${t('remove.filesStay')}`}
      </p>
      {!!p?.downloads && <p className="mt-2 text-sm">{t('remove.downloadsGo', { count: p.downloads })}</p>}
      <div className="mt-4 flex flex-col gap-3">
        {plan.isPending ? (
          <Spinner label={t('remove.checkingFolder')} />
        ) : plan.isError ? (
          <ErrorNote error={plan.error} />
        ) : (
          <Check
            checked={deleteFiles}
            onChange={setDeleteFiles}
            disabled={!canDelete}
            hint={
              p!.problem
                ? t('remove.cantDelete', { problem: p!.problem })
                : p!.count === 0
                  ? t('remove.noFiles')
                  : t('remove.filesIn', { count: p!.count, size: bytes(p!.size), folder: p!.folder })
            }
          >
            {t('remove.alsoDelete')}
          </Check>
        )}
        {deleteFiles && p && <FileList plan={p} />}
      </div>
      <ErrorNote error={remove.error} />
      <Actions>
        <Button variant="ghost" onClick={onClose}>
          {t('remove.keepIt')}
        </Button>
        <Button variant="danger" busy={remove.isPending} disabled={plan.isPending} onClick={() => remove.mutate()}>
          <Trash2 className="size-4" aria-hidden /> {deleteFiles && p ? t('remove.removeAndDelete', { size: bytes(p.size) }) : t('remove.removeFromList')}
        </Button>
      </Actions>
    </Modal>
  )
}

// DeleteFiles deletes a movie's, a season's or an episode's files; the
// title stays on the list. path is the files' API path, such as
// /episodes/7/file or /series/3/seasons/2/files.
export function DeleteFiles<T>({ path, title, what, onClose, onDone }: { path: string; title: string; what: 'movie' | 'season' | 'episode'; onClose: () => void; onDone: (r: T) => void }) {
  const { t } = useTranslation()
  const plan = useQuery({ queryKey: ['removal', path], queryFn: () => get<FileRemoval>(`${path}/removal`), gcTime: 0, staleTime: 0 })
  const [unmonitor, setUnmonitor] = useState(true)
  const [block, setBlock] = useState(false)
  const p = plan.data
  const del = useMutation({
    mutationFn: () => api<T>('DELETE', `${path}?${expect(p!)}&unmonitor=${unmonitor && !!p?.watched}&block=${block}`),
    onSuccess: onDone,
    onError: () => plan.refetch(),
  })
  const many = what === 'season'
  return (
    <Modal title={t('remove.deleteTitle', { title })} onClose={onClose}>
      {plan.isPending ? (
        <Spinner label={t('remove.checkingFiles')} />
      ) : plan.isError ? (
        <ErrorNote error={plan.error} />
      ) : p!.count === 0 ? (
        <p className="text-sm">{t('remove.noFiles')}</p>
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-sm">
            {what === 'season' ? t('remove.episodesStay') : what === 'episode' ? t('remove.episodeStays') : t('remove.movieStays')} {many ? t('remove.extrasEach') : t('remove.extras')}
          </p>
          {p!.problem ? <p className="text-sm text-bad">{many ? t('remove.cantDeleteThem', { problem: p!.problem }) : t('remove.cantDeleteIt', { problem: p!.problem })}</p> : <FileList plan={p!} />}
          {p!.watched && (
            <Check checked={unmonitor} onChange={setUnmonitor} hint={many ? t('remove.againThem') : t('remove.againIt')}>
              {many ? t('remove.stopThem') : t('remove.stopIt')}
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
              {p!.releases.length > 1 ? t('remove.blockMany') : many ? t('remove.blockOneThem') : t('remove.blockOne')}
            </Check>
          )}
        </div>
      )}
      <ErrorNote error={del.error} />
      <Actions>
        <Button variant="ghost" onClick={onClose}>
          {many ? t('remove.keepThem') : t('remove.keepIt')}
        </Button>
        {p && p.count > 0 && (
          <Button variant="danger" busy={del.isPending} disabled={!!p.problem} onClick={() => del.mutate()}>
            <Trash2 className="size-4" aria-hidden /> {t('remove.delete', { size: bytes(p.size) })}
          </Button>
        )}
      </Actions>
    </Modal>
  )
}
