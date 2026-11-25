import { useRef, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { Download, Upload } from 'lucide-react'
import { api } from '../api'
import { Button, Card, ErrorNote, Modal, Toggle } from '../ui'

// What loading a file would do, or did (see internal/titlelist).
type TitleImport = {
  total: number
  counts: Partial<Record<'movie' | 'show' | 'artist' | 'author', number>>
  have: number
  cantAdd: number
  skipped: { title: string; reason: string }[]
  otherProfile: number
  otherFolder: number
  queued: boolean
}

const kinds = [
  ['movie', 'nav.movies'],
  ['show', 'nav.shows'],
  ['artist', 'titlesFile.artists'],
  ['author', 'titlesFile.authors'],
] as const

const linkButton =
  'inline-flex items-center justify-center gap-2 rounded-md border border-line bg-card px-3.5 py-2 text-sm font-medium text-ink transition hover:border-ink/40'

// TitlesFileCard saves the list of titles as a file, and loads one: the
// file is looked at first, and nothing is added until that's been read.
export function TitlesFileCard() {
  const { t } = useTranslation()
  const picker = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File>()
  const [search, setSearch] = useState(false)
  const [added, setAdded] = useState<number>()
  const look = useMutation({ mutationFn: (f: File) => api<TitleImport>('POST', '/titles/import?look=1', f) })
  const add = useMutation({
    mutationFn: (f: File) => api<TitleImport>('POST', '/titles/import' + (search ? '?search=1' : ''), f),
    onSuccess: (r) => {
      setAdded(Object.values(r.counts).reduce((a, n) => a + (n ?? 0), 0))
      close()
    },
  })
  const pick = (f?: File) => {
    if (!f) return
    setAdded(undefined)
    setFile(f)
    add.reset()
    look.mutate(f)
  }
  const close = () => {
    setFile(undefined)
    look.reset()
  }
  const plan = look.data
  const fresh = plan ? Object.values(plan.counts).reduce((a, n) => a + (n ?? 0), 0) : 0
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('titlesFile.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('titlesFile.sub')}</p>
      <div className="flex flex-wrap gap-2">
        <a className={linkButton} href="/api/v1/titles/export?format=json" download>
          <Download className="size-4" aria-hidden /> {t('titlesFile.saveJson')}
        </a>
        <a className={linkButton} href="/api/v1/titles/export?format=csv" download>
          <Download className="size-4" aria-hidden /> {t('titlesFile.saveCsv')}
        </a>
        <Button onClick={() => picker.current?.click()}>
          <Upload className="size-4" aria-hidden /> {t('titlesFile.load')}
        </Button>
        <input
          ref={picker}
          type="file"
          accept=".json,.csv,application/json,text/csv"
          hidden
          aria-label={t('titlesFile.load')}
          onChange={(e) => (pick(e.target.files?.[0]), (e.target.value = ''))}
          data-testid="titles-file"
        />
      </div>
      <p className="mt-3 text-xs text-muted">{t('titlesFile.loadHint')}</p>
      {added !== undefined && (
        <p className="mt-3 text-sm" role="status">
          <Trans i18nKey="titlesFile.queued" values={{ n: added }} components={{ link: <Link className="underline" to="/activity" /> }} />
        </p>
      )}
      {file && (
        <Modal title={t('titlesFile.lookTitle', { name: file.name })} onClose={close}>
          {look.isPending && <p className="text-sm text-muted">{t('common.looking')}</p>}
          <ErrorNote error={look.error} />
          {plan && (
            <div className="flex flex-col gap-3 text-sm">
              <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1">
                <dt>{t('titlesFile.inFile')}</dt>
                <dd className="text-end tabular-nums">{plan.total}</dd>
                <dt className="font-medium">{t('titlesFile.toAdd')}</dt>
                <dd className="text-end font-medium tabular-nums">{fresh}</dd>
                {kinds.map(([kind, label]) =>
                  plan.counts[kind] ? (
                    <div key={kind} className="col-span-2 grid grid-cols-subgrid text-muted">
                      <dt className="ps-4">{t(label)}</dt>
                      <dd className="text-end tabular-nums">{plan.counts[kind]}</dd>
                    </div>
                  ) : null,
                )}
                <dt>{t('titlesFile.have')}</dt>
                <dd className="text-end tabular-nums">{plan.have}</dd>
                {plan.cantAdd > 0 && (
                  <>
                    <dt>{t('titlesFile.cant')}</dt>
                    <dd className="text-end tabular-nums">{plan.cantAdd}</dd>
                  </>
                )}
              </dl>
              {fresh === 0 && plan.cantAdd === 0 && <p>{t('titlesFile.nothing')}</p>}
              {plan.otherProfile > 0 && <p className="text-muted">{t('titlesFile.otherProfile', { n: plan.otherProfile })}</p>}
              {plan.otherFolder > 0 && <p className="text-muted">{t('titlesFile.otherFolder', { n: plan.otherFolder })}</p>}
              {plan.skipped.length > 0 && (
                <details>
                  <summary className="cursor-pointer text-muted hover:text-ink">{t('titlesFile.cant')}</summary>
                  <ul className="mt-2 max-h-48 overflow-y-auto rounded-md border border-line p-2 text-xs">
                    {plan.skipped.map((s, i) => (
                      <li key={i} className="break-words py-0.5">
                        <span className="font-medium">{s.title}</span>: {s.reason}
                      </li>
                    ))}
                    {plan.cantAdd > plan.skipped.length && <li className="py-0.5 text-muted">{t('titlesFile.more', { n: plan.cantAdd - plan.skipped.length })}</li>}
                  </ul>
                </details>
              )}
              {fresh > 0 && (
                <div>
                  <Toggle checked={search} onChange={setSearch} label={t('titlesFile.search')} />
                  <p className="mt-1 text-xs text-muted">{t('titlesFile.searchHint')}</p>
                </div>
              )}
            </div>
          )}
          <ErrorNote error={add.error} />
          <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button variant="ghost" onClick={close}>
              {fresh > 0 ? t('common.cancel') : t('common.back')}
            </Button>
            {fresh > 0 && (
              <Button variant="primary" busy={add.isPending} onClick={() => add.mutate(file)}>
                {t('titlesFile.add')}
              </Button>
            )}
          </div>
        </Modal>
      )}
    </Card>
  )
}
