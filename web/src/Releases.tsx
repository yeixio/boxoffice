// Releases lists what the indexers have for a movie or an episode, with
// what Box Office makes of each, and grabs one by hand.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { CircleCheck, Download as DownloadIcon } from 'lucide-react'
import { get, post, type ReleaseResults } from './api'
import { bytes, day } from './format'
import { useQualities } from './hooks'
import { Badge, Button, ErrorNote, Modal, Spinner } from './ui'

export function Releases({ path, title, onClose }: { path: string; title: string; onClose: () => void }) {
  const qc = useQueryClient()
  const res = useQuery({ queryKey: ['releases', path], queryFn: () => get<ReleaseResults>(path), staleTime: 60_000, retry: false })
  const qualities = useQualities()
  const grab = useMutation({
    mutationFn: (key: string) => post(`/releases/${key}/grab`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['downloads'] }),
  })
  return (
    <Modal title={`Releases for ${title}`} onClose={onClose} wide>
      {res.isPending && <Spinner label="Asking the indexers" />}
      <ErrorNote error={res.error} />
      {res.data && (
        <>
          {res.data.errors?.map((e) => (
            <p key={e} className="mb-2 rounded-md border border-gold/50 px-3 py-2 text-sm text-gold">
              {e}
            </p>
          ))}
          {res.data.releases.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted">Nothing found on {res.data.searched === 1 ? 'the indexer' : `${res.data.searched} indexers`}.</p>
          ) : (
            <ul className="divide-y divide-line">
              {res.data.releases.map((r) => {
                const q = qualities.data?.find((x) => x.id === r.quality)
                const grabbed = grab.isSuccess && grab.variables === r.key
                return (
                  <li key={r.key} className="flex flex-wrap items-start gap-3 py-3">
                    <div className="min-w-0 flex-1 basis-64">
                      <div className={`break-all text-sm font-medium ${r.accepted ? '' : 'text-muted'}`}>{r.title}</div>
                      <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted">
                        {q && <Badge tone={r.accepted ? 'ok' : 'plain'}>{q.name}</Badge>}
                        <span>{bytes(r.size)}</span>
                        <span>{r.indexer}</span>
                        {r.publishedAt && <span>{day(r.publishedAt)}</span>}
                        {!!r.grabs && <span>{r.grabs} grabs</span>}
                      </div>
                      {r.rejections?.map((why) => (
                        <p key={why} className="mt-1 text-xs text-bad">
                          Not picked: {why}
                        </p>
                      ))}
                    </div>
                    {grabbed ? (
                      <Link to="/downloads" className="flex items-center gap-1 text-sm text-ok">
                        <CircleCheck className="size-4" /> In the queue
                      </Link>
                    ) : (
                      <Button variant={r.accepted ? 'primary' : 'plain'} busy={grab.isPending && grab.variables === r.key} disabled={r.protocol !== 'usenet'} onClick={() => grab.mutate(r.key)}>
                        <DownloadIcon className="size-4" aria-hidden /> Grab
                      </Button>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
        </>
      )}
      <ErrorNote error={grab.error} />
    </Modal>
  )
}
