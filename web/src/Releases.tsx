// Releases lists what the indexers have for a movie, an episode or a book, with
// what Box Office makes of each, and grabs one by hand.
import { useTranslation } from 'react-i18next'
import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { CircleCheck, Download as DownloadIcon } from 'lucide-react'
import { get, post, type Candidate, type Quality, type ReleaseResults } from './api'
import { bytes, day } from './format'
import { useQualities } from './hooks'
import { SortControl, sortList, useSort, words, type SortOption } from './sort'
import { Badge, Button, ErrorNote, Modal, Spinner } from './ui'

// Box Office sends releases best first; the other sorts are for looking
// around.
function releaseSorts(releases: Candidate[], qualities?: Quality[]): SortOption<Candidate>[] {
  const order = new Map(releases.map((r, i) => [r.key, i]))
  const rank = (id: number) => qualities?.findIndex((q) => q.id === id) ?? id
  return [
    { key: 'best', label: 'releases.best', value: (r) => order.get(r.key), dir: 'asc', says: { asc: 'common:sort.best', desc: 'common:sort.worst' } },
    { key: 'quality', label: 'releases.quality', value: (r) => rank(r.quality), dir: 'desc', says: words.quality },
    { key: 'size', label: 'releases.size', value: (r) => r.size || undefined, dir: 'desc', says: words.size },
    { key: 'age', label: 'releases.posted', value: (r) => r.publishedAt, dir: 'desc', says: words.date },
    { key: 'grabs', label: 'releases.grabs', value: (r) => r.grabs, dir: 'desc', says: words.count },
  ]
}

export function Releases({ path, title, onClose }: { path: string; title: string; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const res = useQuery({ queryKey: ['releases', path], queryFn: () => get<ReleaseResults>(path), staleTime: 60_000, retry: false })
  const qualities = useQualities()
  const sorts = useMemo(() => releaseSorts(res.data?.releases ?? [], qualities.data), [res.data, qualities.data])
  const sort = useSort('releases', sorts)
  const releases = useMemo(() => sortList(res.data?.releases ?? [], sort.option, sort.dir), [res.data, sort.option, sort.dir])
  const grab = useMutation({
    mutationFn: (key: string) => post(`/releases/${key}/grab`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['downloads'] }),
  })
  return (
    <Modal title={t('releases.title', { title })} onClose={onClose} wide>
      {res.isPending && <Spinner label={t('releases.asking')} />}
      <ErrorNote error={res.error} />
      {res.data && (
        <>
          {res.data.errors?.map((e) => (
            <p key={e} className="mb-2 rounded-md border border-gold/50 px-3 py-2 text-sm text-gold">
              {e}
            </p>
          ))}
          {res.data.releases.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted">{res.data.searched === 1 ? t('releases.nothingOne') : t('releases.nothing', { count: res.data.searched })}</p>
          ) : (
            <>
              {res.data.releases.length > 1 && <SortControl sort={sort} className="mb-2 sm:justify-end" />}
              <ul className="divide-y divide-line">
                {releases.map((r) => {
                  const q = qualities.data?.find((x) => x.id === r.quality)
                  const grabbed = grab.isSuccess && grab.variables === r.key
                  return (
                    <li key={r.key} className="flex flex-wrap items-start gap-3 py-3">
                      <div className="min-w-0 flex-1 basis-64">
                        <div className={`break-all text-sm font-medium ${r.accepted ? '' : 'text-muted'}`}>{r.title}</div>
                        <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted">
                          {r.format ? <Badge tone={r.accepted ? 'ok' : 'plain'}>{r.format}</Badge> : q && <Badge tone={r.accepted ? 'ok' : 'plain'}>{q.name}</Badge>}
                          {r.pack && <Badge tone="marquee">{t('releases.wholeSeason')}</Badge>}
                          {!r.pack && r.episodes && r.episodes.length > 1 && <Badge>{t('releases.episodes', { count: r.episodes.length })}</Badge>}
                          <span>{bytes(r.size)}</span>
                          <span>{r.indexer}</span>
                          {r.publishedAt && <span>{day(r.publishedAt)}</span>}
                          {!!r.grabs && <span>{t('releases.grabCount', { count: r.grabs })}</span>}
                        </div>
                        {r.rejections?.map((why) => (
                          <p key={why} className="mt-1 text-xs text-bad">
                            {t('releases.notPicked', { why })}
                          </p>
                        ))}
                      </div>
                      {grabbed ? (
                        <Link to="/downloads" className="flex items-center gap-1 text-sm text-ok">
                          <CircleCheck className="size-4" /> {t('releases.inQueue')}
                        </Link>
                      ) : (
                        <Button variant={r.accepted ? 'primary' : 'plain'} busy={grab.isPending && grab.variables === r.key} disabled={r.protocol !== 'usenet'} onClick={() => grab.mutate(r.key)}>
                          <DownloadIcon className="size-4" aria-hidden /> {t('releases.grab')}
                        </Button>
                      )}
                    </li>
                  )
                })}
              </ul>
            </>
          )}
        </>
      )}
      <ErrorNote error={grab.error} />
    </Modal>
  )
}
