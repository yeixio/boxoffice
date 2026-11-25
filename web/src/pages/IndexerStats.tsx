import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { get } from '../api'
import { Card, ErrorNote } from '../ui'

// One indexer's numbers (see search.IndexerStats).
type IndexerStatsRow = {
  id: number
  name: string
  queries: number
  auto: number
  nzbs: number
  errors: number
  answerMs: number
  grabbed: number
  completed: number
  failed: number
  today: number
  queryLimit: number
}

// answerTime says 840 ms or 1.2 s the language's way.
export function answerTime(ms: number, lang: string): string {
  if (ms < 1000) return new Intl.NumberFormat(lang, { style: 'unit', unit: 'millisecond', maximumFractionDigits: 0 }).format(ms)
  return new Intl.NumberFormat(lang, { style: 'unit', unit: 'second', maximumFractionDigits: 1 }).format(ms / 1000)
}

// share says what part of a whole something is: 12%.
export function share(part: number, whole: number, lang: string): string {
  return new Intl.NumberFormat(lang, { style: 'percent', maximumFractionDigits: 0 }).format(whole > 0 ? part / whole : 0)
}

function Stat({ label, value, sub, tone }: { label: string; value: ReactNode; sub?: ReactNode; tone?: 'bad' }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className={`text-base font-medium tabular-nums ${tone === 'bad' ? 'text-bad' : ''}`}>{value}</dd>
      {sub && <dd className="text-xs text-muted">{sub}</dd>}
    </div>
  )
}

// IndexerStatsCard shows, for each indexer, what it was asked lately, how
// it answered, and how the downloads grabbed from it ended.
export function IndexerStatsCard() {
  const { t, i18n } = useTranslation()
  const stats = useQuery({ queryKey: ['indexer-stats'], queryFn: () => get<{ days: number; indexers: IndexerStatsRow[] }>('/indexers/stats') })
  const lang = i18n.language
  const n = (v: number) => v.toLocaleString(lang)
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('indexerStats.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('indexerStats.sub', { days: stats.data?.days ?? 30 })}</p>
      <ErrorNote error={stats.error} />
      {stats.data?.indexers.length === 0 && <p className="text-sm text-muted">{t('indexerStats.none')}</p>}
      <ul className="flex flex-col divide-y divide-line">
        {stats.data?.indexers.map((ix) => (
          <li key={ix.id} className="py-3 first:pt-0 last:pb-0" data-testid="indexer-stats-row">
            <h3 className="mb-2 font-medium">{ix.name}</h3>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4">
              <Stat label={t('indexerStats.searches')} value={n(ix.queries)} sub={ix.auto > 0 ? t('indexerStats.auto', { n: n(ix.auto) }) : undefined} />
              <Stat
                label={t('log.error')}
                value={n(ix.errors)}
                sub={ix.errors > 0 ? share(ix.errors, ix.queries, lang) : undefined}
                tone={ix.queries > 0 && ix.errors / ix.queries >= 0.2 ? 'bad' : undefined}
              />
              <Stat label={t('indexerStats.answer')} value={ix.answerMs > 0 ? answerTime(ix.answerMs, lang) : '–'} />
              <Stat label={t('indexerStats.grabbed')} value={n(ix.grabbed)} />
              <Stat label={t('dl.status.completed')} value={n(ix.completed)} sub={ix.grabbed > 0 ? share(ix.completed, ix.grabbed, lang) : undefined} />
              <Stat
                label={t('dl.status.failed')}
                value={n(ix.failed)}
                sub={ix.failed > 0 ? share(ix.failed, ix.grabbed, lang) : undefined}
                tone={ix.grabbed > 0 && ix.failed / ix.grabbed >= 0.5 ? 'bad' : undefined}
              />
              <Stat label={t('indexerStats.today')} value={ix.queryLimit > 0 ? t('dl.of', { done: n(ix.today), total: n(ix.queryLimit) }) : n(ix.today)} />
            </dl>
          </li>
        ))}
      </ul>
      {!!stats.data?.indexers.length && <p className="mt-4 text-xs text-muted">{t('indexerStats.note')}</p>}
    </Card>
  )
}
