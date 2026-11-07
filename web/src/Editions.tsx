// Editions looks for a movie's editions (Extended, Director's Cut…) on the
// indexers and gets one, kept next to the copy there or in its place.
import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { get, post } from './api'
import { bytes } from './format'
import { Badge, Button, ErrorNote, Modal, Select, Spinner } from './ui'

type EditionOption = { edition: string; releases: number; quality?: string; size?: number; owned?: boolean }
type EditionsFound = { owned: string[]; known: string[]; found: EditionOption[]; errors: string[] }
type Mode = 'alongside' | 'replace'

export function Editions({ id, title, hasFile, onClose }: { id: number; title: string; hasFile: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const res = useQuery({ queryKey: ['editions', id], queryFn: () => get<EditionsFound>(`/movies/${id}/editions`), staleTime: 60_000, retry: false })
  const [other, setOther] = useState('')
  const ask = useMutation({
    mutationFn: (v: { edition: string; mode: Mode }) => post(`/movies/${id}/editions`, v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['downloads'] })
      setTimeout(() => qc.invalidateQueries({ queryKey: ['movie', id] }), 2500)
    },
  })
  const owned = (e: string) => res.data?.owned.some((o) => o.toLowerCase() === e.toLowerCase())
  const buttons = (edition: string) =>
    ask.isSuccess && ask.variables.edition === edition ? (
      <p className="text-xs text-ok">{t('editions.asked', { edition })}</p>
    ) : (
      <div className="flex flex-wrap gap-2">
        {hasFile && (
          <Button className="px-3 py-1.5 text-xs" busy={ask.isPending && ask.variables.edition === edition && ask.variables.mode === 'alongside'} onClick={() => ask.mutate({ edition, mode: 'alongside' })}>
            {t('editions.alongside')}
          </Button>
        )}
        <Button className="px-3 py-1.5 text-xs" busy={ask.isPending && ask.variables.edition === edition && ask.variables.mode === 'replace'} onClick={() => ask.mutate({ edition, mode: 'replace' })}>
          {hasFile ? t('editions.replace') : t('editions.get')}
        </Button>
      </div>
    )
  const notFound = res.data?.known.filter((k) => !res.data!.found.some((f) => f.edition === k)) ?? []
  return (
    <Modal title={t('editions.title', { title })} onClose={onClose} wide>
      <p className="mb-3 text-sm text-muted">{t('editions.sub')}</p>
      {res.isPending && <Spinner label={t('editions.searching')} />}
      <ErrorNote error={res.error || ask.error} />
      {res.data && (
        <>
          {res.data.errors?.map((e) => (
            <p key={e} className="mb-2 rounded-md border border-gold/50 px-3 py-2 text-sm text-gold">
              {e}
            </p>
          ))}
          {res.data.found.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted">{t('editions.none')}</p>
          ) : (
            <ul className="divide-y divide-line">
              {res.data.found.map((o) => (
                <li key={o.edition} className="flex flex-wrap items-center gap-3 py-3">
                  <div className="min-w-0 flex-1 basis-52">
                    <div className="flex flex-wrap items-center gap-2 text-sm font-medium">
                      {o.edition}
                      {(o.owned || owned(o.edition)) && <Badge tone="ok">{t('editions.owned')}</Badge>}
                    </div>
                    <div className="mt-1 text-xs text-muted">
                      {[t('editions.releases', { n: o.releases }), o.quality, o.size ? bytes(o.size) : ''].filter(Boolean).join(' · ')}
                    </div>
                  </div>
                  {buttons(o.edition)}
                </li>
              ))}
            </ul>
          )}
          {notFound.length > 0 && (
            <div className="mt-4 border-t border-line pt-4">
              <p className="mb-2 text-sm font-medium">{t('editions.other')}</p>
              <p className="mb-2 text-xs text-muted">{t('editions.otherHint')}</p>
              <div className="flex flex-wrap items-center gap-3">
                <div className="w-56">
                  <Select value={other} onChange={setOther} options={[{ value: '', label: t('editions.pick') }, ...notFound.map((k) => ({ value: k, label: k }))]} />
                </div>
                {other && buttons(other)}
              </div>
            </div>
          )}
        </>
      )}
    </Modal>
  )
}
