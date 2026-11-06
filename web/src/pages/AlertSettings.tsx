import { useTranslation } from 'react-i18next'
import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, BellRing, CircleCheck, Copy, Info, Plus, Send, Trash2, XCircle } from 'lucide-react'
import { del, get, post, put, type Alert, type AlertSettings as Settings, type AlertWebhook, type MetricsSettings, type RateRule, type WebhookKind } from '../api'
import { ago } from './ServerMonitor'
import { Button, Card, ErrorNote, Field, Input, Select, Toggle } from '../ui'

// Alerts: an email or a message when something needs the admin, straight
// from Box Office to their own mail server or webhooks. Prometheus metrics
// are switched on here too.

const kinds: { value: WebhookKind; label: string }[] = [
  { value: 'discord', label: 'Discord' },
  { value: 'ntfy', label: 'ntfy' },
  { value: 'homeassistant', label: 'Home Assistant' },
  { value: 'json', label: 'alerts.other' },
]

const placeholders: Record<WebhookKind, string> = {
  discord: 'https://discord.com/api/webhooks/…',
  ntfy: 'https://ntfy.sh/…',
  homeassistant: 'http://homeassistant.local:8123/api/webhook/…',
  json: 'https://…',
}

// Where to find each webhook's address, step by step.
const help: Record<WebhookKind, string[]> = {
  discord: ['alerts.help.discord1', 'alerts.help.discord2', 'alerts.help.discord3'],
  ntfy: ['alerts.help.ntfy1', 'alerts.help.ntfy2', 'alerts.help.ntfy3', 'alerts.help.ntfy4'],
  homeassistant: ['alerts.help.ha1', 'alerts.help.ha2', 'alerts.help.ha3', 'alerts.help.ha4'],
  json: ['alerts.help.json1', 'alerts.help.json2'],
}

type FormWebhook = AlertWebhook & { key: string }
type Form = Omit<Settings, 'webhooks' | 'firing' | 'history' | 'links' | 'forwarded'> & { webhooks: FormWebhook[] }

const toForm = (s: Settings): Form => ({
  email: { ...s.email, password: '' },
  rules: structuredClone(s.rules),
  webhooks: s.webhooks.map((h) => ({ ...h, key: h.id, url: '', token: '' })),
})

const toConfig = (f: Form) => ({
  email: f.email,
  rules: f.rules,
  webhooks: f.webhooks.map(({ id, kind, name, url, token }) => ({ id, kind, name, url, token })),
})

let nextKey = 0

export function AlertSettings() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { data, error: loadError } = useQuery({ queryKey: ['alerts'], queryFn: () => get<Settings>('/admin/alerts'), refetchInterval: 30_000 })
  const [form, setForm] = useState<Form>()
  const [dirty, setDirty] = useState(false)
  const [tested, setTested] = useState<Alert>()
  useEffect(() => {
    if (data && !dirty) setForm(toForm(data))
  }, [data, dirty])
  const save = useMutation({
    mutationFn: (f: Form) => put<Settings>('/admin/alerts', toConfig(f)),
    onSuccess: (v) => {
      qc.setQueryData(['alerts'], v)
      setForm(toForm(v))
      setDirty(false)
    },
  })
  const test = useMutation({
    mutationFn: (f: Form) => post<Alert>('/admin/alerts/test', toConfig(f)),
    onSuccess: (a) => {
      setTested(a)
      qc.invalidateQueries({ queryKey: ['alerts'] })
    },
  })
  if (!data || !form) return <ErrorNote error={loadError} />

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
  const channels = (form.email.on ? 1 : 0) + form.webhooks.length
  const kindOptions = kinds.map((k) => ({ ...k, label: k.value === 'json' ? t(k.label) : k.label }))
  const keepsPassword = data.email.hasPassword && form.email.username === data.email.username && form.email.host === data.email.host

  return (
    <div className="flex max-w-3xl flex-col gap-8">
      <p className="text-muted">
        {t('alerts.intro')}
        {data.forwarded && ` ${t('alerts.forwarded')}`}
      </p>

      {data.firing.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">{t('alerts.now')}</h2>
          <AlertList alerts={data.firing} />
        </section>
      )}

      <form onSubmit={submit} className="flex flex-col gap-8">
        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">{t('alerts.what')}</h2>
          <Card className="flex flex-col divide-y divide-line p-0!">
            <RuleRow title={t('alerts.disk')} hint={t('alerts.diskHint')} on={form.rules.disk.on} onToggle={(on) => change((f) => (f.rules.disk.on = on))}>
              <NumberField label={t('alerts.percentFull')} value={form.rules.disk.percent} min={50} max={99} onChange={(v) => change((f) => (f.rules.disk.percent = v))} />
            </RuleRow>
            <RuleRow
              title={t('alerts.folder')}
              hint={t('alerts.folderHint')}
              on={form.rules.folder.on}
              onToggle={(on) => change((f) => (f.rules.folder.on = on))}
            />
            <RateRow title={t('alerts.errors')} rule={form.rules.errors} onChange={(r) => change((f) => (f.rules.errors = r))} />
            <RateRow title={t('alerts.downloads')} hint={t('alerts.downloadsHint')} rule={form.rules.downloads} onChange={(r) => change((f) => (f.rules.downloads = r))} />
            <RuleRow
              title={t('alerts.restart')}
              hint={t('alerts.restartHint')}
              on={form.rules.restart.on}
              onToggle={(on) => change((f) => (f.rules.restart.on = on))}
            />
            <RuleRow title={t('alerts.backup')} hint={t('alerts.backupHint')} on={form.rules.backup.on} onToggle={(on) => change((f) => (f.rules.backup.on = on))} />
            <RuleRow
              title={t('alerts.problems')}
              hint={t('alerts.problemsHint')}
              on={form.rules.problems.on}
              onToggle={(on) => change((f) => (f.rules.problems.on = on))}
            />
          </Card>
        </section>

        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">{t('alerts.email')}</h2>
          <Card className="flex flex-col gap-4">
            <Toggle checked={form.email.on} onChange={(on) => change((f) => (f.email.on = on))} label={<span className="font-medium">{t('alerts.byEmail')}</span>} />
            {form.email.on && (
              <>
                <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_6rem_10rem]">
                  <Field label={t('alerts.server')} hint={t('alerts.serverHint')}>
                    {(id) => <Input id={id} value={form.email.host} placeholder="smtp.example.com" autoComplete="off" spellCheck={false} onChange={(e) => change((f) => (f.email.host = e.target.value))} />}
                  </Field>
                  <Field label={t('alerts.port')}>
                    {(id) => <Input id={id} inputMode="numeric" value={form.email.port || ''} onChange={(e) => change((f) => (f.email.port = Number(e.target.value.replace(/\D/g, '')) || 0))} />}
                  </Field>
                  <Field label={t('alerts.connection')}>
                    {(id) => (
                      <Select
                        id={id}
                        value={form.email.security}
                        options={[
                          { value: 'starttls', label: 'STARTTLS' },
                          { value: 'tls', label: 'TLS' },
                          { value: 'none', label: t('alerts.unencrypted') },
                        ]}
                        onChange={(v) =>
                          change((f) => {
                            const security = v as Form['email']['security']
                            // The port follows along while it's a usual one.
                            const usual = { starttls: 587, tls: 465, none: 25 }
                            if (Object.values(usual).includes(f.email.port)) f.email.port = usual[security]
                            f.email.security = security
                          })
                        }
                      />
                    )}
                  </Field>
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t('alerts.username')} hint={t('alerts.usernameHint')}>
                    {(id) => <Input id={id} value={form.email.username} autoComplete="off" spellCheck={false} onChange={(e) => change((f) => (f.email.username = e.target.value))} />}
                  </Field>
                  <Field label={t('alerts.password')} hint={t('alerts.passwordHint')}>
                    {(id) => (
                      <Input
                        id={id}
                        type="password"
                        autoComplete="new-password"
                        value={form.email.password ?? ''}
                        placeholder={keepsPassword ? t('common.saved') : undefined}
                        onChange={(e) => change((f) => (f.email.password = e.target.value))}
                      />
                    )}
                  </Field>
                  <Field label={t('alerts.from')}>{(id) => <Input id={id} type="email" value={form.email.from} placeholder="boxoffice@example.com" onChange={(e) => change((f) => (f.email.from = e.target.value))} />}</Field>
                  <Field label={t('alerts.to')} hint={t('alerts.toHint')}>
                    {(id) => <Input id={id} value={form.email.to} placeholder="you@example.com" onChange={(e) => change((f) => (f.email.to = e.target.value))} />}
                  </Field>
                </div>
              </>
            )}
          </Card>
        </section>

        <section className="flex flex-col gap-3">
          <div>
            <h2 className="font-display text-lg font-semibold">{t('alerts.webhooks')}</h2>
            <p className="mt-1 text-sm text-muted">{t('alerts.webhooksSub')}</p>
          </div>
          {form.webhooks.map((h, i) => (
            <Card key={h.key} className="flex flex-col gap-3">
              <div className="grid gap-3 sm:grid-cols-[12rem_minmax(0,1fr)]">
                <Field label={t('alerts.kind')}>{(id) => <Select id={id} value={h.kind} options={kindOptions} onChange={(v) => change((f) => (f.webhooks[i].kind = v as WebhookKind))} />}</Field>
                <Field label={t('alerts.name')}>
                  {(id) => <Input id={id} value={h.name ?? ''} placeholder={kindOptions.find((k) => k.value === h.kind)?.label} onChange={(e) => change((f) => (f.webhooks[i].name = e.target.value))} />}
                </Field>
              </div>
              <Field label={t('alerts.address')} hint={h.hint ? t('alerts.addressKept') : undefined}>
                {(id) => (
                  <Input id={id} value={h.url ?? ''} autoComplete="off" spellCheck={false} placeholder={h.hint || placeholders[h.kind]} onChange={(e) => change((f) => (f.webhooks[i].url = e.target.value))} />
                )}
              </Field>
              {h.kind === 'ntfy' && (
                <Field label={t('alerts.token')}>
                  {(id) => (
                    <Input
                      id={id}
                      type="password"
                      autoComplete="new-password"
                      value={h.token ?? ''}
                      placeholder={h.hasToken && !h.url ? t('common.saved') : 'tk_…'}
                      onChange={(e) => change((f) => (f.webhooks[i].token = e.target.value))}
                    />
                  )}
                </Field>
              )}
              <details className="text-sm">
                <summary className="cursor-pointer text-muted hover:text-ink">{t('alerts.where')}</summary>
                <ol className="mt-2 flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 ps-8 pe-4 text-muted">
                  {help[h.kind].map((s, n) => (
                    <li key={n}>{t(s)}</li>
                  ))}
                </ol>
              </details>
              <div className="flex justify-end">
                <Button type="button" variant="ghost" onClick={() => change((f) => f.webhooks.splice(i, 1))}>
                  <Trash2 className="size-4" aria-hidden /> {t('common.remove')}
                </Button>
              </div>
            </Card>
          ))}
          <div>
            <Button type="button" disabled={form.webhooks.length >= 20} onClick={() => change((f) => f.webhooks.push({ key: `new${nextKey++}`, id: '', kind: 'discord', name: '', url: '', token: '' }))}>
              <Plus className="size-4" aria-hidden /> {t('alerts.addWebhook')}
            </Button>
          </div>
        </section>

        <div className="flex flex-col gap-3">
          {channels === 0 && <p className="text-sm text-muted">{data.forwarded ? t('alerts.inMatinee') : t('alerts.setUp')}</p>}
          <ErrorNote error={save.error ?? test.error} />
          {tested && <TestResult alert={tested} />}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" variant="primary" disabled={!dirty} busy={save.isPending}>
              {dirty ? t('common.save') : t('common.savedButton')}
            </Button>
            <Button type="button" disabled={channels === 0 && !data.forwarded} busy={test.isPending} onClick={() => test.mutate(form)}>
              <Send className="size-4" aria-hidden /> {t('alerts.test')}
            </Button>
          </div>
          {!data.links && <p className="text-xs text-muted">{t('alerts.links')}</p>}
        </div>
      </form>

      <section className="flex flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">{t('alerts.recent')}</h2>
        {data.history.length === 0 ? <Card className="text-sm text-muted">{t('alerts.none')}</Card> : <AlertList alerts={data.history} />}
      </section>

      <Metrics />
    </div>
  )
}

function RuleRow({ title, hint, on, onToggle, children }: { title: string; hint?: string; on: boolean; onToggle: (on: boolean) => void; children?: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 p-4">
      <Toggle
        checked={on}
        onChange={onToggle}
        label={
          <span>
            <span className="block font-medium">{title}</span>
            {hint && <span className="mt-0.5 block text-muted">{hint}</span>}
          </span>
        }
      />
      {on && children && <div className="flex flex-wrap gap-3 ps-12">{children}</div>}
    </div>
  )
}

function RateRow({ title, hint, rule, onChange }: { title: string; hint?: string; rule: RateRule; onChange: (r: RateRule) => void }) {
  const { t } = useTranslation()
  return (
    <RuleRow title={title} hint={hint} on={rule.on} onToggle={(on) => onChange({ ...rule, on })}>
      <NumberField label={t('alerts.howMany')} value={rule.count} min={1} max={10000} onChange={(count) => onChange({ ...rule, count })} />
      <NumberField label={t('alerts.within')} value={rule.minutes} min={1} max={1440} onChange={(minutes) => onChange({ ...rule, minutes })} />
    </RuleRow>
  )
}

function NumberField({ label, value, min, max, onChange }: { label: string; value: number; min: number; max: number; onChange: (v: number) => void }) {
  return (
    <div className="w-36">
      <Field label={label}>{(id) => <Input id={id} type="number" value={value || ''} min={min} max={max} onChange={(e) => onChange(Number(e.target.value) || 0)} />}</Field>
    </div>
  )
}

function AlertIcon({ a }: { a: Alert }) {
  const { t } = useTranslation()
  if (a.resolved) return <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" aria-label={t('alerts.resolved')} />
  if (a.level === 'critical') return <XCircle className="mt-0.5 size-4 shrink-0 text-bad" aria-label={t('alerts.critical')} />
  if (a.level === 'warning') return <AlertTriangle className="mt-0.5 size-4 shrink-0 text-gold" aria-label={t('alerts.warning')} />
  if (a.rule === 'test') return <BellRing className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
  return <Info className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
}

function AlertList({ alerts }: { alerts: Alert[] }) {
  const { t } = useTranslation()
  const [all, setAll] = useState(false)
  const shown = all ? alerts : alerts.slice(0, 8)
  return (
    <>
      <Card className="p-0!">
        <ul className="divide-y divide-line">
          {shown.map((a) => {
            const total = a.sent + (a.failed?.length ?? 0)
            return (
              <li key={a.id || a.key} className="flex gap-3 p-4 text-sm">
                <AlertIcon a={a} />
                <div className="min-w-0 flex-1">
                  <p className="break-words font-medium">{a.title}</p>
                  <p className="break-words text-muted">{a.message}</p>
                  <p className="mt-1 text-xs text-muted">
                    {ago(a.time)}
                    {a.id && <> · {total ? t('alerts.delivered', { sent: a.sent, count: total }) : t('alerts.nowhere')}</>}
                  </p>
                  {a.failed?.map((f) => (
                    <p key={f} className="mt-1 break-words text-xs text-bad">
                      {f}
                    </p>
                  ))}
                </div>
              </li>
            )
          })}
        </ul>
      </Card>
      {!all && alerts.length > shown.length && (
        <button className="self-start text-sm text-muted underline hover:text-ink" onClick={() => setAll(true)}>
          {t('alerts.more', { count: alerts.length - shown.length })}
        </button>
      )}
    </>
  )
}

function TestResult({ alert }: { alert: Alert }) {
  const { t } = useTranslation()
  const total = alert.sent + (alert.failed?.length ?? 0)
  const ok = !alert.failed?.length
  return (
    <Card className="flex flex-col gap-1 text-sm">
      <p role="status" className={`flex items-center gap-2 font-medium ${ok ? '' : 'text-bad'}`}>
        {ok ? <CircleCheck className="size-4 text-ok" aria-hidden /> : <XCircle className="size-4" aria-hidden />}
        {t('alerts.testDelivered', { sent: alert.sent, count: total })}
      </p>
      {alert.failed?.map((f) => (
        <p key={f} className="break-words text-xs text-bad">
          {f}
        </p>
      ))}
    </Card>
  )
}

// Prometheus metrics at /metrics, for the admin's own monitoring.
function Metrics() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { data } = useQuery({ queryKey: ['metricsSettings'], queryFn: () => get<MetricsSettings>('/admin/metrics') })
  const [copied, setCopied] = useState('')
  const on = useMutation({ mutationFn: () => post<MetricsSettings>('/admin/metrics/token'), onSuccess: (v) => qc.setQueryData(['metricsSettings'], v) })
  const off = useMutation({ mutationFn: () => del('/admin/metrics/token'), onSuccess: () => qc.setQueryData(['metricsSettings'], { on: false, path: '/metrics' }) })
  if (!data) return null
  const url = `${window.location.origin}${data.path}`
  const u = new URL(url)
  const config = `scrape_configs:
  - job_name: boxoffice
    scheme: ${u.protocol.replace(':', '')}
    metrics_path: ${data.path}
    authorization:
      credentials: ${data.token ?? ''}
    static_configs:
      - targets: ['${u.host}']`
  const copy = (what: string, text: string) => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(what)
      setTimeout(() => setCopied(''), 2000)
    })
  }
  return (
    <section className="flex flex-col gap-3">
      <div>
        <h2 className="font-display text-lg font-semibold">Prometheus</h2>
        <p className="mt-1 text-sm text-muted">{t('alerts.prometheusSub')}</p>
      </div>
      <Card className="flex flex-col gap-4">
        {data.on && data.token ? (
          <>
            <Copyable label={t('alerts.address')} value={url} copied={copied === 'url'} onCopy={() => copy('url', url)} />
            <Copyable label={t('alerts.tokenLabel')} value={data.token} copied={copied === 'token'} onCopy={() => copy('token', data.token!)} />
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-medium uppercase tracking-wide text-muted">{t('alerts.addTo')}</span>
                <button type="button" className="inline-flex items-center gap-1 text-xs text-muted hover:text-ink" onClick={() => copy('config', config)}>
                  <Copy className="size-3.5" aria-hidden /> {copied === 'config' ? t('common.copied') : t('common.copy')}
                </button>
              </div>
              <pre className="overflow-x-auto rounded-lg bg-paper p-3 font-mono text-xs">{config}</pre>
              <p className="text-xs text-muted">{t('alerts.k8s')}</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button type="button" busy={on.isPending} onClick={() => on.mutate()}>
                {t('alerts.newToken')}
              </Button>
              <Button type="button" variant="ghost" busy={off.isPending} onClick={() => off.mutate()}>
                {t('common.turnOff')}
              </Button>
            </div>
          </>
        ) : (
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-sm text-muted">{t('common.off')}</p>
            <Button type="button" busy={on.isPending} onClick={() => on.mutate()}>
              {t('common.turnOn')}
            </Button>
          </div>
        )}
        <ErrorNote error={on.error ?? off.error} />
      </Card>
    </section>
  )
}

function Copyable({ label, value, copied, onCopy }: { label: string; value: string; copied: boolean; onCopy: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-xs font-medium uppercase tracking-wide text-muted">{label}</span>
      <div className="flex gap-2">
        <Input readOnly className="font-mono" value={value} onFocus={(e) => e.target.select()} />
        <Button type="button" onClick={onCopy}>
          <Copy className="size-4" aria-hidden /> {copied ? t('common.copied') : t('common.copy')}
        </Button>
      </div>
    </div>
  )
}
