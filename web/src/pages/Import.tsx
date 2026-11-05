import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { AlertTriangle, ArrowRight, Check, CircleDashed, Film, LoaderCircle, Minus, Plus, Server, Tv, X } from 'lucide-react'
import { del, get, post, put, type MigrationApp, type MigrationRequest, type PathMap, type Plan, type Progress, type Scan, type Status, type Step, type SwitchResult } from '../api'
import { ItemRow, useBackground } from '../Tray'
import { appName, when } from '../format'
import { Badge, Button, Card, ErrorNote, Field, Heading, Input, Toggle } from '../ui'

// The steps of moving in, as the admin sees them.
const stages = ['Connect', 'Look it over', 'Move in', 'Matinee', 'Done'] as const
type Stage = (typeof stages)[number]
const stageKey: Record<Stage, string> = { Connect: 'connect', 'Look it over': 'look', 'Move in': 'move', Matinee: 'matinee', Done: 'done' }

const apps: { app: MigrationApp; icon: ReactNode }[] = [
  { app: 'radarr', icon: <Film className="size-6" /> },
  { app: 'sonarr', icon: <Tv className="size-6" /> },
  { app: 'nzbget', icon: <Server className="size-6" /> },
]

export function Import() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const scanId = params.get('scan') ?? ''
  const migrationId = Number(params.get('migration') ?? 0)
  // Each app keeps its own form, so connecting another while one is being
  // looked over starts from that app's own details.
  const [forms, setForms] = useState<Record<MigrationApp, MigrationRequest>>(() => ({ radarr: blank('radarr'), sonarr: blank('sonarr'), nzbget: blank('nzbget') }))
  const [current, setCurrent] = useState<MigrationApp>('radarr')
  const req = forms[current]
  const setReq = (r: MigrationRequest) => setForms((f) => ({ ...f, [r.app]: r }))
  // After the move, the Matinee and Done steps, for the app moved from.
  const [after, setAfter] = useState<{ stage: 'Matinee' | 'Done'; app: MigrationApp; matinee: SwitchResult | null } | null>(null)
  const stage: Stage = after ? after.stage : migrationId ? 'Move in' : scanId ? 'Look it over' : 'Connect'
  const app = after?.app ?? req.app
  const go = (p: Record<string, string>) => {
    setAfter(null)
    setParams(p)
  }
  const restart = () => go({})
  return (
    <>
      <Heading title={t('move.title')} sub={t('move.sub')} />
      <ol className="mb-8 flex flex-wrap gap-2" aria-label={t('move.steps')}>
        {stages
          .filter((s) => s !== 'Matinee' || app !== 'nzbget')
          .map((s, i) => {
            const at = stages.indexOf(stage)
            const me = stages.indexOf(s)
            // Done is reached, not worked on: it shows as done.
            const done = me < at || (s === 'Done' && at === me)
            return (
              <li key={s} className={`flex items-center gap-2 rounded-full border px-3 py-1 text-sm ${done ? 'border-ok/40 text-ok' : me === at ? 'border-marquee text-marquee' : 'border-line text-muted'}`}>
                <span className="font-mono text-xs">{done ? <Check className="size-3.5" /> : i + 1}</span>
                {t(`move.stages.${stageKey[s]}`)}
              </li>
            )
          })}
      </ol>
      {stage === 'Connect' && (
        <>
          <Overview />
          <Connect
            req={req}
            setReq={setReq}
            onPick={setCurrent}
            onStarted={(sc) => {
              // The next app is the likely one to connect meanwhile.
              const order = apps.map((a) => a.app)
              setCurrent(order[(order.indexOf(sc.app) + 1) % order.length])
              go({ scan: sc.id })
            }}
          />
          <Background />
        </>
      )}
      {stage === 'Look it over' && <Looking id={scanId} onBack={restart} onStarted={(id) => go({ migration: String(id) })} />}
      {stage === 'Move in' && <Moving id={migrationId} onDone={(a) => setAfter({ stage: a === 'nzbget' ? 'Done' : 'Matinee', app: a, matinee: null })} onRestart={restart} />}
      {stage === 'Matinee' && after && <SwitchMatinee app={after.app} migrationId={migrationId} onDone={(r) => setAfter({ ...after, stage: 'Done', matinee: r })} />}
      {stage === 'Done' && after && <Finished app={after.app} matinee={after.matinee} onAgain={restart} />}
      <History />
    </>
  )
}

// Background lists what's being looked over or moved in already, so another
// app can be connected meanwhile.
function Background() {
  const { t } = useTranslation()
  const items = useBackground()
  if (!items.length) return null
  return (
    <section className="mt-8">
      <h2 className="mb-3 font-display text-lg font-semibold">{t('move.background')}</h2>
      <ul className="divide-y divide-line rounded-xl border border-line bg-card">
        {items.map((i) => (
          <ItemRow key={i.key} item={i} />
        ))}
      </ul>
    </section>
  )
}

// Looking follows a scan: its progress while Box Office reads the old app,
// then the plan to look over.
function Looking({ id, onBack, onStarted }: { id: string; onBack: () => void; onStarted: (id: number) => void }) {
  const { t } = useTranslation()
  const scan = useQuery({
    queryKey: ['scan', id],
    queryFn: () => get<Scan>(`/migrations/scans/${id}`),
    refetchInterval: (q) => (q.state.data?.status === 'running' ? 1000 : false),
    retry: false,
  })
  const sc = scan.data
  if (sc?.status === 'ready' && sc.plan) return <Review plan={sc.plan} onBack={onBack} onStarted={onStarted} />
  const name = sc ? appName[sc.app] : ''
  return (
    <Card>
      {sc?.status === 'failed' ? (
        <>
          <h2 className="font-display text-lg font-semibold">{t('move.look.failed', { app: name })}</h2>
          <p className="mt-2 text-sm text-bad">{sc.error}</p>
          <p className="mt-2 text-sm text-muted">{t('move.look.nothingChanged', { app: name })}</p>
        </>
      ) : scan.isError ? (
        <ErrorNote error={scan.error} />
      ) : (
        <>
          <div className="flex items-center gap-3">
            <LoaderCircle className="size-5 animate-spin text-marquee" aria-hidden />
            <h2 className="font-display text-lg font-semibold">{t('move.look.looking', { app: name })}</h2>
          </div>
          <ul className="mt-3">{sc && <ItemRow item={{ key: sc.id, state: 'running', title: sc.step, done: sc.done, total: sc.total, to: '/import', action: '' }} />}</ul>
          <p className="mt-2 text-sm text-muted">{t('move.look.wait', { app: name })}</p>
        </>
      )}
      <div className="mt-5">
        <Button variant="ghost" onClick={onBack}>
          {sc?.status === 'running' ? t('move.look.another') : t('common.back')}
        </Button>
      </div>
    </Card>
  )
}

const blank = (app: MigrationApp): MigrationRequest => ({ app, url: '', apiKey: '', pathMap: [] })

function Connect({ req, setReq, onPick, onStarted }: { req: MigrationRequest; setReq: (r: MigrationRequest) => void; onPick: (app: MigrationApp) => void; onStarted: (s: Scan) => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const nzb = req.app === 'nzbget'
  const set = (patch: Partial<MigrationRequest>) => setReq({ ...req, ...patch })
  const setMap = (i: number, m: Partial<PathMap>) => set({ pathMap: req.pathMap.map((x, j) => (i === j ? { ...x, ...m } : x)) })
  const preview = async () => {
    setBusy(true)
    setError(null)
    try {
      const sc = await post<Scan>('/migrations/scans', { ...req, pathMap: req.pathMap.filter((m) => m.from && m.to) })
      qc.invalidateQueries({ queryKey: ['scans'] })
      onStarted(sc)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  const placeholder = { radarr: 'http://radarr:7878', sonarr: 'http://sonarr:8989', nzbget: 'http://nzbget:6789' }[req.app]
  const folder = req.app === 'sonarr' ? 'tv' : 'movies'
  // Look for the app at its usual addresses, and fill in the one that
  // answers unless an address has been typed in.
  const found = useQuery({ queryKey: ['find', req.app], queryFn: () => get<{ url: string }>(`/migrations/find?app=${req.app}`), staleTime: 60_000 })
  const foundUrl = found.data?.url
  useEffect(() => {
    if (foundUrl && req.url === '') setReq({ ...req, url: foundUrl })
  }, [foundUrl, req, setReq])
  const addressHint = found.isLoading ? (
    <>{t('move.connect.lookingFor', { app: appName[req.app] })}</>
  ) : found.data?.url && req.url === found.data.url ? (
    <span className="text-ok">{t('move.connect.found', { app: appName[req.app] })}</span>
  ) : (
    <Trans i18nKey="move.connect.notFound" values={{ app: appName[req.app], example: placeholder, path: req.app }} components={{ code: <code /> }} />
  )
  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_1.2fr]">
      <div className="flex flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">{t('move.connect.from')}</h2>
        {apps.map((a) => (
          <button
            key={a.app}
            onClick={() => onPick(a.app)}
            className={`flex gap-4 rounded-xl border p-4 text-start transition ${req.app === a.app ? 'border-marquee bg-marquee/5' : 'border-line bg-card hover:border-ink/40'}`}
          >
            <span className={req.app === a.app ? 'text-marquee' : 'text-muted'}>{a.icon}</span>
            <span>
              <span className="block font-display font-semibold">{appName[a.app]}</span>
              <span className="text-sm text-muted">{t(`move.blurb.${a.app}`)}</span>
            </span>
          </button>
        ))}
      </div>
      <Card>
        <h2 className="mb-4 font-display text-lg font-semibold">{t('move.connect.to', { app: appName[req.app] })}</h2>
        <div className="flex flex-col gap-4">
          <Field label={t('conn.address')} hint={addressHint}>
            {(id) => <Input id={id} placeholder={placeholder} value={req.url} onChange={(e) => set({ url: e.target.value })} />}
          </Field>
          {nzb ? (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t('conn.username')}>{(id) => <Input id={id} value={req.username ?? ''} onChange={(e) => set({ username: e.target.value })} autoComplete="off" />}</Field>
              <Field label={t('conn.password')}>{(id) => <Input id={id} type="password" value={req.password ?? ''} onChange={(e) => set({ password: e.target.value })} autoComplete="off" />}</Field>
              <Steps
                className="sm:col-span-2"
                steps={[
                  <Trans i18nKey="move.connect.nzb1" components={{ b: <b /> }} />,
                  <Trans i18nKey="move.connect.nzb2" components={{ b: <b /> }} />,
                ]}
              />
            </div>
          ) : (
            <Field label={t('conn.indexer.apiKey')}>
              {(id) => <Input id={id} value={req.apiKey ?? ''} onChange={(e) => set({ apiKey: e.target.value })} autoComplete="off" spellCheck={false} placeholder={t('move.connect.keyPlaceholder')} />}
            </Field>
          )}
          {!nzb && (
            <Steps
              steps={[
                <>{t('move.connect.key1', { app: appName[req.app] })}</>,
                <Trans i18nKey="move.connect.key2" components={{ b: <b /> }} />,
                <Trans i18nKey="move.connect.key3" components={{ b: <b /> }} />,
              ]}
            />
          )}
          {!nzb && (
            <div className="flex flex-col gap-2">
              <div className="text-sm font-medium">{t('move.maps.title')}</div>
              <p className="text-xs text-muted">
                <Trans i18nKey="move.maps.hint" values={{ app: appName[req.app], folder }} components={{ code: <code /> }} />
              </p>
              {req.pathMap.map((m, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Input aria-label={t('move.maps.from', { app: appName[req.app] })} placeholder={t('move.maps.from', { app: appName[req.app] })} value={m.from} onChange={(e) => setMap(i, { from: e.target.value })} />
                  <ArrowRight className="size-4 shrink-0 text-muted" aria-hidden />
                  <Input aria-label={t('move.maps.to')} placeholder={t('move.maps.to')} value={m.to} onChange={(e) => setMap(i, { to: e.target.value })} />
                  <Button variant="ghost" aria-label={t('move.maps.remove')} onClick={() => set({ pathMap: req.pathMap.filter((_, j) => j !== i) })}>
                    <Minus className="size-4" />
                  </Button>
                </div>
              ))}
              <Button variant="ghost" className="self-start" onClick={() => set({ pathMap: [...req.pathMap, { from: '', to: '' }] })}>
                <Plus className="size-4" aria-hidden /> {t('move.maps.add')}
              </Button>
            </div>
          )}
          <ErrorNote error={error} />
          <Button variant="primary" busy={busy} onClick={preview} disabled={!req.url}>
            {t('move.connect.go')}
          </Button>
          <p className="text-xs text-muted">{t('move.connect.onlyReads', { app: appName[req.app] })}</p>
        </div>
      </Card>
    </div>
  )
}

// Steps are numbered instructions for finding something to enter.
function Steps({ steps, className }: { steps: ReactNode[]; className?: string }) {
  return (
    <ol className={`flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 ps-8 pe-4 text-xs text-muted ${className ?? ''}`}>
      {steps.map((s, i) => (
        <li key={i}>{s}</li>
      ))}
    </ol>
  )
}

function Stat({ n, label, tone }: { n: number; label: string; tone?: string }) {
  return (
    <div className="rounded-lg border border-line bg-paper px-4 py-3">
      <div className={`font-display text-2xl font-bold ${tone ?? ''}`}>{n < 0 ? '—' : n}</div>
      <div className="text-xs text-muted">{label}</div>
    </div>
  )
}

function Review({ plan, onBack, onStarted }: { plan: Plan; onBack: () => void; onStarted: (id: number) => void }) {
  const { t } = useTranslation()
  const [backupBoxOffice, setBackupBoxOffice] = useState(true)
  const [backupLegacy, setBackupLegacy] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const name = appName[plan.app]
  const nzb = plan.app === 'nzbget'
  const start = async () => {
    setBusy(true)
    setError(null)
    try {
      const pr = await post<Progress>('/migrations', { token: plan.token, backupBoxOffice, backupLegacy })
      onStarted(pr.id)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  const s = plan.settings
  return (
    <div className="flex flex-col gap-6">
      <Card>
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2 className="font-display text-xl font-semibold">
            {name} {plan.appVersion}
          </h2>
          <span className="text-sm text-muted">{plan.url}</span>
        </div>
        <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
          {nzb ? (
            <>
              <Stat n={s.usenetServers} label={t('move.stat.servers')} />
              <Stat n={s.categories} label={t('move.stat.categories')} />
              <Stat n={s.sections.length} label={t('move.stat.sections')} />
            </>
          ) : (
            <>
              <Stat n={plan.titles.new} label={plan.app === 'radarr' ? t('move.stat.newMovies') : t('move.stat.newShows')} tone="text-marquee" />
              <Stat n={plan.titles.already} label={t('move.stat.already')} />
              <Stat n={plan.titles.skipped} label={t('move.stat.skipped')} tone={plan.titles.skipped ? 'text-bad' : ''} />
              <Stat n={plan.files.total} label={plan.files.total < 0 ? t('move.stat.filesLater') : t('move.stat.files')} />
              <Stat n={s.indexers} label={t('move.stat.indexers')} />
              <Stat n={s.downloadClients} label={t('move.stat.clients')} />
              <Stat n={s.blocklist} label={t('move.stat.blocklist')} />
              <Stat n={s.notifications + s.importLists} label={t('move.stat.lists')} />
            </>
          )}
        </div>
      </Card>

      {plan.warnings.length > 0 && (
        <div className="flex flex-col gap-2 rounded-xl border border-gold/50 bg-gold/5 p-4">
          {plan.warnings.map((w, i) => (
            <p key={i} className="flex gap-2 text-sm">
              <AlertTriangle className="mt-0.5 size-4 shrink-0 text-gold" aria-hidden />
              {w}
            </p>
          ))}
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        {!nzb && (
          <Card>
            <h3 className="mb-3 font-display font-semibold">{t('move.review.profiles')}</h3>
            <ul className="flex flex-col gap-2 text-sm">
              {plan.profiles.map((p) => (
                <li key={p.legacyId}>
                  <span className="font-medium">{p.name}</span> {p.action === 'match' ? <Badge tone="ok">{t('move.review.matches')}</Badge> : <Badge tone="marquee">{t('move.review.new')}</Badge>}
                  <div className="text-xs text-muted">{p.qualities.join(', ')}</div>
                </li>
              ))}
            </ul>
            <h3 className="mb-3 mt-5 font-display font-semibold">{t('move.review.roots')}</h3>
            {plan.pathMapFound && <p className="mb-2 text-xs text-muted">{t('move.review.found', { app: name })}</p>}
            <ul className="flex flex-col gap-2 text-sm">
              {plan.rootFolders.map((f) => (
                <li key={f.legacyPath} className="flex flex-wrap items-center gap-2">
                  <code>{f.legacyPath}</code>
                  {f.path !== f.legacyPath && (
                    <>
                      <ArrowRight className="size-3.5 text-muted rtl:rotate-180" aria-hidden />
                      <code>{f.path}</code>
                    </>
                  )}
                  {f.exists ? <Badge tone="ok">{t('move.review.sees')}</Badge> : <Badge tone="bad">{t('move.review.cantSee')}</Badge>}
                </li>
              ))}
            </ul>
          </Card>
        )}
        {nzb && plan.servers && (
          <Card>
            <h3 className="mb-3 font-display font-semibold">{t('conn.servers.title')}</h3>
            <ul className="flex flex-col gap-2 text-sm">
              {plan.servers.map((sv, i) => (
                <li key={i}>
                  <span className="font-medium">{sv.name}</span> <span className="text-muted">
                    {sv.host}:{sv.port}
                  </span>{' '}
                  {sv.tls && <Badge>TLS</Badge>} <Badge>{t('conn.servers.connections', { count: sv.connections })}</Badge> <Badge>{t('move.review.level', { level: sv.level })}</Badge>{' '}
                  {sv.hasPassword && <Badge tone="ok">{t('move.review.signIn')}</Badge>}
                </li>
              ))}
            </ul>
          </Card>
        )}
        {!nzb && plan.sample.length > 0 && (
          <Card>
            <h3 className="mb-3 font-display font-semibold">{t('move.review.sample')}</h3>
            <ul className="flex flex-col gap-1.5 text-sm">
              {plan.sample.map((x, i) => (
                <li key={i} className="flex justify-between gap-3">
                  <span className="truncate">
                    {x.title} {x.year ? <span className="text-muted">({x.year})</span> : null}
                  </span>
                  {x.hasFile && <Badge tone="ok">{t('move.review.hasFile')}</Badge>}
                </li>
              ))}
            </ul>
          </Card>
        )}
        <Card>
          <h3 className="mb-3 font-display font-semibold">{t('move.review.kept')}</h3>
          <div className="flex flex-wrap gap-1.5">
            {s.sections.map((sec) => (
              <Badge key={sec}>{sec}</Badge>
            ))}
          </div>
          <p className="mt-3 text-xs text-muted">{t('move.review.keptHint')}</p>
        </Card>
      </div>

      <Card>
        <h3 className="mb-4 font-display text-lg font-semibold">{t('move.review.before')}</h3>
        <div className="flex flex-col gap-4">
          <Toggle checked={backupBoxOffice} onChange={setBackupBoxOffice} label={t('move.review.backupHere')} />
          <Toggle
            checked={backupLegacy}
            onChange={setBackupLegacy}
            label={
              <>
                {nzb ? t('move.review.backupNzb') : t('move.review.backupApp', { app: name })}
                {!nzb && s.hiddenSecrets > 0 && <span className="block text-xs text-muted">{t('move.review.hidden', { count: s.hiddenSecrets, app: name })}</span>}
              </>
            }
          />
        </div>
        <ErrorNote error={error} />
        <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
          <Button variant="ghost" onClick={onBack}>
            {t('common.back')}
          </Button>
          <Button variant="primary" busy={busy} onClick={start}>
            {t('move.review.confirm')}
          </Button>
        </div>
        <p className="mt-3 text-end text-xs text-muted">{t('move.review.nothing', { app: name })}</p>
      </Card>
    </div>
  )
}

function StepIcon({ status }: { status: Step['status'] }) {
  const { t } = useTranslation()
  switch (status) {
    case 'done':
      return <Check className="size-5 text-ok" aria-label={t('move.status.done')} />
    case 'running':
      return <LoaderCircle className="size-5 animate-spin text-marquee" aria-label={t('move.status.running')} />
    case 'failed':
      return <X className="size-5 text-bad" aria-label={t('move.status.failed')} />
    case 'skipped':
      return <Minus className="size-5 text-muted" aria-label={t('move.status.skipped')} />
    default:
      return <CircleDashed className="size-5 text-muted" aria-label={t('move.status.waiting')} />
  }
}

function Moving({ id, onDone, onRestart }: { id: number; onDone: (app: MigrationApp) => void; onRestart: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const pr = useQuery({
    queryKey: ['migration', id],
    queryFn: () => get<Progress>(`/migrations/${id}`),
    refetchInterval: (q) => (q.state.data?.status === 'running' ? 1000 : false),
  })
  useEffect(() => {
    if (pr.data && pr.data.status !== 'running') {
      qc.invalidateQueries({ queryKey: ['movies'] })
      qc.invalidateQueries({ queryKey: ['series'] })
      qc.invalidateQueries({ queryKey: ['migrations'] })
    }
  }, [pr.data, qc])
  const p = pr.data
  return (
    <Card>
      <ul className="flex flex-col gap-4">
        {p?.steps?.map((st, i) => (
          <li key={i} className="flex gap-3">
            <StepIcon status={st.status} />
            <div>
              <div className="font-medium">{st.name}</div>
              {st.message && <div className={`text-sm ${st.status === 'failed' ? 'text-bad' : 'text-muted'}`}>{st.message}</div>}
            </div>
          </li>
        ))}
      </ul>
      <ErrorNote error={pr.error} />
      {p && p.warnings.length > 0 && (
        <details className="mt-5 rounded-lg border border-gold/50 p-3 text-sm">
          <summary className="cursor-pointer font-medium">{t('move.moving.warnings', { count: p.warnings.length })}</summary>
          <ul className="mt-2 list-disc ps-5 text-muted">
            {p.warnings.map((w, i) => (
              <li key={i}>{w}</li>
            ))}
          </ul>
        </details>
      )}
      {p?.status === 'done' && (
        <div className="mt-6 flex justify-end">
          <Button variant="primary" onClick={() => onDone(p.app)}>
            {t('move.moving.next')}
          </Button>
        </div>
      )}
      {p?.status === 'failed' && (
        <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm">
            {t('move.moving.asItWas')} {p.backups.length > 0 && t('move.moving.restore')}
          </p>
          <Button onClick={onRestart}>{t('move.moving.again')}</Button>
        </div>
      )}
    </Card>
  )
}

function SwitchMatinee({ app, migrationId, onDone }: { app: MigrationApp; migrationId: number; onDone: (r: SwitchResult | null) => void }) {
  const { t } = useTranslation()
  const [matineeUrl, setMatineeUrl] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [boxOfficeUrl, setBoxOfficeUrl] = useState(window.location.origin)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const go = async () => {
    setBusy(true)
    setError(null)
    try {
      onDone(await post<SwitchResult>('/matinee/switch', { migrationId, matineeUrl, username, password, boxOfficeUrl, app }))
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Card>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h2 className="font-display text-lg font-semibold">
          <Trans i18nKey="move.matinee.title" components={{ muted: <span className="font-normal text-muted" /> }} />
        </h2>
        <Button onClick={() => onDone(null)}>{t('move.matinee.skip')}</Button>
      </div>
      <p className="mt-2 text-sm">{t('move.matinee.only')}</p>
      <p className="mt-2 text-sm text-muted">{t('move.matinee.how', { app: appName[app] })}</p>
      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <Field label={t('move.matinee.address')}>{(id) => <Input id={id} placeholder="http://matinee:7171" value={matineeUrl} onChange={(e) => setMatineeUrl(e.target.value)} />}</Field>
        <Field label={t('move.matinee.ours')} hint={t('move.matinee.oursHint')}>
          {(id) => <Input id={id} value={boxOfficeUrl} onChange={(e) => setBoxOfficeUrl(e.target.value)} />}
        </Field>
        <Field label={t('move.matinee.username')}>{(id) => <Input id={id} autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />}</Field>
        <Field label={t('conn.password')}>{(id) => <Input id={id} type="password" autoComplete="off" value={password} onChange={(e) => setPassword(e.target.value)} />}</Field>
      </div>
      <ErrorNote error={error} />
      <div className="mt-6 flex flex-wrap justify-between gap-3">
        <Button variant="ghost" onClick={() => onDone(null)}>
          {t('move.matinee.later')}
        </Button>
        <Button variant="primary" busy={busy} onClick={go} disabled={!matineeUrl || !username || !password}>
          {t('move.matinee.switch')}
        </Button>
      </div>
    </Card>
  )
}

function Finished({ app, matinee, onAgain }: { app: MigrationApp; matinee: SwitchResult | null; onAgain: () => void }) {
  const { t } = useTranslation()
  const name = appName[app]
  return (
    <Card>
      <div className="flex items-center gap-3">
        <span className="flex size-10 items-center justify-center rounded-full bg-ok/15 text-ok">
          <Check className="size-5" />
        </span>
        <h2 className="font-display text-2xl font-semibold">{t('move.done.title', { app: name })}</h2>
      </div>
      <ul className="mt-5 flex flex-col gap-2 text-sm">
        {matinee ? (
          <li>
            <Trans i18nKey="move.done.switched" values={{ app: name, after: matinee.after, before: matinee.before || t('move.done.notSet') }} components={{ code: <code /> }} />
          </li>
        ) : (
          app !== 'nzbget' && (
            <li>
              <Trans i18nKey="move.done.stillUses" values={{ app: name }} components={{ link: <Link className="font-medium text-marquee" to="/settings" /> }} />
            </li>
          )
        )}
        <li>
          <Trans i18nKey="move.done.shutDown" values={{ app: name }} components={{ link: <Link className="font-medium text-marquee" to="/backups" /> }} />
        </li>
        <li>{t('move.done.backsUp')}</li>
      </ul>
      <div className="mt-6 flex flex-wrap gap-2">
        <Button onClick={onAgain}>{t('move.done.another')}</Button>
        <Link to={app === 'sonarr' ? '/shows' : '/movies'}>
          <Button variant="primary">{t('move.done.see')}</Button>
        </Link>
      </div>
    </Card>
  )
}

// Overview is the move-in as a whole: which apps have come over, and a way
// to finish, after which Move in leaves the menu for Settings.
function Overview() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const status = useQuery({ queryKey: ['status'], queryFn: () => get<Status>('/status') })
  const list = useQuery({ queryKey: ['migrations'], queryFn: () => get<Progress[]>('/migrations') })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const movedIn = !!status.data?.movedIn
  const moves = list.data ?? []
  const moved = (a: MigrationApp) => moves.find((m) => m.app === a && m.status === 'done')
  const matinee = moves.find((m) => m.status === 'done' && m.matinee)
  const any = apps.some((a) => moved(a.app))
  const finish = async (done: boolean) => {
    setBusy(true)
    setError(null)
    try {
      await (done ? put('/migrations/done', {}) : del('/migrations/done'))
      await qc.invalidateQueries({ queryKey: ['status'] })
    } catch (e) {
      setError(e)
    } finally {
      setBusy(false)
    }
  }
  if (!list.data || !status.data) return null
  // Before anything has come over, the form below says it all.
  if (!any && !movedIn) {
    return (
      <p className="mb-6 text-sm text-muted">
        <Trans i18nKey="move.overview.fresh" components={{ button: <button className="font-medium text-marquee" disabled={busy} onClick={() => finish(true)} /> }} />
      </p>
    )
  }
  const row = (key: string, ok: boolean, title: string, detail: string) => (
    <li key={key} className="flex items-start gap-3 py-2">
      {ok ? <Check className="mt-0.5 size-4 shrink-0 text-ok" aria-label={t('move.status.done')} /> : <CircleDashed className="mt-0.5 size-4 shrink-0 text-muted" aria-label={t('move.status.notYet')} />}
      <div>
        <div className="text-sm font-medium">{title}</div>
        <div className="text-xs text-muted">{detail}</div>
      </div>
    </li>
  )
  return (
    <Card className="mb-6">
      <h2 className="font-display text-lg font-semibold">{movedIn ? t('move.overview.done') : t('move.overview.soFar')}</h2>
      <ul className="mt-2 divide-y divide-line">
        {apps.map(({ app }) => {
          const m = moved(app)
          return row(app, !!m, appName[app], m ? t('move.overview.movedIn', { when: when(m.finished ?? m.started) }) : t('move.overview.notMoved'))
        })}
        {row('matinee', !!matinee, t('move.stages.matinee'), matinee ? matinee.matinee! : t('move.overview.notSwitched'))}
      </ul>
      {error ? <ErrorNote error={error} /> : null}
      {movedIn ? (
        <div className="mt-4 flex flex-wrap items-center gap-3 text-sm text-muted">
          <span>{t('move.overview.lives')}</span>
          <Button variant="ghost" busy={busy} onClick={() => finish(false)}>
            {t('move.overview.showAgain')}
          </Button>
        </div>
      ) : (
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button variant="primary" busy={busy} onClick={() => finish(true)}>
            {t('move.overview.finish')}
          </Button>
          <span className="text-sm text-muted">{t('move.overview.finishHint')}</span>
        </div>
      )}
    </Card>
  )
}

function History() {
  const { t } = useTranslation()
  const list = useQuery({ queryKey: ['migrations'], queryFn: () => get<Progress[]>('/migrations') })
  if (!list.data?.length) return null
  return (
    <section className="mt-12">
      <h2 className="mb-3 font-display text-lg font-semibold">{t('move.history.title')}</h2>
      <ul className="divide-y divide-line rounded-xl border border-line bg-card">
        {list.data.map((m) => (
          <li key={m.id} className="flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
            <span className="font-medium">{appName[m.app] ?? m.app}</span>
            <Badge tone={m.status === 'done' ? 'ok' : m.status === 'failed' ? 'bad' : 'marquee'}>{t(`move.history.status.${m.status}`, { defaultValue: m.status })}</Badge>
            <span className="text-muted">{when(m.started)}</span>
            {m.app !== 'nzbget' && m.status === 'done' && (
              <span className="text-muted">
                {t('move.history.summary', { added: m.imported.new, already: m.imported.already, files: t('move.history.files', { count: m.files }) })}
              </span>
            )}
            {m.matinee && <span className="text-muted">{m.matinee}</span>}
          </li>
        ))}
      </ul>
    </section>
  )
}
