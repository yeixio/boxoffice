import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, BellRing, Bug, CircleCheck, HardDrive, XCircle } from 'lucide-react'
import { get, type Monitor, type MonitorDisk, type MonitorPoint, type Problem } from '../api'
import { bytes } from '../format'
import { Badge, Card, ErrorNote, Spinner } from '../ui'

// The server section of Health: how busy Box Office and its machine are
// over the last hour, its disks, and the problems it has logged. It's read
// every few seconds while it's open.

const REFRESH_MS = 5000
const HOUR_MS = 3600_000

export const useMonitor = (refresh = true) => useQuery({ queryKey: ['monitor'], queryFn: () => get<Monitor>('/admin/monitor'), refetchInterval: refresh ? REFRESH_MS : false })

const percent = (v: number) => `${Math.round(v)}%`
const perSecond = (v: number) => (v >= 10 ? Math.round(v).toString() : v.toFixed(1)) + '/s'
const rate = (v: number) => bytes(v) + '/s'
const timeOfDay = (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', second: '2-digit' })

// span says how long, roughly: "3 days", "4 hours", "12 minutes".
export function span(ms: number): string {
  const m = Math.max(0, Math.round(ms / 60_000))
  if (m < 1) return 'under a minute'
  if (m < 60) return `${m} minute${m === 1 ? '' : 's'}`
  const h = Math.round(m / 60)
  if (h < 48) return `${h} hour${h === 1 ? '' : 's'}`
  const d = Math.round(h / 24)
  return `${d} days`
}

export const ago = (ms: number, now = Date.now()) => (now - ms < 60_000 ? 'just now' : `${span(now - ms)} ago`)

export function ServerMonitor({ onShowLog }: { onShowLog: (q: string, level?: 'warn' | 'error') => void }) {
  const m = useMonitor()
  if (m.isPending) return <Spinner label="Reading the server" />
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
  const s = h.system
  const bad = h.databaseError || h.alerts.some((a) => a.level === 'critical')
  const warn = !bad && (h.problems.some((p) => p.level === 'error' && Date.now() - p.last < 15 * 60_000) || h.alerts.length > 0)
  const Icon = bad ? XCircle : warn ? AlertTriangle : CircleCheck
  const tone = bad ? 'text-bad' : warn ? 'text-gold' : 'text-ok'
  return (
    <Card className="flex flex-wrap items-center gap-x-6 gap-y-2">
      <div className="flex items-center gap-2">
        <Icon className={`size-5 ${tone}`} aria-hidden />
        <span className="font-medium">{bad ? 'Something is wrong' : warn ? 'Running, with recent errors' : 'Running smoothly'}</span>
      </div>
      <span className="text-sm text-muted">
        Up for {span(Date.now() - s.started)} · Box Office {h.version}
      </span>
      <span className={`text-sm ${h.databaseError ? 'text-bad' : 'text-muted'}`}>
        {h.databaseError ? `Database: ${h.databaseError}` : `${h.database === 'postgres' ? 'PostgreSQL' : 'SQLite'} answers in ${h.databaseMs < 1 ? 'under 1' : Math.round(h.databaseMs)} ms`}
      </span>
      {h.debug && (
        <Badge tone="gold">
          <Bug className="size-3" aria-hidden /> Debug logging on
        </Badge>
      )}
    </Card>
  )
}

function Tiles({ h }: { h: Monitor }) {
  const s = h.system
  const history = s.history
  const last = history.length ? history[history.length - 1] : undefined
  const memCap = s.memLimit || s.memTotal || 0
  const scope = s.scope === 'group' ? "Box Office's container, with any programs it runs" : 'The Box Office process alone'
  const active = last?.downloads ?? 0
  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <Tile
        label="Processor"
        value={percent(s.cpu)}
        detail={[s.hostCpu !== undefined && `machine ${percent(s.hostCpu)}`, s.load?.length && `load ${s.load.map((l) => l.toFixed(2)).join(' ')}`].filter(Boolean).join(' · ')}
        hint={s.cpuLimit ? `${scope}, limited to ${s.cpuLimit.toFixed(1)} cores` : scope}
        points={history}
        pick={(p) => p.cpu}
        max={100}
        format={percent}
      />
      <Tile
        label="Memory"
        value={bytes(s.memory)}
        detail={memCap ? `of ${bytes(memCap)}${s.memLimit ? ' limit' : ''}` : ''}
        hint={scope}
        points={history}
        pick={(p) => p.mem}
        max={Math.max(...history.map((p) => p.mem), 1) * 1.25}
        format={bytes}
      />
      <Tile
        label="Requests"
        value={perSecond(last?.req ?? 0)}
        detail={`${s.requests.toLocaleString()} answered${s.serverErrors ? `, ${s.serverErrors.toLocaleString()} failed` : ''} · ${bytes(s.bytesOut)} sent`}
        hint="From the web UI, Matinee and anything else using Box Office's API"
        points={history}
        pick={(p) => p.req}
        max={Math.max(...history.map((p) => p.req), 1) * 1.2}
        format={perSecond}
      />
      <Tile
        label="Downloading"
        value={rate(last?.downloadRate ?? 0)}
        detail={`${active} running${h.downloads.queued ? `, ${h.downloads.queued} waiting` : ''}${h.downloadFailures ? ` · ${h.downloadFailures} failed since start` : ''}`}
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
  const [hover, setHover] = useState<number | null>(null)
  const W = 240
  const H = 48
  if (points.length < 2) return <p className="mt-3 flex h-12 items-end text-xs text-muted">The chart fills in over the next few seconds.</p>
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
        aria-label={`${label} over the last hour`}
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
        <span>{range > HOUR_MS - 60_000 ? 'an hour ago' : ago(start, end)}</span>
        <span>now</span>
      </div>
    </div>
  )
}

function Firing({ h }: { h: Monitor }) {
  return (
    <Card>
      <h2 className="flex items-center gap-2 font-display text-lg font-semibold">
        <BellRing className="size-5 text-marquee" aria-hidden /> Alerts now
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
  return (
    <Card>
      <h2 className="font-display text-lg font-semibold">Problems logged</h2>
      <p className="mt-1 text-sm text-muted">
        Warnings and errors since Box Office started, alike ones together. Click one to see it in the log.
      </p>
      {h.problems.length === 0 ? (
        <p className="mt-4 flex items-center gap-2 text-sm text-ok">
          <CircleCheck className="size-4" aria-hidden /> None since it started {ago(h.system.started)}.
        </p>
      ) : (
        <ul className="mt-3 flex flex-col divide-y divide-line">
          {h.problems.slice(0, 12).map((p) => (
            <li key={p.level + p.message + (p.detail ?? '')}>
              <button onClick={() => onShow(p)} className="flex w-full items-start gap-3 py-2.5 text-left hover:bg-line/30">
                {p.level === 'error' ? <XCircle className="mt-0.5 size-4 shrink-0 text-bad" aria-label="Error" /> : <AlertTriangle className="mt-0.5 size-4 shrink-0 text-gold" aria-label="Warning" />}
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">{p.message}</span>
                  {p.detail && <span className="block truncate text-xs text-muted">{p.detail}</span>}
                </span>
                <span className="shrink-0 text-right text-xs text-muted">
                  {p.count > 1 && <span className="block font-medium text-ink">×{p.count.toLocaleString()}</span>}
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

const useName: Record<string, string> = { data: 'Data', downloads: 'Downloads', backups: 'Backups', movies: 'Movies', tv: 'TV' }

function Disks({ disks }: { disks: MonitorDisk[] }) {
  return (
    <Card>
      <h2 className="font-display text-lg font-semibold">Disks</h2>
      <p className="mt-1 text-sm text-muted">The space on each disk Box Office uses. Folders on the same disk are shown together.</p>
      <ul className="mt-3 flex flex-col gap-4">
        {disks.map((d, i) => {
          const used = d.total ? (d.total - d.free) / d.total : 0
          const tone = used >= 0.95 ? 'bg-bad' : used >= 0.85 ? 'bg-gold' : 'bg-ok'
          const names = [...new Set(d.uses.map((u) => useName[u.use] ?? u.use))].join(', ')
          return (
            <li key={i} className="text-sm">
              <div className="flex items-baseline justify-between gap-3">
                <span className="flex min-w-0 items-center gap-2 font-medium">
                  <HardDrive className="size-4 shrink-0 text-muted" aria-hidden />
                  <span className="truncate">{names}</span>
                </span>
                <span className={`shrink-0 text-xs ${d.reachable ? 'text-muted' : 'text-bad'}`}>
                  {d.reachable ? `${bytes(d.free)} free of ${bytes(d.total)}` : "Can't be read"}
                </span>
              </div>
              {d.reachable && (
                <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-line" role="meter" aria-valuenow={Math.round(used * 100)} aria-valuemin={0} aria-valuemax={100} aria-label={`${names}: ${Math.round(used * 100)}% full`}>
                  <div className={`h-full ${tone}`} style={{ width: `${Math.max(1, used * 100)}%` }} />
                </div>
              )}
              <div className="mt-1 truncate text-xs text-muted" title={d.uses.map((u) => u.path).join('\n')}>
                {[...new Set(d.uses.map((u) => u.path))].join(' · ')}
              </div>
              {!d.reachable && <p className="mt-1 text-xs text-bad">If it's on a network share, check the share is still connected and mounted into Box Office.</p>}
            </li>
          )
        })}
      </ul>
    </Card>
  )
}

function Details({ h }: { h: Monitor }) {
  const s = h.system
  const rows: [string, string][] = [
    ['Started', new Date(s.started).toLocaleString()],
    ['Machine', `${s.hostname ? s.hostname + ', ' : ''}${s.os}/${s.arch}, ${s.cores} cores${s.cpuLimit ? ` (limited to ${s.cpuLimit.toFixed(1)})` : ''}`],
    ['Go', `${s.goVersion}, ${s.goroutines} goroutines, ${bytes(s.goHeap)} heap`],
    ['Tasks', `${h.jobs.running} running, ${h.jobs.queued} waiting, ${h.jobs.failed} failed in the last day`],
    ['Logged', `${s.warnings.toLocaleString()} warnings and ${s.errors.toLocaleString()} errors since it started`],
    ['Log kept', h.logLines ? `the latest ${h.logLines.toLocaleString()} lines, since ${new Date(h.logsSince ?? 0).toLocaleString()}` : 'nothing yet'],
  ]
  return (
    <Card>
      <h2 className="font-display text-lg font-semibold">Details</h2>
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
