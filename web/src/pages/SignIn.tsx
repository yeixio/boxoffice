import { useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Clapperboard } from 'lucide-react'
import { post } from '../api'
import { Button, ErrorNote, Field, Input } from '../ui'

// SignIn signs an admin in, or on first start makes the first admin.
export function SignIn({ firstRun }: { firstRun: boolean }) {
  const qc = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError(null)
    if (firstRun && password !== confirm) {
      setError(new Error("The passwords don't match."))
      return
    }
    setBusy(true)
    try {
      await post(firstRun ? '/setup' : '/login', { username, password })
      await qc.invalidateQueries({ queryKey: ['status'] })
    } catch (err) {
      setError(err)
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
          <p className="text-muted">{firstRun ? 'Welcome. Make the admin account to get started.' : 'Sign in to manage your movies and shows.'}</p>
        </div>
        <div className="flex flex-col gap-4 rounded-xl border border-line bg-card p-6">
          <Field label="Username">{(id) => <Input id={id} autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus />}</Field>
          <Field label="Password" hint={firstRun ? 'At least 8 characters.' : undefined}>
            {(id) => <Input id={id} type="password" autoComplete={firstRun ? 'new-password' : 'current-password'} value={password} onChange={(e) => setPassword(e.target.value)} required />}
          </Field>
          {firstRun && (
            <Field label="Password again">{(id) => <Input id={id} type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />}</Field>
          )}
          <ErrorNote error={error} />
          <Button variant="primary" type="submit" busy={busy}>
            {firstRun ? 'Make the account' : 'Sign in'}
          </Button>
        </div>
      </form>
    </div>
  )
}
