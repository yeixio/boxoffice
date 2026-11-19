import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy } from 'lucide-react'
import { get, put } from '../api'
import { Button, Card, ErrorNote, Field, Input, Toggle } from '../ui'

type SsoSettings = {
  enabled: boolean
  name: string
  issuer: string
  clientId: string
  admins: string[]
  adminGroup: string
  members: boolean
  memberGroup: string
  hasSecret: boolean
  portalHost: string
}

// What a trial sign-in found: who the provider says it was, and what that
// account would be let in to.
type Trial = {
  identity: { subject: string; email?: string; username?: string; name?: string; groups: string[] }
  admin: boolean
  member: boolean
  error?: string
}

// lines reads a box with one account on each line.
export const lines = (s: string) =>
  s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean)

// SsoCard sets up signing in through an OpenID Connect provider, for Box
// Office itself and for the request site.
export function SsoCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const saved = useQuery({ queryKey: ['sso'], queryFn: () => get<SsoSettings>('/sso') })
  const [form, setForm] = useState<SsoSettings | null>(null)
  const [admins, setAdmins] = useState('')
  const [secret, setSecret] = useState('')
  const [copied, setCopied] = useState('')
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const [trial, setTrial] = useState<Trial | null>(null)
  const [trialError, setTrialError] = useState<unknown>(null)
  useEffect(() => {
    if (saved.data && !form) {
      setForm(saved.data)
      setAdmins(saved.data.admins.join('\n'))
    }
  }, [saved.data, form])
  // Back from a trial at the provider: fetch what it found, once.
  const state = params.get('sso_test')
  useEffect(() => {
    if (!state) return
    get<Trial>(`/sso/test/${encodeURIComponent(state)}`)
      .then(setTrial)
      .catch(setTrialError)
    const next = new URLSearchParams(params)
    next.delete('sso_test')
    // Stay at this card: the address keeps its #sso.
    void navigate({ search: next.toString(), hash: '#sso' }, { replace: true })
    // Only when a trial comes back.
  }, [state])
  const save = useMutation({
    mutationFn: (v: SsoSettings & { list?: string[] }) => put<SsoSettings>('/sso', { ...v, admins: v.list ?? lines(admins), clientSecret: secret }),
    onSuccess: (v) => {
      qc.setQueryData(['sso'], v)
      setForm(v)
      setAdmins(v.admins.join('\n'))
      setSecret('')
      void qc.invalidateQueries({ queryKey: ['status'] })
    },
  })
  if (!form) {
    return (
      <Card>
        <h2 className="mb-1 font-display text-lg font-semibold">{t('sso.title')}</h2>
        <ErrorNote error={saved.error} />
      </Card>
    )
  }
  const set = (patch: Partial<SsoSettings>) => setForm({ ...form, ...patch })
  const copy = (v: string) => {
    void navigator.clipboard?.writeText(v)
    setCopied(v)
    setTimeout(() => setCopied(''), 1500)
  }
  // The address the provider sends people back to: this one, and the
  // request site's when it has a host name of its own.
  const redirects = [`${window.location.origin}/api/sso/callback`]
  if (form.portalHost) redirects.push(`${window.location.protocol}//${form.portalHost}/api/sso/callback`)
  const changed = JSON.stringify({ ...form, admins: lines(admins) }) !== JSON.stringify(saved.data) || secret !== ''
  const who = trial && (trial.identity.email || trial.identity.username || trial.identity.subject)
  // What to list for the account a trial found: its address if it has one.
  const addMe = () => {
    if (!who) return
    save.mutate({ ...form, list: [...lines(admins), who] })
    setTrial({ ...trial!, admin: true })
  }
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('sso.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('sso.sub')}</p>
      <div className="flex flex-col gap-4">
        <div>
          <div className="mb-1 text-sm font-medium">{t('sso.redirect')}</div>
          <ul className="flex flex-col gap-1">
            {redirects.map((r) => (
              <li key={r} className="flex items-center gap-2 text-sm">
                <code className="min-w-0 break-all">{r}</code>
                <button type="button" onClick={() => copy(r)} aria-label={t('settings.copyThe', { what: r })} className="rounded p-1 text-muted hover:bg-line">
                  <Copy className="size-3.5" aria-hidden />
                </button>
                {copied === r && <span className="text-xs text-ok">{t('common.copied')}</span>}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-xs text-muted">{t('sso.redirectHint')}</p>
        </div>
        <Field label={t('sso.issuer')} hint={t('sso.issuerHint')}>
          {(id) => <Input id={id} type="url" inputMode="url" placeholder="https://auth.example.com" value={form.issuer} onChange={(e) => set({ issuer: e.target.value })} />}
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('sso.clientId')}>{(id) => <Input id={id} autoComplete="off" value={form.clientId} onChange={(e) => set({ clientId: e.target.value })} />}</Field>
          <Field label={t('sso.secret')} hint={form.hasSecret ? t('sso.secretKept') : undefined}>
            {(id) => <Input id={id} type="password" autoComplete="off" value={secret} placeholder={form.hasSecret ? '••••••••' : ''} onChange={(e) => setSecret(e.target.value)} />}
          </Field>
        </div>
        <Field label={t('sso.name')} hint={t('sso.nameHint')}>
          {(id) => <Input id={id} maxLength={40} placeholder="Authentik" value={form.name} onChange={(e) => set({ name: e.target.value })} />}
        </Field>
        <Field label={t('sso.admins')} hint={t('sso.adminsHint')}>
          {(id) => (
            <textarea id={id} rows={3} value={admins} onChange={(e) => setAdmins(e.target.value)} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm" spellCheck={false} />
          )}
        </Field>
        <Field label={t('sso.adminGroup')} hint={t('sso.groupHint')}>
          {(id) => <Input id={id} autoComplete="off" value={form.adminGroup} onChange={(e) => set({ adminGroup: e.target.value })} />}
        </Field>
        <Toggle checked={form.members} onChange={(v) => set({ members: v })} label={t('sso.members')} />
        {form.members && (
          <Field label={t('sso.memberGroup')} hint={t('sso.memberGroupHint')}>
            {(id) => <Input id={id} autoComplete="off" value={form.memberGroup} onChange={(e) => set({ memberGroup: e.target.value })} />}
          </Field>
        )}
        <Toggle checked={form.enabled} onChange={(v) => set({ enabled: v })} label={t('sso.on')} />
        {form.enabled && lines(admins).length === 0 && !form.adminGroup.trim() && (
          <p role="note" className="rounded-md border border-line px-3 py-2 text-sm text-muted">
            {t('sso.noAdmins')}
          </p>
        )}
        <ErrorNote error={save.error} />
        <div className="flex flex-wrap items-center justify-end gap-3">
          <span className="min-w-0 flex-1 text-xs text-muted">{t('sso.tryHint')}</span>
          {/* A trial goes by what's saved, so it waits for a save. */}
          <Button disabled={changed || !saved.data?.issuer || !saved.data?.clientId} onClick={() => window.location.assign('/api/sso/start?for=test')}>
            {t('sso.try')}
          </Button>
          <Button variant="primary" busy={save.isPending} disabled={!changed} onClick={() => save.mutate(form)}>
            {t('common.save')}
          </Button>
        </div>
        <ErrorNote error={trialError} />
        {trial && (
          <div role="status" className="rounded-md border border-line px-3 py-2 text-sm">
            {trial.error ? (
              <p className="text-bad">{t('sso.tryFailed', { error: trial.error })}</p>
            ) : (
              <div className="flex flex-col gap-1">
                <p>{t('sso.found', { who: [trial.identity.name, trial.identity.username, trial.identity.email].filter(Boolean).join(' · ') || trial.identity.subject })}</p>
                <p className="text-muted">{trial.identity.groups.length ? t('sso.foundGroups', { groups: trial.identity.groups.join(', ') }) : t('sso.noGroups')}</p>
                <p className={trial.admin ? 'text-ok' : ''}>{t(trial.admin ? 'sso.isAdmin' : 'sso.notAdmin')}</p>
                {form.members && <p className={trial.member ? 'text-ok' : ''}>{t(trial.member ? 'sso.isMember' : 'sso.notMember')}</p>}
                {!trial.admin && (
                  <div className="mt-1">
                    <Button busy={save.isPending} onClick={addMe}>
                      {t('sso.addMe')}
                    </Button>
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </Card>
  )
}
