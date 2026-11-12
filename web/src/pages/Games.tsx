import i18n from '../i18n'
import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useState, type ReactNode } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ChevronRight, FolderOpen, Gamepad2, Lock, RefreshCw, Upload } from 'lucide-react'
import { get, patch, post, put, type Game, type GameCheck, type GameDetail, type GamePage as GamePageData, type GameSettings, type GameStatus, type GameSystem } from '../api'
import { bytes, day, when } from '../format'
import { useRootFolders } from '../hooks'
import { Badge, Button, Card, Empty, ErrorNote, Field, Heading, Input, Select, Spinner, Toggle } from '../ui'
import { Pager, usePageSize } from './Pager'

// useGameCheck is the last check of the games folders, asked for again
// every couple of seconds while one runs. When one finishes, the lists
// are read again.
export function useGameCheck() {
  const qc = useQueryClient()
  const check = useQuery({
    queryKey: ['game-check'],
    queryFn: () => get<GameCheck>('/games/check'),
    refetchInterval: (q) => (q.state.data?.running ? 2000 : false),
  })
  const running = !!check.data?.running
  const [was, setWas] = useState(running)
  useEffect(() => {
    if (was && !running) {
      qc.invalidateQueries({ queryKey: ['game-systems'] })
      qc.invalidateQueries({ queryKey: ['games'] })
      qc.invalidateQueries({ queryKey: ['game'] })
    }
    setWas(running)
  }, [running, was, qc])
  return check
}

function useStartCheck(body: { rootId?: number; system?: string } = {}) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => post<GameCheck>('/games/check', body),
    onSuccess: (p) => qc.setQueryData(['game-check'], p),
  })
}

// GameCheckButton is a games root folder's Check button, in Settings: it
// looks through the folder for each system's games and checks them.
export function GameCheckButton({ rootId }: { rootId: number }) {
  const { t } = useTranslation()
  const check = useGameCheck()
  const start = useStartCheck({ rootId })
  if (check.data?.running || start.isSuccess) {
    return (
      <Link to="/games" className="text-xs font-medium text-marquee">
        {check.data?.running ? t('games.checking') : t('games.seeGames')}
      </Link>
    )
  }
  return (
    <span className="flex items-center gap-2">
      <Button variant="ghost" busy={start.isPending} onClick={() => start.mutate()}>
        <RefreshCw className="size-4" aria-hidden /> {t('games.check')}
      </Button>
      {start.error instanceof Error && <span className="text-xs text-bad">{start.error.message}</span>}
    </span>
  )
}

const statusTone: Record<GameStatus, 'ok' | 'bad' | 'gold' | 'plain'> = {
  complete: 'ok',
  missing: 'bad',
  wrong: 'bad',
  bios: 'gold',
  unknown: 'plain',
  unreadable: 'bad',
}

// StatusBadge says how complete a game is. A console game that's wrong is
// a bad copy; a MAME set, one for another version of MAME.
export function StatusBadge({ status, list, arcade = true }: { status: GameStatus; list?: string; arcade?: boolean }) {
  const { t } = useTranslation()
  let label = t(`games.status.${status}`)
  if (status === 'wrong') label = !arcade ? t('games.status.badCopy') : list ? t('games.status.wrongFor', { list }) : label
  return <Badge tone={statusTone[status]}>{label}</Badge>
}

// Progress says what a running check is doing, and what the last one
// couldn't tell.
function CheckNote({ check }: { check?: GameCheck }) {
  const { t } = useTranslation()
  if (!check) return null
  if (check.running) {
    const step = check.step ?? 'finding'
    return (
      <div className="mb-6 flex items-center gap-3 rounded-lg border border-line bg-card px-4 py-3 text-sm" role="status">
        <RefreshCw className="size-4 shrink-0 animate-spin text-marquee" aria-hidden />
        <span className="min-w-0">
          {t(`games.step.${step}`, { system: check.system ?? '' })}
          {step !== 'list' && check.total > 0 && <span className="text-muted">{` · ${t('games.stepCount', { done: check.done, total: check.total })}`}</span>}
        </span>
      </div>
    )
  }
  return (
    <>
      {check.error && (
        <p role="alert" className="mb-4 rounded-md border border-bad/40 bg-bad/5 px-3 py-2 text-sm text-bad">
          {check.error}
        </p>
      )}
      {check.unsure.length > 0 && (
        <Card className="mb-6">
          <h2 className="mb-1 font-display text-lg font-semibold">{t('games.unsure', { count: check.unsure.length })}</h2>
          <p className="mb-3 text-sm text-muted">{t('games.unsureHint')}</p>
          <ul className="flex flex-col gap-1 text-sm">
            {check.unsure.map((p) => (
              <li key={p} className="flex items-center gap-2">
                <FolderOpen className="size-4 shrink-0 text-muted" aria-hidden />
                <code className="break-all">{p}</code>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </>
  )
}

// Games lists the systems found in the games root folders.
export function Games() {
  const { t } = useTranslation()
  const systems = useQuery({ queryKey: ['game-systems'], queryFn: () => get<GameSystem[]>('/gamesystems') })
  const roots = useRootFolders('game')
  const check = useGameCheck()
  const start = useStartCheck()
  const total = systems.data?.reduce((n, s) => n + s.groups, 0) ?? 0
  const running = !!check.data?.running
  return (
    <>
      <Heading
        title={t('nav.games')}
        sub={systems.data?.length ? t('games.sub', { games: t('games.games', { count: total }), systems: t('games.systems', { count: systems.data.length }) }) : undefined}
        actions={
          !!roots.data?.length && (
            <>
              <Link to="/import-files" className="inline-flex items-center gap-2 rounded-md border border-line bg-card px-3.5 py-2 text-sm font-medium hover:border-ink/40">
                <Upload className="size-4" aria-hidden /> {t('games.import')}
              </Link>
              <Button busy={start.isPending} disabled={running} onClick={() => start.mutate()}>
                <RefreshCw className="size-4" aria-hidden /> {t('games.checkAgain')}
              </Button>
            </>
          )
        }
      />
      <ErrorNote error={systems.error ?? start.error} />
      <CheckNote check={check.data} />
      {(systems.isPending || roots.isPending) && <Spinner />}
      {roots.data?.length === 0 && (
        <Empty icon={<Gamepad2 className="size-8" />} title={t('games.none')}>
          <p className="mb-3">{t('games.noneBody')}</p>
          <p>
            <Trans i18nKey="games.addRoot" components={{ link: <Link to="/settings#folders-game" className="font-medium text-marquee" /> }} />
          </p>
        </Empty>
      )}
      {!!roots.data?.length && systems.data?.length === 0 && !running && (
        <Empty icon={<Gamepad2 className="size-8" />} title={t('games.noSystems')}>
          {t('games.noSystemsBody')}
        </Empty>
      )}
      {!!systems.data?.length && (
        <ul className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {systems.data.map((s) => (
            <li key={s.id}>
              <Link to={`/games/${s.id}`} className="group flex h-full flex-col rounded-xl border border-line bg-card p-5 transition hover:border-marquee">
                <div className="flex items-start gap-3">
                  <span className="flex size-11 shrink-0 items-center justify-center rounded-lg bg-marquee/10 font-display text-xs font-bold text-marquee" aria-hidden>
                    {s.short.slice(0, 4)}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-display text-lg font-semibold group-hover:text-marquee">{s.name}</span>
                    <span className="block truncate text-xs text-muted">{s.path}</span>
                  </span>
                  <ChevronRight className="mt-1 size-4 shrink-0 text-muted rtl:rotate-180" aria-hidden />
                </div>
                <div className="mt-4 flex flex-wrap items-center gap-2">
                  <Badge>{t('games.games', { count: s.groups })}</Badge>
                  {s.problems > 0 ? <Badge tone="bad">{t('games.withProblems', { count: s.problems })}</Badge> : s.games > 0 && <Badge tone="ok">{t('games.allComplete')}</Badge>}
                  {s.locked && (
                    <Badge>
                      <Lock className="size-3" aria-hidden /> {t('games.locked')}
                    </Badge>
                  )}
                </div>
                <p className="mt-3 text-xs text-muted">{systemFacts(s)}</p>
                {s.note && <p className="mt-2 text-xs text-bad">{s.note}</p>}
              </Link>
            </li>
          ))}
        </ul>
      )}
      {systems.data?.some((s) => s.arcade) && <MAMEVersion />}
    </>
  )
}

// systemFacts says which list a system was checked against, and when.
function systemFacts(s: GameSystem) {
  const parts: string[] = []
  if (s.list) parts.push(s.list)
  if (s.format) parts.push(i18n.t(`games.format.${s.format}`))
  if (s.checkedAt) parts.push(i18n.t('games.checked', { when: when(s.checkedAt) }))
  return parts.join(' · ')
}

// MAMEVersion picks the MAME version sets are checked against.
function MAMEVersion() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const set = useQuery({ queryKey: ['game-settings'], queryFn: () => get<GameSettings>('/games/settings') })
  const save = useMutation({
    mutationFn: (mameVersion: string) => put<GameSettings>('/games/settings', { mameVersion }),
    onSuccess: (v) => {
      qc.setQueryData(['game-settings'], v)
      qc.invalidateQueries({ queryKey: ['game-check'] })
    },
  })
  if (!set.data) return null
  const options = [
    { value: '', label: set.data.newest ? t('games.newest', { version: set.data.newest }) : t('games.newestUnknown') },
    ...set.data.versions.map((v) => ({ value: v, label: `MAME ${v}` })),
  ]
  return (
    <Card className="mt-8 max-w-xl">
      <h2 className="mb-1 font-display text-lg font-semibold">{t('games.mameVersion')}</h2>
      <p className="mb-4 text-sm text-muted">{t('games.mameVersionSub')}</p>
      <Field label={t('games.mameVersionPick')} hint={t('games.mameVersionHint')}>
        {(id) => <Select id={id} value={set.data.mameVersion} onChange={(v) => save.mutate(v)} options={options} />}
      </Field>
      <ErrorNote error={save.error} />
    </Card>
  )
}

const sorts = ['title', 'year', 'added'] as const

// Art is a game's picture, or its name where there's none.
function Art({ game, className = '' }: { game: Game; className?: string }) {
  const [failed, setFailed] = useState(false)
  return (
    <div className={`relative aspect-[3/4] overflow-hidden rounded-lg bg-line ${className}`}>
      {!failed ? (
        <img src={`/api/v1/games/${game.id}/art`} alt="" loading="lazy" className="size-full object-contain" onError={() => setFailed(true)} />
      ) : (
        <div className="flex size-full flex-col items-center justify-center gap-2 p-3 text-center font-display text-sm text-muted">
          <Gamepad2 className="size-6" aria-hidden />
          {game.title}
        </div>
      )}
    </div>
  )
}

// GameSystemPage lists one system's games, a page at a time.
export function GameSystemPage() {
  const { t } = useTranslation()
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const [text, setText] = useState('')
  const [q, setQ] = useState('')
  const [genre, setGenre] = useState('')
  const [decade, setDecade] = useState('')
  const [problems, setProblems] = useState(false)
  const [sort, setSort] = useState<(typeof sorts)[number]>('title')
  const [page, setPage] = useState(0)
  const size = usePageSize([24, 36, 60])
  useEffect(() => {
    const timer = setTimeout(() => setQ(text.trim()), 300)
    return () => clearTimeout(timer)
  }, [text])
  useEffect(() => setPage(0), [q, genre, decade, problems, sort, size])
  const params = new URLSearchParams({ system: String(id), q, genre, decade, sort, page: String(page + 1), size: String(size) })
  if (problems) params.set('problems', '1')
  const list = useQuery({ queryKey: ['games', id, params.toString()], queryFn: () => get<GamePageData>(`/games?${params}`), placeholderData: keepPreviousData })
  const check = useGameCheck()
  const sys = list.data?.system
  const start = useStartCheck(sys ? { rootId: 0, system: sys.system } : {})
  const lock = useMutation({
    mutationFn: (locked: boolean) => patch<GameSystem>(`/gamesystems/${id}`, { locked }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['games', id] })
      qc.invalidateQueries({ queryKey: ['game-systems'] })
    },
  })
  if (list.isPending) return <Spinner />
  if (list.isError) return <ErrorNote error={list.error} />
  const d = list.data
  const s = d.system
  return (
    <>
      <nav className="mb-2 text-sm text-muted">
        <Link to="/games" className="hover:text-ink">
          {t('nav.games')}
        </Link>
      </nav>
      <Heading
        title={s.name}
        sub={t('games.systemSub', { games: t('games.games', { count: s.groups }), versions: t('games.versions', { count: s.games }) })}
        actions={
          <Button busy={start.isPending} disabled={check.data?.running || s.locked} onClick={() => start.mutate()}>
            <RefreshCw className="size-4" aria-hidden /> {t('games.checkAgain')}
          </Button>
        }
      />
      <div className="-mt-3 mb-5 flex flex-wrap gap-2">
        {s.list && <Badge>{s.list}</Badge>}
        {s.format && <Badge>{t(`games.format.${s.format}`)}</Badge>}
        {s.support > 0 && <Badge>{t('games.support', { count: s.support })}</Badge>}
        {s.problems > 0 && <Badge tone="bad">{t('games.withProblems', { count: s.problems })}</Badge>}
        {s.checkedAt && <Badge>{t('games.checked', { when: when(s.checkedAt) })}</Badge>}
      </div>
      <ErrorNote error={start.error ?? lock.error} />
      <CheckNote check={check.data?.running ? check.data : undefined} />
      {s.note && <p className="mb-4 rounded-md border border-bad/40 bg-bad/5 px-3 py-2 text-sm text-bad">{s.note}</p>}
      {d.all === 0 ? (
        <Empty icon={<Gamepad2 className="size-8" />} title={t('games.noGames')}>
          {t('games.noGamesBody', { path: s.path })}
        </Empty>
      ) : (
        <>
          <div className="mb-5 flex flex-col gap-2 lg:flex-row lg:items-center">
            <Input type="search" className="lg:w-64 lg:flex-none" placeholder={t('games.filter')} aria-label={t('games.filter')} value={text} onChange={(e) => setText(e.target.value)} />
            <div className="grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center">
              {d.genres.length > 0 && (
                <div className="sm:w-44">
                  <Select value={genre} onChange={setGenre} options={[{ value: '', label: t('games.allGenres') }, ...d.genres.map((g) => ({ value: g, label: g }))]} />
                </div>
              )}
              {d.decades.length > 0 && (
                <div className="sm:w-36">
                  <Select value={decade} onChange={setDecade} options={[{ value: '', label: t('games.allYears') }, ...d.decades.map((g) => ({ value: g, label: g }))]} />
                </div>
              )}
              <div className="sm:w-40">
                <Select value={sort} onChange={(v) => setSort(v as (typeof sorts)[number])} options={sorts.map((k) => ({ value: k, label: t(`games.sort.${k}`) }))} />
              </div>
              <button
                onClick={() => setProblems(!problems)}
                aria-pressed={problems}
                className={`rounded-full border px-3 py-1.5 text-sm sm:py-1 ${problems ? 'border-ink bg-ink text-paper' : 'border-line text-muted hover:text-ink'}`}
              >
                {t('games.hasProblems')}
              </button>
            </div>
          </div>
          {d.total === 0 && <p className="py-10 text-center text-sm text-muted">{t('games.noMatch')}</p>}
          <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
            {d.groups.map(({ game: g, versions, problems }) => (
              <li key={g.id}>
                <Link to={`/games/game/${g.id}`} className="group block">
                  <Art game={g} className="transition group-hover:ring-2 group-hover:ring-marquee" />
                  <div className="mt-2 line-clamp-2 text-sm font-medium leading-snug">{g.title}</div>
                  <div className="truncate text-xs text-muted">{[g.year, g.maker].filter(Boolean).join(' · ')}</div>
                  <div className="mt-1 flex flex-wrap items-center gap-1">
                    {problems > 0 ? <StatusBadge status={g.status === 'complete' ? 'missing' : g.status} arcade={s.arcade} /> : <StatusBadge status="complete" />}
                    {versions > 1 && <Badge>{t('games.versions', { count: versions })}</Badge>}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          <Pager page={page} size={size} total={d.total} onPage={setPage} label={s.name} />
        </>
      )}
      <Card className="mt-10">
        <Toggle
          checked={s.locked}
          disabled={lock.isPending}
          onChange={(on) => lock.mutate(on)}
          label={
            <span>
              {t('games.locked')}
              <span className="block text-sm text-muted">{t('games.lockedHint', { name: s.name })}</span>
            </span>
          }
        />
      </Card>
    </>
  )
}

// problemText says what's wrong, in words.
function ProblemText({ kind, name, arcade }: { kind: string; name: string; arcade: boolean }) {
  const key = !arcade && kind === 'wrong' ? 'games.problem.wrongCopy' : `games.problem.${kind}`
  return <Trans i18nKey={key} values={{ name }} components={{ code: <code className="break-all" /> }} />
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-3 py-1.5 text-sm">
      <dt className="w-28 shrink-0 text-muted">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  )
}

// GamePage is one game: its facts, what's wrong with its files, and its
// other versions.
export function GamePage() {
  const { t } = useTranslation()
  const id = Number(useParams().id)
  const game = useQuery({ queryKey: ['game', id], queryFn: () => get<GameDetail>(`/games/${id}`) })
  if (game.isPending) return <Spinner />
  if (game.isError) return <ErrorNote error={game.error} />
  const g = game.data
  const s = g.system
  const parent = g.versions.find((v) => v.name === g.parent)
  return (
    <div>
      <nav className="mb-4 flex flex-wrap items-center gap-1 text-sm text-muted">
        <Link to="/games" className="hover:text-ink">
          {t('nav.games')}
        </Link>
        <ChevronRight className="size-3.5 rtl:rotate-180" aria-hidden />
        <Link to={`/games/${s.id}`} className="hover:text-ink">
          {s.name}
        </Link>
      </nav>
      <div className="flex flex-col gap-6 sm:flex-row sm:items-start">
        <Art game={g} className="w-40 shrink-0 self-center sm:w-56 sm:self-start" />
        <div className="min-w-0 flex-1">
          <h1 className="font-display text-3xl font-bold tracking-tight">{g.title}</h1>
          <div className="mt-3 flex flex-wrap gap-2">
            <StatusBadge status={g.status} list={s.list} arcade={s.arcade} />
            {g.inParent && <Badge>{t('games.inParent')}</Badge>}
          </div>
          <dl className="mt-4 divide-y divide-line">
            <Fact label={t('games.fact.system')}>{s.name}</Fact>
            <Fact label={s.arcade ? t('games.fact.set') : t('games.fact.name')}>
              <code className="break-all">{g.name}</code>
            </Fact>
            {g.year && <Fact label={t('games.fact.year')}>{g.year}</Fact>}
            {g.maker && <Fact label={s.arcade ? t('games.fact.manufacturer') : t('games.fact.publisher')}>{g.maker}</Fact>}
            {!!g.players && <Fact label={t('games.fact.players')}>{g.players}</Fact>}
            {g.genre && <Fact label={t('games.fact.genre')}>{g.genre}</Fact>}
            {s.arcade && g.parent && g.parent !== g.name && (
              <Fact label={t('games.fact.parent')}>{parent ? <Link to={`/games/game/${parent.id}`} className="text-marquee">{parent.title}</Link> : <code>{g.parent}</code>}</Fact>
            )}
            {g.crc && <Fact label={t('games.fact.crc')}>{<code>{g.crc}</code>}</Fact>}
            <Fact label={t('games.fact.found')}>{day(g.addedAt)}</Fact>
          </dl>
        </div>
      </div>

      {g.status !== 'complete' && (
        <Card className="mt-6">
          <h2 className="mb-1 font-display text-lg font-semibold">{t('games.whatsWrong')}</h2>
          <p className="mb-3 text-sm text-muted">{t(`games.explain.${g.status}${s.arcade ? 'Arcade' : ''}`, { list: s.list ?? '', name: g.name })}</p>
          {g.problems.length > 0 && (
            <ul className="flex list-disc flex-col gap-1 ps-5 text-sm">
              {g.problems.map((p) => (
                <li key={`${p.kind}:${p.name}`}>
                  <ProblemText kind={p.kind} name={p.name} arcade={s.arcade} />
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}

      <h2 className="mt-8 mb-2 font-display text-lg font-semibold">{t('games.files')}</h2>
      {g.files.length === 0 ? (
        <p className="text-sm text-muted">{t('games.noFiles')}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-line rounded-lg border border-line bg-card">
          {g.files.map((f) => (
            <li key={f.path} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 text-sm">
              <code className="min-w-0 flex-1 break-all">{f.path}</code>
              {f.parent && <Badge>{t('games.parentSet')}</Badge>}
              <span className="text-xs text-muted tabular-nums">{bytes(f.size)}</span>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-xs text-muted">{t('games.readOnly')}</p>

      {g.versions.length > 0 && (
        <>
          <h2 className="mt-8 mb-2 font-display text-lg font-semibold">
            {s.arcade ? t('games.clones') : t('games.otherVersions')} <span className="font-normal text-muted">{g.versions.length}</span>
          </h2>
          <ul className="flex flex-col divide-y divide-line">
            {g.versions.map((v) => (
              <li key={v.id}>
                <Link to={`/games/game/${v.id}`} className="flex items-center gap-3 py-2.5 hover:text-marquee">
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{v.title === g.title || !s.arcade ? v.name : v.title}</span>
                    <span className="block truncate text-xs text-muted">{[s.arcade ? v.name : '', v.year, v.maker, v.name === g.parent ? t('games.theParent') : ''].filter(Boolean).join(' · ')}</span>
                  </span>
                  <StatusBadge status={v.status} arcade={s.arcade} />
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}
