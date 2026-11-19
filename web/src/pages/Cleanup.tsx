import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { api, del, get, post, put, type Cleanup, type CleanupItem, type CleanupPreview, type CleanupRule, type CleanupTitle, type CleanupWatched } from '../api'
import { Badge, Button, Card, ErrorNote, Field, Input, Modal, Select, Spinner, Toggle } from '../ui'
import { bytes, day, when } from '../format'

type Form = Pick<CleanupRule, 'kind' | 'watched' | 'days' | 'endedOnly' | 'graceDays' | 'auto' | 'enabled'>

const fresh: Form = { kind: 'movie', watched: 'everyone', days: 90, endedOnly: true, graceDays: 14, auto: false, enabled: true }
// The fewest quiet days a rule can ask for, and the fewest days on the list.
const leastDays = (f: Form) => (f.watched === 'nobody' ? 30 : 1)
const leastGrace = (f: Form) => (f.auto ? 7 : 1)
const valid = (f: Form) => f.days >= leastDays(f) && f.days <= 3650 && f.graceDays >= leastGrace(f) && f.graceDays <= 365

// What was watched of a title, in a line.
function Plays({ about }: { about: CleanupTitle }) {
  const { t } = useTranslation()
  if (about.viewers === 0) return <>{t('cleanup.neverPlayed', { when: day(about.addedAt) })}</>
  return <>{t('cleanup.plays', { finished: about.finishedBy, viewers: about.viewers, when: day(about.lastPlayed) })}</>
}

// CleanupCard is Settings → Library → Clean-up: rules that pick out
// watched titles, the titles leaving soon, and the ones set to stay.
export function CleanupCard() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  // A new or changed rule lists its titles in the background.
  const [settling, setSettling] = useState(0)
  const cleanup = useQuery({ queryKey: ['cleanup'], queryFn: () => get<Cleanup>('/cleanup'), refetchInterval: () => (Date.now() < settling ? 2000 : false) })
  const [editing, setEditing] = useState<CleanupRule | 'new' | null>(null)
  const [removing, setRemoving] = useState<CleanupRule | null>(null)
  const [deleting, setDeleting] = useState<CleanupItem | null>(null)
  const [cap, setCap] = useState<string | null>(null)
  const show = (d: Cleanup) => qc.setQueryData(['cleanup'], d)
  const settle = () => {
    setSettling(Date.now() + 20_000)
    qc.invalidateQueries({ queryKey: ['cleanup'] })
  }
  const check = useMutation({ mutationFn: () => post<Cleanup>('/cleanup/check'), onSuccess: show })
  const keep = useMutation({ mutationFn: (i: CleanupItem) => post<Cleanup>(`/cleanup/items/${i.id}/keep`), onSuccess: show })
  const unkeep = useMutation({ mutationFn: (k: { kind: string; titleId: number }) => api<Cleanup>('DELETE', `/cleanup/kept/${k.kind}/${k.titleId}`), onSuccess: (d) => (show(d), settle()) })
  const remove = useMutation({
    mutationFn: (r: CleanupRule) => del(`/cleanup/rules/${r.id}`),
    onSuccess: () => {
      setRemoving(null)
      qc.invalidateQueries({ queryKey: ['cleanup'] })
    },
  })
  const saveCap = useMutation({ mutationFn: (n: number) => put<Cleanup>('/cleanup/settings', { dailyCap: n }), onSuccess: (d) => (show(d), setCap(null)) })
  const d = cleanup.data
  const kinds = [...new Set(d?.rules.map((r) => r.kind))]
  const stale = kinds.filter((k) => !d?.reports[k]?.fresh)
  const latest = kinds.map((k) => d?.reports[k]?.at).filter((at): at is string => !!at).sort()[0]
  const kindName = { movie: t('nav.movies'), series: t('nav.shows') }
  return (
    <Card>
      <div className="mb-1 flex items-center justify-between gap-3">
        <h2 className="font-display text-lg font-semibold">{t('cleanup.title')}</h2>
        <Button variant="ghost" onClick={() => setEditing('new')}>
          <Plus className="size-4" aria-hidden /> {t('cleanup.add')}
        </Button>
      </div>
      <p className="mb-4 text-sm text-muted">{t('cleanup.sub')}</p>
      {cleanup.isPending && <Spinner />}
      <ErrorNote error={cleanup.error ?? check.error ?? keep.error ?? unkeep.error} />
      {d?.rules.length === 0 && <p className="text-sm text-muted">{t('cleanup.none')}</p>}
      {d && d.rules.length > 0 && stale.length > 0 && (
        <p role="status" className="mb-3 rounded-md border border-gold/50 bg-gold/5 px-3 py-2 text-sm">
          {t('cleanup.noReport', { kinds: stale.map((k) => kindName[k]).join(', ') })}{' '}
          <Link to="/settings#matinee" className="font-medium text-marquee">
            {t('cleanup.connect')}
          </Link>
        </p>
      )}
      {d && d.rules.length > 0 && stale.length === 0 && latest && <p className="mb-3 text-xs text-muted">{t('cleanup.reported', { when: when(latest) })}</p>}
      <ul className="flex flex-col divide-y divide-line">
        {d?.rules.map((r) => (
          <li key={r.id} className="flex flex-col gap-1 py-3 text-sm">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span className="min-w-0 break-words font-medium">{t(`cleanup.rule.${r.kind}.${r.watched}`, { days: r.days })}</span>
              {r.auto ? <Badge tone="bad">{t('cleanup.autoBadge')}</Badge> : <Badge>{t('cleanup.listsBadge')}</Badge>}
              {!r.enabled && <Badge tone="gold">{t('common.paused')}</Badge>}
              <div className="flex-1" />
              <button onClick={() => setEditing(r)} aria-label={t('cleanup.edit')} title={t('cleanup.edit')} className="rounded p-1.5 text-muted hover:bg-line hover:text-ink">
                <Pencil className="size-4" aria-hidden />
              </button>
              <button onClick={() => (remove.reset(), setRemoving(r))} aria-label={t('cleanup.remove')} title={t('cleanup.remove')} className="rounded p-1.5 text-muted hover:bg-line hover:text-bad">
                <Trash2 className="size-4" aria-hidden />
              </button>
            </div>
            <p className="text-xs text-muted">
              {r.kind === 'series' && r.endedOnly && <>{t('cleanup.endedOnly')} · </>}
              {t('cleanup.grace', { days: r.graceDays })} · {t('cleanup.leaving', { titles: r.leaving, size: bytes(r.leavingSize) })}
            </p>
            {r.enabled && r.waiting === 'plays' && <p className="text-xs text-gold">{t('cleanup.noPlays')}</p>}
          </li>
        ))}
      </ul>

      {d && (d.items.length > 0 || d.rules.length > 0) && (
        <div className="mt-4 border-t border-line pt-4">
          <div className="mb-2 flex items-center justify-between gap-3">
            <h3 className="font-medium">{t('cleanup.leavingSoon')}</h3>
            <Button variant="ghost" busy={check.isPending} onClick={() => check.mutate()}>
              <RefreshCw className="size-4" aria-hidden /> {t('cleanup.checkNow')}
            </Button>
          </div>
          {d.items.length === 0 && <p className="text-sm text-muted">{t('cleanup.nothingLeaving')}</p>}
          <ul className="flex max-h-[28rem] flex-col divide-y divide-line overflow-y-auto">
            {d.items.map((i) => (
              <li key={i.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm">
                <div className="min-w-0 flex-1 basis-56">
                  <Link to={`/${i.kind === 'movie' ? 'movies' : 'shows'}/${i.titleId}`} className="break-words font-medium hover:text-marquee">
                    {i.about.title}
                    {i.about.year ? ` (${i.about.year})` : ''}
                  </Link>
                  <p className="text-xs text-muted">
                    {bytes(i.size)} · <Plays about={i.about} />
                  </p>
                  <p className="text-xs text-muted">{i.auto ? t('cleanup.deletedOn', { when: day(i.leavesAt) }) : t('cleanup.canGo', { when: day(i.listedAt) })}</p>
                  {i.problem && <p className="break-words text-xs text-bad">{i.problem}</p>}
                </div>
                <Button variant="ghost" busy={keep.isPending && keep.variables?.id === i.id} onClick={() => keep.mutate(i)}>
                  {t('cleanup.keep')}
                </Button>
                <Button variant="danger" onClick={() => setDeleting(i)}>
                  {t('cleanup.deleteNow')}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}

      {d && d.kept.length > 0 && (
        <div className="mt-4 border-t border-line pt-4">
          <h3 className="mb-1 font-medium">{t('cleanup.kept')}</h3>
          <p className="mb-2 text-xs text-muted">{t('cleanup.keptSub')}</p>
          <ul className="flex flex-col divide-y divide-line">
            {d.kept.map((k) => (
              <li key={`${k.kind}-${k.titleId}`} className="flex flex-wrap items-center gap-3 py-2 text-sm">
                <span className="min-w-0 flex-1 break-words">
                  {k.title}
                  {k.year ? ` (${k.year})` : ''}
                </span>
                <Button variant="ghost" busy={unkeep.isPending && unkeep.variables?.titleId === k.titleId} onClick={() => unkeep.mutate(k)}>
                  {t('cleanup.unkeep')}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}

      {d && d.rules.some((r) => r.auto) && (
        <form
          className="mt-4 flex flex-col gap-2 border-t border-line pt-4"
          onSubmit={(e) => {
            e.preventDefault()
            saveCap.mutate(Number(cap ?? d.dailyCap))
          }}
        >
          <Field label={t('cleanup.cap')} hint={t('cleanup.capHint', { titles: d.deletedToday })}>
            {(id) => (
              <div className="flex gap-2">
                <Input id={id} type="number" min={1} max={1000} className="max-w-32" value={cap ?? d.dailyCap} onChange={(e) => setCap(e.target.value)} />
                <Button type="submit" busy={saveCap.isPending} disabled={cap === null || !(Number(cap) >= 1 && Number(cap) <= 1000)}>
                  {t('common.save')}
                </Button>
              </div>
            )}
          </Field>
          <ErrorNote error={saveCap.error} />
        </form>
      )}

      {editing && <EditRule rule={editing === 'new' ? null : editing} cap={d?.dailyCap ?? 5} onClose={() => setEditing(null)} onSaved={() => (setEditing(null), settle())} />}
      {deleting && <DeleteItem item={deleting} onClose={() => setDeleting(null)} onDone={(v) => (show(v), setDeleting(null))} onChanged={() => qc.invalidateQueries({ queryKey: ['cleanup'] })} />}
      {removing && (
        <Modal title={t('cleanup.remove')} onClose={() => setRemoving(null)}>
          <div className="flex flex-col gap-4">
            <p className="break-words text-sm">{t('cleanup.removeBody')}</p>
            <ErrorNote error={remove.error} />
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setRemoving(null)}>
                {t('common.cancel')}
              </Button>
              <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate(removing)}>
                {t('cleanup.remove')}
              </Button>
            </div>
          </div>
        </Modal>
      )}
    </Card>
  )
}

// EditRule makes or changes a rule, showing what it would list as it's
// filled in. The preview changes nothing.
function EditRule({ rule, cap, onClose, onSaved }: { rule: CleanupRule | null; cap: number; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [form, setForm] = useState<Form>(rule ?? fresh)
  const set = (v: Partial<Form>) => setForm({ ...form, ...v })
  const ok = valid(form)
  const preview = useQuery({
    queryKey: ['cleanup-preview', form.kind, form.watched, form.days, form.endedOnly],
    queryFn: () => post<CleanupPreview>('/cleanup/preview', { ...form, auto: false, graceDays: 14 }),
    enabled: form.days >= leastDays(form) && form.days <= 3650,
    placeholderData: keepPreviousData,
  })
  const save = useMutation({ mutationFn: (f: Form) => (rule ? put(`/cleanup/rules/${rule.id}`, f) : post('/cleanup/rules', f)), onSuccess: onSaved })
  const p = preview.data
  const watched: CleanupWatched[] = ['everyone', 'anyone', 'nobody']
  return (
    <Modal title={t(rule ? 'cleanup.edit' : 'cleanup.add')} onClose={onClose} wide>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          if (ok) save.mutate(form)
        }}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t('cleanup.kind')}>
            {(id) => (
              <Select
                id={id}
                value={form.kind}
                onChange={(v) => set({ kind: v as Form['kind'] })}
                options={[
                  { value: 'movie', label: t('nav.movies') },
                  { value: 'series', label: t('nav.shows') },
                ]}
              />
            )}
          </Field>
          <Field label={t('cleanup.watched')}>
            {(id) => <Select id={id} value={form.watched} onChange={(v) => set({ watched: v as CleanupWatched, days: Math.max(form.days, v === 'nobody' ? 30 : 1) })} options={watched.map((w) => ({ value: w, label: t(`cleanup.watchedBy.${w}`) }))} />}
          </Field>
        </div>
        <Field label={t('cleanup.days')} hint={t(form.watched === 'nobody' ? 'cleanup.daysHintNobody' : 'cleanup.daysHint', { least: leastDays(form) })}>
          {(id) => <Input id={id} type="number" min={leastDays(form)} max={3650} className="max-w-32" value={form.days || ''} onChange={(e) => set({ days: Number(e.target.value) })} />}
        </Field>
        {form.kind === 'series' && (
          <Toggle
            checked={form.endedOnly}
            onChange={(v) => set({ endedOnly: v })}
            label={
              <span>
                {t('cleanup.endedOnly')}
                <span className="block text-xs text-muted">{t('cleanup.endedOnlyHint')}</span>
              </span>
            }
          />
        )}
        <Field label={t('cleanup.graceDays')} hint={t('cleanup.graceHint', { least: leastGrace(form) })}>
          {(id) => <Input id={id} type="number" min={leastGrace(form)} max={365} className="max-w-32" value={form.graceDays || ''} onChange={(e) => set({ graceDays: Number(e.target.value) })} />}
        </Field>
        <Toggle
          checked={form.auto}
          onChange={(v) => set({ auto: v, graceDays: v ? Math.max(form.graceDays, 7) : form.graceDays })}
          label={
            <span>
              {t('cleanup.auto')}
              <span className={`block text-xs ${form.auto ? 'text-bad' : 'text-muted'}`}>{t(form.auto ? 'cleanup.autoOn' : 'cleanup.autoOff', { most: cap })}</span>
            </span>
          }
        />
        {rule && <Toggle checked={form.enabled} onChange={(v) => set({ enabled: v })} label={t('cleanup.enabled')} />}

        <div className="rounded-lg border border-line bg-paper p-3 text-sm" aria-live="polite">
          <p className="mb-1 font-medium">{t('cleanup.preview')}</p>
          {preview.isPending && preview.fetchStatus !== 'idle' && <Spinner />}
          <ErrorNote error={preview.error} />
          {p?.waiting === 'report' && <p className="text-gold">{t('cleanup.previewNoReport')}</p>}
          {p?.waiting === 'plays' && <p className="text-gold">{t('cleanup.noPlays')}</p>}
          {p && !p.waiting && (
            <>
              <p className="text-muted">{p.count === 0 ? t('cleanup.previewNone') : t('cleanup.previewCount', { titles: p.count, size: bytes(p.size) })}</p>
              {p.count > 0 && (
                <ul className="mt-2 flex max-h-48 flex-col gap-1 overflow-y-auto text-xs">
                  {p.titles.map((m) => (
                    <li key={m.titleId} className="flex flex-wrap justify-between gap-x-3">
                      <span className="min-w-0 break-words">
                        {m.title}
                        {m.year ? ` (${m.year})` : ''}
                      </span>
                      <span className="text-muted">
                        {bytes(m.size)} · <Plays about={m} />
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </>
          )}
          <p className="mt-2 text-xs text-muted">{t('cleanup.previewSafe')}</p>
        </div>
        {rule && <p className="text-xs text-muted">{t('cleanup.editNote')}</p>}
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button type="submit" variant="primary" busy={save.isPending} disabled={!ok}>
            {t('common.save')}
          </Button>
        </div>
      </form>
    </Modal>
  )
}

// DeleteItem asks before deleting a listed title for good, saying what
// goes. The server only deletes what's shown here.
function DeleteItem({ item, onClose, onDone, onChanged }: { item: CleanupItem; onClose: () => void; onDone: (d: Cleanup) => void; onChanged: () => void }) {
  const { t } = useTranslation()
  const remove = useMutation({
    mutationFn: () => api<Cleanup>('DELETE', `/cleanup/items/${item.id}?expectFiles=${item.files}&expectSize=${item.size}`),
    onSuccess: onDone,
    // If the folder changed, the list has what's there now.
    onError: onChanged,
  })
  const name = item.about.title + (item.about.year ? ` (${item.about.year})` : '')
  return (
    <Modal title={t('cleanup.deleteTitle', { title: name })} onClose={onClose}>
      <div className="flex flex-col gap-4">
        <p className="break-words text-sm">{t('cleanup.deleteBody', { files: item.files, size: bytes(item.size) })}</p>
        <p className="text-sm text-muted">
          <Plays about={item.about} />
        </p>
        <ErrorNote error={remove.error} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          {!remove.error && (
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              {t('cleanup.deleteNow')}
            </Button>
          )}
        </div>
      </div>
    </Modal>
  )
}
