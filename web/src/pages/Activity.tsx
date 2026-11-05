import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { History } from 'lucide-react'
import { get, type Job } from '../api'
import { when } from '../format'
import { Badge, Empty, ErrorNote, Heading, Spinner } from '../ui'

const tone: Record<string, 'ok' | 'bad' | 'gold' | 'plain' | 'marquee'> = { done: 'ok', failed: 'bad', running: 'marquee', queued: 'gold', skipped: 'plain' }

export function Activity() {
  const { t } = useTranslation()
  const status = (s: string) => t(`activity.status.${s}`, { defaultValue: s })
  const jobs = useQuery({ queryKey: ['activity'], queryFn: () => get<Job[]>('/activity?limit=200'), refetchInterval: 5000 })
  return (
    <>
      <Heading title={t('nav.activity')} sub={t('activity.sub')} />
      <ErrorNote error={jobs.error} />
      {jobs.isPending && <Spinner />}
      {jobs.data?.length === 0 && <Empty icon={<History className="size-8" />} title={t('activity.nothing')} />}
      {!!jobs.data?.length && (
        <ul className="divide-y divide-line rounded-xl border border-line bg-card sm:hidden">
          {jobs.data.map((j) => (
            <li key={j.id} className="px-4 py-3">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 font-medium">{j.subject}</div>
                <Badge tone={tone[j.status] ?? 'plain'}>{status(j.status)}</Badge>
              </div>
              {j.message && <p className="mt-1 break-words text-sm text-muted">{j.message}</p>}
              <div className="mt-1 text-xs text-muted">
                {j.kind} · {when(j.finishedAt || j.startedAt || j.createdAt)}
              </div>
            </li>
          ))}
        </ul>
      )}
      {!!jobs.data?.length && (
        <div className="hidden overflow-x-auto rounded-xl border border-line bg-card sm:block">
          <table className="w-full text-start text-sm">
            <thead className="border-b border-line text-muted">
              <tr>
                <th className="px-4 py-2 font-medium">{t('activity.what')}</th>
                <th className="px-4 py-2 font-medium">{t('activity.statusCol')}</th>
                <th className="px-4 py-2 font-medium">{t('activity.note')}</th>
                <th className="px-4 py-2 font-medium">{t('activity.when')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {jobs.data.map((j) => (
                <tr key={j.id}>
                  <td className="px-4 py-2">
                    <div className="font-medium">{j.subject}</div>
                    <div className="text-xs text-muted">{j.kind}</div>
                  </td>
                  <td className="px-4 py-2">
                    <Badge tone={tone[j.status] ?? 'plain'}>{status(j.status)}</Badge>
                  </td>
                  <td className="px-4 py-2 text-muted">{j.message}</td>
                  <td className="whitespace-nowrap px-4 py-2 text-muted">{when(j.finishedAt || j.startedAt || j.createdAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
