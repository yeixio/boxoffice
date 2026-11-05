import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { AlertTriangle, CircleCheck, Info, RefreshCw, XCircle } from 'lucide-react'
import { get, type Health as HealthData, type HealthIssue, type LogLevel } from '../api'
import { when } from '../format'
import { Button, Card, ErrorNote, Heading, Spinner } from '../ui'
import { ServerMonitor } from './ServerMonitor'
import { LogViewer, type LogSearch } from './LogViewer'
import { AlertSettings } from './AlertSettings'

// useHealth is Box Office's own check-up, looked at again now and then.
export function useHealth() {
  return useQuery({ queryKey: ['health'], queryFn: () => get<HealthData>('/health'), refetchInterval: 5 * 60_000, staleTime: 60_000 })
}

// needsAttention counts the issues worth a badge: errors and warnings.
export const needsAttention = (h?: HealthData) => h?.issues.filter((i) => i.level !== 'notice').length ?? 0

const look = {
  error: { icon: XCircle, tone: 'text-bad', label: 'health.error' },
  warning: { icon: AlertTriangle, tone: 'text-gold', label: 'health.warning' },
  notice: { icon: Info, tone: 'text-muted', label: 'health.notice' },
}

// Health has four tabs: the check-up (problems an admin can fix, and how),
// the server (how busy it is, its disks and what it logged), the log, and
// alerts.
const tabs = [
  { id: 'checkup', label: 'health.tabs.checkup' },
  { id: 'server', label: 'health.tabs.server' },
  { id: 'log', label: 'health.tabs.log' },
  { id: 'alerts', label: 'health.tabs.alerts' },
] as const
type Tab = (typeof tabs)[number]['id']

const subs: Record<Tab, string> = {
  checkup: 'health.subs.checkup',
  server: 'health.subs.server',
  log: 'health.subs.log',
  alerts: 'health.subs.alerts',
}

export function Health() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const tab: Tab = tabs.some((x) => x.id === params.get('tab')) ? (params.get('tab') as Tab) : 'checkup'
  const [search, setSearch] = useState<LogSearch>({ q: '', n: 0 })
  const go = (to: Tab) => setParams(to === 'checkup' ? {} : { tab: to }, { replace: true })
  const showLog = (q: string, level?: LogLevel) => {
    setSearch((s) => ({ q, level, n: s.n + 1 }))
    go('log')
  }
  return (
    <>
      <Heading title={t('nav.health')} sub={t(subs[tab])} actions={tab === 'checkup' ? <CheckAgain /> : undefined} />
      <div className="mb-6 flex gap-1 overflow-x-auto border-b border-line" role="tablist" aria-label={t('nav.health')}>
        {tabs.map((x) => (
          <button
            key={x.id}
            role="tab"
            aria-selected={tab === x.id}
            onClick={() => go(x.id)}
            className={`-mb-px shrink-0 border-b-2 px-3 py-2 text-sm font-medium transition ${tab === x.id ? 'border-marquee text-ink' : 'border-transparent text-muted hover:text-ink'}`}
          >
            {t(x.label)}
          </button>
        ))}
      </div>
      <div role="tabpanel">
        {tab === 'checkup' && <Checkup />}
        {tab === 'server' && <ServerMonitor onShowLog={showLog} />}
        {tab === 'log' && <LogViewer search={search} />}
        {tab === 'alerts' && <AlertSettings />}
      </div>
    </>
  )
}

function CheckAgain() {
  const { t } = useTranslation()
  const h = useHealth()
  return (
    <Button busy={h.isFetching} onClick={() => h.refetch()}>
      <RefreshCw className="size-4" /> {t('health.checkAgain')}
    </Button>
  )
}

function Checkup() {
  const { t } = useTranslation()
  const h = useHealth()
  return (
    <>
      <ErrorNote error={h.error} />
      {h.isPending && <Spinner label={t('health.checking')} />}
      {h.data && (
        <>
          {h.data.issues.length === 0 ? (
            <Card className="flex items-center gap-3">
              <CircleCheck className="size-6 text-ok" aria-hidden />
              <div>
                <div className="font-medium">{t('health.allGood')}</div>
                <div className="text-sm text-muted">{t('health.nothing')}</div>
              </div>
            </Card>
          ) : (
            <div className="flex flex-col gap-4">
              {h.data.issues.map((i) => (
                <Issue key={i.id} issue={i} />
              ))}
            </div>
          )}
          <p className="mt-6 text-xs text-muted">{t('health.checked', { when: when(h.data.checked) })}</p>
        </>
      )}
    </>
  )
}

function Issue({ issue }: { issue: HealthIssue }) {
  const { t } = useTranslation()
  const l = look[issue.level]
  const Icon = l.icon
  return (
    <Card>
      <div className="flex gap-3">
        <Icon className={`mt-0.5 size-5 shrink-0 ${l.tone}`} aria-label={t(l.label)} />
        <div className="min-w-0 flex-1">
          <h2 className="font-display text-lg font-semibold">{issue.title}</h2>
          <p className="mt-1 text-sm text-muted">{issue.detail}</p>
          {issue.steps.length > 0 && (
            <>
              <div className="mt-4 text-sm font-medium">{t('health.howToFix')}</div>
              <ol className="mt-2 flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 ps-8 pe-4 text-sm text-muted">
                {issue.steps.map((s, n) => (
                  <li key={n}>{s}</li>
                ))}
              </ol>
            </>
          )}
          {issue.link && (
            <Link to={issue.link.to} className="mt-4 inline-block">
              <Button>{issue.link.label}</Button>
            </Link>
          )}
        </div>
      </div>
    </Card>
  )
}
