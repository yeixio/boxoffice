// Passkeys on a page: the button that signs in with one, and the list in
// someone's settings where they add and remove their own. The admin's
// pages and the request site both use these, each with its own server
// calls.

import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Fingerprint, Trash2 } from 'lucide-react'
import { day } from './format'
import { PasskeyProblem, deviceName, makePasskey, passkeySignIn, supported, type Calls, type Passkey } from './passkey'
import { Button, ErrorNote, Field, Input } from './ui'

// usePasskeyError turns what went wrong into words: a passkey that wasn't
// used says so in the page's language, anything else is the server's.
function usePasskeyError() {
  const { t } = useTranslation('common')
  return (err: unknown) => (err instanceof PasskeyProblem ? new Error(t(`passkey.${err.why}`)) : err)
}

// PasskeyButton signs in with a passkey, where the server says there is
// one for this address.
export function PasskeyButton({ calls, onSignedIn }: { calls: Calls; onSignedIn: () => void | Promise<void> }) {
  const { t } = useTranslation('common')
  const say = usePasskeyError()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  if (!supported()) return null
  const go = async () => {
    setBusy(true)
    setError(null)
    try {
      await passkeySignIn(calls)
      await onSignedIn()
    } catch (err) {
      setError(say(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Button type="button" busy={busy} onClick={go}>
        <Fingerprint className="size-4" aria-hidden /> {t('passkey.signIn')}
      </Button>
      <ErrorNote error={error} />
    </>
  )
}

type Listing = { passkeys: Passkey[]; host: string }

// PasskeyList shows the signed-in account's passkeys, adds one on this
// device and removes ones that are gone. queryKey tells the two sites'
// lists apart.
export function PasskeyList({ calls, list, remove, queryKey }: { calls: Calls; list: () => Promise<Listing>; remove: (id: number) => Promise<void>; queryKey: string[] }) {
  const { t } = useTranslation('common')
  const qc = useQueryClient()
  const say = usePasskeyError()
  const [name, setName] = useState(deviceName)
  const have = useQuery({ queryKey, queryFn: list })
  const reload = () => qc.invalidateQueries({ queryKey })
  const add = useMutation({ mutationFn: () => makePasskey(calls, name.trim()), onSuccess: reload })
  const drop = useMutation({ mutationFn: remove, onSuccess: reload })
  const can = supported()
  return (
    <div className="flex flex-col gap-3">
      <ErrorNote error={have.error} />
      {have.data && have.data.passkeys.length === 0 && <p className="text-sm text-muted">{t('passkey.none')}</p>}
      {have.data && have.data.passkeys.length > 0 && (
        <ul className="flex flex-col divide-y divide-line rounded-md border border-line">
          {have.data.passkeys.map((p) => (
            <li key={p.id} className="flex items-center gap-3 px-3 py-2 text-sm">
              <Fingerprint className="size-4 shrink-0 text-muted" aria-hidden />
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium">{p.name || t('passkey.unnamed')}</div>
                <div className="text-xs text-muted">
                  {t('passkey.added', { date: day(p.createdAt) })} · {p.usedAt ? t('passkey.used', { date: day(p.usedAt) }) : t('passkey.never')}
                  {p.host !== have.data.host && <> · {t('passkey.otherHost', { host: p.host })}</>}
                </div>
              </div>
              <Button variant="ghost" busy={drop.isPending && drop.variables === p.id} onClick={() => drop.mutate(p.id)} aria-label={t('passkey.removeNamed', { name: p.name || t('passkey.unnamed') })}>
                <Trash2 className="size-4" aria-hidden /> {t('passkey.remove')}
              </Button>
            </li>
          ))}
        </ul>
      )}
      <ErrorNote error={drop.error} />
      {can ? (
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-48 flex-1">
            <Field label={t('passkey.name')}>{(id) => <Input id={id} maxLength={60} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
          </div>
          <Button busy={add.isPending} onClick={() => add.mutate()}>
            <Fingerprint className="size-4" aria-hidden /> {t('passkey.add')}
          </Button>
        </div>
      ) : (
        <p role="note" className="rounded-md border border-line px-3 py-2 text-sm text-muted">
          {t('passkey.unsupported')}
        </p>
      )}
      <ErrorNote error={add.error ? say(add.error) : null} />
    </div>
  )
}
