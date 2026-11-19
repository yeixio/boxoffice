import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowUp, Plus } from 'lucide-react'
import { get, put, type AddRule, type AddRules, type RootKind } from '../api'
import { useProfiles, useRootFolders } from '../hooks'
import { Button, Card, ErrorNote, Field, Input, Modal, Select, Spinner } from '../ui'

// The languages a rule can ask for, as TMDB writes them. TMDB's "cn" is
// Cantonese.
const languages = ['en', 'ja', 'ko', 'zh', 'cn', 'es', 'fr', 'de', 'it', 'pt', 'hi', 'ta', 'te', 'ru', 'pl', 'nl', 'sv', 'da', 'no', 'fi', 'tr', 'th', 'id', 'ar', 'he', 'fa']

// languageName names a language in the language Box Office is shown in.
function languageName(code: string, shown: string): string {
  try {
    return new Intl.DisplayNames([shown], { type: 'language' }).of(code === 'cn' ? 'yue' : code) || code
  } catch {
    return code
  }
}

export const useAddRules = () => useQuery({ queryKey: ['addrules'], queryFn: () => get<AddRules>('/addrules') })

// useRuleFor is the rule that would pick the folder and profile of a title
// about to be added, if any.
export function useRuleFor(kind: RootKind, tmdbId: number) {
  return useQuery({
    queryKey: ['addrules', 'for', kind, tmdbId],
    queryFn: () => get<{ rule: AddRule | null }>(`/addrules/for?kind=${kind}&tmdbId=${tmdbId}`),
    enabled: tmdbId > 0,
    staleTime: 60_000,
    retry: false,
  })
}

// AddRulesCard is Settings' rules for new titles: which folder and quality
// profile a title gets by its genre or language.
export function AddRulesCard() {
  const { t, i18n } = useTranslation()
  const qc = useQueryClient()
  const rules = useAddRules()
  const profiles = useProfiles()
  const [editing, setEditing] = useState<AddRule | 'new' | null>(null)
  const save = useMutation({
    mutationFn: (list: AddRule[]) => put<AddRules>('/addrules', { rules: list }),
    onSuccess: (d) => {
      qc.setQueryData(['addrules'], d)
      qc.invalidateQueries({ queryKey: ['addrules', 'for'] })
      setEditing(null)
    },
  })
  const list = rules.data?.rules ?? []
  const says = (r: AddRule) => {
    const genres = (r.kind === 'movie' ? rules.data?.movieGenres : rules.data?.seriesGenres) ?? []
    const when = [...r.genres.map((id) => genres.find((g) => g.id === id)?.name ?? String(id)), r.language && languageName(r.language, i18n.language)].filter(Boolean).join(', ')
    const then = [r.rootFolder, profiles.data?.find((p) => p.id === r.profileId)?.name].filter(Boolean).join(' · ')
    return `${t(r.kind === 'movie' ? 'nav.movies' : 'nav.shows')}: ${when} → ${then}`
  }
  return (
    <Card>
      <div className="mb-3 flex items-center justify-between gap-2">
        <h2 className="font-display text-lg font-semibold">{t('rules.title')}</h2>
        <Button variant="ghost" onClick={() => setEditing('new')}>
          <Plus className="size-4" aria-hidden /> {t('rules.new')}
        </Button>
      </div>
      <p className="mb-3 text-sm text-muted">{t('rules.intro')}</p>
      {rules.isPending && <Spinner />}
      <ErrorNote error={rules.error ?? (editing ? null : save.error)} />
      {rules.data && list.length === 0 && <p className="text-sm text-muted">{t('rules.none')}</p>}
      <ul className="flex flex-col divide-y divide-line">
        {list.map((r, i) => (
          <li key={r.id} className="flex items-center gap-2">
            <button className="min-w-0 flex-1 py-2 text-start text-sm hover:text-marquee" onClick={() => setEditing(r)}>
              <span className="font-medium">{r.name}</span>
              <span className="block break-words text-xs text-muted">{says(r)}</span>
            </button>
            {i > 0 && (
              <Button variant="ghost" disabled={save.isPending} aria-label={`${t('rules.moveUp')}: ${r.name}`} title={t('rules.moveUp')} onClick={() => save.mutate([...list.slice(0, i - 1), r, list[i - 1], ...list.slice(i + 1)])}>
                <ArrowUp className="size-4" aria-hidden />
              </Button>
            )}
          </li>
        ))}
      </ul>
      {editing && rules.data && (
        <RuleEditor
          rule={editing === 'new' ? null : editing}
          data={rules.data}
          busy={save.isPending}
          error={save.error}
          onSave={(r) => save.mutate(r.id ? list.map((x) => (x.id === r.id ? r : x)) : [...list, r])}
          onDelete={(id) => save.mutate(list.filter((x) => x.id !== id))}
          onClose={() => {
            save.reset()
            setEditing(null)
          }}
        />
      )}
    </Card>
  )
}

function RuleEditor({
  rule,
  data,
  busy,
  error,
  onSave,
  onDelete,
  onClose,
}: {
  rule: AddRule | null
  data: AddRules
  busy: boolean
  error: unknown
  onSave: (r: AddRule) => void
  onDelete: (id: number) => void
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const [name, setName] = useState(rule?.name ?? '')
  const [kind, setKind] = useState<'movie' | 'series'>(rule?.kind ?? 'movie')
  const [genres, setGenres] = useState<number[]>(rule?.genres ?? [])
  const [language, setLanguage] = useState(rule?.language ?? '')
  const [rootFolder, setRootFolder] = useState(rule?.rootFolder ?? '')
  const [profileId, setProfileId] = useState(rule?.profileId ?? 0)
  const profiles = useProfiles()
  const folders = useRootFolders(kind)
  const all = kind === 'movie' ? data.movieGenres : data.seriesGenres
  const toggle = (id: number) => setGenres(genres.includes(id) ? genres.filter((g) => g !== id) : [...genres, id])
  const codes = language && !languages.includes(language) ? [...languages, language] : languages
  const named = codes.map((c) => ({ value: c, label: languageName(c, i18n.language) })).sort((a, b) => a.label.localeCompare(b.label, i18n.language))
  return (
    <Modal title={rule ? t('settings.edit', { name: rule.name }) : t('rules.new')} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label={t('alerts.name')}>{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
        <Field label={t('rules.kind')}>
          {(id) => (
            <Select
              id={id}
              value={kind}
              onChange={(v) => {
                // Movies and shows have their own genres and folders.
                setKind(v as 'movie' | 'series')
                setGenres([])
                setRootFolder('')
              }}
              options={[
                { value: 'movie', label: t('nav.movies') },
                { value: 'series', label: t('nav.shows') },
              ]}
            />
          )}
        </Field>
        <fieldset>
          <legend className="mb-1 text-sm font-medium">{t('rules.genres')}</legend>
          <p className="mb-2 text-xs text-muted">{all.length ? t('rules.genresHint') : t('rules.noGenres')}</p>
          <ul className="grid grid-cols-2 gap-1.5 text-sm">
            {all.map((g) => (
              <li key={g.id} className="flex items-center gap-2">
                <input type="checkbox" id={`genre${g.id}`} checked={genres.includes(g.id)} onChange={() => toggle(g.id)} className="accent-[var(--marquee)]" />
                <label htmlFor={`genre${g.id}`}>{g.name}</label>
              </li>
            ))}
          </ul>
        </fieldset>
        <Field label={t('rules.language')}>{(id) => <Select id={id} value={language} onChange={setLanguage} options={[{ value: '', label: t('rules.anyLanguage') }, ...named]} />}</Field>
        <Field label={t('add.root')}>
          {(id) => <Select id={id} value={rootFolder} onChange={setRootFolder} options={[{ value: '', label: t('rules.keepFolder') }, ...(folders.data ?? []).map((f) => ({ value: f.path, label: f.path }))]} />}
        </Field>
        <Field label={t('add.profile')}>
          {(id) => (
            <Select
              id={id}
              value={profileId}
              onChange={(v) => setProfileId(Number(v))}
              options={[{ value: 0, label: t('rules.keepProfile') }, ...(profiles.data ?? []).filter((p) => !p.locked).map((p) => ({ value: p.id, label: p.name }))]}
            />
          )}
        </Field>
        <ErrorNote error={error} />
        <div className="flex justify-between gap-2">
          {rule ? (
            <Button variant="danger" disabled={busy} onClick={() => onDelete(rule.id)}>
              {t('settings.delete')}
            </Button>
          ) : (
            <span />
          )}
          <Button
            variant="primary"
            busy={busy}
            disabled={!name.trim() || (genres.length === 0 && !language) || (!rootFolder && !profileId)}
            onClick={() => onSave({ id: rule?.id ?? 0, name, kind, genres, language, rootFolder, profileId })}
          >
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
