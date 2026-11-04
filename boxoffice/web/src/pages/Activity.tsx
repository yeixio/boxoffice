import { useQuery } from '@tanstack/react-query'
import { History } from 'lucide-react'
import { get, type Job } from '../api'
import { when } from '../format'
import { Badge, Empty, ErrorNote, Heading, Spinner } from '../ui'

const tone: Record<string, 'ok' | 'bad' | 'gold' | 'plain' | 'marquee'> = { done: 'ok', failed: 'bad', running: 'marquee', queued: 'gold', skipped: 'plain' }

export function Activity() {
  const jobs = useQuery({ queryKey: ['activity'], queryFn: () => get<Job[]>('/activity?limit=200'), refetchInterval: 5000 })
  return (
    <>
      <Heading title="Activity" sub="What Box Office has been doing: refreshes, searches and imports." />
      <ErrorNote error={jobs.error} />
      {jobs.isPending && <Spinner />}
      {jobs.data?.length === 0 && <Empty icon={<History className="size-8" />} title="Nothing yet" />}
      {!!jobs.data?.length && (
        <div className="overflow-x-auto rounded-xl border border-line bg-card">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-line text-muted">
              <tr>
                <th className="px-4 py-2 font-medium">What</th>
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2 font-medium">Note</th>
                <th className="px-4 py-2 font-medium">When</th>
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
                    <Badge tone={tone[j.status] ?? 'plain'}>{j.status}</Badge>
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
