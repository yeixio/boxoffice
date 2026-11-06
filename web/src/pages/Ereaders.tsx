import { useTranslation } from 'react-i18next'
import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleCheck, Copy, Plus, Send, Tablet, Trash2, XCircle } from 'lucide-react'
import { get, post, put, type Book, type Ereader, type EreaderKind, type EreaderSend, type EreaderSettings } from '../api'
import { ago } from './ServerMonitor'
import { Badge, Button, Card, ErrorNote, Field, Input, Select, Toggle } from '../ui'

// Send to e-readers: Box Office emails ebooks to a Kindle, a PocketBook or
// any address that takes them, by hand from a book or by itself as each
// one downloads. The reader's kind comes from its address, and the mail
// server from Health → Alerts when there's one, so mostly the admin only
// pastes their reader's address.

export const ereadersDocs = 'https://github.com/yeixio/boxoffice/blob/main/docs/ereaders.md'

// kindOf works out a reader's kind from its address, as the server does.
export function kindOf(address: string): EreaderKind {
  const domain = address.trim().toLowerCase().split('@')[1] ?? ''
  if (['kindle.com', 'free.kindle.com', 'kindle.cn'].includes(domain)) return 'kindle'
  if (domain === 'pbsync.com') return 'pocketbook'
  return 'other'
}

// Mail servers of well-known providers, filled in from the username so
// only the password is left to find.
const providers: Record<string, { host: string; port: number; security: 'starttls' | 'tls' }> = {
  'gmail.com': { host: 'smtp.gmail.com', port: 587, security: 'starttls' },
  'googlemail.com': { host: 'smtp.gmail.com', port: 587, security: 'starttls' },
  'icloud.com': { host: 'smtp.mail.me.com', port: 587, security: 'starttls' },
  'me.com': { host: 'smtp.mail.me.com', port: 587, security: 'starttls' },
  'mac.com': { host: 'smtp.mail.me.com', port: 587, security: 'starttls' },
  'yahoo.com': { host: 'smtp.mail.yahoo.com', port: 465, security: 'tls' },
  'fastmail.com': { host: 'smtp.fastmail.com', port: 465, security: 'tls' },
}

export function providerFor(address: string) {
  return providers[address.trim().toLowerCase().split('@')[1] ?? '']
}

// Where to find each kind of reader's address, and how to let Box Office
// send to it, step by step. {{from}} is the address books come from.
const help: Record<EreaderKind, string[]> = {
  kindle: ['ereaders.help.kindle1', 'ereaders.help.kindle2', 'ereaders.help.kindle3', 'ereaders.help.kindle4', 'ereaders.help.kindle5'],
  pocketbook: ['ereaders.help.pocketbook1', 'ereaders.help.pocketbook2', 'ereaders.help.pocketbook3'],
  other: ['ereaders.help.other1', 'ereaders.help.other2'],
}

const kindLabel = (kind: EreaderKind, t: (k: string) => string) => (kind === 'kindle' ? 'Kindle' : kind === 'pocketbook' ? 'PocketBook' : t('ereaders.otherKind'))

type FormReader = Ereader & { key: string }
type Form = { mail: EreaderSettings['mail']; devices: FormReader[] }

const toForm = (s: EreaderSettings): Form => ({
  mail: { ...s.mail, password: '' },
  devices: s.devices.map((d) => ({ ...d, key: d.id })),
})

const toConfig = (f: Form) => ({
  mail: f.mail,
  devices: f.devices.map(({ key, name, kind, address, auto }) => ({ id: key, name, kind, address, auto })),
})

let nextKey = 0

export function useEreaders(enabled = true) {
  return useQuery({ queryKey: ['ereaders'], queryFn: () => get<EreaderSettings>('/ereaders'), enabled })
}

export function EreaderSettingsCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { data, error: loadError } = useEreaders()
  const [form, setForm] = useState<Form>()
  const [dirty, setDirty] = useState(false)
  const [tested, setTested] = useState<{ key: string; sent: EreaderSend }>()
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (data && !dirty) setForm(toForm(data))
  }, [data, dirty])
  const save = useMutation({
    mutationFn: (f: Form) => put<EreaderSettings>('/ereaders', toConfig(f)),
    onSuccess: (v) => {
      qc.setQueryData(['ereaders'], v)
      setForm(toForm(v))
      setDirty(false)
    },
  })
  const test = useMutation({
    mutationFn: (v: { form: Form; key: string }) => post<EreaderSend>(`/ereaders/${encodeURIComponent(v.key)}/test`, toConfig(v.form)),
    onSuccess: (sent, v) => {
      setTested({ key: v.key, sent })
      qc.invalidateQueries({ queryKey: ['ereaders'] })
    },
  })
  if (!data || !form) {
    return (
      <Card>
        <ErrorNote error={loadError} />
      </Card>
    )
  }
  const change = (f: (draft: Form) => void) => {
    const next = structuredClone(form)
    f(next)
    setForm(next)
    setDirty(true)
    setTested(undefined)
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(form)
  }
  const m = form.mail
  // The address books come from, as the form stands now.
  const from = m.sameAsAlerts ? m.from || data.alertMail.from || '' : m.from
  const keepsPassword = data.mail.hasPassword && m.username === data.mail.username && m.host === data.mail.host
  const copyFrom = () =>
    navigator.clipboard?.writeText(from).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })

  return (
    <Card>
      <form onSubmit={submit} className="flex flex-col gap-5">
        <div>
          <h2 className="mb-1 flex items-center gap-2 font-display text-lg font-semibold">
            <Tablet className="size-5 text-muted" aria-hidden /> {t('ereaders.title')}
          </h2>
          <p className="text-sm text-muted">
            {t('ereaders.intro')}{' '}
            <a href={ereadersDocs} target="_blank" rel="noreferrer" className="underline hover:text-ink">
              {t('ereaders.guide')}
            </a>
          </p>
        </div>

        {form.devices.map((d, i) => {
          const result = tested?.key === d.key ? tested.sent : undefined
          return (
            <div key={d.key} className="flex flex-col gap-3 rounded-lg border border-line p-4">
              <div className="grid gap-3 sm:grid-cols-[10rem_minmax(0,1fr)]">
                <Field label={t('ereaders.name')}>
                  {(id) => <Input id={id} value={d.name} placeholder={kindLabel(d.kind, t)} onChange={(e) => change((f) => (f.devices[i].name = e.target.value))} />}
                </Field>
                <Field
                  label={t('ereaders.address')}
                  hint={d.address.includes('@') ? <Badge tone={d.kind === 'other' ? 'plain' : 'ok'}>{t('ereaders.detected', { kind: kindLabel(d.kind, t) })}</Badge> : t('ereaders.addressHint')}
                >
                  {(id) => (
                    <Input
                      id={id}
                      type="email"
                      value={d.address}
                      placeholder="yourname@kindle.com"
                      autoComplete="off"
                      spellCheck={false}
                      onChange={(e) =>
                        change((f) => {
                          f.devices[i].address = e.target.value
                          f.devices[i].kind = kindOf(e.target.value)
                        })
                      }
                    />
                  )}
                </Field>
              </div>
              <Toggle
                checked={d.auto}
                onChange={(on) => change((f) => (f.devices[i].auto = on))}
                label={
                  <span>
                    <span className="block font-medium">{t('ereaders.auto')}</span>
                    <span className="block text-muted">{t('ereaders.autoHint')}</span>
                  </span>
                }
              />
              <details className="text-sm" open={!d.id && !d.address}>
                <summary className="cursor-pointer text-muted hover:text-ink">{t('ereaders.where', { kind: kindLabel(d.kind === 'other' && !d.address ? 'kindle' : d.kind, t) })}</summary>
                <ol className="mt-2 flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 ps-8 pe-4 text-muted">
                  {help[d.kind === 'other' && !d.address ? 'kindle' : d.kind].map((s, n) => (
                    <li key={n} className="break-words">
                      {t(s, { from: from || t('ereaders.yourFrom') })}
                    </li>
                  ))}
                </ol>
              </details>
              {d.kind === 'kindle' && !data.canConvert && <p className="text-xs text-muted">{t('ereaders.kindleFormats')}</p>}
              {result && <SendResult sent={result} />}
              <div className="flex flex-wrap justify-end gap-2">
                <Button type="button" disabled={!d.address.includes('@')} busy={test.isPending && test.variables?.key === d.key} onClick={() => test.mutate({ form, key: d.key })}>
                  <Send className="size-4" aria-hidden /> {t('ereaders.test')}
                </Button>
                <Button type="button" variant="ghost" onClick={() => change((f) => f.devices.splice(i, 1))}>
                  <Trash2 className="size-4" aria-hidden /> {t('common.remove')}
                </Button>
              </div>
            </div>
          )
        })}
        <div>
          <Button
            type="button"
            disabled={form.devices.length >= 20}
            onClick={() => change((f) => f.devices.push({ key: `new${nextKey++}`, id: '', name: '', kind: 'other', address: '', auto: true }))}
          >
            <Plus className="size-4" aria-hidden /> {t('ereaders.add')}
          </Button>
        </div>
        <p className="text-xs text-muted">{t('ereaders.kobo')}</p>

        {form.devices.length > 0 && (
          <section className="flex flex-col gap-3 border-t border-line pt-5">
            <h3 className="font-medium">{t('ereaders.sendFrom')}</h3>
            <Toggle
              checked={m.sameAsAlerts}
              disabled={!data.alertMail.ready && !m.sameAsAlerts}
              onChange={(on) => change((f) => (f.mail.sameAsAlerts = on))}
              label={
                <span>
                  <span className="block font-medium">{t('ereaders.sameAsAlerts')}</span>
                  <span className="block text-muted">{data.alertMail.ready ? t('ereaders.sameAsAlertsHint', { host: data.alertMail.host }) : t('ereaders.noAlertMail')}</span>
                </span>
              }
            />
            {m.sameAsAlerts && !data.alertMail.ready && (
              <p className="text-sm text-muted">
                <Link to="/health?tab=alerts" className="underline hover:text-ink">
                  {t('ereaders.setUpAlerts')}
                </Link>
              </p>
            )}
            {m.sameAsAlerts ? (
              <Field label={t('ereaders.from')} hint={t('ereaders.fromSameHint')}>
                {(id) => <Input id={id} type="email" value={m.from} placeholder={data.alertMail.from || 'boxoffice@example.com'} onChange={(e) => change((f) => (f.mail.from = e.target.value))} />}
              </Field>
            ) : (
              <>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t('alerts.username')} hint={t('ereaders.usernameHint')}>
                    {(id) => (
                      <Input
                        id={id}
                        value={m.username}
                        autoComplete="off"
                        spellCheck={false}
                        placeholder="you@gmail.com"
                        onChange={(e) =>
                          change((f) => {
                            const v = e.target.value
                            // A known provider's server fills itself in.
                            const p = providerFor(v)
                            if (p && (!f.mail.host || Object.values(providers).some((x) => x.host === f.mail.host))) Object.assign(f.mail, p)
                            if (!f.mail.from || f.mail.from === f.mail.username) f.mail.from = v.includes('@') ? v : f.mail.from
                            f.mail.username = v
                          })
                        }
                      />
                    )}
                  </Field>
                  <Field label={t('alerts.password')} hint={t('ereaders.passwordHint')}>
                    {(id) => (
                      <Input
                        id={id}
                        type="password"
                        autoComplete="new-password"
                        value={m.password ?? ''}
                        placeholder={keepsPassword ? t('common.saved') : undefined}
                        onChange={(e) => change((f) => (f.mail.password = e.target.value))}
                      />
                    )}
                  </Field>
                </div>
                <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_6rem_10rem]">
                  <Field label={t('alerts.server')} hint={providerFor(m.username)?.host === m.host && m.host ? t('ereaders.filledIn') : t('alerts.serverHint')}>
                    {(id) => <Input id={id} value={m.host} placeholder="smtp.example.com" autoComplete="off" spellCheck={false} onChange={(e) => change((f) => (f.mail.host = e.target.value))} />}
                  </Field>
                  <Field label={t('alerts.port')}>
                    {(id) => <Input id={id} inputMode="numeric" value={m.port || ''} onChange={(e) => change((f) => (f.mail.port = Number(e.target.value.replace(/\D/g, '')) || 0))} />}
                  </Field>
                  <Field label={t('alerts.connection')}>
                    {(id) => (
                      <Select
                        id={id}
                        value={m.security}
                        options={[
                          { value: 'starttls', label: 'STARTTLS' },
                          { value: 'tls', label: 'TLS' },
                          { value: 'none', label: t('alerts.unencrypted') },
                        ]}
                        onChange={(v) =>
                          change((f) => {
                            const security = v as Form['mail']['security']
                            const usual = { starttls: 587, tls: 465, none: 25 }
                            if (!f.mail.port || Object.values(usual).includes(f.mail.port)) f.mail.port = usual[security]
                            f.mail.security = security
                          })
                        }
                      />
                    )}
                  </Field>
                </div>
                <Field label={t('ereaders.from')} hint={t('ereaders.fromHint')}>
                  {(id) => <Input id={id} type="email" value={m.from} placeholder="you@gmail.com" onChange={(e) => change((f) => (f.mail.from = e.target.value))} />}
                </Field>
                <details className="text-sm">
                  <summary className="cursor-pointer text-muted hover:text-ink">{t('ereaders.mailWhere')}</summary>
                  <ol className="mt-2 flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 ps-8 pe-4 text-muted">
                    {['ereaders.help.mail1', 'ereaders.help.mail2', 'ereaders.help.mail3', 'ereaders.help.mail4'].map((s) => (
                      <li key={s}>{t(s)}</li>
                    ))}
                  </ol>
                </details>
              </>
            )}
            {from && (
              <div className="flex flex-col gap-1.5 rounded-lg bg-paper p-3 text-sm">
                <span className="text-muted">{t('ereaders.approve')}</span>
                <div className="flex flex-wrap items-center gap-2">
                  <code className="break-all font-mono text-ink">{from}</code>
                  <button type="button" className="inline-flex items-center gap-1 text-xs text-muted hover:text-ink" onClick={copyFrom}>
                    <Copy className="size-3.5" aria-hidden /> {copied ? t('common.copied') : t('common.copy')}
                  </button>
                </div>
              </div>
            )}
          </section>
        )}

        <ErrorNote error={save.error ?? test.error} />
        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="primary" disabled={!dirty} busy={save.isPending}>
            {dirty ? t('common.save') : t('common.savedButton')}
          </Button>
        </div>

        {data.history.length > 0 && (
          <section className="flex flex-col gap-2 border-t border-line pt-5">
            <h3 className="font-medium">{t('ereaders.recent')}</h3>
            <ul className="flex flex-col gap-2 text-sm">
              {data.history.slice(0, 8).map((s) => (
                <li key={s.id} className="flex gap-2">
                  {s.error ? <XCircle className="mt-0.5 size-4 shrink-0 text-bad" aria-hidden /> : <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" aria-hidden />}
                  <div className="min-w-0">
                    <p className="break-words">{t(s.auto ? 'ereaders.sentAuto' : 'ereaders.sentTo', { title: s.title, reader: s.deviceName })}</p>
                    {s.error && <p className="break-words text-xs text-bad">{s.error}</p>}
                    <p className="text-xs text-muted">{ago(Date.parse(s.at))}</p>
                  </div>
                </li>
              ))}
            </ul>
          </section>
        )}
      </form>
    </Card>
  )
}

function SendResult({ sent }: { sent: EreaderSend }) {
  const { t } = useTranslation()
  return (
    <div role="status" className="flex flex-col gap-1 text-sm">
      {sent.error ? (
        <>
          <p className="flex items-center gap-2 font-medium text-bad">
            <XCircle className="size-4" aria-hidden /> {t('ereaders.failed')}
          </p>
          <p className="break-words text-xs text-bad">{sent.error}</p>
        </>
      ) : (
        <p className="flex items-center gap-2 font-medium">
          <CircleCheck className="size-4 text-ok" aria-hidden /> {t('ereaders.sent', { reader: sent.deviceName })}
        </p>
      )}
    </div>
  )
}

// SendToReader sends one book's ebook to a reader, on the book's page.
export function SendToReader({ book }: { book: Book }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { data } = useEreaders(!!book.ebookFile)
  const [device, setDevice] = useState('')
  const send = useMutation({
    mutationFn: (id: string) => post<EreaderSend>(`/books/${book.id}/send`, { device: id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['ereaders'] }),
  })
  if (!book.ebookFile || !data) return null
  if (data.devices.length === 0) {
    return (
      <p className="text-sm text-muted">
        <Link to="/settings#ereaders" className="underline hover:text-ink">
          {t('ereaders.setUp')}
        </Link>
      </p>
    )
  }
  const picked = data.devices.find((d) => d.id === device) ?? data.devices[0]
  const label = (d: Ereader) => d.name || d.address
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {data.devices.length > 1 && (
          <div className="min-w-40">
            <Select value={picked.id} options={data.devices.map((d) => ({ value: d.id, label: label(d) }))} onChange={setDevice} />
          </div>
        )}
        <Button busy={send.isPending} onClick={() => send.mutate(picked.id)}>
          <Tablet className="size-4" aria-hidden /> {t('ereaders.sendTo', { reader: label(picked) })}
        </Button>
      </div>
      {send.data && <SendResult sent={send.data} />}
      <ErrorNote error={send.error} />
    </div>
  )
}
