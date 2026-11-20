// The single sign-on button on the sign-in pages, and what's said when a
// trip to the provider didn't sign anyone in.

import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { KeyRound } from 'lucide-react'
import { Button } from './ui'

const outcomes = ['refused', 'notallowed', 'failed'] as const
type Outcome = (typeof outcomes)[number]

// outcome reads how the trip ended from the address (?sso=…).
export function outcome(search: string): Outcome | null {
  const v = new URLSearchParams(search).get('sso')
  return outcomes.includes(v as Outcome) ? (v as Outcome) : null
}

// How the trip that brought the browser to this page ended, read once as
// the page loads and taken out of the address, so a reload doesn't say it
// again.
const arrived = typeof window === 'undefined' ? null : outcome(window.location.search)
if (arrived) {
  const q = new URLSearchParams(window.location.search)
  q.delete('sso')
  window.history.replaceState(null, '', window.location.pathname + (q.size ? `?${q}` : '') + window.location.hash)
}

// SsoButton offers single sign-on under the provider's name, when the
// server has it on. what is admin for Box Office itself or member for the
// request site.
// Or is the line between the password form and the other ways in.
export function Or() {
  const { t } = useTranslation('common')
  return (
    <div className="flex items-center gap-3 text-xs text-muted" aria-hidden>
      <span className="h-px flex-1 bg-line" />
      {t('sso.or')}
      <span className="h-px flex-1 bg-line" />
    </div>
  )
}

export function SsoButton({ name, what }: { name?: string; what: 'admin' | 'member' }) {
  const { t } = useTranslation('common')
  const went = arrived
  const [busy, setBusy] = useState(false)
  return (
    <>
      {went && (
        <p role="alert" className="rounded-md border border-bad/40 bg-bad/5 px-3 py-2 text-sm text-bad">
          {t(`sso.${went}`)}
        </p>
      )}
      {name && (
        <>
          <Button
            type="button"
            busy={busy}
            onClick={() => {
              setBusy(true)
              window.location.assign(`/api/sso/start?for=${what}`)
            }}
          >
            <KeyRound className="size-4" aria-hidden /> {t('sso.signIn', { name })}
          </Button>
        </>
      )}
    </>
  )
}
