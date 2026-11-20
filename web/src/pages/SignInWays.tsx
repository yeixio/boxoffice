import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import qrcode from 'qrcode-generator'
import { Copy } from 'lucide-react'
import { del, get, post } from '../api'
import { PasskeyList } from '../Passkeys'
import type { Passkey } from '../passkey'
import { Button, Card, ErrorNote, Field, Input } from '../ui'

// PasskeysCard is where the admin adds and removes their passkeys.
export function PasskeysCard() {
  const { t } = useTranslation('common')
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('passkey.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('passkey.sub')}</p>
      <PasskeyList calls={{ post }} queryKey={['passkeys']} list={() => get<{ passkeys: Passkey[]; host: string }>('/passkeys')} remove={(id) => del(`/passkeys/${id}`)} />
    </Card>
  )
}

type CodeState = { on: boolean; recoveryLeft: number; skipped: boolean }

// qrPath draws text as a QR code: one SVG path of its dark squares, and
// how many squares wide it is.
export function qrPath(text: string): { d: string; size: number } {
  const qr = qrcode(0, 'M')
  qr.addData(text)
  qr.make()
  const size = qr.getModuleCount()
  let d = ''
  for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (qr.isDark(y, x)) d += `M${x} ${y}h1v1h-1z`
  return { d, size }
}

// groups breaks a key into fours, which is easier to type into a phone.
export const groups = (key: string) => key.replace(/(.{4})(?=.)/g, '$1 ')

// CodeCard sets up the one-time code asked for after the password.
export function CodeCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const state = useQuery({ queryKey: ['sign-in-code'], queryFn: () => get<CodeState>('/code') })
  const reload = () => qc.invalidateQueries({ queryKey: ['sign-in-code'] })
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null)
  const [code, setCode] = useState('')
  const [recovery, setRecovery] = useState<string[] | null>(null)
  const [copied, setCopied] = useState(false)
  const [turningOff, setTurningOff] = useState(false)
  const [password, setPassword] = useState('')
  const start = useMutation({ mutationFn: () => post<{ secret: string; uri: string }>('/code/start'), onSuccess: setSetup })
  const confirm = useMutation({
    mutationFn: () => post<{ recovery: string[] }>('/code/confirm', { code }),
    onSuccess: (r) => {
      setRecovery(r.recovery)
      setSetup(null)
      setCode('')
      void reload()
    },
  })
  const off = useMutation({
    mutationFn: () => post('/code/off', { password }),
    onSuccess: () => {
      setTurningOff(false)
      setPassword('')
      void reload()
    },
  })
  const qr = useMemo(() => (setup ? qrPath(setup.uri) : null), [setup])
  const copy = () => {
    void navigator.clipboard?.writeText((recovery ?? []).join('\n'))
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('code.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('code.sub')}</p>
      <div className="flex flex-col gap-4">
        <ErrorNote error={state.error} />
        {recovery ? (
          <div className="flex flex-col gap-3">
            <div>
              <div className="text-sm font-medium">{t('code.recoveryTitle')}</div>
              <p className="text-sm text-muted">{t('code.recoveryHint')}</p>
            </div>
            <ul className="grid grid-cols-2 gap-x-6 gap-y-1 rounded-md border border-line px-4 py-3 font-mono text-sm sm:max-w-sm">
              {recovery.map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
            <div className="flex flex-wrap items-center justify-end gap-3">
              {copied && <span className="text-xs text-ok">{t('common.copied')}</span>}
              <Button onClick={copy}>
                <Copy className="size-4" aria-hidden /> {t('code.copy')}
              </Button>
              <Button variant="primary" onClick={() => setRecovery(null)}>
                {t('code.saved')}
              </Button>
            </div>
          </div>
        ) : setup && qr ? (
          <div className="flex flex-col gap-4">
            <p className="text-sm">{t('code.scan')}</p>
            <div className="flex flex-wrap items-start gap-4">
              <svg viewBox={`-2 -2 ${qr.size + 4} ${qr.size + 4}`} role="img" aria-label={t('code.qr')} className="size-44 shrink-0 rounded-md bg-white" shapeRendering="crispEdges">
                <path d={qr.d} fill="#000" />
              </svg>
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{t('code.key')}</div>
                <code className="break-words text-sm">{groups(setup.secret)}</code>
              </div>
            </div>
            <Field label={t('code.enter')}>
              {(id) => <Input id={id} inputMode="numeric" autoComplete="one-time-code" maxLength={10} value={code} onChange={(e) => setCode(e.target.value)} className="sm:max-w-40" />}
            </Field>
            <ErrorNote error={confirm.error} />
            <div className="flex justify-end gap-3">
              <Button variant="ghost" onClick={() => setSetup(null)}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" busy={confirm.isPending} disabled={code.trim().length < 6} onClick={() => confirm.mutate()}>
                {t('code.confirm')}
              </Button>
            </div>
          </div>
        ) : state.data?.on ? (
          <div className="flex flex-col gap-3">
            <p className="text-sm">{t('code.on', { n: state.data.recoveryLeft })}</p>
            {state.data.skipped && (
              <p role="note" className="rounded-md border border-line px-3 py-2 text-sm text-muted">
                {t('code.skipped')}
              </p>
            )}
            {turningOff ? (
              <>
                <Field label={t('code.password')}>
                  {(id) => <Input id={id} type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} className="sm:max-w-xs" />}
                </Field>
                <ErrorNote error={off.error} />
                <div className="flex justify-end gap-3">
                  <Button variant="ghost" onClick={() => setTurningOff(false)}>
                    {t('common.cancel')}
                  </Button>
                  <Button busy={off.isPending} disabled={!password} onClick={() => off.mutate()}>
                    {t('code.off')}
                  </Button>
                </div>
              </>
            ) : (
              <Button className="self-end" onClick={() => setTurningOff(true)}>
                {t('code.off')}
              </Button>
            )}
          </div>
        ) : (
          <>
            <ErrorNote error={start.error} />
            <Button className="self-end" busy={start.isPending} disabled={!state.data} onClick={() => start.mutate()}>
              {t('code.setUp')}
            </Button>
          </>
        )}
      </div>
    </Card>
  )
}
