import { useEffect, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, X } from 'lucide-react'
import { get, put } from '../api'
import { bytes } from '../format'
import { Button, Card, ErrorNote, Field, Select, Toggle } from '../ui'
import { useLanguageName } from './Subtitles'

// Settings → Library → Unwanted tracks (see internal/tracks).
type TrackSettings = {
  config: { on: boolean; languages: string[] }
  saved: { files: number; bytes: number }
  ready: boolean
  languages: { code: string; name: string }[]
  suggested: string[]
}

// TracksCard turns on dropping the audio and subtitle tracks nobody here
// wants from files as they come in, and picks the languages kept.
export function TracksCard() {
  const { t, i18n } = useTranslation()
  const qc = useQueryClient()
  const name = useLanguageName()
  const { data, error } = useQuery({ queryKey: ['tracks'], queryFn: () => get<TrackSettings>('/tracks') })
  const [form, setForm] = useState<TrackSettings['config']>()
  const [dirty, setDirty] = useState(false)
  const [adding, setAdding] = useState('')
  useEffect(() => {
    // Until it's been saved once, the list starts with the languages subtitles are fetched in.
    if (data && !dirty) setForm({ on: data.config.on, languages: data.config.languages.length || data.config.on ? data.config.languages : data.suggested })
  }, [data, dirty])
  const save = useMutation({
    mutationFn: (f: TrackSettings['config']) => put<TrackSettings>('/tracks', f),
    onSuccess: (v) => {
      qc.setQueryData(['tracks'], v)
      setDirty(false)
    },
  })
  if (!data || !form) {
    return (
      <Card>
        <h2 className="mb-1 font-display text-lg font-semibold">{t('tracks.title')}</h2>
        <ErrorNote error={error} />
      </Card>
    )
  }
  const change = (next: TrackSettings['config']) => {
    setForm(next)
    setDirty(true)
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(form)
  }
  const choices = data.languages.filter((l) => !form.languages.includes(l.code)).map((l) => ({ value: l.code, label: name(l.code) }))
  choices.sort((a, b) => a.label.localeCompare(b.label))
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('tracks.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('tracks.sub')}</p>
      <form onSubmit={submit} className="flex flex-col gap-5">
        <Toggle checked={form.on} onChange={(v) => change({ ...form, on: v })} label={t('tracks.on')} />
        {!data.ready && <p className="text-sm text-bad">{t('tracks.noTools')}</p>}
        <Field label={t('tracks.languages')} hint={t('tracks.languagesHint')}>
          {(id) => (
            <div className="flex flex-col gap-2">
              <div className="flex flex-wrap gap-2">
                {form.languages.map((l) => (
                  <span key={l} className="inline-flex items-center gap-1 rounded-full border border-line bg-paper py-1 ps-3 pe-1 text-sm">
                    {name(l)}
                    <button
                      type="button"
                      className="rounded-full p-0.5 text-muted hover:bg-line hover:text-ink"
                      aria-label={t('subtitles.removeLanguage', { language: name(l) })}
                      onClick={() => change({ ...form, languages: form.languages.filter((x) => x !== l) })}
                    >
                      <X className="size-3.5" aria-hidden />
                    </button>
                  </span>
                ))}
                {form.languages.length === 0 && <span className="text-sm text-muted">{t('subtitles.noLanguages')}</span>}
              </div>
              {form.languages.length < 10 && (
                <div className="flex max-w-sm gap-2">
                  <Select id={id} value={adding} onChange={setAdding} options={[{ value: '', label: t('subtitles.pickLanguage') }, ...choices]} />
                  <Button
                    type="button"
                    disabled={!adding}
                    onClick={() => {
                      change({ ...form, languages: [...form.languages, adding] })
                      setAdding('')
                    }}
                  >
                    <Plus className="size-4" aria-hidden /> {t('subtitles.add')}
                  </Button>
                </div>
              )}
            </div>
          )}
        </Field>
        {data.saved.files > 0 && (
          <p className="text-sm text-muted" data-testid="tracks-saved">
            {t('tracks.saved', { size: bytes(data.saved.bytes), files: data.saved.files.toLocaleString(i18n.language) })}
          </p>
        )}
        <ErrorNote error={save.error} />
        <div>
          <Button type="submit" variant="primary" busy={save.isPending} disabled={!dirty}>
            {t('common.save')}
          </Button>
        </div>
      </form>
    </Card>
  )
}
