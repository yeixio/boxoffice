import { useTranslation } from 'react-i18next'
import { useEffect, useId, useRef, useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from 'react'
import { Film, LoaderCircle, X } from 'lucide-react'

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'plain' | 'danger' | 'ghost'; busy?: boolean }

export function Button({ variant = 'plain', busy, className = '', children, disabled, ...rest }: ButtonProps) {
  const look = {
    primary: 'bg-marquee text-marquee-ink hover:brightness-110 border-transparent',
    plain: 'bg-card text-ink border-line hover:border-ink/40',
    danger: 'bg-card text-bad border-bad/40 hover:bg-bad hover:text-marquee-ink',
    ghost: 'bg-transparent text-ink border-transparent hover:bg-line/60',
  }[variant]
  return (
    <button
      className={`inline-flex items-center justify-center gap-2 rounded-md border px-3.5 py-2 text-sm font-medium transition disabled:opacity-50 disabled:cursor-not-allowed ${look} ${className}`}
      disabled={disabled || busy}
      {...rest}
    >
      {busy && <LoaderCircle className="size-4 animate-spin" aria-hidden />}
      {children}
    </button>
  )
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <section className={`rounded-xl border border-line bg-card p-5 ${className}`}>{children}</section>
}

export function Heading({ title, sub, actions }: { title: string; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="font-display text-3xl font-bold tracking-tight">{title}</h1>
        {sub && <p className="mt-1 text-muted">{sub}</p>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  )
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: (id: string) => ReactNode }) {
  const id = useId()
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      {children(id)}
      {hint && <p className="text-xs text-muted">{hint}</p>}
    </div>
  )
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`w-full rounded-md border border-line bg-paper px-3 py-2 text-sm placeholder:text-muted/70 ${props.className ?? ''}`} />
}

export function Select({ value, onChange, options, id }: { value: string | number; onChange: (v: string) => void; options: { value: string | number; label: string }[]; id?: string }) {
  return (
    <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm">
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  )
}

export function Toggle({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: ReactNode; disabled?: boolean }) {
  return (
    <label className={`flex cursor-pointer items-start gap-3 ${disabled ? 'opacity-50' : ''}`}>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={`relative mt-0.5 h-5 w-9 shrink-0 rounded-full transition ${checked ? 'bg-marquee' : 'bg-line'}`}
      >
        <span className={`absolute top-0.5 size-4 rounded-full bg-card shadow transition ${checked ? 'start-4.5' : 'start-0.5'}`} />
      </button>
      <span className="text-sm">{label}</span>
    </label>
  )
}

export function Badge({ children, tone = 'plain' }: { children: ReactNode; tone?: 'plain' | 'ok' | 'bad' | 'gold' | 'marquee' }) {
  const look = {
    plain: 'border-line text-muted',
    ok: 'border-ok/40 text-ok',
    bad: 'border-bad/40 text-bad',
    gold: 'border-gold/50 text-gold',
    marquee: 'border-marquee/50 text-marquee',
  }[tone]
  return <span className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium ${look}`}>{children}</span>
}

export function Spinner({ label }: { label?: string }) {
  const { t } = useTranslation('common')
  return (
    <div className="flex items-center gap-2 py-10 text-muted" role="status">
      <LoaderCircle className="size-5 animate-spin" aria-hidden />
      {label ?? t('loading')}
    </div>
  )
}

export function ErrorNote({ error }: { error: unknown }) {
  if (!error) return null
  return (
    <p role="alert" className="rounded-md border border-bad/40 bg-bad/5 px-3 py-2 text-sm text-bad">
      {error instanceof Error ? error.message : String(error)}
    </p>
  )
}

export function Empty({ icon, title, children }: { icon?: ReactNode; title: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-xl border border-dashed border-line px-6 py-14 text-center">
      <div className="text-muted">{icon ?? <Film className="size-8" aria-hidden />}</div>
      <h2 className="font-display text-xl font-semibold">{title}</h2>
      {children && <div className="max-w-md text-sm text-muted">{children}</div>}
    </div>
  )
}

export function Poster({ src, title, className = '' }: { src?: string; title: string; className?: string }) {
  const [failed, setFailed] = useState('')
  return (
    <div className={`relative aspect-[2/3] overflow-hidden rounded-lg bg-line ${className}`}>
      {src && failed !== src ? (
        <img src={src} alt="" loading="lazy" className="size-full object-cover" onError={() => setFailed(src)} />
      ) : (
        <div className="flex size-full items-center justify-center p-3 text-center font-display text-sm text-muted">{title}</div>
      )}
    </div>
  )
}

export function Modal({ title, onClose, children, wide }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  const { t } = useTranslation('common')
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const d = ref.current
    if (d && !d.open) d.showModal?.()
  }, [])
  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onCancel={onClose}
      className={`m-auto w-[calc(100%-2rem)] ${wide ? 'max-w-3xl' : 'max-w-lg'} rounded-xl border border-line bg-card p-0 text-ink backdrop:bg-black/50`}
    >
      <div className="flex items-center justify-between border-b border-line px-5 py-3">
        <h2 className="font-display text-lg font-semibold">{title}</h2>
        <button onClick={onClose} className="rounded p-1 hover:bg-line" aria-label={t('close')}>
          <X className="size-5" />
        </button>
      </div>
      <div className="max-h-[75vh] overflow-y-auto p-5">{children}</div>
    </dialog>
  )
}
