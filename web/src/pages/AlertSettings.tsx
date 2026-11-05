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
  { value: 'json', label: 'Other (JSON)' },
]

const placeholders: Record<WebhookKind, string> = {
  discord: 'https://discord.com/api/webhooks/…',
  ntfy: 'https://ntfy.sh/…',
  homeassistant: 'http://homeassistant.local:8123/api/webhook/…',
  json: 'https://…',
}

// Where to find each webhook's address, step by step.
const help: Record<WebhookKind, string[]> = {
  discord: ['In Discord, open the channel you want alerts in and click its gear (Edit Channel).', 'Open Integrations, then Webhooks, and click New Webhook.', 'Click Copy Webhook URL and paste it here.'],
  ntfy: [
    'Pick a long, hard-to-guess topic name, such as boxoffice-7f3k9q2m.',
    'Paste https://ntfy.sh/ followed by the topic here (or your own ntfy server instead of ntfy.sh).',
    'In the ntfy app on your phone, tap + and subscribe to the same topic.',
    'Only if the topic is protected: make an access token in ntfy under Account, and paste it below.',
  ],
  homeassistant: [
    'In Home Assistant, open Settings, then Automations & scenes, and click Create automation.',
    'Add a trigger, pick Webhook, and copy the webhook address it shows.',
    'Add the actions you want, such as a notification to your phone, and save.',
    'Paste the address here.',
  ],
  json: ['Box Office posts each alert as JSON with its title, message, level, rule, time and server.', "Paste the address that should receive it, starting with https:// or http://."],
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
  const keepsPassword = data.email.hasPassword && form.email.username === data.email.username && form.email.host === data.email.host

  return (
    <div className="flex max-w-3xl flex-col gap-8">
      <p className="text-muted">
        Get an email or a message when something needs you. Alerts go straight from Box Office to your own mail server or webhooks, and nowhere else.
        {data.forwarded && ' They also show up in Matinee for its admins.'}
      </p>

      {data.firing.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">Happening now</h2>
          <AlertList alerts={data.firing} />
        </section>
      )}

      <form onSubmit={submit} className="flex flex-col gap-8">
        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">What to alert on</h2>
          <Card className="flex flex-col divide-y divide-line p-0!">
            <RuleRow title="A disk is almost full" hint="Box Office's data, downloads, backups, or a root folder's disk." on={form.rules.disk.on} onToggle={(on) => change((f) => (f.rules.disk.on = on))}>
              <NumberField label="Percent full" value={form.rules.disk.percent} min={50} max={99} onChange={(v) => change((f) => (f.rules.disk.percent = v))} />
            </RuleRow>
            <RuleRow
              title="A root folder can't be read"
              hint="After 2 minutes, so a network share that drops for a moment doesn't count."
              on={form.rules.folder.on}
              onToggle={(on) => change((f) => (f.rules.folder.on = on))}
            />
            <RateRow title="Errors pile up" rule={form.rules.errors} onChange={(r) => change((f) => (f.rules.errors = r))} />
            <RateRow title="Downloads keep failing" hint="Each failed release is blocked and another looked for; this says when that keeps happening." rule={form.rules.downloads} onChange={(r) => change((f) => (f.rules.downloads = r))} />
            <RuleRow
              title="Box Office starts or restarts"
              hint="Says whether it was updated, or stopped without shutting down cleanly."
              on={form.rules.restart.on}
              onToggle={(on) => change((f) => (f.rules.restart.on = on))}
            />
            <RuleRow title="A backup fails" hint="When a scheduled database backup can't be made." on={form.rules.backup.on} onToggle={(on) => change((f) => (f.rules.backup.on = on))} />
          </Card>
        </section>

        <section className="flex flex-col gap-3">
          <h2 className="font-display text-lg font-semibold">Email</h2>
          <Card className="flex flex-col gap-4">
            <Toggle checked={form.email.on} onChange={(on) => change((f) => (f.email.on = on))} label={<span className="font-medium">Send alerts by email</span>} />
            {form.email.on && (
              <>
                <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_6rem_10rem]">
                  <Field label="Mail server" hint="Your email provider's SMTP server, such as smtp.gmail.com or smtp.fastmail.com.">
                    {(id) => <Input id={id} value={form.email.host} placeholder="smtp.example.com" autoComplete="off" spellCheck={false} onChange={(e) => change((f) => (f.email.host = e.target.value))} />}
                  </Field>
                  <Field label="Port">
                    {(id) => <Input id={id} inputMode="numeric" value={form.email.port || ''} onChange={(e) => change((f) => (f.email.port = Number(e.target.value.replace(/\D/g, '')) || 0))} />}
                  </Field>
                  <Field label="Connection">
                    {(id) => (
                      <Select
                        id={id}
                        value={form.email.security}
                        options={[
                          { value: 'starttls', label: 'STARTTLS' },
                          { value: 'tls', label: 'TLS' },
                          { value: 'none', label: 'Not encrypted' },
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
                  <Field label="Username" hint="Usually your full email address.">
                    {(id) => <Input id={id} value={form.email.username} autoComplete="off" spellCheck={false} onChange={(e) => change((f) => (f.email.username = e.target.value))} />}
                  </Field>
                  <Field label="Password" hint="For Gmail, iCloud and others with two-step sign-in, make an app password in your account's security settings and use that.">
                    {(id) => (
                      <Input
                        id={id}
                        type="password"
                        autoComplete="new-password"
                        value={form.email.password ?? ''}
                        placeholder={keepsPassword ? '•••••••• (saved)' : undefined}
                        onChange={(e) => change((f) => (f.email.password = e.target.value))}
                      />
                    )}
                  </Field>
                  <Field label="From">{(id) => <Input id={id} type="email" value={form.email.from} placeholder="boxoffice@example.com" onChange={(e) => change((f) => (f.email.from = e.target.value))} />}</Field>
                  <Field label="To" hint="Separate several addresses with commas.">
                    {(id) => <Input id={id} value={form.email.to} placeholder="you@example.com" onChange={(e) => change((f) => (f.email.to = e.target.value))} />}
                  </Field>
                </div>
              </>
            )}
          </Card>
        </section>

        <section className="flex flex-col gap-3">
          <div>
            <h2 className="font-display text-lg font-semibold">Webhooks</h2>
            <p className="mt-1 text-sm text-muted">Post alerts to Discord, ntfy, Home Assistant, or anything else that takes JSON.</p>
          </div>
          {form.webhooks.map((h, i) => (
            <Card key={h.key} className="flex flex-col gap-3">
              <div className="grid gap-3 sm:grid-cols-[12rem_minmax(0,1fr)]">
                <Field label="Kind">{(id) => <Select id={id} value={h.kind} options={kinds} onChange={(v) => change((f) => (f.webhooks[i].kind = v as WebhookKind))} />}</Field>
                <Field label="Name">
                  {(id) => <Input id={id} value={h.name ?? ''} placeholder={kinds.find((k) => k.value === h.kind)?.label} onChange={(e) => change((f) => (f.webhooks[i].name = e.target.value))} />}
                </Field>
              </div>
              <Field label="Address" hint={h.hint ? 'The saved address is kept unless you enter a new one.' : undefined}>
                {(id) => (
                  <Input id={id} value={h.url ?? ''} autoComplete="off" spellCheck={false} placeholder={h.hint || placeholders[h.kind]} onChange={(e) => change((f) => (f.webhooks[i].url = e.target.value))} />
                )}
              </Field>
              {h.kind === 'ntfy' && (
                <Field label="Access token (only for a protected topic)">
                  {(id) => (
                    <Input
                      id={id}
                      type="password"
                      autoComplete="new-password"
                      value={h.token ?? ''}
                      placeholder={h.hasToken && !h.url ? '•••••••• (saved)' : 'tk_…'}
                      onChange={(e) => change((f) => (f.webhooks[i].token = e.target.value))}
                    />
                  )}
                </Field>
              )}
              <details className="text-sm">
                <summary className="cursor-pointer text-muted hover:text-ink">Where do I find this?</summary>
                <ol className="mt-2 flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 pl-8 pr-4 text-muted">
                  {help[h.kind].map((s, n) => (
                    <li key={n}>{s}</li>
                  ))}
                </ol>
              </details>
              <div className="flex justify-end">
                <Button type="button" variant="ghost" onClick={() => change((f) => f.webhooks.splice(i, 1))}>
                  <Trash2 className="size-4" aria-hidden /> Remove
                </Button>
              </div>
            </Card>
          ))}
          <div>
            <Button type="button" disabled={form.webhooks.length >= 20} onClick={() => change((f) => f.webhooks.push({ key: `new${nextKey++}`, id: '', kind: 'discord', name: '', url: '', token: '' }))}>
              <Plus className="size-4" aria-hidden /> Add a webhook
            </Button>
          </div>
        </section>

        <div className="flex flex-col gap-3">
          {channels === 0 && <p className="text-sm text-muted">{data.forwarded ? 'Alerts show up in Matinee. Set up email or a webhook to get them elsewhere too.' : 'Set up email or a webhook to get alerts.'}</p>}
          <ErrorNote error={save.error ?? test.error} />
          {tested && <TestResult alert={tested} />}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" variant="primary" disabled={!dirty} busy={save.isPending}>
              {dirty ? 'Save' : 'Saved'}
            </Button>
            <Button type="button" disabled={channels === 0 && !data.forwarded} busy={test.isPending} onClick={() => test.mutate(form)}>
              <Send className="size-4" aria-hidden /> Send a test alert
            </Button>
          </div>
          {!data.links && <p className="text-xs text-muted">Alerts link back to this page once Box Office knows its address, which it learns the next time you open it.</p>}
        </div>
      </form>

      <section className="flex flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">Recent alerts</h2>
        {data.history.length === 0 ? <Card className="text-sm text-muted">No alerts yet.</Card> : <AlertList alerts={data.history} />}
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
      {on && children && <div className="flex flex-wrap gap-3 pl-12">{children}</div>}
    </div>
  )
}

function RateRow({ title, hint, rule, onChange }: { title: string; hint?: string; rule: RateRule; onChange: (r: RateRule) => void }) {
  return (
    <RuleRow title={title} hint={hint} on={rule.on} onToggle={(on) => onChange({ ...rule, on })}>
      <NumberField label="How many" value={rule.count} min={1} max={10000} onChange={(count) => onChange({ ...rule, count })} />
      <NumberField label="Within minutes" value={rule.minutes} min={1} max={1440} onChange={(minutes) => onChange({ ...rule, minutes })} />
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
  if (a.resolved) return <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" aria-label="Resolved" />
  if (a.level === 'critical') return <XCircle className="mt-0.5 size-4 shrink-0 text-bad" aria-label="Critical" />
  if (a.level === 'warning') return <AlertTriangle className="mt-0.5 size-4 shrink-0 text-gold" aria-label="Warning" />
  if (a.rule === 'test') return <BellRing className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
  return <Info className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
}

function AlertList({ alerts }: { alerts: Alert[] }) {
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
                    {a.id && <> · {total ? `delivered to ${a.sent} of ${total}` : 'nowhere to send it'}</>}
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
          Show {alerts.length - shown.length} more
        </button>
      )}
    </>
  )
}

function TestResult({ alert }: { alert: Alert }) {
  const total = alert.sent + (alert.failed?.length ?? 0)
  const ok = !alert.failed?.length
  return (
    <Card className="flex flex-col gap-1 text-sm">
      <p role="status" className={`flex items-center gap-2 font-medium ${ok ? '' : 'text-bad'}`}>
        {ok ? <CircleCheck className="size-4 text-ok" aria-hidden /> : <XCircle className="size-4" aria-hidden />}
        Test alert delivered to {alert.sent} of {total}.
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
        <p className="mt-1 text-sm text-muted">For Prometheus, Grafana and other monitoring on your network: the readings on the Server tab, at an address that needs a token.</p>
      </div>
      <Card className="flex flex-col gap-4">
        {data.on && data.token ? (
          <>
            <Copyable label="Address" value={url} copied={copied === 'url'} onCopy={() => copy('url', url)} />
            <Copyable label="Token" value={data.token} copied={copied === 'token'} onCopy={() => copy('token', data.token!)} />
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-medium uppercase tracking-wide text-muted">Add to prometheus.yml</span>
                <button type="button" className="inline-flex items-center gap-1 text-xs text-muted hover:text-ink" onClick={() => copy('config', config)}>
                  <Copy className="size-3.5" aria-hidden /> {copied === 'config' ? 'Copied' : 'Copy'}
                </button>
              </div>
              <pre className="overflow-x-auto rounded-lg bg-paper p-3 font-mono text-xs">{config}</pre>
              <p className="text-xs text-muted">In Kubernetes, Prometheus can reach Box Office at its service address inside the cluster (its service name, namespace and port 7272) instead of the address above.</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button type="button" busy={on.isPending} onClick={() => on.mutate()}>
                New token
              </Button>
              <Button type="button" variant="ghost" busy={off.isPending} onClick={() => off.mutate()}>
                Turn off
              </Button>
            </div>
          </>
        ) : (
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-sm text-muted">Off.</p>
            <Button type="button" busy={on.isPending} onClick={() => on.mutate()}>
              Turn on
            </Button>
          </div>
        )}
        <ErrorNote error={on.error ?? off.error} />
      </Card>
    </section>
  )
}

function Copyable({ label, value, copied, onCopy }: { label: string; value: string; copied: boolean; onCopy: () => void }) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-xs font-medium uppercase tracking-wide text-muted">{label}</span>
      <div className="flex gap-2">
        <Input readOnly className="font-mono" value={value} onFocus={(e) => e.target.select()} />
        <Button type="button" onClick={onCopy}>
          <Copy className="size-4" aria-hidden /> {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
    </div>
  )
}
