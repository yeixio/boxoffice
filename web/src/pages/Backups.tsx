import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Cloud, Download, HardDrive, Lock, RotateCcw, Upload } from 'lucide-react'
import { api, get, post, put, type Backup, type BackupRemote, type Backups as BackupsData } from '../api'
import { bytes, when } from '../format'
import { Badge, Button, Card, ErrorNote, Field, Heading, Input, Modal, Select, Spinner, Toggle } from '../ui'

const reasons: Record<string, string> = { scheduled: 'Scheduled', manual: 'Made by hand', 'before-migration': 'Before moving in', 'before-restore': 'Before a restore' }

export function Backups() {
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
        title="Backups"
        sub="Everything Box Office knows, in one file: titles, settings, profiles and what came over from other apps. A backup restores into SQLite or PostgreSQL."
        actions={
          <>
            <Button onClick={() => setRestoring('upload')}>
              <Upload className="size-4" aria-hidden /> Restore from a file
            </Button>
            <Button variant="primary" busy={make.isPending} onClick={() => make.mutate()}>
              Back up now
            </Button>
          </>
        }
      />
      {make.data?.warning && <p className="mb-4 text-sm text-gold">{make.data.warning}</p>}
      <ErrorNote error={make.error} />
      {d.remoteError && <p className="mb-4 text-sm text-bad">The second place couldn't be listed: {d.remoteError}</p>}
      <div className="grid gap-6 lg:grid-cols-[1.4fr_1fr]">
        <Card className="p-0">
          {d.backups.length === 0 ? (
            <p className="p-5 text-sm text-muted">No backups yet.</p>
          ) : (
            <ul className="divide-y divide-line">
              {d.backups.map((b) => (
                <li key={b.name} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
                  <div className="min-w-0 flex-1">
                    <div className="font-medium">{when(b.createdAt)}</div>
                    <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted">
                      {reasons[b.reason] ?? b.reason} · {bytes(b.size)}
                      {b.encrypted && (
                        <Badge>
                          <Lock className="size-3" /> encrypted
                        </Badge>
                      )}
                      {b.local && (
                        <Badge>
                          <HardDrive className="size-3" /> here
                        </Badge>
                      )}
                      {b.remote && (
                        <Badge tone="ok">
                          <Cloud className="size-3" /> second place
                        </Badge>
                      )}
                    </div>
                  </div>
                  <a href={`/api/v1/backups/${encodeURIComponent(b.name)}`} download className="rounded p-2 text-muted hover:bg-line hover:text-ink" title="Download">
                    <Download className="size-4" aria-label="Download" />
                  </a>
                  <button onClick={() => setRestoring(b)} className="rounded p-2 text-muted hover:bg-line hover:text-ink" title="Restore">
                    <RotateCcw className="size-4" aria-label="Restore" />
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
      setNote('Saved.')
    },
  })
  const test = useMutation({ mutationFn: () => post<{ result: string }>('/backups/test', body()), onSuccess: (r) => setNote(r.result) })
  const r = remote
  const setR = (p: Partial<typeof remote>) => setRemote({ ...remote, ...p })
  return (
    <Card>
      <h2 className="mb-4 font-display text-lg font-semibold">Schedule and places</h2>
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label="Every (hours)" hint="0 turns scheduled backups off.">
            {(id) => <Input id={id} type="number" min={0} value={every} onChange={(e) => setEvery(Number(e.target.value))} />}
          </Field>
          <Field label="Keep">{(id) => <Input id={id} type="number" min={1} value={keep} onChange={(e) => setKeep(Number(e.target.value))} />}</Field>
        </div>
        <p className="text-xs text-muted">
          On this server in <code>{data.dir}</code>.
        </p>
        <Field label="Also send to">
          {(id) => (
            <Select
              id={id}
              value={r.kind}
              onChange={(v) => setR({ kind: v as BackupRemote['kind'] })}
              options={[
                { value: '', label: 'Nowhere else' },
                { value: 'folder', label: 'Another folder (a NAS mount, say)' },
                { value: 's3', label: 'S3-compatible storage (AWS, Backblaze B2, R2, MinIO…)' },
              ]}
            />
          )}
        </Field>
        {r.kind === 'folder' && <Field label="Folder">{(id) => <Input id={id} placeholder="/mnt/nas/boxoffice" value={r.folder ?? ''} onChange={(e) => setR({ folder: e.target.value })} />}</Field>}
        {r.kind === 's3' && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Endpoint">{(id) => <Input id={id} placeholder="https://s3.us-east-1.amazonaws.com" value={r.endpoint ?? ''} onChange={(e) => setR({ endpoint: e.target.value })} />}</Field>
            <Field label="Region">{(id) => <Input id={id} placeholder="us-east-1" value={r.region ?? ''} onChange={(e) => setR({ region: e.target.value })} />}</Field>
            <Field label="Bucket">{(id) => <Input id={id} value={r.bucket ?? ''} onChange={(e) => setR({ bucket: e.target.value })} />}</Field>
            <Field label="Folder in the bucket">{(id) => <Input id={id} placeholder="boxoffice/" value={r.prefix ?? ''} onChange={(e) => setR({ prefix: e.target.value })} />}</Field>
            <Field label="Access key">{(id) => <Input id={id} autoComplete="off" value={r.accessKey ?? ''} onChange={(e) => setR({ accessKey: e.target.value })} />}</Field>
            <Field label="Secret" hint={r.hasSecret ? 'Saved. Leave blank to keep it.' : undefined}>
              {(id) => <Input id={id} type="password" autoComplete="off" value={r.secretKey ?? ''} onChange={(e) => setR({ secretKey: e.target.value })} />}
            </Field>
            <Toggle checked={!!r.pathStyle} onChange={(v) => setR({ pathStyle: v })} label="Bucket in the path (MinIO usually needs this)" />
          </div>
        )}
        <Field
          label="Passphrase"
          hint={s.hasPassphrase ? 'Backups are encrypted. Leave blank to keep the passphrase.' : 'Set one to encrypt backups. Keep it somewhere safe: without it a backup cannot be restored.'}
        >
          {(id) => <Input id={id} type="password" autoComplete="new-password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />}
        </Field>
        {s.hasPassphrase && <Toggle checked={clearPassphrase} onChange={setClearPassphrase} label="Stop encrypting new backups" />}
        <ErrorNote error={save.error || test.error} />
        {note && <p className="text-sm text-ok">{note}</p>}
        <div className="flex flex-wrap justify-end gap-2">
          {r.kind && (
            <Button busy={test.isPending} onClick={() => test.mutate()}>
              Test the place
            </Button>
          )}
          <Button variant="primary" busy={save.isPending} onClick={() => save.mutate()}>
            Save
          </Button>
        </div>
      </div>
    </Card>
  )
}

function Restore({ target, onClose }: { target: Backup | 'upload'; onClose: () => void }) {
  const file = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const go = async () => {
    setBusy(true)
    setError(null)
    try {
      if (target === 'upload') {
        const f = file.current?.files?.[0]
        if (!f) throw new Error('Pick a backup file first.')
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
    <Modal title="Restore a backup?" onClose={onClose}>
      <div className="flex flex-col gap-4 text-sm">
        <p>Everything in Box Office is replaced by the backup's copy. Box Office backs up what's here now first, so this can be undone. Backup settings stay as they are, and everyone signs in again.</p>
        {target === 'upload' ? (
          <input ref={file} type="file" accept=".gz,.enc" className="text-sm" />
        ) : (
          <p>
            From <strong>{when(target.createdAt)}</strong>.
          </p>
        )}
        <ErrorNote error={error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="danger" busy={busy} onClick={go}>
            Restore
          </Button>
        </div>
      </div>
    </Modal>
  )
}
