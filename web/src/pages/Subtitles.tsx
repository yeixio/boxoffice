import { useTranslation } from 'react-i18next'
import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Captions, CircleCheck, Plus, RefreshCw, X } from 'lucide-react'
import { get, post, put, type FileSubtitle, type SubtitleSettings } from '../api'
import { day, episodeCode, when } from '../format'
import { Badge, Button, Card, ErrorNote, Field, Input, Select, Toggle } from '../ui'

// Subtitles: Box Office fetches subtitles in the languages picked here,
// for each new download at once and for the rest of the library a few
// hundred files a day, the way Bazarr did. Podnapisi needs no account, so
// turning subtitles on is all it takes; OpenSubtitles adds more with a
// free API key.

export const subtitlesDocs = 'https://github.com/yeixio/boxoffice/blob/main/docs/subtitles.md'

export function useSubtitles(enabled = true) {
  return useQuery({
    queryKey: ['subtitles'],
    queryFn: () => get<SubtitleSettings>('/subtitles'),
    enabled,
    refetchInterval: (q) => (q.state.data?.status.running ? 5000 : 60000),
  })
}

// useLanguageName names a language in the page's language: "en" is
// "English", or "anglais" in French.
export function useLanguageName() {
  const { i18n } = useTranslation()
  let names: Intl.DisplayNames | undefined
  try {
    names = new Intl.DisplayNames([i18n.language], { type: 'language' })
  } catch {
    // An old browser: the code will do.
  }
  return (code: string) => {
    try {
      return names?.of(code) ?? code
    } catch {
      return code
    }
  }
}

const has = (s: FileSubtitle) => s.state === 'embedded' || s.state === 'on_disk' || s.state === 'fetched'

const providerName: Record<string, string> = { opensubtitles: 'OpenSubtitles', podnapisi: 'Podnapisi' }

// subtitleNote says where a file's subtitle is from, or why it isn't
// there yet.
function useSubtitleNote() {
  const { t } = useTranslation()
  return (s: FileSubtitle) => {
    switch (s.state) {
      case 'embedded':
        return t('subtitles.state.embedded')
      case 'on_disk':
        return t('subtitles.state.onDisk')
      case 'fetched':
        return t('subtitles.state.fetched', { provider: providerName[s.provider ?? ''] ?? s.provider })
      case 'missing':
        return t('subtitles.state.missing', { when: when(s.nextAt) })
      case 'waiting':
        return s.note ? `${t('subtitles.state.waiting', { when: when(s.nextAt) })} (${s.note})` : t('subtitles.state.waiting', { when: when(s.nextAt) })
      default:
        return t('subtitles.state.pending')
    }
  }
}

// SubtitleBadges are a file's subtitles, one badge per language: green
// when it has them, gold while they're being looked for.
export function SubtitleBadges({ subs }: { subs: FileSubtitle[] }) {
  const name = useLanguageName()
  const note = useSubtitleNote()
  return (
    <span className="flex flex-wrap gap-1.5">
      {subs.map((s) => (
        <span key={s.language} title={note(s)}>
          <Badge tone={has(s) ? 'ok' : s.state === 'pending' ? 'plain' : 'gold'}>
            {has(s) ? <CircleCheck className="size-3" aria-hidden /> : null}
            {name(s.language)}
          </Badge>
          <span className="sr-only">: {note(s)}</span>
        </span>
      ))}
    </span>
  )
}

// SubtitleMark is a small mark on an episode's row: lit when it has
// subtitles in every language, dim while some are missing. Its title
// says which.
export function SubtitleMark({ subs }: { subs?: FileSubtitle[] }) {
  const name = useLanguageName()
  const note = useSubtitleNote()
  const { t } = useTranslation()
  if (!subs?.length) return null
  const all = subs.every(has)
  const text = subs.map((s) => `${name(s.language)}: ${note(s)}`).join('\n')
  return (
    <span title={text} className={all ? 'text-ok' : 'text-muted/60'}>
      <Captions className="size-4" aria-hidden />
      <span className="sr-only">
        {all ? t('subtitles.allThere') : t('subtitles.someMissing')}. {text}
      </span>
    </span>
  )
}

// FindSubtitles looks for a title's missing subtitles now.
export function FindSubtitles({ path, onFound }: { path: string; onFound?: () => void }) {
  const { t } = useTranslation()
  const find = useMutation({
    mutationFn: () => post<{ files: number }>(path),
    onSuccess: () => {
      setTimeout(() => onFound?.(), 8000)
    },
  })
  return (
    <span className="flex flex-col gap-1">
      <Button variant="ghost" busy={find.isPending} onClick={() => find.mutate()} className="self-start">
        <Captions className="size-4" aria-hidden /> {t('subtitles.findNow')}
      </Button>
      {find.isSuccess && <span className="text-xs text-muted">{t('subtitles.looking')}</span>}
      <ErrorNote error={find.error} />
    </span>
  )
}

type Form = {
  on: boolean
  languages: string[]
  preferSdh: boolean
  dailyLimit: number
  podnapisi: boolean
  openSubtitles: { apiKey: string; username: string; password: string }
  forgetOpenSubtitles: boolean
}

// toForm starts a never-saved list with the languages people use Box
// Office and the request site in, then the browser's.
const toForm = (s: SubtitleSettings): Form => {
  let langs = s.config.languages
  if (!s.config.on && langs.length === 0) {
    const known = new Set(s.languages.map((l) => l.code))
    const browser = (navigator.languages ?? [navigator.language]).map((l) => l.split('-')[0].toLowerCase())
    langs = [...new Set([...s.suggested, ...browser])].filter((l) => known.has(l)).slice(0, 3)
    if (langs.length === 0) langs = ['en']
  }
  return {
    on: s.config.on,
    languages: langs,
    preferSdh: s.config.preferSdh,
    dailyLimit: s.config.dailyLimit,
    podnapisi: s.config.podnapisi,
    openSubtitles: { apiKey: '', username: s.config.openSubtitles.username, password: '' },
    forgetOpenSubtitles: false,
  }
}

export function SubtitleSettingsCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const name = useLanguageName()
  const { data, error: loadError } = useSubtitles()
  const [form, setForm] = useState<Form>()
  const [dirty, setDirty] = useState(false)
  const [adding, setAdding] = useState('')
  useEffect(() => {
    if (data && !dirty) setForm(toForm(data))
  }, [data, dirty])
  const save = useMutation({
    mutationFn: (f: Form) => put<SubtitleSettings>('/subtitles', f),
    onSuccess: (v) => {
      qc.setQueryData(['subtitles'], v)
      setForm(toForm(v))
      setDirty(false)
    },
  })
  const test = useMutation({ mutationFn: (f: Form) => post<{ ok: boolean }>('/subtitles/test', f) })
  const again = useMutation({
    mutationFn: () => post<SubtitleSettings>('/subtitles/again'),
    onSuccess: (v) => qc.setQueryData(['subtitles'], v),
  })
  if (!data || !form) {
    return (
      <Card>
        <h2 className="mb-1 font-display text-lg font-semibold">{t('subtitles.title')}</h2>
        <ErrorNote error={loadError} />
      </Card>
    )
  }
  const change = (f: (draft: Form) => void) => {
    const next = structuredClone(form)
    f(next)
    setForm(next)
    setDirty(true)
    test.reset()
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate(form)
  }
  const os = data.config.openSubtitles
  const hasKey = !form.forgetOpenSubtitles && (os.hasApiKey || form.openSubtitles.apiKey !== '')
  const st = data.status
  const c = st.counts
  const choices = data.languages.filter((l) => !form.languages.includes(l.code)).map((l) => ({ value: l.code, label: name(l.code) }))
  choices.sort((a, b) => a.label.localeCompare(b.label))
  const osUse = st.today.providers.opensubtitles
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('subtitles.title')}</h2>
      <p className="mb-4 text-sm text-muted">
        {t('subtitles.sub')}{' '}
        <a href={subtitlesDocs} target="_blank" rel="noreferrer" className="text-marquee hover:underline">
          {t('subtitles.docs')}
        </a>
      </p>
      <form onSubmit={submit} className="flex flex-col gap-5">
        <Toggle checked={form.on} onChange={(v) => change((f) => (f.on = v))} label={t('subtitles.on')} />

        <Field label={t('subtitles.languages')} hint={data.config.on || data.config.languages.length ? t('subtitles.languagesHint') : t('subtitles.languagesGuessed')}>
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
                      onClick={() => change((f) => (f.languages = f.languages.filter((x) => x !== l)))}
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
                      change((f) => f.languages.push(adding))
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

        <Toggle checked={form.preferSdh} onChange={(v) => change((f) => (f.preferSdh = v))} label={<span>{t('subtitles.preferSdh')}<span className="block text-xs text-muted">{t('subtitles.preferSdhHint')}</span></span>} />

        <Field label={t('subtitles.dailyLimit')} hint={t('subtitles.dailyLimitHint')}>
          {(id) => (
            <Input id={id} type="number" min={1} max={5000} className="max-w-32" value={form.dailyLimit} onChange={(e) => change((f) => (f.dailyLimit = Number(e.target.value)))} />
          )}
        </Field>

        <section className="flex flex-col gap-4 rounded-lg border border-line p-4">
          <h3 className="font-display font-semibold">{t('subtitles.where')}</h3>
          <Toggle checked={form.podnapisi} onChange={(v) => change((f) => (f.podnapisi = v))} label={<span>{providerName.podnapisi}<span className="block text-xs text-muted">{t('subtitles.podnapisiHint')}</span></span>} />
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{providerName.opensubtitles}</span>
              {hasKey ? <Badge tone="ok">{t('subtitles.keySaved')}</Badge> : <Badge>{t('subtitles.optional')}</Badge>}
              {osUse?.usedUp && <Badge tone="gold">{t('subtitles.usedUp')}</Badge>}
            </div>
            <p className="text-xs text-muted">{t('subtitles.osHint')}</p>
            <details className="text-sm" open={!os.hasApiKey && form.on}>
              <summary className="cursor-pointer text-muted hover:text-ink">{t('subtitles.osWhere')}</summary>
              <ol className="mt-2 flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 ps-8 pe-4 text-muted">
                {['subtitles.help.os1', 'subtitles.help.os2', 'subtitles.help.os3', 'subtitles.help.os4', 'subtitles.help.os5'].map((s) => (
                  <li key={s}>{t(s)}</li>
                ))}
              </ol>
            </details>
            <div className="grid gap-3 sm:grid-cols-3">
              <Field label={t('subtitles.apiKey')}>
                {(id) => (
                  <Input
                    id={id}
                    autoComplete="off"
                    value={form.openSubtitles.apiKey}
                    placeholder={os.hasApiKey && !form.forgetOpenSubtitles ? t('subtitles.keepKey') : ''}
                    onChange={(e) => change((f) => ((f.openSubtitles.apiKey = e.target.value.trim()), (f.forgetOpenSubtitles = false)))}
                  />
                )}
              </Field>
              <Field label={t('subtitles.username')}>
                {(id) => <Input id={id} autoComplete="off" value={form.openSubtitles.username} onChange={(e) => change((f) => ((f.openSubtitles.username = e.target.value), (f.forgetOpenSubtitles = false)))} />}
              </Field>
              <Field label={t('subtitles.password')}>
                {(id) => (
                  <Input
                    id={id}
                    type="password"
                    autoComplete="new-password"
                    value={form.openSubtitles.password}
                    placeholder={os.hasPassword && form.openSubtitles.username === os.username ? t('subtitles.keepPassword') : ''}
                    onChange={(e) => change((f) => (f.openSubtitles.password = e.target.value))}
                  />
                )}
              </Field>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button type="button" disabled={!hasKey} busy={test.isPending} onClick={() => test.mutate(form)}>
                {t('subtitles.test')}
              </Button>
              {(os.hasApiKey || os.username) && !form.forgetOpenSubtitles && (
                <Button type="button" variant="ghost" onClick={() => change((f) => ((f.forgetOpenSubtitles = true), (f.openSubtitles = { apiKey: '', username: '', password: '' })))}>
                  {t('subtitles.forget')}
                </Button>
              )}
              {test.isSuccess && (
                <span className="inline-flex items-center gap-1 text-sm text-ok">
                  <CircleCheck className="size-4" aria-hidden /> {t('subtitles.works')}
                </span>
              )}
            </div>
            <ErrorNote error={test.error} />
          </div>
        </section>

        <ErrorNote error={save.error} />
        <div>
          <Button variant="primary" type="submit" busy={save.isPending} disabled={!dirty}>
            {t('common.save')}
          </Button>
        </div>
      </form>

      {data.config.on && (
        <>
          <h3 className="mb-2 mt-6 text-sm font-semibold">{t('subtitles.library')}</h3>
          {!st.ffprobe && <p className="mb-2 text-sm text-gold">{t('subtitles.noFFprobe')}</p>}
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Badge tone="ok">{t('subtitles.complete', { count: c.complete, files: c.files })}</Badge>
            {c.pending > 0 && <Badge>{t('subtitles.pending', { count: c.pending })}</Badge>}
            {c.missing > 0 && <Badge tone="gold">{t('subtitles.missing', { count: c.missing })}</Badge>}
            {c.waiting > 0 && <Badge tone="gold">{t('subtitles.waiting', { count: c.waiting })}</Badge>}
            {st.running && <Badge tone="marquee">{t('subtitles.running', { done: st.done })}</Badge>}
          </div>
          <p className="mt-2 text-sm text-muted">
            {t('subtitles.today', { looked: st.today.looked, limit: st.today.limit })}
            {c.pending > 0 && st.today.limit > 0 && ` ${t('subtitles.doneBy', { date: day(new Date(Date.now() + Math.ceil(c.pending / st.today.limit) * 86400000).toISOString()) })}`}
          </p>
          {st.error && <p className="mt-2 text-sm text-bad">{st.error}</p>}
          <div className="mt-3">
            <Button onClick={() => again.mutate()} busy={again.isPending} disabled={c.missing + c.waiting === 0}>
              <RefreshCw className="size-4" aria-hidden /> {t('subtitles.again')}
            </Button>
          </div>
          <ErrorNote error={again.error} />
          <h3 className="mb-2 mt-6 text-sm font-semibold">{t('subtitles.recent')}</h3>
          {st.recent.length === 0 ? (
            <p className="text-sm text-muted">{t('subtitles.none')}</p>
          ) : (
            <ul className="max-h-80 divide-y divide-line overflow-y-auto rounded-md border border-line text-sm">
              {st.recent.map((r, i) => (
                <li key={i} className="flex flex-wrap items-baseline gap-x-3 px-3 py-2">
                  <Link to={`/${r.kind === 'movie' ? 'movies' : 'shows'}/${r.titleId}`} className="font-medium hover:underline">
                    {r.title}
                    {r.kind === 'series' && r.season !== undefined && r.episode ? ` ${episodeCode(r.season, r.episode)}` : ''}
                  </Link>
                  <span className="text-muted">
                    {name(r.language)} · {providerName[r.provider] ?? r.provider} · {when(r.at)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </Card>
  )
}
