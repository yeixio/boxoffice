import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ListPlus, Pencil, RefreshCw, Trash2 } from 'lucide-react'
import { del, get, post, put, type FollowedList, type FollowedLists, type ListItem, type ListPreview, type ListState } from '../api'
import { Badge, Button, Card, ErrorNote, Field, Input, Modal, Select, Spinner, Toggle } from '../ui'
import { useProfiles, useRootFolders } from '../hooks'
import { when } from '../format'

const sourceName = { tmdb: 'TMDB', trakt: 'Trakt', plex: 'Plex' }
// The most titles Box Office reads from one list.
const longest = 2000

type Form = Pick<FollowedList, 'name' | 'enabled' | 'ask' | 'search' | 'movies' | 'shows' | 'movieProfile' | 'movieRoot' | 'showProfile' | 'showRoot'>

// ListsCard is Settings → Library → Lists: lists kept on TMDB, Trakt or
// Plex, whose titles are added here by themselves.
export function ListsCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const lists = useQuery({ queryKey: ['lists'], queryFn: () => get<FollowedLists>('/lists'), refetchInterval: (q) => (q.state.data?.lists.some((l) => l.enabled && !l.checkedAt) ? 3000 : false) })
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<FollowedList | null>(null)
  const [viewing, setViewing] = useState<FollowedList | null>(null)
  const [stopping, setStopping] = useState<FollowedList | null>(null)
  const refresh = () => qc.invalidateQueries({ queryKey: ['lists'] })
  const check = useMutation({ mutationFn: (l: FollowedList) => post(`/lists/${l.id}/check`), onSettled: refresh })
  const stop = useMutation({
    mutationFn: (l: FollowedList) => del(`/lists/${l.id}`),
    onSuccess: () => {
      setStopping(null)
      refresh()
    },
  })
  const d = lists.data
  return (
    <Card>
      <div className="mb-1 flex items-center justify-between gap-3">
        <h2 className="font-display text-lg font-semibold">{t('lists.title')}</h2>
        <Button variant="ghost" onClick={() => setAdding(true)}>
          <ListPlus className="size-4" aria-hidden /> {t('lists.follow')}
        </Button>
      </div>
      <p className="mb-4 text-sm text-muted">{t('lists.sub', { hours: d?.everyHours ?? 6, most: d?.perCheck ?? 20 })}</p>
      {lists.isPending && <Spinner />}
      <ErrorNote error={lists.error ?? check.error} />
      {d?.lists.length === 0 && <p className="text-sm text-muted">{t('lists.none')}</p>}
      <ul className="flex flex-col divide-y divide-line">
        {d?.lists.map((l) => (
          <li key={l.id} className="flex flex-col gap-1 py-3 text-sm">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span className="min-w-0 break-words font-medium">{l.name}</span>
              <Badge>{sourceName[l.source]}</Badge>
              {!l.enabled && <Badge tone="gold">{t('common.paused')}</Badge>}
              {(l.counts.waiting ?? 0) > 0 && <Badge tone="marquee">{t('lists.state.waiting')}: {l.counts.waiting}</Badge>}
              <div className="flex-1" />
              <Button variant="ghost" onClick={() => setViewing(l)}>
                {t('lists.titles')}
              </Button>
              <button
                onClick={() => check.mutate(l)}
                disabled={check.isPending}
                aria-label={`${t('lists.checkNow')}: ${l.name}`}
                title={t('lists.checkNow')}
                className="rounded p-1.5 text-muted hover:bg-line hover:text-ink disabled:opacity-50"
              >
                <RefreshCw className={`size-4 ${check.isPending && check.variables?.id === l.id ? 'animate-spin' : ''}`} aria-hidden />
              </button>
              <button onClick={() => setEditing(l)} aria-label={t('lists.edit', { name: l.name })} className="rounded p-1.5 text-muted hover:bg-line hover:text-ink">
                <Pencil className="size-4" aria-hidden />
              </button>
              <button onClick={() => (stop.reset(), setStopping(l))} aria-label={`${t('lists.stop')}: ${l.name}`} className="rounded p-1.5 text-muted hover:bg-line hover:text-bad">
                <Trash2 className="size-4" aria-hidden />
              </button>
            </div>
            <p className="break-all text-xs text-muted">{l.address}</p>
            <p className="text-xs text-muted">
              {l.checkedAt ? t('lists.checked', { when: when(l.checkedAt) }) : t('lists.notChecked')}
              {' · '}
              {t('lists.counts', { added: l.counts.added ?? 0, had: l.counts.had ?? 0, waiting: l.counts.waiting ?? 0 })}
            </p>
            {l.lastError && <p className="text-xs text-bad">{t('lists.error', { error: l.lastError })}</p>}
          </li>
        ))}
      </ul>
      {adding && d && <FollowList traktClient={d.traktClient} onClose={() => setAdding(false)} />}
      {editing && <EditList list={editing} onClose={() => setEditing(null)} />}
      {viewing && <ListTitles list={viewing} onClose={() => (setViewing(null), refresh())} />}
      {stopping && (
        <Modal title={t('lists.stop')} onClose={() => setStopping(null)}>
          <div className="flex flex-col gap-4">
            <p className="break-words text-sm">{t('lists.stopBody', { name: stopping.name })}</p>
            <ErrorNote error={stop.error} />
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setStopping(null)}>
                {t('common.cancel')}
              </Button>
              <Button variant="danger" busy={stop.isPending} onClick={() => stop.mutate(stopping)}>
                {t('lists.stop')}
              </Button>
            </div>
          </div>
        </Modal>
      )}
    </Card>
  )
}

function Steps({ keys }: { keys: string[] }) {
  const { t } = useTranslation()
  return (
    <ol className="flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 ps-8 pe-4 text-sm text-muted">
      {keys.map((k) => (
        <li key={k} className="break-words">
          {t(k)}
        </li>
      ))}
    </ol>
  )
}

// FollowList asks for the list's address, shows what's on it, and then how
// its titles should be added.
function FollowList({ traktClient, onClose }: { traktClient: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [address, setAddress] = useState('')
  const [client, setClient] = useState('')
  // Trakt's client ID is asked for when Trakt wants one, or to change it.
  const [askClient, setAskClient] = useState(false)
  const [form, setForm] = useState<Form | null>(null)
  const look = useMutation({
    mutationFn: async () => {
      if (askClient && client.trim()) await put('/lists/trakt', { clientId: client.trim() })
      return post<ListPreview>('/lists/look', { address })
    },
    onSuccess: (p) => {
      if (p.needsTraktClient) {
        setAskClient(true)
        return
      }
      setAskClient(false)
      qc.invalidateQueries({ queryKey: ['lists'] })
      setForm({ name: p.name, enabled: true, ask: false, search: true, movies: p.movies > 0 || p.shows === 0, shows: p.shows > 0, movieProfile: 0, movieRoot: '', showProfile: 0, showRoot: '' })
    },
  })
  const pre = look.data && !look.data.needsTraktClient ? look.data : undefined
  const trakt = /trakt\.tv/i.test(address)
  const follow = useMutation({
    mutationFn: (f: Form) => post<FollowedList>('/lists', { ...f, address: pre?.address ?? address }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['lists'] })
      onClose()
    },
  })
  return (
    <Modal title={t('lists.follow')} onClose={onClose} wide>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          if (form && pre) follow.mutate(form)
          else look.mutate()
        }}
      >
        <Field label={t('lists.address')} hint={t('lists.addressHint')}>
          {(id) => (
            <Input
              id={id}
              autoFocus
              autoComplete="off"
              inputMode="url"
              value={address}
              placeholder="https://www.themoviedb.org/list/12345"
              onChange={(e) => {
                setAddress(e.target.value)
                setForm(null)
                look.reset()
              }}
            />
          )}
        </Field>
        {!pre && !askClient && !trakt && (
          <details className="text-sm">
            <summary className="cursor-pointer text-muted hover:text-ink">{t('lists.plexWhere')}</summary>
            <div className="mt-2">
              <Steps keys={['lists.plex1', 'lists.plex2', 'lists.plex3']} />
            </div>
          </details>
        )}
        {askClient && (
          <div className="flex flex-col gap-3 rounded-lg border border-line p-4">
            <p className="text-sm">{t('lists.traktNeeded')}</p>
            <Steps keys={['lists.trakt1', 'lists.trakt2', 'lists.trakt3']} />
            <Field label={t('lists.traktClient')}>{(id) => <Input id={id} autoComplete="off" spellCheck={false} value={client} onChange={(e) => setClient(e.target.value)} />}</Field>
          </div>
        )}
        {!askClient && !pre && trakt && traktClient && (
          <button type="button" className="self-start text-sm text-muted underline hover:text-ink" onClick={() => setAskClient(true)}>
            {t('lists.traktChange')}
          </button>
        )}
        <ErrorNote error={look.error} />
        {pre && form && (
          <>
            <p className="break-words rounded-lg border border-line bg-paper p-3 text-sm">
              <Badge>{sourceName[pre.source]}</Badge> {t('lists.found', { name: pre.name, movies: pre.movies, shows: pre.shows, sample: pre.sample.slice(0, 4).join(', ') })}
              {pre.capped && <span className="mt-1 block text-gold">{t('lists.capped', { most: longest.toLocaleString() })}</span>}
            </p>
            <ListForm form={form} set={setForm} />
            <ErrorNote error={follow.error} />
          </>
        )}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          {pre && form ? (
            <Button type="submit" busy={follow.isPending} disabled={!form.name.trim() || (!form.movies && !form.shows)}>
              {t('lists.follow')}
            </Button>
          ) : (
            <Button type="submit" busy={look.isPending} disabled={!address.trim() || (askClient && !client.trim() && !traktClient)}>
              {t('lists.look')}
            </Button>
          )}
        </div>
      </form>
    </Modal>
  )
}

// ListForm is how a list's titles are added: which kinds, where to, and
// whether to ask first.
function ListForm({ form, set }: { form: Form; set: (f: Form) => void }) {
  const { t } = useTranslation()
  const profiles = useProfiles()
  const movieRoots = useRootFolders('movie')
  const showRoots = useRootFolders('series')
  const profileOptions = (profiles.data ?? []).map((p) => ({ value: p.id, label: p.name }))
  const kinds = [
    { on: form.movies, label: t('nav.movies'), roots: movieRoots.data, none: t('add.noMovieRoot'), profile: form.movieProfile, root: form.movieRoot, change: (v: Partial<Form>) => set({ ...form, ...v }), keys: ['movies', 'movieProfile', 'movieRoot'] as const },
    { on: form.shows, label: t('nav.shows'), roots: showRoots.data, none: t('add.noShowRoot'), profile: form.showProfile, root: form.showRoot, change: (v: Partial<Form>) => set({ ...form, ...v }), keys: ['shows', 'showProfile', 'showRoot'] as const },
  ]
  return (
    <div className="flex flex-col gap-4">
      <Field label={t('lists.name')}>{(id) => <Input id={id} maxLength={80} value={form.name} onChange={(e) => set({ ...form, name: e.target.value })} />}</Field>
      <fieldset className="flex flex-col gap-3">
        <legend className="mb-2 text-sm font-medium">{t('lists.what')}</legend>
        {kinds.map((k) => (
          <div key={k.keys[0]} className="flex flex-col gap-3 rounded-lg border border-line p-3">
            <Toggle checked={k.on} onChange={(v) => k.change({ [k.keys[0]]: v })} label={k.label} />
            {k.on && k.roots?.length === 0 && <p className="text-sm text-bad">{k.none}</p>}
            {k.on && !!k.roots?.length && (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label={t('add.profile')}>
                  {(id) => <Select id={id} value={k.profile || (profileOptions[0]?.value ?? 0)} onChange={(v) => k.change({ [k.keys[1]]: Number(v) })} options={profileOptions} />}
                </Field>
                <Field label={t('add.root')}>
                  {(id) => <Select id={id} value={k.root || k.roots![0].path} onChange={(v) => k.change({ [k.keys[2]]: v })} options={k.roots!.map((r) => ({ value: r.path, label: r.path }))} />}
                </Field>
              </div>
            )}
          </div>
        ))}
      </fieldset>
      <Toggle
        checked={form.ask}
        onChange={(v) => set({ ...form, ask: v })}
        label={
          <span>
            {t('lists.ask')}
            <span className="block text-xs text-muted">{t('lists.askHint')}</span>
          </span>
        }
      />
      <Toggle checked={form.search} onChange={(v) => set({ ...form, search: v })} label={t('lists.search')} />
    </div>
  )
}

function EditList({ list, onClose }: { list: FollowedList; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [form, setForm] = useState<Form>(list)
  const save = useMutation({
    mutationFn: (f: Form) => put(`/lists/${list.id}`, f),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['lists'] })
      onClose()
    },
  })
  return (
    <Modal title={list.name} onClose={onClose} wide>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate(form)
        }}
      >
        <Toggle checked={form.enabled} onChange={(v) => setForm({ ...form, enabled: v })} label={t('lists.on')} />
        <ListForm form={form} set={setForm} />
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button type="submit" busy={save.isPending} disabled={!form.name.trim() || (!form.movies && !form.shows)}>
            {t('common.save')}
          </Button>
        </div>
      </form>
    </Modal>
  )
}

const tone: Record<ListState, 'plain' | 'ok' | 'bad' | 'gold' | 'marquee'> = { waiting: 'marquee', added: 'ok', had: 'plain', removed: 'gold', skipped: 'plain', failed: 'bad' }
// Waiting titles first: they're what needs an answer.
const order: ListState[] = ['waiting', 'failed', 'added', 'removed', 'skipped', 'had']

// ListTitles is what a list has had, and what became of each. Titles that
// wait get a yes or a no; ones left out or removed can be added after all.
function ListTitles({ list, onClose }: { list: FollowedList; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const items = useQuery({ queryKey: ['lists', list.id, 'items'], queryFn: () => get<ListItem[]>(`/lists/${list.id}/items`) })
  const decide = useMutation({
    mutationFn: (v: { item: ListItem; add: boolean }) => post<ListItem>(`/lists/${list.id}/items`, { kind: v.item.kind, tmdbId: v.item.tmdbId, add: v.add }),
    onSettled: () => qc.invalidateQueries({ queryKey: ['lists', list.id, 'items'] }),
  })
  const sorted = [...(items.data ?? [])].sort((a, b) => order.indexOf(a.state) - order.indexOf(b.state) || a.title.localeCompare(b.title))
  const busy = (it: ListItem) => decide.isPending && decide.variables?.item.tmdbId === it.tmdbId && decide.variables.item.kind === it.kind
  return (
    <Modal title={list.name} onClose={onClose} wide>
      <div className="flex flex-col gap-3">
        {items.isPending && <Spinner />}
        <ErrorNote error={items.error ?? decide.error} />
        {items.data?.length === 0 && <p className="text-sm text-muted">{t('lists.noTitles')}</p>}
        <ul className="flex max-h-[60vh] flex-col divide-y divide-line overflow-y-auto">
          {sorted.map((it) => (
            <li key={it.kind + it.tmdbId} className="flex flex-wrap items-center gap-x-2 gap-y-1 py-2 text-sm">
              <span className="min-w-40 flex-1 break-words">
                {it.title} {!!it.year && <span className="text-muted">({it.year})</span>}
                {it.note && <span className="block text-xs text-bad">{it.note}</span>}
              </span>
              <Badge tone={tone[it.state]}>{t(`lists.state.${it.state}`)}</Badge>
              {it.state !== 'added' && it.state !== 'had' && (
                <Button variant="ghost" busy={busy(it)} onClick={() => decide.mutate({ item: it, add: true })}>
                  {t('settings.add')}
                </Button>
              )}
              {(it.state === 'waiting' || it.state === 'failed') && (
                <Button variant="ghost" disabled={busy(it)} onClick={() => decide.mutate({ item: it, add: false })}>
                  {t('lists.no')}
                </Button>
              )}
            </li>
          ))}
        </ul>
      </div>
    </Modal>
  )
}
