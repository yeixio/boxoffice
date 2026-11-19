import { Component, type ErrorInfo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { TriangleAlert } from 'lucide-react'
import { Button } from './ui'

// PageBoundary keeps a page that breaks while drawing from blanking the
// whole app: the menu stays, and the page's place says what went wrong, with
// the error's text for a bug report. Moving to another page (a new at)
// clears it.
export class PageBoundary extends Component<{ at: string; children: ReactNode }, { error: unknown; at: string }> {
  state = { error: null as unknown, at: this.props.at }

  static getDerivedStateFromError(error: unknown) {
    return { error }
  }

  static getDerivedStateFromProps(props: { at: string }, state: { error: unknown; at: string }) {
    return props.at === state.at ? null : { error: null, at: props.at }
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error('Box Office: this page broke while drawing', error, info.componentStack)
  }

  render() {
    return this.state.error ? <BrokenPage error={this.state.error} /> : this.props.children
  }
}

function BrokenPage({ error }: { error: unknown }) {
  const { t } = useTranslation('common')
  const text = error instanceof Error ? `${error.name}: ${error.message}` : String(error)
  return (
    <div role="alert" className="mx-auto flex max-w-xl flex-col items-center gap-3 rounded-xl border border-dashed border-line px-6 py-14 text-center">
      <TriangleAlert className="size-8 text-bad" aria-hidden />
      <h1 className="font-display text-xl font-semibold">{t('broken.title')}</h1>
      <p className="text-sm text-muted">{t('broken.body')}</p>
      <pre dir="ltr" className="max-h-40 w-full overflow-auto rounded-md border border-line bg-card px-3 py-2 text-start text-xs whitespace-pre-wrap select-all">
        {text}
      </pre>
      <Button variant="primary" onClick={() => window.location.reload()}>
        {t('broken.reload')}
      </Button>
    </div>
  )
}

// SkipLink is the first thing Tab reaches, and jumps past the menu to the
// page itself. It only shows while it has the focus.
export function SkipLink() {
  const { t } = useTranslation('common')
  return (
    <a
      href="#content"
      onClick={(e) => {
        // Move the focus without changing the address.
        e.preventDefault()
        document.getElementById('content')?.focus()
      }}
      className="sr-only focus:not-sr-only focus:fixed focus:start-3 focus:top-3 focus:z-50 focus:rounded-md focus:bg-card focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:shadow"
    >
      {t('skip')}
    </a>
  )
}
