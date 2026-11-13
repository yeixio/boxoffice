// AddNzb adds a download by hand from an NZB: a file from this device, or
// a link to one. Box Office reads it first and says what it thinks it is,
// then, once it's downloaded, files it in the library (or, opened from a
// title's releases, downloads it for that title).
import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { FileUp, KeyRound, Link2 } from 'lucide-react'
import { api, post, type Download } from './api'
import { bytes } from './format'
import { Badge, Button, ErrorNote, Input, Modal, Select } from './ui'
import { What, type ImportItem, type ImportKind } from './pages/ImportFiles'

// NZBCheck is what the server makes of an NZB.
export type NZBCheck = {
  token: string
  name: string
  size: number
  files: number
  password?: boolean
  guess: ImportItem
  kinds?: ImportKind[]
}

// isNzb says whether a dropped file looks like an NZB.
export const isNzb = (f: File) => /\.nzb(\.gz)?$/i.test(f.name) || f.type === 'application/x-nzb'

export function AddNzb({ forPath, forTitle, file, onClose }: { forPath?: string; forTitle?: string; file?: File; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const picker = useRef<HTMLInputElement>(null)
  const [link, setLink] = useState('')
  const [over, setOver] = useState(false)
  const [fileAs, setFileAs] = useState('auto')
  const check = useMutation({
    mutationFn: (what: File | string) =>
      typeof what === 'string' ? post<NZBCheck>('/downloads/nzb/check', { url: what }) : api<NZBCheck>('POST', `/downloads/nzb/check?name=${encodeURIComponent(what.name)}`, what),
  })
  const add = useMutation({
    mutationFn: (c: NZBCheck) => post<Download>('/downloads/nzb/add', forPath ? { token: c.token, for: forPath } : { token: c.token, fileAs }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['downloads'] })
      onClose()
    },
  })
  // A file dropped on the Downloads page is read straight away.
  const { mutate } = check
  useEffect(() => {
    if (file) mutate(file)
  }, [file, mutate])
  const c = check.data
  const pick = (f?: File) => f && check.mutate(f)
  return (
    <Modal title={forTitle ? t('nzb.titleFor', { title: forTitle }) : t('nzb.title')} onClose={onClose}>
      {!c ? (
        <div className="flex flex-col gap-4">
          <input ref={picker} type="file" accept=".nzb,.gz,application/x-nzb" className="hidden" onChange={(e) => (pick(e.target.files?.[0]), (e.target.value = ''))} />
          <button
            type="button"
            onClick={() => picker.current?.click()}
            onDragOver={(e) => (e.preventDefault(), setOver(true))}
            onDragLeave={() => setOver(false)}
            onDrop={(e) => {
              e.preventDefault()
              setOver(false)
              pick(e.dataTransfer.files[0])
            }}
            disabled={check.isPending}
            className={`flex flex-col items-center gap-2 rounded-xl border-2 border-dashed px-4 py-8 text-center transition-colors ${over ? 'border-marquee bg-marquee/5' : 'border-line hover:border-ink/40'}`}
          >
            <FileUp className="size-8 text-marquee" aria-hidden />
            <span className="font-medium">{t('nzb.choose')}</span>
            <span className="text-sm text-muted">{t('nzb.drop')}</span>
          </button>
          <div className="flex items-center gap-3 text-xs uppercase tracking-wide text-muted">
            <span className="h-px flex-1 bg-line" />
            {t('nzb.or')}
            <span className="h-px flex-1 bg-line" />
          </div>
          <form
            className="flex flex-col gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (link.trim()) check.mutate(link.trim())
            }}
          >
            <label htmlFor="nzb-link" className="text-sm font-medium">
              {t('nzb.link')}
            </label>
            <div className="flex gap-2">
              <Input id="nzb-link" type="url" inputMode="url" placeholder="https://indexer.example/getnzb/…" value={link} onChange={(e) => setLink(e.target.value)} />
              <Button type="submit" variant="primary" className="shrink-0 whitespace-nowrap" busy={check.isPending && typeof check.variables === 'string'} disabled={!link.trim()}>
                <Link2 className="size-4" aria-hidden /> {t('nzb.get')}
              </Button>
            </div>
            <ol className="list-decimal space-y-0.5 ps-5 text-xs text-muted">
              <li>{t('nzb.linkStep1')}</li>
              <li>{t('nzb.linkStep2')}</li>
            </ol>
            <p className="text-xs text-muted">{t('nzb.linkKey')}</p>
          </form>
          {check.isPending && typeof check.variables !== 'string' && <p className="text-sm text-muted">{t('nzb.reading')}</p>}
          <ErrorNote error={check.error} />
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="rounded-lg border border-line p-3">
            <p className="break-all text-sm font-medium">{c.name}</p>
            <p className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted">
              <span>{t('nzb.sizeFiles', { count: c.files, size: bytes(c.size) })}</span>
              {c.password && (
                <Badge tone="ok">
                  <KeyRound className="me-1 inline size-3" aria-hidden />
                  {t('nzb.password')}
                </Badge>
              )}
            </p>
          </div>
          {forPath ? (
            <p className="text-sm">
              <Trans i18nKey="nzb.forBody" values={{ title: forTitle }} components={{ b: <b /> }} />
            </p>
          ) : (
            <>
              <div>
                <p className="mb-1.5 text-sm font-medium">{t('nzb.thinks')}</p>
                {c.guess.kind ? (
                  <>
                    <What it={c.guess} systems={[]} />
                    {c.guess.problem && <p className="mt-1 text-xs text-muted">{c.guess.problem}</p>}
                  </>
                ) : (
                  <p className="text-sm text-muted">{t('nzb.noIdea')}</p>
                )}
              </div>
              <div>
                <label htmlFor="nzb-file-as" className="mb-1.5 block text-sm font-medium">
                  {t('nzb.whenDone')}
                </label>
                <Select
                  id="nzb-file-as"
                  value={fileAs}
                  onChange={setFileAs}
                  options={[
                    { value: 'auto', label: t('nzb.fileAuto') },
                    ...(c.kinds ?? []).map((k) => ({ value: k, label: t('nzb.fileAs', { kind: t(`importFiles.kind.${k}`) }) })),
                    { value: '', label: t('nzb.leave') },
                  ]}
                />
                <p className="mt-1.5 text-xs text-muted">
                  {fileAs === '' ? (
                    t('nzb.leaveHint')
                  ) : (
                    <Trans i18nKey="nzb.fileHint" components={{ link: <Link to="/import-files" className="underline" onClick={onClose} /> }} />
                  )}
                </p>
              </div>
            </>
          )}
          <ErrorNote error={add.error} />
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => (check.reset(), add.reset())}>
              {t('nzb.back')}
            </Button>
            <Button variant="primary" busy={add.isPending} onClick={() => add.mutate(c)}>
              {t('nzb.download')}
            </Button>
          </div>
        </div>
      )}
    </Modal>
  )
}
