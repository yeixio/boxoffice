// The speed schedule card in Settings, and the Downloads page's quick
// change of speed. Speeds are typed in MB/s and saved in KB/s (1024 to a
// MB, as the page shows speeds); times are on the server's clock.
import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Gauge, Plus, Trash2 } from 'lucide-react'
import { del, put, type Downloads as DownloadsData, type SpeedMode, type SpeedNow, type SpeedRange, type SpeedSchedule } from '../api'
import { formatLocale } from '../i18n'
import { Button, Card, ErrorNote, Field, Input, Modal, Select, Toggle } from '../ui'
import { speed, useDownloads } from './Downloads'

const DAY = 24 * 60

export const toMB = (kb: number) => Math.round((kb / 1024) * 10) / 10
export const toKB = (mb: number) => Math.max(0, Math.round(mb * 1024))

const minutes = (hhmm: string) => {
  const [h, m] = hhmm.split(':').map(Number)
  return h * 60 + m
}

// weekday is a day's short name in the page's language: 0 is Sunday.
// 2026-11-15 was a Sunday.
export function weekday(d: number, style: 'short' | 'long' = 'short') {
  return new Intl.DateTimeFormat(formatLocale(), { weekday: style, timeZone: 'UTC' }).format(new Date(Date.UTC(2026, 10, 15 + d)))
}

// The week starts on Monday in most places; days are still saved 0 to 6.
const weekOrder = [1, 2, 3, 4, 5, 6, 0]

// hhmm shows a saved "HH:MM" the way the page's language writes times.
export function hhmm(v: string) {
  const [h, m] = v.split(':').map(Number)
  return new Intl.DateTimeFormat(formatLocale(), { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' }).format(new Date(Date.UTC(2026, 0, 1, h, m)))
}

// at shows a moment on the server's clock, with the day when it isn't today.
export function at(iso: string, zone: string) {
  const d = new Date(iso)
  const opts: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit' }
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: zone })
    opts.timeZone = zone
  } catch {
    // Not a zone name the browser knows; the browser's own clock it is.
  }
  const day = (x: Date) => new Intl.DateTimeFormat('en', { ...opts, hour: undefined, minute: undefined, dateStyle: 'short' }).format(x)
  if (day(d) !== day(new Date())) opts.weekday = 'short'
  return new Intl.DateTimeFormat(formatLocale(), opts).format(d)
}

// The parts of the week each time covers, per day: [day, from, to) in minutes.
export function segments(r: SpeedRange): [number, number, number][] {
  const from = minutes(r.from)
  let len = (minutes(r.to) - from + DAY) % DAY
  if (len === 0) len = DAY
  return r.days.flatMap((d) => {
    const end = from + len
    return end <= DAY ? [[d, from, end] as [number, number, number]] : [[d, from, DAY] as [number, number, number], [(d + 1) % 7, 0, end - DAY] as [number, number, number]]
  })
}

const modeTone: Record<SpeedMode, string> = { full: 'bg-ok', limit: 'bg-gold', pause: 'bg-bad' }

// SpeedLine says the speed now in a few words, for the Downloads page.
export function useSpeedLine() {
  const { t } = useTranslation()
  return (s: SpeedNow) => {
    const state =
      s.mode === 'pause'
        ? s.until
          ? t('speed.now.pauseUntil', { time: at(s.until, s.zone) })
          : t('speed.now.pause')
        : s.mode === 'limit'
          ? s.until
            ? t('speed.now.limitUntil', { speed: speed(s.limit * 1024), time: at(s.until, s.zone) })
            : t('speed.now.limit', { speed: speed(s.limit * 1024) })
          : s.until
            ? t('speed.now.fullUntil', { time: at(s.until, s.zone) })
            : t('speed.now.full')
    if (s.source === 'schedule') return t('speed.now.bySchedule', { state })
    if (s.source === 'override') return t('speed.now.byHand', { state })
    return state
  }
}

// ---- Settings: the schedule ----

const example: SpeedRange[] = [
  { days: [1, 2, 3, 4, 5], from: '16:00', to: '23:00', mode: 'limit', limit: 5 * 1024 },
  { days: [0, 6], from: '09:00', to: '23:00', mode: 'limit', limit: 5 * 1024 },
  { days: [0, 1, 2, 3, 4, 5, 6], from: '23:00', to: '07:00', mode: 'full', limit: 0 },
]

export function SpeedScheduleCard() {
  const qc = useQueryClient()
  const { t } = useTranslation()
  const d = useDownloads()
  const [draft, setDraft] = useState<SpeedSchedule | null>(null)
  const save = useMutation({
    mutationFn: (s: SpeedSchedule) => put<DownloadsData>('/downloads/schedule', s),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['downloads'] })
      setDraft(null)
    },
  })
  if (!d.data) return null
  const saved = d.data.schedule
  const s = draft ?? saved
  const now = d.data.speedNow
  const edit = (next: Partial<SpeedSchedule>) => setDraft({ ...s, ...next })
  const editRange = (i: number, r: Partial<SpeedRange>) => edit({ ranges: s.ranges.map((x, j) => (j === i ? { ...x, ...r } : x)) })
  const changed = draft !== null && JSON.stringify(draft) !== JSON.stringify(saved)
  const base = d.data.settings.speedLimit
  return (
    <Card>
      <h2 id="speed-schedule" className="mb-1 scroll-mt-24 font-display text-lg font-semibold">
        {t('speed.title')}
      </h2>
      <p className="mb-4 text-sm text-muted">{t('speed.sub')}</p>
      <div className="flex flex-col gap-4">
        <Toggle
          checked={s.enabled}
          onChange={(v) => (s.ranges.length === 0 && !draft ? save.mutate({ ...s, enabled: v }) : edit({ enabled: v }))}
          disabled={save.isPending}
          label={
            <>
              {t('speed.enable')}
              <span className="block text-xs text-muted">{t('speed.clock', { zone: now.zone, time: hhmm(now.clock) })}</span>
            </>
          }
        />
        {s.enabled && (
          <>
            {s.ranges.length > 0 && <Week ranges={s.ranges} base={base} />}
            {s.ranges.map((r, i) => (
              <RangeRow key={i} n={i + 1} r={r} onChange={(x) => editRange(i, x)} onRemove={() => edit({ ranges: s.ranges.filter((_, j) => j !== i) })} />
            ))}
            {s.ranges.length === 0 && (
              <div className="rounded-lg border border-dashed border-line p-4 text-sm">
                <p className="mb-3 text-muted">{t('speed.exampleHint')}</p>
                <Button onClick={() => edit({ ranges: example })}>{t('speed.example')}</Button>
              </div>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="ghost"
                disabled={s.ranges.length >= 24}
                onClick={() => edit({ ranges: [...s.ranges, { days: [1, 2, 3, 4, 5], from: '08:00', to: '17:00', mode: 'limit', limit: 5 * 1024 }] })}
              >
                <Plus className="size-4" aria-hidden /> {t('speed.add')}
              </Button>
            </div>
            <p className="text-xs text-muted">{base > 0 ? t('speed.outsideLimit', { speed: speed(base * 1024) }) : t('speed.outsideFull')}</p>
          </>
        )}
        {changed && (
          <div className="flex flex-wrap gap-2">
            <Button variant="primary" busy={save.isPending} onClick={() => save.mutate(s)}>
              {t('common.save')}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setDraft(null)
                save.reset()
              }}
            >
              {t('common.cancel')}
            </Button>
          </div>
        )}
        <ErrorNote error={save.error} />
      </div>
    </Card>
  )
}

function RangeRow({ n, r, onChange, onRemove }: { n: number; r: SpeedRange; onChange: (r: Partial<SpeedRange>) => void; onRemove: () => void }) {
  const { t } = useTranslation()
  const [mb, setMb] = useState<string | null>(null)
  const from = minutes(r.from)
  const to = minutes(r.to)
  return (
    <fieldset className="rounded-lg border border-line p-3">
      <legend className="flex items-center gap-2 px-1 text-sm font-medium">
        <span className={`size-2.5 rounded-full ${modeTone[r.mode]}`} aria-hidden />
        {t('speed.rangeN', { n })}
      </legend>
      <div className="mb-3 flex flex-wrap gap-1.5" role="group" aria-label={t('speed.days')}>
        {weekOrder.map((day) => {
          const on = r.days.includes(day)
          return (
            <button
              key={day}
              type="button"
              aria-pressed={on}
              title={weekday(day, 'long')}
              onClick={() => onChange({ days: on ? r.days.filter((x) => x !== day) : [...r.days, day].sort() })}
              className={`min-w-11 rounded-full border px-2.5 py-1 text-xs font-medium transition ${on ? 'border-marquee bg-marquee text-marquee-ink' : 'border-line text-muted hover:border-ink/40'}`}
            >
              {weekday(day)}
            </button>
          )
        })}
      </div>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-[8rem_8rem_1fr]">
        <Field label={t('speed.from')}>{(id) => <Input id={id} type="time" value={r.from} onChange={(e) => e.target.value && onChange({ from: e.target.value })} />}</Field>
        <Field label={t('speed.to')}>{(id) => <Input id={id} type="time" value={r.to} onChange={(e) => e.target.value && onChange({ to: e.target.value })} />}</Field>
        <Field label={t('speed.mode')}>
          {(id) => (
            <div className="col-span-2 flex flex-wrap items-center gap-2 sm:col-span-1">
              <div className="min-w-36 flex-1">
                <Select
                  id={id}
                  value={r.mode}
                  onChange={(v) => onChange({ mode: v as SpeedMode, limit: v === 'limit' && r.limit < 1 ? 5 * 1024 : r.limit })}
                  options={[
                    { value: 'limit', label: t('speed.limit') },
                    { value: 'full', label: t('speed.full') },
                    { value: 'pause', label: t('speed.pause') },
                  ]}
                />
              </div>
              {r.mode === 'limit' && (
                <span className="flex items-center gap-2">
                  <Input
                    type="number"
                    min={0.1}
                    step={0.1}
                    aria-label={t('speed.mbs')}
                    value={mb ?? String(toMB(r.limit))}
                    onChange={(e) => {
                      setMb(e.target.value)
                      const v = Number(e.target.value)
                      if (Number.isFinite(v) && v > 0) onChange({ limit: toKB(v) })
                    }}
                    onBlur={() => setMb(null)}
                    className="w-24"
                  />
                  <span className="text-sm text-muted">{t('speed.mbs')}</span>
                </span>
              )}
            </div>
          )}
        </Field>
      </div>
      <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs text-muted">
          {r.days.length === 0 ? t('speed.noDays') : from === to ? t('speed.allDay') : to < from ? t('speed.overnight', { time: hhmm(r.to) }) : ''}
        </p>
        <Button variant="ghost" onClick={onRemove} className="text-bad">
          <Trash2 className="size-4" aria-hidden /> {t('speed.remove')}
        </Button>
      </div>
    </fieldset>
  )
}

// Week draws the schedule: a row a day, Monday first, midnight to midnight.
function Week({ ranges, base }: { ranges: SpeedRange[]; base: number }) {
  const { t } = useTranslation()
  const byDay = new Map<number, { from: number; to: number; mode: SpeedMode; n: number }[]>()
  ranges.forEach((r, i) => {
    for (const [d, from, to] of segments(r)) byDay.set(d, [...(byDay.get(d) ?? []), { from, to, mode: r.mode, n: i + 1 }])
  })
  const hours = [0, 6, 12, 18, 24]
  return (
    <div aria-label={t('speed.week')} role="img" className="rounded-lg border border-line p-3">
      <div className="grid grid-cols-[2.75rem_1fr] items-center gap-x-2 gap-y-1.5">
        {weekOrder.map((day) => (
          <div key={day} className="contents">
            <span className="text-xs text-muted">{weekday(day)}</span>
            <div className="relative h-4 overflow-hidden rounded bg-line/60">
              {(byDay.get(day) ?? []).map((s, i) => (
                <div
                  key={i}
                  className={`absolute inset-y-0 ${modeTone[s.mode]} opacity-80`}
                  style={{ insetInlineStart: `${(s.from / DAY) * 100}%`, width: `${((s.to - s.from) / DAY) * 100}%` }}
                  title={t('speed.rangeN', { n: s.n })}
                />
              ))}
            </div>
          </div>
        ))}
        <span />
        <div className="relative h-4 text-[10px] text-muted">
          {hours.map((h) => (
            <span key={h} className="absolute top-0 -translate-x-1/2 rtl:translate-x-1/2" style={{ insetInlineStart: `${(h / 24) * 100}%` }}>
              {h === 24 ? '' : hhmm(`${String(h).padStart(2, '0')}:00`)}
            </span>
          ))}
        </div>
      </div>
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
        <Legend tone="bg-gold" label={t('speed.limited')} />
        <Legend tone="bg-ok" label={t('speed.full')} />
        <Legend tone="bg-bad" label={t('speed.pause')} />
        <Legend tone="bg-line" label={base > 0 ? t('speed.otherLimit', { speed: speed(base * 1024) }) : t('speed.otherFull')} />
      </div>
    </div>
  )
}

function Legend({ tone, label }: { tone: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className={`size-2.5 rounded-sm ${tone}`} aria-hidden />
      {label}
    </span>
  )
}

// ---- the Downloads page: change the speed for now ----

export function SpeedButton({ data }: { data: DownloadsData }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button onClick={() => setOpen(true)} variant={data.speedNow.source === 'override' ? 'primary' : 'plain'}>
        <Gauge className="size-4" aria-hidden /> {t('speed.button')}
      </Button>
      {open && <SpeedOverride data={data} onClose={() => setOpen(false)} />}
    </>
  )
}

function SpeedOverride({ data, onClose }: { data: DownloadsData; onClose: () => void }) {
  const qc = useQueryClient()
  const { t } = useTranslation()
  const line = useSpeedLine()
  const now = data.speedNow
  const sched = data.schedule
  const [mode, setMode] = useState<SpeedMode>(now.mode === 'full' ? 'limit' : 'full')
  const [mb, setMb] = useState(String(toMB(now.limit || data.settings.speedLimit || 5 * 1024)))
  // "Until the schedule changes" needs the schedule's next change, which
  // without the override in the way is now.next when it's the schedule's.
  const nextChange = now.source !== 'override' && sched.enabled && now.until ? now.until : undefined
  const [howLong, setHowLong] = useState(nextChange ? 'next' : '1')
  const done = () => {
    qc.invalidateQueries({ queryKey: ['downloads'] })
    onClose()
  }
  const apply = useMutation({
    mutationFn: () =>
      put<DownloadsData>('/downloads/speed', {
        mode,
        limit: mode === 'limit' ? toKB(Number(mb)) : 0,
        hours: howLong === 'next' || howLong === 'ever' ? 0 : Number(howLong),
        untilNext: howLong === 'next',
      }),
    onSuccess: done,
  })
  const undo = useMutation({ mutationFn: () => del('/downloads/speed'), onSuccess: done })
  const mbOK = mode !== 'limit' || Number(mb) > 0
  const options = [
    ...(sched.enabled && (nextChange || now.source === 'override') ? [{ value: 'next', label: nextChange ? t('speed.override.untilNext', { time: at(nextChange, now.zone) }) : t('speed.override.untilNextPlain') }] : []),
    { value: '1', label: t('speed.override.h1') },
    { value: '3', label: t('speed.override.h3') },
    { value: '8', label: t('speed.override.h8') },
    { value: 'ever', label: t('speed.override.ever') },
  ]
  return (
    <Modal title={t('speed.override.title')} onClose={onClose}>
      <p className="mb-1 text-sm">
        <span className="text-muted">{t('speed.override.now')}</span> {line(now)}
      </p>
      <p className="mb-4 text-xs text-muted">{t('speed.override.sub')}</p>
      <div className="flex flex-col gap-4">
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1.5 text-sm font-medium">{t('speed.mode')}</legend>
          {(['full', 'limit', 'pause'] as SpeedMode[]).map((m) => (
            <label key={m} className="flex min-h-9 flex-wrap items-center gap-2 text-sm">
              <input type="radio" name="speed-mode" checked={mode === m} onChange={() => setMode(m)} className="accent-marquee" />
              {m === 'full' ? t('speed.full') : m === 'pause' ? t('speed.pause') : t('speed.limit')}
              {m === 'limit' && (
                <span className="flex items-center gap-2">
                  <Input type="number" min={0.1} step={0.1} aria-label={t('speed.mbs')} value={mb} onFocus={() => setMode('limit')} onChange={(e) => setMb(e.target.value)} className="w-24" />
                  <span className="text-muted">{t('speed.mbs')}</span>
                </span>
              )}
            </label>
          ))}
        </fieldset>
        <Field label={t('speed.override.for')}>{(id) => <Select id={id} value={options.some((o) => o.value === howLong) ? howLong : options[0].value} onChange={setHowLong} options={options} />}</Field>
        <ErrorNote error={apply.error ?? undo.error} />
        <div className="flex flex-wrap justify-end gap-2">
          {now.source === 'override' && (
            <Button variant="ghost" busy={undo.isPending} onClick={() => undo.mutate()}>
              {sched.enabled ? t('speed.override.backSchedule') : t('speed.override.back')}
            </Button>
          )}
          <Button variant="primary" busy={apply.isPending} disabled={!mbOK} onClick={() => apply.mutate()}>
            {t('speed.override.apply')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
