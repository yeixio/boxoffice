import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Cloud, Download, HardDrive, Lock, RotateCcw, Upload } from 'lucide-react'
import { api, get, post, put, type Backup, type BackupRemote, type Backups as BackupsData } from '../api'
import { bytes, when } from '../format'
import { Badge, Button, Card, ErrorNote, Field, Heading, Input, Modal, Select, Spinner, Toggle } from '../ui'

const reasons: Record<string, string> = {
  scheduled: 'backups.reason.scheduled',
  manual: 'backups.reason.manual',
  'before-migration': 'backups.reason.beforeMigration',
  'before-restore': 'backups.reason.beforeRestore',
}

export function Backups() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const data = useQuery({ queryKey: ['backups'], queryFn: () => get<BackupsData>('/backups') })
  const [restoring, setRestoring] = useState<Backup | 'upload' | null>(null)
  const make = useMutation({
    mutationFn: () => post<{ backup: Backup; warning?: string }>('/backups'),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['backups'] }),
  })
  if (data.isPending) return <Spinner />
  if (data.isError) return <ErrorNote error={data.error} />
  const d = data.data
  return (
    <>
      <Heading
        title={t('nav.backups')}
        sub={t('backups.sub')}
        actions={
          <>
            <Button onClick={() => setRestoring('upload')}>
              <Upload className="size-4" aria-hidden /> {t('backups.fromFile')}
            </Button>
            <Button variant="primary" busy={make.isPending} onClick={() => make.mutate()}>
              {t('backups.now')}
            </Button>
          </>
        }
      />
      {make.data?.warning && <p className="mb-4 text-sm text-gold">{make.data.warning}</p>}
      <ErrorNote error={make.error} />
      {d.remoteError && <p className="mb-4 text-sm text-bad">{t('backups.remoteError', { error: d.remoteError })}</p>}
      <div className="grid gap-6 lg:grid-cols-[1.4fr_1fr]">
        <Card className="p-0">
          {d.backups.length === 0 ? (
            <p className="p-5 text-sm text-muted">{t('backups.none')}</p>
          ) : (
            <ul className="divide-y divide-line">
              {d.backups.map((b) => (
                <li key={b.name} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
                  <div className="min-w-0 flex-1">
                    <div className="font-medium">{when(b.createdAt)}</div>
                    <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted">
                      {reasons[b.reason] ? t(reasons[b.reason]) : b.reason} · {bytes(b.size)}
                      {b.encrypted && (
                        <Badge>
                          <Lock className="size-3" /> {t('backups.encrypted')}
                        </Badge>
                      )}
                      {b.local && (
                        <Badge>
                          <HardDrive className="size-3" /> {t('backups.here')}
                        </Badge>
                      )}
                      {b.remote && (
                        <Badge tone="ok">
                          <Cloud className="size-3" /> {t('backups.second')}
                        </Badge>
                      )}
                    </div>
                  </div>
                  <a href={`/api/v1/backups/${encodeURIComponent(b.name)}`} download className="rounded p-2 text-muted hover:bg-line hover:text-ink" title={t('log.download')}>
                    <Download className="size-4" aria-label={t('log.download')} />
                  </a>
                  <button onClick={() => setRestoring(b)} className="rounded p-2 text-muted hover:bg-line hover:text-ink" title={t('backups.restore')}>
                    <RotateCcw className="size-4" aria-label={t('backups.restore')} />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Card>
        <BackupSettings data={d} />
      </div>
      {restoring && <Restore target={restoring} onClose={() => setRestoring(null)} />}
    </>
  )
}

function BackupSettings({ data }: { data: BackupsData }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const s = data.settings
  const [every, setEvery] = useState(s.everyHours)
  const [keep, setKeep] = useState(s.keep)
  const [passphrase, setPassphrase] = useState('')
  const [clearPassphrase, setClearPassphrase] = useState(false)
  const [remote, setRemote] = useState<BackupRemote & { secretKey?: string }>(s.remote)
  const [note, setNote] = useState('')
  useEffect(() => setRemote(s.remote), [s.remote])
  const body = () => ({ everyHours: every, keep, passphrase, clearPassphrase, remote })
  const save = useMutation({
    mutationFn: () => put<BackupsData>('/backups/settings', body()),
    onSuccess: (d) => {
      qc.setQueryData(['backups'], d)
      setPassphrase('')
      setClearPassphrase(false)
      setNote(t('backups.savedNote'))
    },
  })
  const test = useMutation({ mutationFn: () => post<{ result: string }>('/backups/test', body()), onSuccess: (r) => setNote(r.result) })
  const r = remote
  const setR = (p: Partial<typeof remote>) => setRemote({ ...remote, ...p })
  return (
    <Card>
      <h2 className="mb-4 font-display text-lg font-semibold">{t('backups.schedule')}</h2>
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label={t('backups.every')} hint={t('backups.everyHint')}>
            {(id) => <Input id={id} type="number" min={0} value={every} onChange={(e) => setEvery(Number(e.target.value))} />}
          </Field>
          <Field label={t('backups.keep')}>{(id) => <Input id={id} type="number" min={1} value={keep} onChange={(e) => setKeep(Number(e.target.value))} />}</Field>
        </div>
        <p className="text-xs text-muted">
          <Trans i18nKey="backups.onServer" values={{ dir: data.dir }} components={{ code: <code /> }} />
        </p>
        <Field label={t('backups.alsoSend')}>
          {(id) => (
            <Select
              id={id}
              value={r.kind}
              onChange={(v) => setR({ kind: v as BackupRemote['kind'] })}
              options={[
                { value: '', label: t('backups.nowhere') },
                { value: 'folder', label: t('backups.folderKind') },
                { value: 's3', label: t('backups.s3') },
              ]}
            />
          )}
        </Field>
        {r.kind === 'folder' && <Field label={t('backups.folder')}>{(id) => <Input id={id} placeholder="/mnt/nas/boxoffice" value={r.folder ?? ''} onChange={(e) => setR({ folder: e.target.value })} />}</Field>}
        {r.kind === 's3' && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t('backups.endpoint')}>{(id) => <Input id={id} placeholder="https://s3.us-east-1.amazonaws.com" value={r.endpoint ?? ''} onChange={(e) => setR({ endpoint: e.target.value })} />}</Field>
            <Field label={t('backups.region')}>{(id) => <Input id={id} placeholder="us-east-1" value={r.region ?? ''} onChange={(e) => setR({ region: e.target.value })} />}</Field>
            <Field label={t('backups.bucket')}>{(id) => <Input id={id} value={r.bucket ?? ''} onChange={(e) => setR({ bucket: e.target.value })} />}</Field>
            <Field label={t('backups.prefix')}>{(id) => <Input id={id} placeholder="boxoffice/" value={r.prefix ?? ''} onChange={(e) => setR({ prefix: e.target.value })} />}</Field>
            <Field label={t('backups.accessKey')}>{(id) => <Input id={id} autoComplete="off" value={r.accessKey ?? ''} onChange={(e) => setR({ accessKey: e.target.value })} />}</Field>
            <Field label={t('backups.secret')} hint={r.hasSecret ? t('backups.secretKept') : undefined}>
              {(id) => <Input id={id} type="password" autoComplete="off" value={r.secretKey ?? ''} onChange={(e) => setR({ secretKey: e.target.value })} />}
            </Field>
            <Toggle checked={!!r.pathStyle} onChange={(v) => setR({ pathStyle: v })} label={t('backups.pathStyle')} />
          </div>
        )}
        <Field
          label={t('backups.passphrase')}
          hint={s.hasPassphrase ? t('backups.passphraseKept') : t('backups.passphraseHint')}
        >
          {(id) => <Input id={id} type="password" autoComplete="new-password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />}
        </Field>
        {s.hasPassphrase && <Toggle checked={clearPassphrase} onChange={setClearPassphrase} label={t('backups.stopEncrypting')} />}
        <ErrorNote error={save.error || test.error} />
        {note && <p className="text-sm text-ok">{note}</p>}
        <div className="flex flex-wrap justify-end gap-2">
          {r.kind && (
            <Button busy={test.isPending} onClick={() => test.mutate()}>
              {t('backups.test')}
            </Button>
          )}
          <Button variant="primary" busy={save.isPending} onClick={() => save.mutate()}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Card>
  )
}

function Restore({ target, onClose }: { target: Backup | 'upload'; onClose: () => void }) {
  const { t } = useTranslation()
  const file = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const go = async () => {
    setBusy(true)
    setError(null)
    try {
      if (target === 'upload') {
        const f = file.current?.files?.[0]
        if (!f) throw new Error(t('backups.pickFile'))
        await api('POST', '/backups/restore', f)
      } else {
        await post(`/backups/${encodeURIComponent(target.name)}/restore`)
      }
      window.location.assign('/')
    } catch (err) {
      setError(err)
      setBusy(false)
    }
  }
  return (
    <Modal title={t('backups.restoreTitle')} onClose={onClose}>
      <div className="flex flex-col gap-4 text-sm">
        <p>{t('backups.restoreBody')}</p>
        {target === 'upload' ? (
          <input ref={file} type="file" accept=".gz,.enc" className="text-sm" />
        ) : (
          <p>
            <Trans i18nKey="backups.from" values={{ when: when(target.createdAt) }} components={{ strong: <strong /> }} />
          </p>
        )}
        <ErrorNote error={error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="danger" busy={busy} onClick={go}>
            {t('backups.restore')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
