import { LanguagePicker } from '../LanguagePicker'
import i18n from '../i18n'
import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Lock, Folder, FolderUp, KeyRound, Plus, Trash2 } from 'lucide-react'
import { del, get, post, put, type Status, type MediaType, type MediaTypeKey, type Profile, type Recycle, type RootFolder, type RootKind, type Settings as SettingsData, type ShowDefaults, type SystemInfo } from '../api'
import { bytes, day } from '../format'
import { monitorOptions, useProfiles, useQualities, useRootFolders, useShowDefaults } from '../hooks'
import { AutoSearchSettings, Connections, DataCapSettingsCard, DownloadSettings, NumberField } from './Connections'
import { ScanButton, scannable } from './LibraryScan'
import { EreaderSettingsCard } from './Ereaders'
import { ProblemsCard } from './Problems'
import { Badge, Button, Card, ErrorNote, Field, Heading, Input, Modal, Select, Spinner, Toggle } from '../ui'

export function Settings() {
  const { t } = useTranslation()
  return (
    <>
      <Heading title={t('nav.settings')} />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="flex flex-col gap-6">
          <Language />
          <General />
          <MediaTypes />
          <Ereaders />
          <ConnectMatinee />
          <MoveIn />
          <Account />
        </div>
        <div className="flex flex-col gap-6">
          <RootFolders kind="movie" />
          <RootFolders kind="series" />
          <MediaTypeFolders />
          <NewShows />
          <RecycleFolders />
          <ProblemsCard />
          <Profiles />
        </div>
      </div>
      <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="flex flex-col gap-6">
          <DownloadSettings />
          <AutoSearchSettings />
          <DataCapSettingsCard />
        </div>
        <div className="flex flex-col gap-6">
          <Connections section="indexers" />
          <Connections section="news-servers" />
          <Connections section="download-clients" />
        </div>
      </div>
      <System />
    </>
  )
}

function useSettings() {
  return useQuery({ queryKey: ['settings'], queryFn: () => get<SettingsData>('/settings') })
}

// Language is the admin's own: it follows them to every browser they sign
// in on.
function Language() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const status = useQuery({ queryKey: ['status'], queryFn: () => get<Status>('/status') })
  const pick = useMutation({
    mutationFn: (language: string) => put('/me/language', { language }),
    onMutate: (language) => qc.setQueryData<Status>(['status'], (s) => s && s.user && { ...s, user: { ...s.user, language } }),
    onSettled: () => qc.invalidateQueries({ queryKey: ['status'] }),
  })
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('common:language.title')}</h2>
      <p className="mb-4 text-sm text-muted">{t('settings.languageHint')}</p>
      <LanguagePicker saved={status.data?.user?.language ?? (status.data ? '' : undefined)} busy={pick.isPending} onPick={(code) => pick.mutate(code)} />
      <ErrorNote error={pick.error} />
    </Card>
  )
}

function General() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const s = useSettings()
  const [key, setKey] = useState('')
  const save = useMutation({
    mutationFn: () => put<SettingsData>('/settings', { tmdbKey: key }),
    onSuccess: (d) => {
      qc.setQueryData(['settings'], d)
      setKey('')
    },
  })
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">TMDB</h2>
      <p className="mb-4 text-sm text-muted">
        <Trans i18nKey="settings.tmdbIntro" components={{ link: <a className="text-marquee" href="https://www.themoviedb.org/settings/api" target="_blank" rel="noreferrer" /> }} />
      </p>
      {s.data?.tmdbKeyBuiltIn && (
        <p className="mb-3 text-sm text-muted">{t('settings.tmdbBuiltIn')}</p>
      )}
      {s.data && (
        <p className="mb-3 text-sm">
          {s.data.hasTmdbKey ? <Badge tone="ok">{s.data.tmdbKeyBuiltIn ? t('settings.builtIn') : s.data.tmdbKeyFromEnv ? t('settings.onServer') : t('settings.savedBadge')}</Badge> : <Badge tone="bad">{t('settings.notSet')}</Badge>}
        </p>
      )}
      <div className="flex gap-2">
        <Input aria-label={t('settings.tmdbKey')} placeholder={s.data?.hasTmdbKey ? t('settings.replaceKey') : t('settings.tmdbKey')} value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off" />
        <Button variant="primary" busy={save.isPending} disabled={!key} onClick={() => save.mutate()}>
          {t('settings.checkSave')}
        </Button>
      </div>
      <ErrorNote error={save.error} />
    </Card>
  )
}

// MoveIn is where Move in lives once it has left the menu.
function MoveIn() {
  const { t } = useTranslation()
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('nav.import')}</h2>
      <p className="mb-4 text-sm text-muted">{t('settings.moveInSub')}</p>
      <Link to="/import">
        <Button>{t('settings.openMoveIn')}</Button>
      </Link>
    </Card>
  )
}

function ConnectMatinee() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const s = useSettings()
  const [shown, setShown] = useState(false)
  const [copied, setCopied] = useState('')
  const regen = useMutation({ mutationFn: () => post<SettingsData>('/settings/apikey'), onSuccess: (d) => qc.setQueryData(['settings'], d) })
  const copy = (what: string, v: string) => {
    navigator.clipboard?.writeText(v)
    setCopied(what)
    setTimeout(() => setCopied(''), 1500)
  }
  const origin = window.location.origin
  const rows = [[t('settings.address'), origin]]
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.matinee')}</h2>
      <p className="mb-4 text-sm text-muted">
        {t('settings.matineeSub')}
      </p>
      <dl className="flex flex-col gap-3 text-sm">
        {rows.map(([label, v]) => (
          <div key={label} className="flex items-center justify-between gap-3">
            <dt className="text-muted">{label}</dt>
            <dd className="flex items-center gap-2">
              <code className="break-all">{v}</code>
              <button onClick={() => copy(label, v)} aria-label={t('settings.copyThe', { what: label })} className="rounded p-1 text-muted hover:bg-line">
                <Copy className="size-3.5" />
              </button>
              {copied === label && <span className="text-xs text-ok">{t('common.copied')}</span>}
            </dd>
          </div>
        ))}
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted">{t('settings.apiKey')}</dt>
          <dd className="flex items-center gap-2">
            <code className="break-all">{s.data ? (shown ? s.data.apiKey : '•'.repeat(16)) : '…'}</code>
            <button onClick={() => setShown(!shown)} className="text-xs text-marquee">
              {shown ? t('settings.hide') : t('settings.show')}
            </button>
            {s.data && (
              <button onClick={() => copy('key', s.data.apiKey)} aria-label={t('settings.copyKey')} className="rounded p-1 text-muted hover:bg-line">
                <Copy className="size-3.5" />
              </button>
            )}
            {copied === 'key' && <span className="text-xs text-ok">{t('common.copied')}</span>}
          </dd>
        </div>
      </dl>
      {s.data?.apiKeyFromEnv ? (
        <p className="mt-4 text-xs text-muted">{t('settings.keyEnv')}</p>
      ) : (
        <Button className="mt-4" busy={regen.isPending} onClick={() => regen.mutate()}>
          <KeyRound className="size-4" aria-hidden /> {t('settings.newKey')}
        </Button>
      )}
      <ErrorNote error={regen.error} />
    </Card>
  )
}

// MediaTypes turns on the kinds of media beyond movies and TV. Each is off
// until it's turned on here.
function MediaTypes() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const s = useSettings()
  const save = useMutation({
    mutationFn: (v: { key: MediaTypeKey; on: boolean }) => put<SettingsData>('/settings', { mediaTypes: { [v.key]: v.on } }),
    onSuccess: (d) => {
      qc.setQueryData(['settings'], d)
      qc.invalidateQueries({ queryKey: ['rootfolders'] })
    },
  })
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.more')}</h2>
      <p className="mb-4 text-sm text-muted">
        {t('settings.moreSub')}
      </p>
      {s.isPending && <Spinner />}
      <ErrorNote error={s.error} />
      <div className="flex flex-col gap-4">
        {s.data?.mediaTypes.map((m) => (
          <Toggle
            key={m.key}
            checked={m.enabled}
            disabled={save.isPending}
            onChange={(on) => save.mutate({ key: m.key, on })}
            label={
              <span>
                <span className="font-medium">{typeName(m)}</span>
                {m.replaces && <span className="text-muted"> {t('settings.inPlaceOf', { app: m.replaces })}</span>}
                <span className="block text-sm text-muted">{t(`mediaTypes.${m.key}.summary`, { defaultValue: m.summary })}</span>
              </span>
            }
          />
        ))}
      </div>
      {s.data?.mediaTypes.some((m) => m.enabled) && (
        <p className="mt-4 text-sm text-muted">{t('settings.pickRoot')}</p>
      )}
      <ErrorNote error={save.error} />
    </Card>
  )
}

// Ereaders sets up sending ebooks to e-readers, once ebooks are on.
function Ereaders() {
  const s = useSettings()
  const on = !!s.data?.mediaTypes.find((m) => m.key === 'ebook')?.enabled
  // A book's "set up e-readers" link lands here.
  const { hash } = useLocation()
  useEffect(() => {
    if (on && hash === '#ereaders') document.getElementById('ereaders')?.scrollIntoView()
  }, [on, hash])
  if (!on) return null
  return (
    <div id="ereaders" className="scroll-mt-20">
      <EreaderSettingsCard />
    </div>
  )
}

// MediaTypeFolders shows a root folder card for each media type that's on.
function MediaTypeFolders() {
  const s = useSettings()
  return (
    <>
      {s.data?.mediaTypes
        .filter((m) => m.enabled)
        .map((m) => (
          <RootFolders key={m.key} kind={m.key} type={m} />
        ))}
    </>
  )
}

// typeName is a media type's name in the page's language.
const typeName = (m: MediaType) => i18n.t(`mediaTypes.${m.key}.name`, { defaultValue: m.name })

// rootTitle heads a kind's root folders, and addTitle its folder picker.
const rootTitle = (kind: RootKind, type?: MediaType) =>
  kind === 'movie' || kind === 'series' ? i18n.t(`settings.roots.${kind}`) : i18n.t('settings.roots.other', { name: type ? typeName(type) : kind })
const addTitle = (kind: RootKind, type?: MediaType) =>
  kind === 'movie' || kind === 'series' ? i18n.t(`settings.addRoot.${kind}`) : i18n.t('settings.addRoot.other', { name: type ? typeName(type) : kind })

function RootFolders({ kind, type }: { kind: RootKind; type?: MediaType }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const folders = useRootFolders(kind)
  const [picking, setPicking] = useState(false)
  const remove = useMutation({
    mutationFn: (f: RootFolder) => del(`/rootfolders/${f.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rootfolders', kind] })
      qc.invalidateQueries({ queryKey: ['settings'] })
    },
  })
  return (
    <Card>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold">{rootTitle(kind, type)}</h2>
        <Button variant="ghost" onClick={() => setPicking(true)}>
          <Plus className="size-4" aria-hidden /> {t('settings.add')}
        </Button>
      </div>
      {folders.isPending && <Spinner />}
      {folders.data?.length === 0 && <p className="text-sm text-muted">{t('settings.noRoots')}</p>}
      {folders.data?.length === 0 && type && type.suggested.length > 0 && <SuggestedFolders kind={kind} type={type} />}
      <ul className="flex flex-col divide-y divide-line">
        {folders.data?.map((f) => (
          <li key={f.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm">
            <Folder className="size-4 text-muted" aria-hidden />
            <code className="min-w-48 flex-1 break-all">{f.path}</code>
            {f.accessible ? <span className="text-xs text-muted">{t('settings.free', { size: bytes(f.freeSpace) })}</span> : <Badge tone="bad">{t('settings.cantSee')}</Badge>}
            {f.accessible && scannable(kind) && <ScanButton folder={f} />}
            <button onClick={() => remove.mutate(f)} aria-label={t('settings.removePath', { path: f.path })} className="rounded p-1 text-muted hover:bg-line hover:text-bad">
              <Trash2 className="size-4" />
            </button>
          </li>
        ))}
      </ul>
      {scannable(kind) && !!folders.data?.length && (
        <p className="mt-2 text-xs text-muted">{t(`settings.scanFor.${kind === 'movie' || kind === 'series' || kind === 'music' ? kind : 'books'}`)}</p>
      )}
      <ErrorNote error={remove.error} />
      {picking && <FolderPicker kind={kind} title={addTitle(kind, type)} onClose={() => setPicking(false)} />}
    </Card>
  )
}

// NewShows sets which seasons a new show watches when nobody picks, for
// shows added here and requests from Matinee. Shows already on the list,
// and ones brought over by Move in, keep what they have.
function NewShows() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const d = useShowDefaults()
  const save = useMutation({
    mutationFn: (s: Partial<ShowDefaults>) => put<ShowDefaults>('/series/defaults', { ...d.data!, ...s }),
    onSuccess: (v) => qc.setQueryData(['show-defaults'], v),
  })
  if (!d.data) return null
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.newShows')}</h2>
      <p className="mb-4 text-sm text-muted">
        {t('settings.newShowsSub')}
      </p>
      <div className="flex flex-col gap-4">
        <Field label={t('add.which')} hint={t('settings.specialsOut')}>
          {(id) => <Select id={id} value={d.data.monitor} onChange={(v) => save.mutate({ monitor: v })} options={monitorOptions(d.data.recent)} />}
        </Field>
        {d.data.monitor === 'recent' && (
          <NumberField
            label={t('add.howMany')}
            value={d.data.recent}
            max={99}
            busy={save.isPending}
            onSave={(v) => save.mutate({ recent: v })}
            hint={t('settings.howManyHint')}
          />
        )}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}

// RecycleFolders shows the copies upgrades replaced, set aside in each
// library's .recycle folder, and frees their space.
function RecycleFolders() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const recycle = useQuery({ queryKey: ['recycle'], queryFn: () => get<Recycle>('/recycle') })
  const [confirm, setConfirm] = useState(false)
  const set = (r: Recycle) => qc.setQueryData(['recycle'], r)
  const keep = useMutation({ mutationFn: (keepDays: number) => put<Recycle>('/recycle', { keepDays }), onSuccess: set })
  const empty = useMutation({
    mutationFn: () => post<Recycle>('/recycle/empty'),
    onSuccess: (r) => {
      set(r)
      setConfirm(false)
    },
  })
  const folders = recycle.data?.folders ?? []
  const total = folders.reduce((n, f) => n + f.size, 0)
  const files = folders.reduce((n, f) => n + f.count, 0)
  return (
    <Card>
      <h2 className="mb-1 font-display text-lg font-semibold">{t('settings.recycle')}</h2>
      <p className="mb-4 text-sm text-muted">
        <Trans i18nKey="settings.recycleSub" components={{ code: <code /> }} />
      </p>
      {recycle.isPending ? (
        <Spinner />
      ) : recycle.isError ? (
        <ErrorNote error={recycle.error} />
      ) : (
        <div className="flex flex-col gap-4">
          {folders.length === 0 ? (
            <p className="text-sm">{t('settings.nothingAside')}</p>
          ) : (
            <ul className="flex flex-col divide-y divide-line text-sm">
              {folders.map((f) => (
                <li key={f.folder} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                  <code className="min-w-0 flex-1 basis-48 break-all">{f.folder}</code>
                  <span className="text-muted">
                    {t('scan.files', { count: f.count, size: bytes(f.size) })}
                    {f.oldest && t('settings.oldest', { date: day(f.oldest) })}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <Field label={t('settings.deleteReplaced')} hint={t('settings.deleteReplacedHint')}>
            {(id) => (
              <Select
                id={id}
                value={recycle.data.keepDays}
                onChange={(v) => keep.mutate(Number(v))}
                options={[
                  { value: 0, label: t('settings.never') },
                  ...[7, 14, 30, 90].map((n) => ({ value: n, label: t('settings.after', { count: n }) })),
                  ...([0, 7, 14, 30, 90].includes(recycle.data.keepDays) ? [] : [{ value: recycle.data.keepDays, label: t('settings.after', { count: recycle.data.keepDays }) }]),
                ]}
              />
            )}
          </Field>
          <div>
            <Button variant="danger" disabled={total === 0} onClick={() => setConfirm(true)}>
              <Trash2 className="size-4" aria-hidden /> {t('settings.empty')}{total > 0 && ` (${bytes(total)})`}
            </Button>
          </div>
          <ErrorNote error={keep.error} />
        </div>
      )}
      {confirm && (
        <Modal title={t('settings.emptyTitle')} onClose={() => setConfirm(false)}>
          <p className="text-sm">
            {t('settings.emptyBody', { count: files, size: bytes(total) })}
          </p>
          <ErrorNote error={empty.error} />
          <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button variant="ghost" onClick={() => setConfirm(false)}>
              {t('dlBulk.keep')}
            </Button>
            <Button variant="danger" busy={empty.isPending} onClick={() => empty.mutate()}>
              <Trash2 className="size-4" aria-hidden /> {t('remove.delete', { size: bytes(total) })}
            </Button>
          </div>
        </Modal>
      )}
    </Card>
  )
}

// SuggestedFolders offers, with one click, the folders Box Office found
// where a media type's library is usually mounted.
function SuggestedFolders({ kind, type }: { kind: RootKind; type: MediaType }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const add = useMutation({
    mutationFn: (path: string) => post('/rootfolders', { kind, path }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rootfolders', kind] })
      qc.invalidateQueries({ queryKey: ['settings'] })
    },
  })
  return (
    <div className="mt-2 text-sm">
      <p className="mb-2">{t(type.suggested.length === 1 ? 'settings.foundOne' : 'settings.foundMany', { name: typeName(type) })}</p>
      <div className="flex flex-wrap gap-2">
        {type.suggested.map((p) => (
          <Button key={p} busy={add.isPending && add.variables === p} onClick={() => add.mutate(p)}>
            <Plus className="size-4" aria-hidden /> <code>{p}</code>
          </Button>
        ))}
      </div>
      <ErrorNote error={add.error} />
    </div>
  )
}

function FolderPicker({ kind, title, onClose }: { kind: RootKind; title: string; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [path, setPath] = useState('/')
  const [typed, setTyped] = useState('/')
  const list = useQuery({ queryKey: ['fs', path], queryFn: () => get<{ path: string; parent: string; folders: string[] }>(`/fs?path=${encodeURIComponent(path)}`) })
  const add = useMutation({
    mutationFn: () => post('/rootfolders', { kind, path: typed }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rootfolders', kind] })
      qc.invalidateQueries({ queryKey: ['settings'] })
      onClose()
    },
  })
  const go = (p: string) => {
    setPath(p)
    setTyped(p)
  }
  return (
    <Modal title={title} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <Input aria-label={t('files.folder')} value={typed} onChange={(e) => setTyped(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && go(typed)} />
        <ul className="max-h-72 overflow-y-auto rounded-md border border-line text-sm">
          {list.data?.parent && (
            <li>
              <button className="flex w-full items-center gap-2 px-3 py-1.5 hover:bg-line" onClick={() => go(list.data.parent)}>
                <FolderUp className="size-4 text-muted" aria-hidden /> ..
              </button>
            </li>
          )}
          {list.data?.folders.map((f) => (
            <li key={f}>
              <button className="flex w-full items-center gap-2 px-3 py-1.5 hover:bg-line" onClick={() => go((list.data.path === '/' ? '' : list.data.path) + '/' + f)}>
                <Folder className="size-4 text-muted" aria-hidden /> {f}
              </button>
            </li>
          ))}
        </ul>
        <ErrorNote error={list.error || add.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" busy={add.isPending} onClick={() => add.mutate()}>
            {t('settings.addFolder')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function Profiles() {
  const { t } = useTranslation()
  const profiles = useProfiles()
  const qualities = useQualities()
  const [editing, setEditing] = useState<Profile | 'new' | null>(null)
  const name = (id: number) => qualities.data?.find((q) => q.id === id)?.name ?? '?'
  return (
    <Card>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold">{t('settings.profiles')}</h2>
        <Button variant="ghost" onClick={() => setEditing('new')}>
          <Plus className="size-4" aria-hidden /> {t('settings.newProfile')}
        </Button>
      </div>
      <ul className="flex flex-col divide-y divide-line">
        {profiles.data?.map((p) => (
          <li key={p.id}>
            {p.locked ? (
              <div className="flex gap-2 py-2 text-sm">
                <Lock className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
                <span>
                  <span className="font-medium">{p.name}</span>
                  <span className="block text-xs text-muted">
                    {t('settings.lockedProfile')}
                  </span>
                </span>
              </div>
            ) : (
            <button className="w-full py-2 text-start text-sm hover:text-marquee" onClick={() => setEditing(p)}>
              <span className="font-medium">{p.name}</span>
              <span className="block text-xs text-muted">
                {t('settings.profileSummary', { count: p.allowed.length, cutoff: name(p.cutoff) })}
                {p.upgrade ? t('settings.upgradesOn') : ''}
              </span>
            </button>
            )}
          </li>
        ))}
      </ul>
      {editing && <ProfileEditor profile={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
    </Card>
  )
}

function ProfileEditor({ profile, onClose }: { profile: Profile | null; onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const qualities = useQualities()
  const [name, setName] = useState(profile?.name ?? '')
  const [allowed, setAllowed] = useState<number[]>(profile?.allowed ?? [])
  const [cutoff, setCutoff] = useState(profile?.cutoff ?? 0)
  const [upgrade, setUpgrade] = useState(profile?.upgrade ?? true)
  const done = () => {
    qc.invalidateQueries({ queryKey: ['profiles'] })
    onClose()
  }
  const save = useMutation({
    mutationFn: () => {
      const body = { name, allowed, cutoff: allowed.includes(cutoff) ? cutoff : allowed[allowed.length - 1], upgrade }
      return profile ? put(`/profiles/${profile.id}`, body) : post('/profiles', body)
    },
    onSuccess: done,
  })
  const remove = useMutation({ mutationFn: () => del(`/profiles/${profile!.id}`), onSuccess: done })
  const toggle = (id: number) => setAllowed(allowed.includes(id) ? allowed.filter((x) => x !== id) : [...allowed, id])
  return (
    <Modal title={profile ? t('settings.edit', { name: profile.name }) : t('settings.newProfileTitle')} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <Field label={t('alerts.name')}>{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
        <div>
          <div className="mb-2 text-sm font-medium">{t('settings.qualities')}</div>
          <ul className="grid grid-cols-2 gap-1.5 text-sm">
            {qualities.data?.map((q) => (
              <li key={q.id} className="flex items-center gap-2">
                <input type="checkbox" id={`q${q.id}`} checked={allowed.includes(q.id)} onChange={() => toggle(q.id)} className="accent-[var(--marquee)]" />
                <label htmlFor={`q${q.id}`} className="flex-1">
                  {q.name}
                </label>
                {allowed.includes(q.id) && (
                  <button onClick={() => setCutoff(q.id)} className={`text-xs ${cutoff === q.id ? 'font-semibold text-marquee' : 'text-muted hover:text-ink'}`}>
                    {cutoff === q.id ? t('settings.goodEnough') : t('settings.setGoodEnough')}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </div>
        <Toggle checked={upgrade} onChange={setUpgrade} label={t('settings.upgradeUntil')} />
        <ErrorNote error={save.error || remove.error} />
        <div className="flex justify-between gap-2">
          {profile ? (
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              {t('settings.delete')}
            </Button>
          ) : (
            <span />
          )}
          <Button variant="primary" busy={save.isPending} disabled={!name || allowed.length === 0} onClick={() => save.mutate()}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function Account() {
  const { t } = useTranslation()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const change = useMutation({
    mutationFn: () => put('/me/password', { current, new: next }),
    onSuccess: () => window.location.assign('/'),
  })
  return (
    <Card>
      <h2 className="mb-4 font-display text-lg font-semibold">{t('settings.password')}</h2>
      <div className="flex flex-col gap-3">
        <Field label={t('settings.current')}>{(id) => <Input id={id} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />}</Field>
        <Field label={t('settings.new')} hint={t('settings.newHint')}>
          {(id) => <Input id={id} type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />}
        </Field>
        <ErrorNote error={change.error} />
        <Button className="self-end" busy={change.isPending} disabled={!current || next.length < 8} onClick={() => change.mutate()}>
          {t('settings.change')}
        </Button>
      </div>
    </Card>
  )
}

function System() {
  const s = useQuery({ queryKey: ['system'], queryFn: () => get<SystemInfo>('/system') })
  if (!s.data) return null
  return (
    <p className="mt-8 text-xs text-muted">
      <Trans
        i18nKey="settings.system"
        values={{ version: s.data.version, os: s.data.os, go: s.data.go, db: s.data.database === 'postgres' ? 'PostgreSQL' : 'SQLite', dir: s.data.dataDir, since: day(s.data.startedAt) }}
        components={{ code: <code /> }}
      />
    </p>
  )
}
