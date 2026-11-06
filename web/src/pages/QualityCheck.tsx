import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { get, post, type FileMedia, type QualityCheckState } from '../api'
import { qualityName, useQualities } from '../hooks'
import { Badge, Button, Card, ErrorNote } from '../ui'

// The quality check looks inside movie and episode files in the
// background and corrects qualities guessed wrong from names.
export function useQualityCheck() {
  return useQuery({
    queryKey: ['quality-check'],
    queryFn: () => get<QualityCheckState>('/quality-check'),
    refetchInterval: (q) => (q.state.data?.status.running ? 3000 : 60000),
  })
}

// pictureText says what a file's picture and sound are, such as
// "1920×800 · HEVC 10-bit · HDR10 · TrueHD 7.1".
export function pictureText(m: FileMedia, bits: (n: number) => string): string {
  const parts = [`${m.width}×${m.height}`]
  if (m.videoCodec) parts.push(m.videoCodec.toUpperCase() + (m.bitDepth && m.bitDepth > 8 ? ' ' + bits(m.bitDepth) : ''))
  if (m.hdr) parts.push(m.hdr)
  if (m.audioCodec) parts.push(m.audioCodec + (m.channels ? ' ' + channels(m.channels) : ''))
  return parts.join(' · ')
}

// channels names a speaker layout the way people know it: 6 is 5.1.
const channels = (n: number) => (n === 1 ? '1.0' : n === 2 ? '2.0' : n >= 6 ? `${n - 1}.1` : `${n}.0`)

export function QualityCheckCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const qualities = useQualities()
  const state = useQualityCheck()
  const again = useMutation({
    mutationFn: () => post<QualityCheckState>('/quality-check/again'),
    onSuccess: (s) => qc.setQueryData(['quality-check'], s),
  })
  const s = state.data
  const name = (id: number) => qualityName(qualities.data, id)
  const bits = (n: number) => t('qualityCheck.bits', { bits: n })
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('qualityCheck.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('qualityCheck.sub')}</p>
      <ErrorNote error={state.error ?? again.error} />
      {s && (
        <>
          {!s.status.ffprobe && <p className="mb-3 text-sm text-bad">{t('qualityCheck.noFFprobe')}</p>}
          <div className="flex flex-wrap items-center gap-3 text-sm">
            <span>{t('qualityCheck.checked', { checked: s.status.checked, files: s.status.files })}</span>
            {s.status.running && <Badge tone="marquee">{t('qualityCheck.running', { done: s.status.done })}</Badge>}
            {s.status.unknown > 0 && <Badge>{t('qualityCheck.unknown', { count: s.status.unknown })}</Badge>}
            {s.status.unreadable > 0 && <Badge tone="gold">{t('qualityCheck.unreadable', { count: s.status.unreadable })}</Badge>}
          </div>
          <div className="mt-4">
            <Button onClick={() => again.mutate()} busy={again.isPending} disabled={s.status.running || !s.status.ffprobe}>
              <RefreshCw className="size-4" aria-hidden /> {t('qualityCheck.again')}
            </Button>
          </div>
          <h3 className="mb-2 mt-6 text-sm font-semibold">{t('qualityCheck.corrections')}</h3>
          {s.corrections.length === 0 ? (
            <p className="text-sm text-muted">{t('qualityCheck.none')}</p>
          ) : (
            <ul className="max-h-96 divide-y divide-line overflow-y-auto rounded-md border border-line text-sm">
              {s.corrections.map((c) => (
                <li key={c.fileId} className="px-3 py-2">
                  <Link to={`/${c.kind === 'movie' ? 'movies' : 'shows'}/${c.titleId}`} className="font-medium hover:underline">
                    {c.title}
                  </Link>
                  <div className="text-muted">
                    {t('qualityCheck.wasNow', { was: name(c.was), now: name(c.now) })} · {pictureText(c.media, bits)}
                  </div>
                </li>
              ))}
            </ul>
          )}
          {s.more > 0 && <p className="mt-2 text-sm text-muted">{t('qualityCheck.more', { count: s.more })}</p>}
        </>
      )}
    </Card>
  )
}
