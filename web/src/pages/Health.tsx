import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { AlertTriangle, CircleCheck, Info, RefreshCw, XCircle } from 'lucide-react'
import { get, type Health as HealthData, type HealthIssue } from '../api'
import { when } from '../format'
import { Button, Card, ErrorNote, Heading, Spinner } from '../ui'

// useHealth is Box Office's own check-up, looked at again now and then.
export function useHealth() {
  return useQuery({ queryKey: ['health'], queryFn: () => get<HealthData>('/health'), refetchInterval: 5 * 60_000, staleTime: 60_000 })
}

// needsAttention counts the issues worth a badge: errors and warnings.
export const needsAttention = (h?: HealthData) => h?.issues.filter((i) => i.level !== 'notice').length ?? 0

const look = {
  error: { icon: XCircle, tone: 'text-bad', label: 'Not working' },
  warning: { icon: AlertTriangle, tone: 'text-gold', label: 'Needs a look' },
  notice: { icon: Info, tone: 'text-muted', label: 'Good to know' },
}

export function Health() {
  const h = useHealth()
  return (
    <>
      <Heading
        title="Health"
        sub="Box Office checks itself for problems you can fix: keys, folders, files, connections, backups and tasks."
        actions={
          <Button busy={h.isFetching} onClick={() => h.refetch()}>
            <RefreshCw className="size-4" /> Check again
          </Button>
        }
      />
      <ErrorNote error={h.error} />
      {h.isPending && <Spinner label="Checking" />}
      {h.data && (
        <>
          {h.data.issues.length === 0 ? (
            <Card className="flex items-center gap-3">
              <CircleCheck className="size-6 text-ok" aria-hidden />
              <div>
                <div className="font-medium">All good</div>
                <div className="text-sm text-muted">Nothing needs fixing.</div>
              </div>
            </Card>
          ) : (
            <div className="flex flex-col gap-4">
              {h.data.issues.map((i) => (
                <Issue key={i.id} issue={i} />
              ))}
            </div>
          )}
          <p className="mt-6 text-xs text-muted">Checked {when(h.data.checked)}.</p>
        </>
      )}
    </>
  )
}

function Issue({ issue }: { issue: HealthIssue }) {
  const l = look[issue.level]
  const Icon = l.icon
  return (
    <Card>
      <div className="flex gap-3">
        <Icon className={`mt-0.5 size-5 shrink-0 ${l.tone}`} aria-label={l.label} />
        <div className="min-w-0 flex-1">
          <h2 className="font-display text-lg font-semibold">{issue.title}</h2>
          <p className="mt-1 text-sm text-muted">{issue.detail}</p>
          {issue.steps.length > 0 && (
            <>
              <div className="mt-4 text-sm font-medium">How to fix it</div>
              <ol className="mt-2 flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 pl-8 pr-4 text-sm text-muted">
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
