import { useTranslation } from 'react-i18next'
import { useEffect, useState } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'

// usePageSize is how many rows a list shows at a time: a few on a phone,
// more as the window grows. [phone, tablet, desktop].
export function usePageSize(sizes: [number, number, number]) {
  const pick = () => {
    if (typeof window === 'undefined' || !window.matchMedia) return sizes[2]
    if (window.matchMedia('(min-width: 1024px)').matches) return sizes[2]
    if (window.matchMedia('(min-width: 640px)').matches) return sizes[1]
    return sizes[0]
  }
  const [n, setN] = useState(pick)
  useEffect(() => {
    const on = () => setN(pick())
    window.addEventListener('resize', on)
    return () => window.removeEventListener('resize', on)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sizes[0], sizes[1], sizes[2]])
  return n
}

// Pager moves between pages of a list. It shows only when there's more
// than one page.
export function Pager({ page, size, total, onPage, label }: { page: number; size: number; total: number; onPage: (p: number) => void; label: string }) {
  const { t } = useTranslation('common')
  const pages = Math.max(1, Math.ceil(total / size))
  if (pages <= 1) return null
  const from = page * size + 1
  const to = Math.min(total, from + size - 1)
  const btn = 'inline-flex min-h-9 min-w-9 items-center justify-center rounded-md border border-line bg-card text-sm hover:border-ink/40 disabled:cursor-not-allowed disabled:opacity-40'
  return (
    <nav className="mt-3 flex items-center justify-between gap-3 text-sm text-muted" aria-label={t('pager.pages', { label })}>
      <span className="tabular-nums">
        {t('pager.range', { from, to, total })}
      </span>
      <div className="flex items-center gap-2">
        <button className={btn} disabled={page === 0} onClick={() => onPage(page - 1)} aria-label={t('pager.previous')}>
          <ChevronLeft className="size-4 rtl:rotate-180" />
        </button>
        <span className="tabular-nums">
          {t('pager.page', { page: page + 1, pages })}
        </span>
        <button className={btn} disabled={page >= pages - 1} onClick={() => onPage(page + 1)} aria-label={t('pager.next')}>
          <ChevronRight className="size-4 rtl:rotate-180" />
        </button>
      </div>
    </nav>
  )
}
