import { useEffect, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { AlertTriangle, ArrowRight, Check, CircleDashed, Film, LoaderCircle, Minus, Plus, Server, Tv, X } from 'lucide-react'
import { get, post, type MigrationApp, type MigrationRequest, type PathMap, type Plan, type Progress, type Scan, type Step, type SwitchResult } from '../api'
import { ItemRow, useBackground } from '../Tray'
import { appName, count, when } from '../format'
import { Badge, Button, Card, ErrorNote, Field, Heading, Input, Toggle } from '../ui'

// The steps of moving in, as the admin sees them.
const stages = ['Connect', 'Look it over', 'Move in', 'Matinee', 'Done'] as const
type Stage = (typeof stages)[number]

const apps: { app: MigrationApp; icon: ReactNode; blurb: string }[] = [
  { app: 'radarr', icon: <Film className="size-6" />, blurb: 'Movies, their files, quality profiles, indexers, download clients and every other setting.' },
  { app: 'sonarr', icon: <Tv className="size-6" />, blurb: 'Shows, seasons, episodes and their files, profiles, indexers, download clients and settings.' },
  { app: 'nzbget', icon: <Server className="size-6" />, blurb: 'News servers with their sign-ins, categories and the rest of its settings.' },
]

export function Import() {
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
      <Heading title="Move in" sub="Bring everything over from Radarr, Sonarr or NZBGet, step by step. The old app is only read from, so nothing in it changes." />
      <ol className="mb-8 flex flex-wrap gap-2" aria-label="Steps">
        {stages
          .filter((s) => s !== 'Matinee' || app !== 'nzbget')
          .map((s, i) => {
            const at = stages.indexOf(stage)
            const me = stages.indexOf(s)
            return (
              <li key={s} className={`flex items-center gap-2 rounded-full border px-3 py-1 text-sm ${me === at ? 'border-marquee text-marquee' : me < at ? 'border-ok/40 text-ok' : 'border-line text-muted'}`}>
                <span className="font-mono text-xs">{me < at ? <Check className="size-3.5" /> : i + 1}</span>
                {s === 'Matinee' ? 'Matinee (optional)' : s}
              </li>
            )
          })}
      </ol>
      {stage === 'Connect' && (
        <>
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
  const items = useBackground()
  if (!items.length) return null
  return (
    <section className="mt-8">
      <h2 className="mb-3 font-display text-lg font-semibold">In the background</h2>
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
          <h2 className="font-display text-lg font-semibold">Couldn't look over {name}</h2>
          <p className="mt-2 text-sm text-bad">{sc.error}</p>
          <p className="mt-2 text-sm text-muted">Nothing changed here or in {name}.</p>
        </>
      ) : scan.isError ? (
        <ErrorNote error={scan.error} />
      ) : (
        <>
          <div className="flex items-center gap-3">
            <LoaderCircle className="size-5 animate-spin text-marquee" aria-hidden />
            <h2 className="font-display text-lg font-semibold">Looking over {name}</h2>
          </div>
          <ul className="mt-3">{sc && <ItemRow item={{ key: sc.id, state: 'running', title: sc.step, done: sc.done, total: sc.total, to: '/import', action: '' }} />}</ul>
          <p className="mt-2 text-sm text-muted">
            A large library can take a few minutes. This carries on in the background: you can connect another app or leave this page, and the bell at the top says when it's ready. Nothing changes here or in {name} until
            you confirm on the next step.
          </p>
        </>
      )}
      <div className="mt-5">
        <Button variant="ghost" onClick={onBack}>
          {sc?.status === 'running' ? 'Connect another app' : 'Back'}
        </Button>
      </div>
    </Card>
  )
}

const blank = (app: MigrationApp): MigrationRequest => ({ app, url: '', apiKey: '', pathMap: [] })

function Connect({ req, setReq, onPick, onStarted }: { req: MigrationRequest; setReq: (r: MigrationRequest) => void; onPick: (app: MigrationApp) => void; onStarted: (s: Scan) => void }) {
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
    <>Looking for {appName[req.app]}…</>
  ) : found.data?.url && req.url === found.data.url ? (
    <span className="text-ok">Found {appName[req.app]} here.</span>
  ) : (
    <>
      Box Office couldn't find {appName[req.app]} by itself. Enter the address you open it at, such as <code>{placeholder}</code>. If you open it at an address like <code>https://example.com/{req.app}</code>, keep the{' '}
      <code>/{req.app}</code> at the end. In Kubernetes, use its service name: <code>kubectl get svc -A | grep {req.app}</code> lists it.
    </>
  )
  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_1.2fr]">
      <div className="flex flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">What are you moving from?</h2>
        {apps.map((a) => (
          <button
            key={a.app}
            onClick={() => onPick(a.app)}
            className={`flex gap-4 rounded-xl border p-4 text-left transition ${req.app === a.app ? 'border-marquee bg-marquee/5' : 'border-line bg-card hover:border-ink/40'}`}
          >
            <span className={req.app === a.app ? 'text-marquee' : 'text-muted'}>{a.icon}</span>
            <span>
              <span className="block font-display font-semibold">{appName[a.app]}</span>
              <span className="text-sm text-muted">{a.blurb}</span>
            </span>
          </button>
        ))}
      </div>
      <Card>
        <h2 className="mb-4 font-display text-lg font-semibold">Connect to {appName[req.app]}</h2>
        <div className="flex flex-col gap-4">
          <Field label="Address" hint={addressHint}>
            {(id) => <Input id={id} placeholder={placeholder} value={req.url} onChange={(e) => set({ url: e.target.value })} />}
          </Field>
          {nzb ? (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Username">{(id) => <Input id={id} value={req.username ?? ''} onChange={(e) => set({ username: e.target.value })} autoComplete="off" />}</Field>
              <Field label="Password">{(id) => <Input id={id} type="password" value={req.password ?? ''} onChange={(e) => set({ password: e.target.value })} autoComplete="off" />}</Field>
              <Steps
                className="sm:col-span-2"
                steps={[
                  <>
                    Open NZBGet and go to <b>Settings</b>, then <b>Security</b>.
                  </>,
                  <>
                    Copy <b>ControlUsername</b> and <b>ControlPassword</b> here. They're what you sign in to NZBGet with.
                  </>,
                ]}
              />
            </div>
          ) : (
            <Field label="API key">
              {(id) => <Input id={id} value={req.apiKey ?? ''} onChange={(e) => set({ apiKey: e.target.value })} autoComplete="off" spellCheck={false} placeholder="32 letters and numbers" />}
            </Field>
          )}
          {!nzb && (
            <Steps
              steps={[
                <>Open {appName[req.app]} in another tab.</>,
                <>
                  Go to <b>Settings</b>, then <b>General</b>.
                </>,
                <>
                  Under <b>Security</b>, copy the <b>API Key</b> and paste it above.
                </>,
              ]}
            />
          )}
          {!nzb && (
            <div className="flex flex-col gap-2">
              <div className="text-sm font-medium">Path maps (usually not needed)</div>
              <p className="text-xs text-muted">
                Box Office works out where your files are by itself, even if it sees them somewhere else than {appName[req.app]} does. Add a map only if the next step says it can't see them: {appName[req.app]}'s folder on the
                left, such as <code>/{folder}</code>, and where Box Office sees it on the right, such as <code>/media/{folder}</code>.
              </p>
              {req.pathMap.map((m, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Input aria-label={`${appName[req.app]}'s path`} placeholder={`${appName[req.app]}'s path`} value={m.from} onChange={(e) => setMap(i, { from: e.target.value })} />
                  <ArrowRight className="size-4 shrink-0 text-muted" aria-hidden />
                  <Input aria-label="Box Office's path" placeholder="Box Office's path" value={m.to} onChange={(e) => setMap(i, { to: e.target.value })} />
                  <Button variant="ghost" aria-label="Remove this map" onClick={() => set({ pathMap: req.pathMap.filter((_, j) => j !== i) })}>
                    <Minus className="size-4" />
                  </Button>
                </div>
              ))}
              <Button variant="ghost" className="self-start" onClick={() => set({ pathMap: [...req.pathMap, { from: '', to: '' }] })}>
                <Plus className="size-4" aria-hidden /> Add a path map
              </Button>
            </div>
          )}
          <ErrorNote error={error} />
          <Button variant="primary" busy={busy} onClick={preview} disabled={!req.url}>
            Connect and look it over
          </Button>
          <p className="text-xs text-muted">This only reads. Nothing changes here or in {appName[req.app]} until you confirm on the next step.</p>
        </div>
      </Card>
    </div>
  )
}

// Steps are numbered instructions for finding something to enter.
function Steps({ steps, className }: { steps: ReactNode[]; className?: string }) {
  return (
    <ol className={`flex list-decimal flex-col gap-1 rounded-lg border border-line bg-paper py-3 pl-8 pr-4 text-xs text-muted ${className ?? ''}`}>
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
              <Stat n={s.usenetServers} label="news servers" />
              <Stat n={s.categories} label="categories" />
              <Stat n={s.sections.length} label="settings sections" />
            </>
          ) : (
            <>
              <Stat n={plan.titles.new} label={`new ${plan.app === 'radarr' ? 'movies' : 'shows'}`} tone="text-marquee" />
              <Stat n={plan.titles.already} label="here already, left as they are" />
              <Stat n={plan.titles.skipped} label="can't come across" tone={plan.titles.skipped ? 'text-bad' : ''} />
              <Stat n={plan.files.total} label={plan.files.total < 0 ? 'files, counted while moving' : 'files'} />
              <Stat n={s.indexers} label="indexers" />
              <Stat n={s.downloadClients} label="download clients" />
              <Stat n={s.blocklist} label="blocked releases" />
              <Stat n={s.notifications + s.importLists} label="notifications and lists" />
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
            <h3 className="mb-3 font-display font-semibold">Quality profiles</h3>
            <ul className="flex flex-col gap-2 text-sm">
              {plan.profiles.map((p) => (
                <li key={p.legacyId}>
                  <span className="font-medium">{p.name}</span> {p.action === 'match' ? <Badge tone="ok">matches one here</Badge> : <Badge tone="marquee">new</Badge>}
                  <div className="text-xs text-muted">{p.qualities.join(', ')}</div>
                </li>
              ))}
            </ul>
            <h3 className="mb-3 mt-5 font-display font-semibold">Root folders</h3>
            {plan.pathMapFound && <p className="mb-2 text-xs text-muted">Box Office found where these are by looking for {name}'s titles in its own folders. If one is wrong, go back and add a path map.</p>}
            <ul className="flex flex-col gap-2 text-sm">
              {plan.rootFolders.map((f) => (
                <li key={f.legacyPath} className="flex flex-wrap items-center gap-2">
                  <code>{f.legacyPath}</code>
                  {f.path !== f.legacyPath && (
                    <>
                      <ArrowRight className="size-3.5 text-muted" aria-hidden />
                      <code>{f.path}</code>
                    </>
                  )}
                  {f.exists ? <Badge tone="ok">Box Office can see it</Badge> : <Badge tone="bad">Box Office can't see it</Badge>}
                </li>
              ))}
            </ul>
          </Card>
        )}
        {nzb && plan.servers && (
          <Card>
            <h3 className="mb-3 font-display font-semibold">News servers</h3>
            <ul className="flex flex-col gap-2 text-sm">
              {plan.servers.map((sv, i) => (
                <li key={i}>
                  <span className="font-medium">{sv.name}</span> <span className="text-muted">
                    {sv.host}:{sv.port}
                  </span>{' '}
                  {sv.tls && <Badge>TLS</Badge>} <Badge>{sv.connections} connections</Badge> <Badge>level {sv.level}</Badge> {sv.hasPassword && <Badge tone="ok">sign-in comes too</Badge>}
                </li>
              ))}
            </ul>
          </Card>
        )}
        {!nzb && plan.sample.length > 0 && (
          <Card>
            <h3 className="mb-3 font-display font-semibold">Some of what comes across</h3>
            <ul className="flex flex-col gap-1.5 text-sm">
              {plan.sample.map((t, i) => (
                <li key={i} className="flex justify-between gap-3">
                  <span className="truncate">
                    {t.title} {t.year ? <span className="text-muted">({t.year})</span> : null}
                  </span>
                  {t.hasFile && <Badge tone="ok">has file</Badge>}
                </li>
              ))}
            </ul>
          </Card>
        )}
        <Card>
          <h3 className="mb-3 font-display font-semibold">Settings kept as they are</h3>
          <div className="flex flex-wrap gap-1.5">
            {s.sections.map((sec) => (
              <Badge key={sec}>{sec}</Badge>
            ))}
          </div>
          <p className="mt-3 text-xs text-muted">Box Office uses indexers, download clients and news servers once its downloader is on. The rest is kept for reference.</p>
        </Card>
      </div>

      <Card>
        <h3 className="mb-4 font-display text-lg font-semibold">Before moving in</h3>
        <div className="flex flex-col gap-4">
          <Toggle checked={backupBoxOffice} onChange={setBackupBoxOffice} label="Back up Box Office first, so this can be undone with a restore" />
          <Toggle
            checked={backupLegacy}
            onChange={setBackupLegacy}
            label={
              <>
                {nzb ? "Save a copy of NZBGet's settings" : `Have ${name} make a backup of itself, and keep it here`}
                {!nzb && s.hiddenSecrets > 0 && <span className="block text-xs text-muted">Needed for the {s.hiddenSecrets} passwords and API keys {name} hides from other apps.</span>}
              </>
            }
          />
        </div>
        <ErrorNote error={error} />
        <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
          <Button variant="primary" busy={busy} onClick={start}>
            Confirm and move in
          </Button>
        </div>
        <p className="mt-3 text-right text-xs text-muted">Nothing in {name} changes. Files stay where they are.</p>
      </Card>
    </div>
  )
}

function StepIcon({ status }: { status: Step['status'] }) {
  switch (status) {
    case 'done':
      return <Check className="size-5 text-ok" aria-label="done" />
    case 'running':
      return <LoaderCircle className="size-5 animate-spin text-marquee" aria-label="running" />
    case 'failed':
      return <X className="size-5 text-bad" aria-label="failed" />
    case 'skipped':
      return <Minus className="size-5 text-muted" aria-label="skipped" />
    default:
      return <CircleDashed className="size-5 text-muted" aria-label="waiting" />
  }
}

function Moving({ id, onDone, onRestart }: { id: number; onDone: (app: MigrationApp) => void; onRestart: () => void }) {
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
        {p?.steps.map((st, i) => (
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
          <summary className="cursor-pointer font-medium">{p.warnings.length} things to look at</summary>
          <ul className="mt-2 list-disc pl-5 text-muted">
            {p.warnings.map((w, i) => (
              <li key={i}>{w}</li>
            ))}
          </ul>
        </details>
      )}
      {p?.status === 'done' && (
        <div className="mt-6 flex justify-end">
          <Button variant="primary" onClick={() => onDone(p.app)}>
            Next
          </Button>
        </div>
      )}
      {p?.status === 'failed' && (
        <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm">The old app is as it was. {p.backups.length > 0 && 'You can restore the backup made first, under Backups.'}</p>
          <Button onClick={onRestart}>Start again</Button>
        </div>
      )}
    </Card>
  )
}

function SwitchMatinee({ app, migrationId, onDone }: { app: MigrationApp; migrationId: number; onDone: (r: SwitchResult | null) => void }) {
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
          Point Matinee at Box Office <span className="font-normal text-muted">(optional)</span>
        </h2>
        <Button onClick={() => onDone(null)}>Skip this step</Button>
      </div>
      <p className="mt-2 text-sm">
        Only if you watch with Matinee. If you use Plex, Jellyfin, Emby or another player, skip this: the move-in is already done, and nothing else depends on it. You can also do it later.
      </p>
      <p className="mt-2 text-sm text-muted">
        Matinee's {appName[app]} connection moves to Box Office, keeping its quality profile and root folder. Matinee checks the new connection before saving it, so a wrong address changes nothing. Your
        Matinee sign-in is used once and not kept.
      </p>
      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <Field label="Matinee's address">{(id) => <Input id={id} placeholder="http://matinee:7171" value={matineeUrl} onChange={(e) => setMatineeUrl(e.target.value)} />}</Field>
        <Field label="Box Office's address, as Matinee reaches it" hint="Inside Kubernetes this is usually the service, such as http://boxoffice:7272.">
          {(id) => <Input id={id} value={boxOfficeUrl} onChange={(e) => setBoxOfficeUrl(e.target.value)} />}
        </Field>
        <Field label="Matinee admin username">{(id) => <Input id={id} autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />}</Field>
        <Field label="Password">{(id) => <Input id={id} type="password" autoComplete="off" value={password} onChange={(e) => setPassword(e.target.value)} />}</Field>
      </div>
      <ErrorNote error={error} />
      <div className="mt-6 flex flex-wrap justify-between gap-3">
        <Button variant="ghost" onClick={() => onDone(null)}>
          Skip, I'll do it later or I don't use Matinee
        </Button>
        <Button variant="primary" busy={busy} onClick={go} disabled={!matineeUrl || !username || !password}>
          Switch Matinee over
        </Button>
      </div>
    </Card>
  )
}

function Finished({ app, matinee, onAgain }: { app: MigrationApp; matinee: SwitchResult | null; onAgain: () => void }) {
  const name = appName[app]
  return (
    <Card>
      <div className="flex items-center gap-3">
        <span className="flex size-10 items-center justify-center rounded-full bg-ok/15 text-ok">
          <Check className="size-5" />
        </span>
        <h2 className="font-display text-2xl font-semibold">Moved in from {name}</h2>
      </div>
      <ul className="mt-5 flex flex-col gap-2 text-sm">
        {matinee ? (
          <li>
            Matinee's {name} connection now goes to <code>{matinee.after}</code> (it was <code>{matinee.before || 'not set'}</code>).
          </li>
        ) : (
          app !== 'nzbget' && (
            <li>
              If you watch with Matinee, it still uses {name}. When you're ready, point it at Box Office from here or in Matinee's admin settings, using the address and API key under{' '}
              <Link className="font-medium text-marquee" to="/settings">
                Settings
              </Link>
              .
            </li>
          )
        )}
        <li>
          You can shut down {name} now. Box Office keeps its backup under{' '}
          <Link className="font-medium text-marquee" to="/backups">
            Backups
          </Link>{' '}
          in case you need anything from it.
        </li>
        <li>From here on, Box Office backs itself up. Send backups to another folder or cloud storage under Backups.</li>
      </ul>
      <div className="mt-6 flex flex-wrap gap-2">
        <Button onClick={onAgain}>Move in from another app</Button>
        <Link to={app === 'sonarr' ? '/shows' : '/movies'}>
          <Button variant="primary">See what came over</Button>
        </Link>
      </div>
    </Card>
  )
}

function History() {
  const list = useQuery({ queryKey: ['migrations'], queryFn: () => get<Progress[]>('/migrations') })
  if (!list.data?.length) return null
  return (
    <section className="mt-12">
      <h2 className="mb-3 font-display text-lg font-semibold">Earlier moves</h2>
      <ul className="divide-y divide-line rounded-xl border border-line bg-card">
        {list.data.map((m) => (
          <li key={m.id} className="flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
            <span className="font-medium">{appName[m.app] ?? m.app}</span>
            <Badge tone={m.status === 'done' ? 'ok' : m.status === 'failed' ? 'bad' : 'marquee'}>{m.status}</Badge>
            <span className="text-muted">{when(m.started)}</span>
            {m.app !== 'nzbget' && m.status === 'done' && (
              <span className="text-muted">
                {m.imported.new} added, {m.imported.already} already here, {count(m.files, 'file', 'files')}
              </span>
            )}
            {m.matinee && <span className="text-muted">{m.matinee}</span>}
          </li>
        ))}
      </ul>
    </section>
  )
}
