import i18n from '../i18n'
import { Trans, useTranslation } from 'react-i18next'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Copy,
  Download,
  FolderPlus,
  Images,
  MapPin,
  Pencil,
  Play,
  RefreshCw,
  Trash2,
  X,
} from 'lucide-react'
import {
  del,
  get,
  patch,
  post,
  type HomeAlbum,
  type HomeCopies,
  type HomeItem,
  type HomeItemDetail,
  type HomePage,
  type HomeScan,
  type HomeSummary,
} from '../api'
import { bytes } from '../format'
import { useRootFolders } from '../hooks'
import { Badge, Button, Card, Empty, ErrorNote, Field, Heading, Input, Modal, Select, Spinner, TabRow } from '../ui'

// Taken times are the camera's clock, sent as Unix seconds read as UTC,
// so they're shown in UTC: 14:30 on the camera is 14:30 here.
const fmt = (opts: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(i18n.language, { timeZone: 'UTC', ...opts })
const at = (s: number) => new Date(s * 1000)
export const takenDay = (s: number) => fmt({ weekday: 'short', day: 'numeric', month: 'long', year: 'numeric' }).format(at(s))
export const takenMonth = (s: number) => fmt({ month: 'long', year: 'numeric' }).format(at(s))
export const takenWhen = (s: number) => fmt({ dateStyle: 'full', timeStyle: 'short' }).format(at(s))
const dayKey = (s: number) => at(s).toISOString().slice(0, 10)
const monthKey = (s: number) => at(s).toISOString().slice(0, 7)

// dateRange is an album's dates: one day, or from one to another.
export function dateRange(start: number, end: number): string {
  if (!start) return ''
  if (dayKey(start) === dayKey(end)) return fmt({ day: 'numeric', month: 'long', year: 'numeric' }).format(at(start))
  return fmt({ day: 'numeric', month: 'short', year: 'numeric' }).formatRange(at(start), at(end))
}

// albumName is an album's name, or for one Box Office made and nobody
// named, where and when it was.
export function albumName(a: HomeAlbum): string {
  return a.name || a.place || dateRange(a.start, a.end) || i18n.t('web:home.album.untitled')
}

function duration(s: number): string {
  const m = Math.floor(s / 60)
  const sec = Math.floor(s % 60)
  if (m >= 60) return `${Math.floor(m / 60)}:${String(m % 60).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
  return `${m}:${String(sec).padStart(2, '0')}`
}

const thumb = (id: number) => `/api/v1/home/items/${id}/thumb`

// useHomeSummary is what's in the library and the last scan, asked for
// again while one runs; when one finishes, everything is read again.
function useHomeSummary() {
  const qc = useQueryClient()
  const summary = useQuery({
    queryKey: ['home'],
    queryFn: () => get<HomeSummary>('/home'),
    refetchInterval: (q) => (q.state.data?.scan.running ? 2000 : false),
  })
  const running = !!summary.data?.scan.running
  const was = useRef(running)
  useEffect(() => {
    if (was.current && !running) for (const key of ['home-items', 'home-albums', 'home-copies']) qc.invalidateQueries({ queryKey: [key] })
    was.current = running
  }, [running, qc])
  return summary
}

function ScanNote({ scan }: { scan?: HomeScan }) {
  const { t } = useTranslation()
  if (!scan) return null
  if (scan.running) {
    const step = t(`home.scan.${scan.step || 'finding'}`)
    return (
      <Card className="mb-4 flex items-center gap-3 p-4 text-sm">
        <RefreshCw className="size-4 shrink-0 animate-spin text-marquee" aria-hidden />
        <span className="min-w-0">
          {step}
          {scan.total > 0 && <span className="text-muted"> {t('home.scan.count', { done: scan.done.toLocaleString(i18n.language), total: scan.total.toLocaleString(i18n.language) })}</span>}
        </span>
      </Card>
    )
  }
  if (scan.error) return <ErrorNote error={new Error(scan.error)} />
  if (!scan.note && !scan.skipped.length && !scan.failed) return null
  return (
    <Card className="mb-4 space-y-1 p-4 text-sm">
      {scan.note && <p>{scan.note}</p>}
      {!!scan.skipped.length && <p className="text-muted">{t('home.scan.skipped', { folders: scan.skipped.join(', ') })}</p>}
      {!!scan.failed && <p className="text-muted">{t('home.scan.failed', { count: scan.failed })}</p>}
    </Card>
  )
}

type Tab = 'timeline' | 'albums' | 'copies'
const tabs: { id: Tab; label: string }[] = [
  { id: 'timeline', label: 'home.tab.timeline' },
  { id: 'albums', label: 'home.tab.albums' },
  { id: 'copies', label: 'home.tab.copies' },
]

// Photos is the Home videos and photos section: a timeline, albums, and
// the copies found.
export function Photos() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const tab: Tab = tabs.some((x) => x.id === params.get('tab')) ? (params.get('tab') as Tab) : 'timeline'
  const roots = useRootFolders('home')
  const summary = useHomeSummary()
  const qc = useQueryClient()
  const scan = useMutation({ mutationFn: () => post('/home/scan'), onSuccess: () => qc.invalidateQueries({ queryKey: ['home'] }) })
  const s = summary.data
  const running = !!s?.scan.running
  const empty = s && s.photos + s.videos === 0
  return (
    <>
      <Heading
        title={t('nav.photos')}
        sub={s && !empty ? t('home.sub', { photos: t('home.photos', { count: s.photos }), videos: t('home.videos', { count: s.videos }), size: bytes(s.size) }) : undefined}
        actions={
          !!roots.data?.length && (
            <Button busy={scan.isPending} disabled={running} onClick={() => scan.mutate()}>
              <RefreshCw className="size-4" aria-hidden /> {t('home.scanAgain')}
            </Button>
          )
        }
      />
      <ErrorNote error={summary.error ?? scan.error} />
      <ScanNote scan={s?.scan} />
      {(summary.isPending || roots.isPending) && <Spinner />}
      {roots.data?.length === 0 && (
        <Empty icon={<Images className="size-8" />} title={t('home.none')}>
          <p className="mb-3">{t('home.noneBody')}</p>
          <p>
            <Trans i18nKey="home.addRoot" components={{ link: <Link to="/settings#folders-home" className="font-medium text-marquee" /> }} />
          </p>
        </Empty>
      )}
      {!!roots.data?.length && empty && !running && (
        <Empty icon={<Images className="size-8" />} title={t('home.nothingFound')}>
          {t('home.nothingFoundBody')}
        </Empty>
      )}
      {!!roots.data?.length && s && !empty && (
        <>
          <TabRow selected={tab} label={t('nav.photos')}>
            {tabs.map((x) => (
              <button
                key={x.id}
                role="tab"
                aria-selected={tab === x.id}
                onClick={() => setParams(x.id === 'timeline' ? {} : { tab: x.id }, { replace: true })}
                className={`-mb-px shrink-0 border-b-2 px-3 py-2 text-sm font-medium transition ${tab === x.id ? 'border-marquee text-ink' : 'border-transparent text-muted hover:text-ink'}`}
              >
                {t(x.label)}
              </button>
            ))}
          </TabRow>
          <div role="tabpanel">
            {tab === 'timeline' && <Timeline />}
            {tab === 'albums' && <Albums />}
            {tab === 'copies' && <Copies />}
          </div>
        </>
      )}
    </>
  )
}

type Kind = '' | 'photo' | 'video'

function KindChips({ kind, onChange }: { kind: Kind; onChange: (k: Kind) => void }) {
  const { t } = useTranslation()
  const opts: { k: Kind; label: string }[] = [
    { k: '', label: t('home.kind.all') },
    { k: 'photo', label: t('home.kind.photos') },
    { k: 'video', label: t('home.kind.videos') },
  ]
  return (
    <div className="flex flex-wrap gap-2" role="group" aria-label={t('home.kind.label')}>
      {opts.map((o) => (
        <button
          key={o.k}
          aria-pressed={kind === o.k}
          onClick={() => onChange(o.k)}
          className={`rounded-full border px-3 py-1 text-sm transition ${kind === o.k ? 'border-marquee bg-marquee text-marquee-ink' : 'border-line bg-card hover:border-ink/40'}`}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

// useItems pages through the photos and videos, newest first.
function useItems(kind: Kind, album?: number) {
  return useInfiniteQuery({
    queryKey: ['home-items', kind, album ?? 0],
    queryFn: ({ pageParam }) => {
      const q = new URLSearchParams({ limit: '200' })
      if (kind) q.set('kind', kind)
      if (album) q.set('album', String(album))
      if (pageParam) q.set('before', pageParam)
      return get<HomePage>('/home/items?' + q)
    },
    initialPageParam: '',
    getNextPageParam: (last) => last.next || undefined,
  })
}

// Timeline shows everything by month and day, with picking to put photos
// in an album.
function Timeline() {
  const { t } = useTranslation()
  const [kind, setKind] = useState<Kind>('')
  const items = useItems(kind)
  const [picked, setPicked] = useState<Set<number> | null>(null)
  const [adding, setAdding] = useState(false)
  const list = useMemo(() => items.data?.pages.flatMap((p) => p.items) ?? [], [items.data])
  return (
    <>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <KindChips kind={kind} onChange={setKind} />
        {picked ? (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm text-muted">{t('home.picked', { count: picked.size })}</span>
            <Button variant="primary" disabled={!picked.size} onClick={() => setAdding(true)}>
              <FolderPlus className="size-4" aria-hidden /> {t('home.addToAlbum')}
            </Button>
            <Button onClick={() => setPicked(null)}>{t('common.cancel')}</Button>
          </div>
        ) : (
          <Button onClick={() => setPicked(new Set())}>
            <Check className="size-4" aria-hidden /> {t('home.pick')}
          </Button>
        )}
      </div>
      <ErrorNote error={items.error} />
      {items.isPending && <Spinner />}
      {items.data && list.length === 0 && <p className="text-sm text-muted">{t('home.noneOfKind')}</p>}
      <Grid items={list} picked={picked} onPick={setPicked} more={items.hasNextPage ? () => items.fetchNextPage() : undefined} loading={items.isFetchingNextPage} />
      {adding && picked && (
        <AddToAlbum
          items={[...picked]}
          onClose={() => setAdding(false)}
          onDone={() => {
            setAdding(false)
            setPicked(null)
          }}
        />
      )}
    </>
  )
}

// Grid is photos and videos as thumbnails, under a heading for each month
// and day. Clicking one opens it, or picks it while picking.
function Grid({
  items,
  picked,
  onPick,
  more,
  loading,
}: {
  items: HomeItem[]
  picked: Set<number> | null
  onPick: (s: Set<number>) => void
  more?: () => void
  loading?: boolean
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState<number | null>(null)
  const end = useRef<HTMLDivElement>(null)
  // Load more as the end comes into view.
  useEffect(() => {
    if (!more || !end.current) return
    const o = new IntersectionObserver((e) => e[0].isIntersecting && more(), { rootMargin: '800px' })
    o.observe(end.current)
    return () => o.disconnect()
  }, [more])
  const days = useMemo(() => {
    const out: { month: string; day: string; items: { it: HomeItem; i: number }[] }[] = []
    items.forEach((it, i) => {
      const d = dayKey(it.taken)
      const last = out[out.length - 1]
      if (last && last.day === d) last.items.push({ it, i })
      else out.push({ month: monthKey(it.taken), day: d, items: [{ it, i }] })
    })
    return out
  }, [items])
  const toggle = (id: number) => {
    if (!picked) return
    const n = new Set(picked)
    if (n.has(id)) n.delete(id)
    else n.add(id)
    onPick(n)
  }
  const pickDay = (ids: number[]) => {
    if (!picked) return
    const n = new Set(picked)
    const all = ids.every((id) => n.has(id))
    ids.forEach((id) => (all ? n.delete(id) : n.add(id)))
    onPick(n)
  }
  return (
    <>
      {days.map((d, k) => (
        <section key={d.day} className="mb-6">
          {(k === 0 || days[k - 1].month !== d.month) && (
            <h2 className="mb-3 font-display text-xl font-semibold">{takenMonth(d.items[0].it.taken)}</h2>
          )}
          <div className="mb-2 flex items-center gap-2">
            <h3 className="text-sm font-medium text-muted">{takenDay(d.items[0].it.taken)}</h3>
            {picked && (
              <button className="text-xs font-medium text-marquee hover:underline" onClick={() => pickDay(d.items.map((x) => x.it.id))}>
                {t('home.pickDay')}
              </button>
            )}
          </div>
          <ul className="grid grid-cols-3 gap-1 sm:grid-cols-[repeat(auto-fill,minmax(150px,1fr))]">
            {d.items.map(({ it, i }) => (
              <li key={it.id}>
                <Tile item={it} picked={picked ? picked.has(it.id) : undefined} onClick={() => (picked ? toggle(it.id) : setOpen(i))} />
              </li>
            ))}
          </ul>
        </section>
      ))}
      <div ref={end} />
      {loading && <Spinner />}
      {open !== null && items[open] && (
        <Viewer items={items} index={open} onIndex={setOpen} onClose={() => setOpen(null)} onEnd={more} />
      )}
    </>
  )
}

function Tile({ item, picked, onClick }: { item: HomeItem; picked?: boolean; onClick: () => void }) {
  const { t } = useTranslation()
  const label = `${item.kind === 'video' ? t('home.kind.video') : t('home.kind.photo')}, ${takenWhen(item.taken)}${item.place ? ', ' + item.place : ''}`
  return (
    <button
      onClick={onClick}
      aria-label={label}
      aria-pressed={picked}
      className={`group relative block aspect-square w-full overflow-hidden rounded bg-line focus-visible:outline-2 focus-visible:outline-marquee ${picked ? 'ring-4 ring-marquee ring-inset' : ''}`}
    >
      {item.hasThumb ? (
        <img src={thumb(item.id)} alt="" loading="lazy" decoding="async" className={`size-full object-cover transition ${picked ? 'scale-90 rounded' : 'group-hover:brightness-110'}`} />
      ) : (
        <span className="flex size-full items-center justify-center p-2 text-center text-xs break-all text-muted">{item.name}</span>
      )}
      {item.kind === 'video' && (
        <span className="absolute end-1 bottom-1 inline-flex items-center gap-1 rounded bg-black/60 px-1.5 py-0.5 text-xs text-white">
          <Play className="size-3" aria-hidden /> {item.duration ? duration(item.duration) : ''}
        </span>
      )}
      {picked !== undefined && (
        <span className={`absolute start-1 top-1 flex size-6 items-center justify-center rounded-full border-2 ${picked ? 'border-marquee bg-marquee text-marquee-ink' : 'border-white bg-black/30'}`}>
          {picked && <Check className="size-4" aria-hidden />}
        </span>
      )}
    </button>
  )
}

// Viewer shows one photo or plays one video, filling the screen, with
// what Box Office knows of it. Arrow keys go back and forward.
function Viewer({ items, index, onIndex, onClose, onEnd }: { items: HomeItem[]; index: number; onIndex: (i: number) => void; onClose: () => void; onEnd?: () => void }) {
  const { t } = useTranslation()
  const it = items[index]
  const ref = useRef<HTMLDialogElement>(null)
  const [info, setInfo] = useState(() => window.matchMedia('(min-width: 1024px)').matches)
  const [adding, setAdding] = useState(false)
  const [failed, setFailed] = useState(false)
  const detail = useQuery({ queryKey: ['home-item', it.id], queryFn: () => get<HomeItemDetail>(`/home/items/${it.id}`) })
  const prev = index > 0 ? () => onIndex(index - 1) : undefined
  const next = index < items.length - 1 ? () => onIndex(index + 1) : undefined
  useEffect(() => {
    const d = ref.current
    if (d && !d.open) d.showModal?.()
  }, [])
  useEffect(() => setFailed(false), [it.id])
  useEffect(() => {
    if (index >= items.length - 3) onEnd?.()
  }, [index, items.length, onEnd])
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (adding) return
      if (e.key === 'ArrowLeft') (document.dir === 'rtl' ? next : prev)?.()
      if (e.key === 'ArrowRight') (document.dir === 'rtl' ? prev : next)?.()
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [prev, next, adding])
  // Swipes on a phone go back and forward too.
  const touch = useRef<number | null>(null)
  const src = `/api/v1/home/items/${it.id}/view`
  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onCancel={onClose}
      aria-label={takenWhen(it.taken)}
      className="m-0 size-full max-h-none max-w-none bg-black p-0 text-white backdrop:bg-black"
    >
      <div className="flex size-full flex-col lg:flex-row">
        <div
          className="relative flex min-h-0 flex-1 items-center justify-center"
          onTouchStart={(e) => (touch.current = e.touches[0].clientX)}
          onTouchEnd={(e) => {
            if (touch.current === null) return
            const dx = e.changedTouches[0].clientX - touch.current
            touch.current = null
            if (Math.abs(dx) < 50) return
            const back = dx > 0 !== (document.dir === 'rtl')
            ;(back ? prev : next)?.()
          }}
        >
          {failed || !it.viewable ? (
            <div className="max-w-sm p-6 text-center text-sm">
              {it.hasThumb && <img src={thumb(it.id)} alt="" className="mx-auto mb-4 max-h-64 rounded" />}
              <p className="mb-3">{it.kind === 'video' ? t('home.view.cantPlay') : t('home.view.cantShow')}</p>
              <a href={`/api/v1/home/items/${it.id}/file`} className="inline-flex items-center gap-2 rounded-md bg-white/15 px-3 py-2 font-medium hover:bg-white/25">
                <Download className="size-4" aria-hidden /> {t('home.view.download')}
              </a>
            </div>
          ) : it.kind === 'video' ? (
            <video key={it.id} src={src} controls autoPlay playsInline poster={it.hasThumb ? thumb(it.id) : undefined} onError={() => setFailed(true)} className="max-h-full max-w-full" />
          ) : (
            <img key={it.id} src={src} alt={it.name} onError={() => setFailed(true)} className="max-h-full max-w-full object-contain" />
          )}
          {prev && (
            <button onClick={prev} aria-label={t('home.view.previous')} className="absolute start-2 top-1/2 -translate-y-1/2 rounded-full bg-black/50 p-2 hover:bg-black/80">
              <ChevronLeft className="size-6 rtl:rotate-180" aria-hidden />
            </button>
          )}
          {next && (
            <button onClick={next} aria-label={t('home.view.next')} className="absolute end-2 top-1/2 -translate-y-1/2 rounded-full bg-black/50 p-2 hover:bg-black/80">
              <ChevronRight className="size-6 rtl:rotate-180" aria-hidden />
            </button>
          )}
          <div className="absolute end-2 top-2 flex gap-2">
            <button onClick={() => setInfo(!info)} className="rounded-full bg-black/50 px-3 py-2 text-sm hover:bg-black/80" aria-pressed={info}>
              {t('home.view.info')}
            </button>
            <button onClick={onClose} aria-label={t('common:close')} className="rounded-full bg-black/50 p-2 hover:bg-black/80">
              <X className="size-5" aria-hidden />
            </button>
          </div>
        </div>
        {info && (
          <aside className="max-h-[45vh] shrink-0 overflow-y-auto border-t border-white/15 bg-neutral-900 p-5 text-sm lg:max-h-none lg:w-80 lg:border-s lg:border-t-0">
            <p className="font-display text-lg font-semibold">{takenWhen(it.taken)}</p>
            {it.takenFrom === 'file' && <p className="mt-1 text-xs text-white/60">{t('home.view.guessedDate')}</p>}
            {it.place && (
              <p className="mt-3 flex items-center gap-2">
                <MapPin className="size-4 shrink-0" aria-hidden /> {it.place}
              </p>
            )}
            <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-white/80">
              {(it.make || it.model) && (
                <>
                  <dt className="text-white/50">{t('home.view.camera')}</dt>
                  <dd>{[it.make, it.model?.startsWith(it.make ?? '') ? it.model.slice((it.make ?? '').length).trim() : it.model].filter(Boolean).join(' ')}</dd>
                </>
              )}
              {!!it.width && (
                <>
                  <dt className="text-white/50">{t('home.view.size')}</dt>
                  <dd>
                    {it.width} × {it.height}
                    {it.kind === 'video' && it.duration ? `, ${duration(it.duration)}` : ''}
                  </dd>
                </>
              )}
              <dt className="text-white/50">{t('home.view.file')}</dt>
              <dd className="break-all">
                {it.name}, {bytes(it.size)}
              </dd>
              {it.folder && (
                <>
                  <dt className="text-white/50">{t('home.view.folder')}</dt>
                  <dd className="break-all">{it.folder}</dd>
                </>
              )}
            </dl>
            {!!detail.data?.albums.length && (
              <div className="mt-4">
                <p className="mb-1 text-white/50">{t('home.view.inAlbums')}</p>
                <ul className="flex flex-wrap gap-1.5">
                  {detail.data.albums.map((a) => (
                    <li key={a.id}>
                      <Link to={`/photos/albums/${a.id}`} className="inline-block rounded-full bg-white/10 px-2.5 py-1 text-xs hover:bg-white/20">
                        {albumName(a)}
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            )}
            <div className="mt-5 flex flex-wrap gap-2">
              <button onClick={() => setAdding(true)} className="inline-flex items-center gap-2 rounded-md bg-white/15 px-3 py-2 font-medium hover:bg-white/25">
                <FolderPlus className="size-4" aria-hidden /> {t('home.addToAlbum')}
              </button>
              <a href={`/api/v1/home/items/${it.id}/file`} className="inline-flex items-center gap-2 rounded-md bg-white/15 px-3 py-2 font-medium hover:bg-white/25">
                <Download className="size-4" aria-hidden /> {t('home.view.download')}
              </a>
            </div>
          </aside>
        )}
      </div>
      {adding && <AddToAlbum items={[it.id]} onClose={() => setAdding(false)} onDone={() => setAdding(false)} />}
    </dialog>
  )
}

function useAlbums(hidden = false) {
  return useQuery({ queryKey: ['home-albums', hidden], queryFn: () => get<HomeAlbum[]>('/home/albums' + (hidden ? '?hidden=true' : '')) })
}

// AddToAlbum puts photos in an album of one's own, or a new one.
function AddToAlbum({ items, onClose, onDone }: { items: number[]; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const albums = useAlbums()
  const own = albums.data?.filter((a) => a.kind === 'own') ?? []
  const [album, setAlbum] = useState<string>('new')
  const [name, setName] = useState('')
  useEffect(() => {
    if (own.length && album === 'new' && !name) setAlbum(String(own[0].id))
    // Only once the albums arrive.
  }, [albums.data])
  const save = useMutation({
    mutationFn: () => (album === 'new' ? post<HomeAlbum>('/home/albums', { name, items }) : post<HomeAlbum>(`/home/albums/${album}/items`, { items })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['home-albums'] })
      qc.invalidateQueries({ queryKey: ['home-item'] })
      qc.invalidateQueries({ queryKey: ['home'] })
      onDone()
    },
  })
  return (
    <Modal title={t('home.addTitle', { count: items.length })} onClose={onClose}>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate()
        }}
      >
        <Field label={t('home.album.label')}>
          {(id) => (
            <Select
              id={id}
              value={album}
              onChange={setAlbum}
              options={[...own.map((a) => ({ value: String(a.id), label: albumName(a) })), { value: 'new', label: t('home.album.new') }]}
            />
          )}
        </Field>
        {album === 'new' && (
          <Field label={t('home.album.name')} hint={t('home.album.nameHint')}>
            {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} autoFocus required maxLength={200} />}
          </Field>
        )}
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button type="button" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" type="submit" busy={save.isPending} disabled={album === 'new' && !name.trim()}>
            {t('home.addToAlbum')}
          </Button>
        </div>
      </form>
    </Modal>
  )
}

function AlbumCard({ a }: { a: HomeAlbum }) {
  const { t } = useTranslation()
  return (
    <Link to={`/photos/albums/${a.id}`} className="group block min-w-0">
      <div className="aspect-[4/3] overflow-hidden rounded-lg bg-line">
        {a.coverId ? <img src={thumb(a.coverId)} alt="" loading="lazy" className="size-full object-cover transition group-hover:scale-105" /> : null}
      </div>
      <p className="mt-2 truncate font-medium group-hover:text-marquee">{albumName(a)}</p>
      <p className="text-xs text-muted">
        {[a.name || a.place ? dateRange(a.start, a.end) : '', t('home.items', { count: a.count })].filter(Boolean).join(' · ')}
      </p>
    </Link>
  )
}

// Albums lists the albums: one's own first, then those Box Office made.
function Albums() {
  const { t } = useTranslation()
  const albums = useAlbums()
  const [making, setMaking] = useState(false)
  const own = albums.data?.filter((a) => a.kind === 'own') ?? []
  const events = albums.data?.filter((a) => a.kind === 'event') ?? []
  const away = useAlbums(true).data?.filter((a) => a.hidden) ?? []
  return (
    <>
      <ErrorNote error={albums.error} />
      {albums.isPending && <Spinner />}
      <section className="mb-8">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="font-display text-xl font-semibold">{t('home.album.yours')}</h2>
          <Button onClick={() => setMaking(true)}>
            <FolderPlus className="size-4" aria-hidden /> {t('home.album.new')}
          </Button>
        </div>
        {own.length === 0 ? (
          <p className="text-sm text-muted">{t('home.album.noneYours')}</p>
        ) : (
          <ul className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
            {own.map((a) => (
              <li key={a.id}>
                <AlbumCard a={a} />
              </li>
            ))}
          </ul>
        )}
      </section>
      <section>
        <h2 className="mb-1 font-display text-xl font-semibold">{t('home.album.events')}</h2>
        <p className="mb-3 text-sm text-muted">{t('home.album.eventsSub')}</p>
        {events.length === 0 ? (
          <p className="text-sm text-muted">{t('home.album.noneEvents')}</p>
        ) : (
          <ul className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
            {events.map((a) => (
              <li key={a.id}>
                <AlbumCard a={a} />
              </li>
            ))}
          </ul>
        )}
      </section>
      {away.length > 0 && (
        <details className="mt-8">
          <summary className="cursor-pointer text-sm text-muted hover:text-ink">
            {t('home.album.hide')} ({away.length})
          </summary>
          <ul className="mt-3 grid grid-cols-2 gap-4 opacity-80 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
            {away.map((a) => (
              <li key={a.id}>
                <AlbumCard a={a} />
              </li>
            ))}
          </ul>
        </details>
      )}
      {making && <NewAlbum onClose={() => setMaking(false)} />}
    </>
  )
}

function NewAlbum({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const make = useMutation({
    mutationFn: () => post<HomeAlbum>('/home/albums', { name, items: [] }),
    onSuccess: (a) => {
      qc.invalidateQueries({ queryKey: ['home-albums'] })
      navigate(`/photos/albums/${a.id}`)
    },
  })
  return (
    <Modal title={t('home.album.new')} onClose={onClose}>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          make.mutate()
        }}
      >
        <Field label={t('home.album.name')} hint={t('home.album.newHint')}>
          {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} autoFocus required maxLength={200} />}
        </Field>
        <ErrorNote error={make.error} />
        <div className="flex justify-end gap-2">
          <Button type="button" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" type="submit" busy={make.isPending} disabled={!name.trim()}>
            {t('home.album.make')}
          </Button>
        </div>
      </form>
    </Modal>
  )
}

// AlbumPage is one album: its photos, renaming it, and taking photos out
// of it or deleting it (an album Box Office made is put away instead).
export function AlbumPage() {
  const { t } = useTranslation()
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const navigate = useNavigate()
  const album = useQuery({ queryKey: ['home-album', id], queryFn: () => get<HomeAlbum>(`/home/albums/${id}`) })
  const [kind, setKind] = useState<Kind>('')
  const items = useItems(kind, id)
  const list = useMemo(() => items.data?.pages.flatMap((p) => p.items) ?? [], [items.data])
  const [picked, setPicked] = useState<Set<number> | null>(null)
  const [renaming, setRenaming] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['home-album', id] })
    qc.invalidateQueries({ queryKey: ['home-albums'] })
    qc.invalidateQueries({ queryKey: ['home-items'] })
  }
  const remove = useMutation({
    mutationFn: () => post(`/home/albums/${id}/remove`, { items: [...(picked ?? [])] }),
    onSuccess: () => {
      setPicked(null)
      refresh()
    },
  })
  const show = useMutation({ mutationFn: () => patch(`/home/albums/${id}`, { hidden: false }), onSuccess: refresh })
  const a = album.data
  return (
    <>
      <Link to="/photos?tab=albums" className="mb-3 inline-flex items-center gap-1 text-sm text-muted hover:text-ink">
        <ChevronLeft className="size-4 rtl:rotate-180" aria-hidden /> {t('home.tab.albums')}
      </Link>
      <ErrorNote error={album.error ?? remove.error ?? show.error} />
      {album.isPending && <Spinner />}
      {a && (
        <>
          <Heading
            title={albumName(a)}
            sub={[a.name || a.place ? dateRange(a.start, a.end) : '', t('home.items', { count: a.count })].filter(Boolean).join(' · ')}
            actions={
              <>
                <Button onClick={() => setRenaming(true)}>
                  <Pencil className="size-4" aria-hidden /> {t('home.album.rename')}
                </Button>
                {a.hidden ? (
                  <Button onClick={() => show.mutate()} busy={show.isPending}>
                    {t('home.album.unhide')}
                  </Button>
                ) : (
                  <Button onClick={() => setDeleting(true)}>
                    <Trash2 className="size-4" aria-hidden /> {a.kind === 'event' ? t('home.album.hide') : t('home.album.delete')}
                  </Button>
                )}
              </>
            }
          />
          {a.kind === 'event' && <p className="-mt-3 mb-4 text-sm text-muted">{a.hidden ? t('home.album.hiddenNote') : t('home.album.eventNote')}</p>}
          <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
            <KindChips kind={kind} onChange={setKind} />
            {a.kind === 'own' &&
              (picked ? (
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm text-muted">{t('home.picked', { count: picked.size })}</span>
                  <Button disabled={!picked.size} busy={remove.isPending} onClick={() => remove.mutate()}>
                    {t('home.album.takeOut')}
                  </Button>
                  <Button onClick={() => setPicked(null)}>{t('common.cancel')}</Button>
                </div>
              ) : (
                a.count > 0 && (
                  <Button onClick={() => setPicked(new Set())}>
                    <Check className="size-4" aria-hidden /> {t('home.pick')}
                  </Button>
                )
              ))}
          </div>
          {a.count === 0 && a.kind === 'own' && (
            <Empty icon={<Images className="size-8" />} title={t('home.album.emptyTitle')}>
              <Trans i18nKey="home.album.emptyBody" components={{ link: <Link to="/photos" className="font-medium text-marquee" /> }} />
            </Empty>
          )}
          <ErrorNote error={items.error} />
          <Grid items={list} picked={picked} onPick={setPicked} more={items.hasNextPage ? () => items.fetchNextPage() : undefined} loading={items.isFetchingNextPage} />
          {renaming && <RenameAlbum album={a} onClose={() => setRenaming(false)} onDone={refresh} />}
          {deleting && (
            <DeleteAlbum
              album={a}
              onClose={() => setDeleting(false)}
              onDone={() => {
                refresh()
                navigate('/photos?tab=albums')
              }}
            />
          )}
        </>
      )}
    </>
  )
}

function RenameAlbum({ album, onClose, onDone }: { album: HomeAlbum; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState(album.name)
  const save = useMutation({
    mutationFn: () => patch(`/home/albums/${album.id}`, { name }),
    onSuccess: () => {
      onDone()
      onClose()
    },
  })
  return (
    <Modal title={t('home.album.rename')} onClose={onClose}>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate()
        }}
      >
        <Field label={t('home.album.name')} hint={album.kind === 'event' ? t('home.album.renameEventHint', { name: album.place || dateRange(album.start, album.end) }) : undefined}>
          {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} autoFocus required={album.kind === 'own'} maxLength={200} />}
        </Field>
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <Button type="button" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" type="submit" busy={save.isPending} disabled={album.kind === 'own' && !name.trim()}>
            {t('common.save')}
          </Button>
        </div>
      </form>
    </Modal>
  )
}

function DeleteAlbum({ album, onClose, onDone }: { album: HomeAlbum; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const go = useMutation({ mutationFn: () => del(`/home/albums/${album.id}`), onSuccess: onDone })
  const event = album.kind === 'event'
  return (
    <Modal title={event ? t('home.album.hideTitle') : t('home.album.deleteTitle')} onClose={onClose}>
      <p className="mb-4 text-sm">{event ? t('home.album.hideBody') : t('home.album.deleteBody', { name: albumName(album) })}</p>
      <ErrorNote error={go.error} />
      <div className="flex justify-end gap-2">
        <Button onClick={onClose}>{t('common.cancel')}</Button>
        <Button variant={event ? 'primary' : 'danger'} busy={go.isPending} onClick={() => go.mutate()}>
          {event ? t('home.album.hide') : t('home.album.delete')}
        </Button>
      </div>
    </Modal>
  )
}

// Copies lists the same photo or video found more than once, with the
// extra copies ticked to move into the recycle folder.
function Copies() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const copies = useQuery({ queryKey: ['home-copies'], queryFn: () => get<HomeCopies[]>('/home/duplicates') })
  const [unticked, setUnticked] = useState<Set<number>>(new Set())
  const [confirm, setConfirm] = useState(false)
  const [done, setDone] = useState<{ moved: number; freed: number; error?: string } | null>(null)
  const [open, setOpen] = useState<{ items: HomeItem[]; index: number } | null>(null)
  // Ticked: every copy but the one to keep, unless unticked.
  const ticked = useMemo(() => {
    const out: HomeItem[] = []
    for (const g of copies.data ?? []) for (const it of g.items) if (it.id !== g.keep && !unticked.has(it.id)) out.push(it)
    return out
  }, [copies.data, unticked])
  const size = ticked.reduce((n, it) => n + it.size, 0)
  const move = useMutation({
    mutationFn: () => post<{ moved: number; freed: number; error?: string }>('/home/duplicates/recycle', { items: ticked.map((it) => it.id), size }),
    onSuccess: (r) => {
      setDone(r)
      setConfirm(false)
      setUnticked(new Set())
      qc.invalidateQueries({ queryKey: ['home-copies'] })
      qc.invalidateQueries({ queryKey: ['home'] })
      qc.invalidateQueries({ queryKey: ['home-items'] })
      qc.invalidateQueries({ queryKey: ['home-albums'] })
    },
  })
  const flip = (id: number) => {
    const n = new Set(unticked)
    if (n.has(id)) n.delete(id)
    else n.add(id)
    setUnticked(n)
  }
  return (
    <>
      <p className="mb-4 max-w-3xl text-sm text-muted">{t('home.copies.intro')}</p>
      <ErrorNote error={copies.error} />
      {copies.isPending && <Spinner />}
      {done && (
        <Card className="mb-4 p-4 text-sm">
          {t('home.copies.moved', { count: done.moved, size: bytes(done.freed) })}
          {done.error && <p className="mt-1 text-bad">{done.error}</p>}
        </Card>
      )}
      {copies.data?.length === 0 && (
        <Empty icon={<Copy className="size-8" />} title={t('home.copies.none')}>
          {t('home.copies.noneBody')}
        </Empty>
      )}
      {!!copies.data?.length && (
        <>
          <div className="sticky top-0 z-10 -mx-1 mb-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-card px-4 py-3">
            <span className="text-sm">
              {t('home.copies.groups', { count: copies.data.length })} · {t('home.copies.ticked', { count: ticked.length, size: bytes(size) })}
            </span>
            <Button variant="primary" disabled={!ticked.length} onClick={() => setConfirm(true)}>
              <Trash2 className="size-4" aria-hidden /> {t('home.copies.move')}
            </Button>
          </div>
          <ul className="space-y-4">
            {copies.data.map((g) => (
              <li key={g.keep}>
                <Card className="p-4">
                  <div className="mb-3 flex flex-wrap items-center gap-2">
                    <Badge tone={g.kind === 'same' ? 'plain' : 'gold'}>{t(`home.copies.${g.kind}`)}</Badge>
                    <span className="text-sm text-muted">{takenWhen(g.items[0].taken)}</span>
                  </div>
                  <ul className="grid gap-3 sm:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">
                    {g.items.map((it, i) => {
                      const keep = it.id === g.keep
                      const on = !keep && !unticked.has(it.id)
                      return (
                        <li key={it.id} className={`flex gap-3 rounded-lg border p-2 ${keep ? 'border-ok/60' : on ? 'border-bad/60' : 'border-line'}`}>
                          <button onClick={() => setOpen({ items: g.items, index: i })} className="size-20 shrink-0 overflow-hidden rounded bg-line" aria-label={t('home.copies.look', { name: it.name })}>
                            {it.hasThumb && <img src={thumb(it.id)} alt="" loading="lazy" className="size-full object-cover" />}
                          </button>
                          <div className="min-w-0 text-xs">
                            <p className="truncate font-medium" title={it.name}>
                              {it.name}
                            </p>
                            <p className="truncate text-muted" title={it.folder}>
                              {it.folder || '/'}
                            </p>
                            <p className="text-muted">
                              {bytes(it.size)}
                              {!!it.width && ` · ${it.width} × ${it.height}`}
                            </p>
                            {keep ? (
                              <p className="mt-1 font-medium text-ok">{t('home.copies.keep')}</p>
                            ) : (
                              <label className="mt-1 flex items-center gap-1.5">
                                <input type="checkbox" checked={on} onChange={() => flip(it.id)} className="accent-marquee" />
                                {t('home.copies.moveThis')}
                              </label>
                            )}
                          </div>
                        </li>
                      )
                    })}
                  </ul>
                </Card>
              </li>
            ))}
          </ul>
        </>
      )}
      {confirm && (
        <Modal title={t('home.copies.confirmTitle')} onClose={() => setConfirm(false)}>
          <p className="mb-2 text-sm">{t('home.copies.confirmBody', { count: ticked.length, size: bytes(size) })}</p>
          <p className="mb-4 text-sm text-muted">{t('home.copies.confirmNote')}</p>
          <ErrorNote error={move.error} />
          <div className="flex justify-end gap-2">
            <Button onClick={() => setConfirm(false)}>{t('common.cancel')}</Button>
            <Button variant="primary" busy={move.isPending} onClick={() => move.mutate()}>
              {t('home.copies.move')}
            </Button>
          </div>
        </Modal>
      )}
      {open && <Viewer items={open.items} index={open.index} onIndex={(i) => setOpen({ ...open, index: i })} onClose={() => setOpen(null)} />}
    </>
  )
}
