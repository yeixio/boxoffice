import i18n, { formatLocale } from '../i18n'
import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, BellRing, Bug, CircleCheck, HardDrive, XCircle } from 'lucide-react'
import { get, type Monitor, type MonitorDisk, type MonitorPoint, type Problem } from '../api'
import { bytes, decimal } from '../format'
import { Badge, Card, ErrorNote, Spinner } from '../ui'

// The server section of Health: how busy Box Office and its machine are
// over the last hour, its disks, and the problems it has logged. It's read
// every few seconds while it's open.

const REFRESH_MS = 5000
const HOUR_MS = 3600_000

export const useMonitor = (refresh = true) => useQuery({ queryKey: ['monitor'], queryFn: () => get<Monitor>('/admin/monitor'), refetchInterval: refresh ? REFRESH_MS : false })

const percent = (v: number) => new Intl.NumberFormat(formatLocale(), { style: 'percent', maximumFractionDigits: 0 }).format(v / 100)
const perSecond = (v: number) => (v >= 10 ? decimal(Math.round(v), 0) : decimal(v)) + '/s'
const rate = (v: number) => bytes(v) + '/s'
const timeOfDay = (ms: number) => new Date(ms).toLocaleTimeString(formatLocale(), { hour: 'numeric', minute: '2-digit', second: '2-digit' })

// roughly is how long, roughly, as a number and a unit: 3 days, 4 hours.
function roughly(ms: number): [number, 'minute' | 'hour' | 'day'] {
  const m = Math.max(0, Math.round(ms / 60_000))
  if (m < 60) return [m, 'minute']
  const h = Math.round(m / 60)
  if (h < 48) return [h, 'hour']
  return [Math.round(h / 24), 'day']
}

// span says how long, roughly: "3 days", "4 hours", "12 minutes".
export function span(ms: number): string {
  const [n, unit] = roughly(ms)
  if (unit === 'minute' && n < 1) return i18n.t('monitor.underMinute')
  return new Intl.NumberFormat(formatLocale(), { style: 'unit', unit, unitDisplay: 'long' }).format(n)
}

// ago says how long ago: "3 days ago", "just now".
export function ago(ms: number, now = Date.now()): string {
  if (now - ms < 60_000) return i18n.t('monitor.justNow')
  const [n, unit] = roughly(now - ms)
  return new Intl.RelativeTimeFormat(formatLocale(), { numeric: 'always' }).format(-n, unit)
}

export function ServerMonitor({ onShowLog }: { onShowLog: (q: string, level?: 'warn' | 'error') => void }) {
  const { t } = useTranslation()
  const m = useMonitor()
  if (m.isPending) return <Spinner label={t('monitor.reading')} />
  if (!m.data) return <ErrorNote error={m.error} />
  const h = m.data
  return (
    <div className="flex flex-col gap-6">
      <ErrorNote error={m.error} />
      <StatusLine h={h} />
      <Tiles h={h} />
      {h.alerts.length > 0 && <Firing h={h} />}
      <div className="grid gap-6 lg:grid-cols-2">
        <Problems h={h} onShow={(p) => onShowLog(p.detail || p.message, p.level)} />
        <Disks disks={h.disks} />
      </div>
      <Details h={h} />
    </div>
  )
}

function StatusLine({ h }: { h: Monitor }) {
  const { t } = useTranslation()
  const s = h.system
  const bad = h.databaseError || h.alerts.some((a) => a.level === 'critical')
  const warn = !bad && (h.problems.some((p) => p.level === 'error' && Date.now() - p.last < 15 * 60_000) || h.alerts.length > 0)
  const Icon = bad ? XCircle : warn ? AlertTriangle : CircleCheck
  const tone = bad ? 'text-bad' : warn ? 'text-gold' : 'text-ok'
  return (
    <Card className="flex flex-wrap items-center gap-x-6 gap-y-2">
      <div className="flex items-center gap-2">
        <Icon className={`size-5 ${tone}`} aria-hidden />
        <span className="font-medium">{bad ? t('monitor.wrong') : warn ? t('monitor.recentErrors') : t('monitor.smooth')}</span>
      </div>
      <span className="text-sm text-muted">
        {t('monitor.upFor', { span: span(Date.now() - s.started), version: h.version })}
      </span>
      <span className={`text-sm ${h.databaseError ? 'text-bad' : 'text-muted'}`}>
        {h.databaseError
          ? t('monitor.database', { error: h.databaseError })
          : h.databaseMs < 1
            ? t('monitor.dbUnder', { db: h.database === 'postgres' ? 'PostgreSQL' : 'SQLite' })
            : t('monitor.dbAnswers', { db: h.database === 'postgres' ? 'PostgreSQL' : 'SQLite', ms: Math.round(h.databaseMs) })}
      </span>
      {h.debug && (
        <Badge tone="gold">
          <Bug className="size-3" aria-hidden /> {t('monitor.debugOn')}
        </Badge>
      )}
    </Card>
  )
}

function Tiles({ h }: { h: Monitor }) {
  const { t } = useTranslation()
  const s = h.system
  const history = s.history
  const last = history.length ? history[history.length - 1] : undefined
  const memCap = s.memLimit || s.memTotal || 0
  const scope = s.scope === 'group' ? t('monitor.scopeGroup') : t('monitor.scopeProcess')
  const active = last?.downloads ?? 0
  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <Tile
        label={t('monitor.processor')}
        value={percent(s.cpu)}
        detail={[s.hostCpu !== undefined && t('monitor.machine', { percent: percent(s.hostCpu) }), s.load?.length && t('monitor.load', { load: s.load.map((l) => decimal(l, 2)).join(' ') })].filter(Boolean).join(' · ')}
        hint={s.cpuLimit ? t('monitor.limitedCores', { scope, cores: decimal(s.cpuLimit) }) : scope}
        points={history}
        pick={(p) => p.cpu}
        max={100}
        format={percent}
      />
      <Tile
        label={t('monitor.memory')}
        value={bytes(s.memory)}
        detail={memCap ? (s.memLimit ? t('monitor.ofLimit', { size: bytes(memCap) }) : t('monitor.of', { size: bytes(memCap) })) : ''}
        hint={scope}
        points={history}
        pick={(p) => p.mem}
        max={Math.max(...history.map((p) => p.mem), 1) * 1.25}
        format={bytes}
      />
      <Tile
        label={t('monitor.requests')}
        value={perSecond(last?.req ?? 0)}
        detail={`${t('monitor.answered', { count: s.requests })}${s.serverErrors ? t('monitor.failed', { count: s.serverErrors }) : ''} ${t('monitor.sent', { size: bytes(s.bytesOut) })}`}
        hint={t('monitor.requestsHint')}
        points={history}
        pick={(p) => p.req}
        max={Math.max(...history.map((p) => p.req), 1) * 1.2}
        format={perSecond}
      />
      <Tile
        label={t('monitor.downloading')}
        value={rate(last?.downloadRate ?? 0)}
        detail={`${t('monitor.running', { count: active })}${h.downloads.queued ? t('monitor.waiting', { count: h.downloads.queued }) : ''}${h.downloadFailures ? ` ${t('monitor.failedSince', { count: h.downloadFailures })}` : ''}`}
        points={history}
        pick={(p) => p.downloadRate}
        max={Math.max(...history.map((p) => p.downloadRate), 1_000_000) * 1.15}
        format={rate}
      />
    </div>
  )
}

function Tile({ label, value, detail, hint, points, pick, max, format }: { label: string; value: string; detail?: string; hint?: string; points: MonitorPoint[]; pick: (p: MonitorPoint) => number; max: number; format: (v: number) => string }) {
  return (
    <Card className="flex min-w-0 flex-col p-4!">
      <div className="text-xs font-medium uppercase tracking-wide text-muted" title={hint}>
        {label}
      </div>
      <div className="mt-1 font-display text-2xl font-semibold tabular-nums">{value}</div>
      <div className="min-h-5 truncate text-xs text-muted" title={detail}>
        {detail}
      </div>
      <Sparkline label={label} points={points} pick={pick} max={max} format={format} />
    </Card>
  )
}

// Sparkline is the last hour of one reading, with the value under the pointer.
export function Sparkline({ label, points, pick, max, format }: { label: string; points: MonitorPoint[]; pick: (p: MonitorPoint) => number; max: number; format: (v: number) => string }) {
  const { t } = useTranslation()
  const [hover, setHover] = useState<number | null>(null)
  const W = 240
  const H = 48
  if (points.length < 2) return <p className="mt-3 flex h-12 items-end text-xs text-muted">{t('monitor.chartFills')}</p>
  const end = points[points.length - 1].t
  const start = Math.min(points[0].t, end - 60_000)
  const range = end - start
  const x = (ms: number) => ((ms - start) / range) * W
  const y = (v: number) => H - 2 - (Math.min(v, max) / max) * (H - 4)
  const line = points.map((p, i) => `${i ? 'L' : 'M'}${x(p.t).toFixed(1)},${y(pick(p)).toFixed(1)}`).join('')
  const area = `${line}L${x(end).toFixed(1)},${H}L${x(points[0].t).toFixed(1)},${H}Z`
  const hp = hover !== null ? points[hover] : undefined
  return (
    <div className="relative mt-3">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="block h-12 w-full overflow-visible"
        role="img"
        aria-label={t('monitor.overHour', { label })}
        onPointerMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect()
          const ms = start + ((e.clientX - r.left) / r.width) * range
          let best = 0
          for (let i = 1; i < points.length; i++) if (Math.abs(points[i].t - ms) < Math.abs(points[best].t - ms)) best = i
          setHover(best)
        }}
        onPointerLeave={() => setHover(null)}
      >
        <line x1={0} x2={W} y1={H - 0.5} y2={H - 0.5} className="stroke-line" strokeWidth={1} vectorEffect="non-scaling-stroke" />
        <path d={area} className="fill-marquee/15" />
        <path d={line} className="fill-none stroke-marquee" strokeWidth={2} strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
        {hp && <line x1={x(hp.t)} x2={x(hp.t)} y1={0} y2={H} className="stroke-muted" strokeWidth={1} vectorEffect="non-scaling-stroke" />}
      </svg>
      {hp && (
        <div
          className="pointer-events-none absolute bottom-full z-10 mb-1 -translate-x-1/2 whitespace-nowrap rounded-md border border-line bg-card px-2 py-1 text-xs shadow-lg"
          style={{ left: `${Math.min(85, Math.max(15, (x(hp.t) / W) * 100))}%` }}
        >
          <span className="font-medium tabular-nums">{format(pick(hp))}</span> <span className="text-muted">{timeOfDay(hp.t)}</span>
        </div>
      )}
      <div className="mt-1 flex justify-between text-[10px] text-muted">
        <span>{range > HOUR_MS - 60_000 ? t('monitor.hourAgo') : ago(start, end)}</span>
        <span>{t('monitor.now')}</span>
      </div>
    </div>
  )
}

function Firing({ h }: { h: Monitor }) {
  const { t } = useTranslation()
  return (
    <Card>
      <h2 className="flex items-center gap-2 font-display text-lg font-semibold">
        <BellRing className="size-5 text-marquee" aria-hidden /> {t('monitor.alertsNow')}
      </h2>
      <ul className="mt-3 flex flex-col gap-2 text-sm">
        {h.alerts.map((a) => (
          <li key={a.key}>
            <span className={`font-medium ${a.level === 'critical' ? 'text-bad' : ''}`}>{a.title}.</span> <span className="text-muted">{a.message}</span>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function Problems({ h, onShow }: { h: Monitor; onShow: (p: Problem) => void }) {
  const { t } = useTranslation()
  return (
    <Card>
      <h2 className="font-display text-lg font-semibold">{t('monitor.problems')}</h2>
      <p className="mt-1 text-sm text-muted">
        {t('monitor.problemsSub')}
      </p>
      {h.problems.length === 0 ? (
        <p className="mt-4 flex items-center gap-2 text-sm text-ok">
          <CircleCheck className="size-4" aria-hidden /> {t('monitor.noneSince', { ago: ago(h.system.started) })}
        </p>
      ) : (
        <ul className="mt-3 flex flex-col divide-y divide-line">
          {h.problems.slice(0, 12).map((p) => (
            <li key={p.level + p.message + (p.detail ?? '')}>
              <button onClick={() => onShow(p)} className="flex w-full items-start gap-3 py-2.5 text-start hover:bg-line/30">
                {p.level === 'error' ? <XCircle className="mt-0.5 size-4 shrink-0 text-bad" aria-label={t('monitor.error')} /> : <AlertTriangle className="mt-0.5 size-4 shrink-0 text-gold" aria-label={t('monitor.warning')} />}
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">{p.message}</span>
                  {p.detail && <span className="block truncate text-xs text-muted">{p.detail}</span>}
                </span>
                <span className="shrink-0 text-end text-xs text-muted">
                  {p.count > 1 && <span className="block font-medium text-ink">×{decimal(p.count, 0)}</span>}
                  {ago(p.last)}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}

const useName: Record<string, string> = { data: 'monitor.use.data', downloads: 'monitor.use.downloads', backups: 'monitor.use.backups', movies: 'monitor.use.movies', tv: 'monitor.use.tv' }

function Disks({ disks }: { disks: MonitorDisk[] }) {
  const { t } = useTranslation()
  return (
    <Card>
      <h2 className="font-display text-lg font-semibold">{t('monitor.disks')}</h2>
      <p className="mt-1 text-sm text-muted">{t('monitor.disksSub')}</p>
      <ul className="mt-3 flex flex-col gap-4">
        {disks.map((d, i) => {
          const used = d.total ? (d.total - d.free) / d.total : 0
          const tone = used >= 0.95 ? 'bg-bad' : used >= 0.85 ? 'bg-gold' : 'bg-ok'
          const names = [...new Set(d.uses.map((u) => (useName[u.use] ? t(useName[u.use]) : u.use)))].join(', ')
          return (
            <li key={i} className="text-sm">
              <div className="flex items-baseline justify-between gap-3">
                <span className="flex min-w-0 items-center gap-2 font-medium">
                  <HardDrive className="size-4 shrink-0 text-muted" aria-hidden />
                  <span className="truncate">{names}</span>
                </span>
                <span className={`shrink-0 text-xs ${d.reachable ? 'text-muted' : 'text-bad'}`}>
                  {d.reachable ? t('monitor.free', { free: bytes(d.free), total: bytes(d.total) }) : t('monitor.unreadable')}
                </span>
              </div>
              {d.reachable && (
                <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-line" role="meter" aria-valuenow={Math.round(used * 100)} aria-valuemin={0} aria-valuemax={100} aria-label={t('monitor.full', { names, percent: Math.round(used * 100) })}>
                  <div className={`h-full ${tone}`} style={{ width: `${Math.max(1, used * 100)}%` }} />
                </div>
              )}
              <div className="mt-1 truncate text-xs text-muted" title={d.uses.map((u) => u.path).join('\n')}>
                {[...new Set(d.uses.map((u) => u.path))].join(' · ')}
              </div>
              {!d.reachable && <p className="mt-1 text-xs text-bad">{t('monitor.shareHint')}</p>}
            </li>
          )
        })}
      </ul>
    </Card>
  )
}

function Details({ h }: { h: Monitor }) {
  const { t } = useTranslation()
  const s = h.system
  const rows: [string, string][] = [
    [t('monitor.started'), new Date(s.started).toLocaleString(formatLocale())],
    [t('monitor.machineRow'), `${s.hostname ? s.hostname + ', ' : ''}${s.os}/${s.arch}, ${t('monitor.cores', { count: s.cores })}${s.cpuLimit ? ` ${t('monitor.limitedTo', { cores: decimal(s.cpuLimit) })}` : ''}`],
    ['Go', t('monitor.goRow', { version: s.goVersion, count: s.goroutines, heap: bytes(s.goHeap) })],
    [t('monitor.tasks'), t('monitor.tasksRow', { running: h.jobs.running, queued: h.jobs.queued, failed: h.jobs.failed })],
    [t('monitor.logged'), t('monitor.loggedRow', { warnings: s.warnings, errors: s.errors })],
    [t('monitor.logKept'), h.logLines ? t('monitor.logKeptRow', { count: h.logLines, since: new Date(h.logsSince ?? 0).toLocaleString(formatLocale()) }) : t('monitor.nothingYet')],
  ]
  return (
    <Card>
      <h2 className="font-display text-lg font-semibold">{t('monitor.details')}</h2>
      <dl className="mt-3 grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted">{k}</dt>
            <dd className="min-w-0 break-words">{v}</dd>
          </div>
        ))}
      </dl>
    </Card>
  )
}
