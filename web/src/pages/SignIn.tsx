import { LanguageMenu } from '../LanguagePicker'
import { useTranslation } from 'react-i18next'
import { useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Clapperboard } from 'lucide-react'
import { ApiError, post } from '../api'
import { browserZone } from '../format'
import { PasskeyButton } from '../Passkeys'
import { Or, SsoButton } from '../Sso'
import { Button, ErrorNote, Field, Input } from '../ui'

// SignIn signs an admin in, or on first start makes the first admin.
export function SignIn({ firstRun, sso, passkeys }: { firstRun: boolean; sso?: string; passkeys?: boolean }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  // The second step, once the server asks for it: the code from the
  // authenticator app.
  const [needsCode, setNeedsCode] = useState(false)
  const [code, setCode] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError(null)
    if (firstRun && password !== confirm) {
      setError(new Error(t('signIn.mismatch')))
      return
    }
    setBusy(true)
    try {
      // Setting up also says which time zone this browser is in, for a
      // server that doesn't know its own.
      await post(firstRun ? '/setup' : '/login', firstRun ? { username, password, timeZone: browserZone() } : { username, password, code: needsCode ? code : undefined })
      await qc.invalidateQueries({ queryKey: ['status'] })
    } catch (err) {
      // The password was right and a code is wanted: ask for it, without
      // calling that a mistake.
      const asked = err instanceof ApiError && (err.data as { needsCode?: boolean } | undefined)?.needsCode === true
      if (asked && !needsCode) setNeedsCode(true)
      else setError(err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <form onSubmit={submit} className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <span className="flex size-14 items-center justify-center rounded-xl bg-marquee text-marquee-ink">
            <Clapperboard className="size-7" aria-hidden />
          </span>
          <h1 className="font-display text-3xl font-bold uppercase tracking-[0.18em]">Box Office</h1>
          <p className="text-muted">{firstRun ? t('signIn.welcome') : t('signIn.intro')}</p>
        </div>
        <div className="flex flex-col gap-4 rounded-xl border border-line bg-card p-6">
          <Field label={t('signIn.username')}>{(id) => <Input id={id} autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus />}</Field>
          <Field label={t('signIn.password')} hint={firstRun ? t('signIn.passwordHint') : undefined}>
            {(id) => <Input id={id} type="password" autoComplete={firstRun ? 'new-password' : 'current-password'} value={password} onChange={(e) => setPassword(e.target.value)} required />}
          </Field>
          {firstRun && (
            <Field label={t('signIn.passwordAgain')}>{(id) => <Input id={id} type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />}</Field>
          )}
          {needsCode && (
            <Field label={t('signIn.code')} hint={t('signIn.codeHint')}>
              {(id) => <Input id={id} inputMode="numeric" autoComplete="one-time-code" maxLength={20} value={code} onChange={(e) => setCode(e.target.value)} required autoFocus />}
            </Field>
          )}
          <ErrorNote error={error} />
          <Button variant="primary" type="submit" busy={busy}>
            {firstRun ? t('signIn.make') : t('signIn.signIn')}
          </Button>
          {!firstRun && (sso || passkeys) && <Or />}
          {!firstRun && passkeys && <PasskeyButton calls={{ post }} onSignedIn={() => qc.invalidateQueries({ queryKey: ['status'] })} />}
          {!firstRun && <SsoButton name={sso} what="admin" />}
        </div>
        <div className="mt-6 flex justify-center">
          <LanguageMenu />
        </div>
      </form>
    </div>
  )
}
