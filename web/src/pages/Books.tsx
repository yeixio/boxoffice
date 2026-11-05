import { Trans, useTranslation } from 'react-i18next'
import i18n from '../i18n'
import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom'
import { BookOpen, CircleCheck, Headphones, ListFilter, Lock, Plus, RefreshCw, Search, Trash2 } from 'lucide-react'
import { del, get, patch, post, put, type Author, type AuthorDetail, type Book, type BookFormat, type LookupAuthor } from '../api'
import { bytes, day } from '../format'
import { Releases } from '../Releases'
import { useRootFolders } from '../hooks'
import { SortControl, sortList, useSort, words, type SortOption } from '../sort'
import { Badge, Button, Card, Empty, ErrorNote, Heading, Input, Modal, Spinner, Toggle } from '../ui'

// Ebooks and audiobooks share one list of authors and books; each page
// shows what's wanted in its own format.
const formats = {
  ebook: {
    title: 'nav.books',
    path: '/books',
    icon: BookOpen,
    wanted: (a: Author) => a.wantedEbooks,
    have: (a: Author) => a.haveEbooks,
    wants: (b: Book) => b.wantEbook,
    file: (b: Book) => b.ebookFile,
    newFlag: 'newEbooks' as const,
  },
  audiobook: {
    title: 'nav.audiobooks',
    path: '/audiobooks',
    icon: Headphones,
    wanted: (a: Author) => a.wantedAudiobooks,
    have: (a: Author) => a.haveAudiobooks,
    wants: (b: Book) => b.wantAudiobook,
    file: (b: Book) => b.audiobookFile,
    newFlag: 'newAudiobooks' as const,
  },
}

const byName = (a: Author) => a.sortName || a.name

// Books lists the authors Box Office follows, for one format.
export function Books({ format }: { format: BookFormat }) {
  const { t } = useTranslation()
  const f = formats[format]
  const authors = useQuery({ queryKey: ['authors'], queryFn: () => get<Author[]>('/authors') })
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState('')
  const sorts = useMemo<SortOption<Author>[]>(
    () => [
      { key: 'name', label: 'sorts.name', value: byName, dir: 'asc', says: words.text },
      { key: 'added', label: 'sorts.added', value: (a) => a.addedAt, dir: 'desc', says: words.date },
      { key: 'wanted', label: `books.${format}.wantedSort`, value: f.wanted, dir: 'desc', says: words.count },
    ],
    [f, format],
  )
  const sort = useSort(`authors-${format}`, sorts)
  const shown = useMemo(() => {
    const q = text.trim().toLowerCase()
    return sortList(authors.data ?? [], sort.option, sort.dir, byName).filter((a) => !q || a.name.toLowerCase().includes(q))
  }, [authors.data, sort.option, sort.dir, text])
  const wanted = authors.data?.reduce((n, a) => n + f.wanted(a), 0) ?? 0
  const Icon = f.icon
  return (
    <>
      <Heading
        title={t(f.title)}
        sub={authors.data ? t('books.sub', { authors: t('books.authors', { count: authors.data.length }), wanted: t(`books.${format}.wanted`, { count: wanted }) }) : undefined}
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus className="size-4" aria-hidden /> {t('books.add')}
          </Button>
        }
      />
      {adding && <AddAuthor format={format} onClose={() => setAdding(false)} />}
      <ErrorNote error={authors.error} />
      {authors.isPending && <Spinner />}
      {authors.data?.length === 0 && (
        <Empty icon={<Icon className="size-8" />} title={t('books.none')}>
          {t('books.noneBody')}
        </Empty>
      )}
      {!!authors.data?.length && (
        <>
          <div className="mb-5 flex flex-col gap-2 sm:flex-row sm:items-center">
            <Input type="search" className="sm:w-60 sm:flex-none" placeholder={t('list.filterName')} aria-label={t('list.filterName')} value={text} onChange={(e) => setText(e.target.value)} />
            <SortControl sort={sort} className="sm:ms-auto" />
          </div>
          {shown.length === 0 && <p className="py-10 text-center text-sm text-muted">{t('books.noMatch')}</p>}
          <ul className="flex flex-col divide-y divide-line">
            {shown.map((a) => (
              <li key={a.id}>
                <Link to={`${f.path}/${a.id}`} className="flex items-center gap-3 py-3 hover:text-marquee">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-full bg-line font-display font-semibold text-muted" aria-hidden>
                    {a.name.slice(0, 1).toUpperCase()}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{a.name}</span>
                    <span className="block truncate text-xs text-muted">{sort.option.key === 'added' ? t('fact.added', { date: day(a.addedAt) }) : t('books.count', { count: a.books })}</span>
                  </span>
                  {a.locked && <Lock className="size-4 shrink-0 text-muted" aria-label={t('books.locked')} />}
                  {f.wanted(a) === 0 ? (
                    <Badge>{t('music.nothingWanted')}</Badge>
                  ) : f.have(a) >= f.wanted(a) ? (
                    <Badge tone="ok">{t('books.allOnDisk', { count: f.wanted(a) })}</Badge>
                  ) : (
                    <Badge tone="gold">{t('books.missing', { missing: f.wanted(a) - f.have(a), count: f.wanted(a) })}</Badge>
                  )}
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  )
}

function lifespan(a: { born?: string; died?: string }): string {
  if (!a.born) return ''
  return a.died ? i18n.t('books.lived', { born: a.born, died: a.died }) : i18n.t('books.born', { born: a.born })
}

// AddAuthor finds an author on Open Library and adds them.
function AddAuthor({ format, onClose }: { format: BookFormat; onClose: () => void }) {
  const { t } = useTranslation()
  const f = formats[format]
  const [term, setTerm] = useState('')
  const [q, setQ] = useState('')
  const [picked, setPicked] = useState<LookupAuthor | null>(null)
  useEffect(() => {
    const timer = setTimeout(() => setQ(term.trim()), 400)
    return () => clearTimeout(timer)
  }, [term])
  const results = useQuery({ queryKey: ['lookup', 'authors', q], queryFn: () => get<LookupAuthor[]>(`/lookup/authors?q=${encodeURIComponent(q)}`), enabled: q.length > 1 })
  return (
    <Modal title={picked ? t('add.title', { title: picked.name }) : t('books.addAn')} onClose={onClose} wide>
      {picked ? (
        <AddOptions format={format} author={picked} onBack={() => setPicked(null)} onDone={onClose} />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="relative">
            <Search className="pointer-events-none absolute start-3 top-2.5 size-4 text-muted" aria-hidden />
            <Input autoFocus className="ps-9" placeholder={t('books.placeholder')} value={term} onChange={(e) => setTerm(e.target.value)} />
          </div>
          {results.isFetching && <Spinner label={t('books.looking')} />}
          <ErrorNote error={results.error} />
          {results.data?.length === 0 && <p className="text-sm text-muted">{t('books.nobody')}</p>}
          <ul className="flex flex-col divide-y divide-line">
            {results.data?.map((a) => (
              <li key={a.key} className="flex items-center gap-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="font-display font-semibold">{a.name}</div>
                  <p className="truncate text-sm text-muted">
                    {[lifespan(a), a.topWork && t('books.knownFor', { work: a.topWork }), a.works ? t('books.works', { count: a.works }) : ''].filter(Boolean).join(' · ')}
                  </p>
                </div>
                {a.id ? (
                  <Link className="shrink-0 text-sm font-medium text-marquee" to={`${f.path}/${a.id}`} onClick={onClose}>
                    {t('add.onList')}
                  </Link>
                ) : (
                  <Button onClick={() => setPicked(a)}>{t('add.pick')}</Button>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </Modal>
  )
}

function AddOptions({ format, author, onBack, onDone }: { format: BookFormat; author: LookupAuthor; onBack: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const f = formats[format]
  const folders = useRootFolders(format)
  const qc = useQueryClient()
  const nav = useNavigate()
  const [all, setAll] = useState(false)
  const [fresh, setFresh] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  if (folders.isPending) return <Spinner />
  if (!folders.data?.length) {
    return (
      <p className="text-sm">
        <Trans i18nKey={`books.${format}.noRoot`} components={{ link: <Link to="/settings" className="font-medium text-marquee" onClick={onDone} /> }} />
      </p>
    )
  }
  const add = async () => {
    setBusy(true)
    setError(null)
    try {
      const added = await post<Author>('/authors', { key: author.key, format, all, new: fresh })
      await qc.invalidateQueries({ queryKey: ['authors'] })
      onDone()
      nav(`${f.path}/${added.id}`)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex flex-col gap-4">
      <Toggle
        checked={all}
        onChange={setAll}
        label={
          <span>
            {t(`books.${format}.wantAll`)}
            <span className="block text-sm text-muted">{t('books.wantAllHint')}</span>
          </span>
        }
      />
      <Toggle checked={fresh} onChange={setFresh} label={t(`books.${format}.wantNew`)} />
      <p className="text-sm text-muted">{t('books.lookup')}</p>
      <ErrorNote error={error} />
      <div className="flex justify-between">
        <Button variant="ghost" onClick={onBack}>
          {t('common.back')}
        </Button>
        <Button variant="primary" busy={busy} onClick={add}>
          {t('books.add')}
        </Button>
      </div>
    </div>
  )
}

// AuthorPage shows an author's books and which are wanted in one format.
export function AuthorPage({ format }: { format: BookFormat }) {
  const { t } = useTranslation()
  const f = formats[format]
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const nav = useNavigate()
  const author = useQuery({ queryKey: ['author', id], queryFn: () => get<AuthorDetail>(`/authors/${id}`) })
  const [removing, setRemoving] = useState(false)
  const [text, setText] = useState('')
  const [open, setOpen] = useState<number>()
  const [releasesFor, setReleasesFor] = useState<Book>()
  const set = (a: AuthorDetail) => {
    qc.setQueryData(['author', id], a)
    qc.invalidateQueries({ queryKey: ['authors'] })
  }
  const edit = useMutation({ mutationFn: (body: Partial<Pick<Author, 'newEbooks' | 'newAudiobooks' | 'locked'>>) => patch<AuthorDetail>(`/authors/${id}`, body), onSuccess: set })
  const refresh = useMutation({ mutationFn: () => post<AuthorDetail>(`/authors/${id}/refresh`), onSuccess: set })
  const all = useMutation({ mutationFn: (wanted: boolean) => put<AuthorDetail>(`/authors/${id}/wanted`, { format, wanted }), onSuccess: set })
  const book = useMutation({
    mutationFn: (v: { id: number; wanted: boolean }) => put<Book>(`/books/${v.id}/wanted`, { format, wanted: v.wanted }),
    onSuccess: (b) => {
      qc.setQueryData<AuthorDetail>(['author', id], (a) => {
        if (!a) return a
        const was = a.bookList.find((x) => x.id === b.id)
        const delta = was && f.wants(was) !== f.wants(b) ? (f.wants(b) ? 1 : -1) : 0
        const key = format === 'ebook' ? 'wantedEbooks' : 'wantedAudiobooks'
        return { ...a, bookList: a.bookList.map((x) => (x.id === b.id ? b : x)), [key]: a[key] + delta }
      })
      qc.invalidateQueries({ queryKey: ['authors'] })
    },
  })
  const remove = useMutation({
    mutationFn: () => del(`/authors/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['authors'] })
      nav(f.path)
    },
  })
  const shown = useMemo(() => {
    const q = text.trim().toLowerCase()
    return (author.data?.bookList ?? []).filter((b) => !q || b.title.toLowerCase().includes(q))
  }, [author.data, text])
  // A link to one book (from Downloads, say) opens it.
  useEffect(() => {
    const m = /^#book-(\d+)$/.exec(window.location.hash)
    if (m && author.data?.bookList.some((b) => b.id === Number(m[1]))) setOpen(Number(m[1]))
  }, [author.data])

  if (author.isPending) return <Spinner />
  if (author.isError) return <ErrorNote error={author.error} />
  const a = author.data
  const wanted = f.wanted(a)
  const have = f.have(a)
  const opened = a.bookList.find((b) => b.id === open)
  return (
    <div>
      <h1 className="font-display text-4xl font-bold tracking-tight">{a.name}</h1>
      <div className="mt-2 flex flex-wrap gap-2">
        <Badge tone={wanted ? 'gold' : 'plain'}>
          {t(`books.${format}.wanted`, { count: wanted })}
        </Badge>
        {wanted > 0 && <Badge tone={have >= wanted ? 'ok' : 'plain'}>{t('books.onDiskCount', { count: have })}</Badge>}
        {a.locked && <Badge>{t('books.locked')}</Badge>}
        {lifespan(a) && <Badge>{lifespan(a)}</Badge>}
      </div>
      {a.bio && <p className="mt-4 line-clamp-4 max-w-3xl text-sm leading-relaxed">{a.bio}</p>}
      <div className="mt-5 flex flex-wrap gap-2">
        <Button busy={refresh.isPending} onClick={() => refresh.mutate()}>
          <RefreshCw className="size-4" aria-hidden /> {t('books.refresh')}
        </Button>
        <Button variant="ghost" onClick={() => setRemoving(true)}>
          <Trash2 className="size-4" aria-hidden /> {t('title.remove')}
        </Button>
      </div>
      <ErrorNote error={refresh.error ?? edit.error ?? all.error ?? book.error} />

      <Card className="mt-6">
        <div className="flex flex-col gap-4">
          <Toggle checked={a[f.newFlag]} disabled={edit.isPending} onChange={(on) => edit.mutate({ [f.newFlag]: on })} label={t(`books.${format}.wantNew`)} />
          <Toggle
            checked={a.locked}
            disabled={edit.isPending}
            onChange={(on) => edit.mutate({ locked: on })}
            label={
              <span>
                {t('books.locked')}
                <span className="block text-sm text-muted">{t('books.lockedHint', { name: a.name })}</span>
              </span>
            }
          />
          <div className="flex flex-wrap gap-2">
            <Button busy={all.isPending && all.variables === true} disabled={wanted === a.books} onClick={() => all.mutate(true)}>
              {t('books.wantAllCount', { count: a.books })}
            </Button>
            <Button variant="ghost" busy={all.isPending && all.variables === false} disabled={wanted === 0} onClick={() => all.mutate(false)}>
              {t('books.wantNone')}
            </Button>
          </div>
        </div>
      </Card>

      <div className="mt-8 mb-3 flex flex-col gap-2 sm:flex-row sm:items-center">
        <h2 className="font-display text-lg font-semibold">
          {t('books.heading')} <span className="font-normal text-muted">{a.books}</span>
        </h2>
        {a.books > 12 && <Input type="search" className="sm:ms-auto sm:w-60" placeholder={t('list.filterTitle')} aria-label={t('list.filterTitle')} value={text} onChange={(e) => setText(e.target.value)} />}
      </div>
      {a.books === 0 && <p className="text-sm text-muted">{t('books.noBooks', { name: a.name })}</p>}
      <p className="mb-4 text-xs text-muted">{t('books.orderPick')}</p>
      <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
        {shown.map((b) => {
          const on = f.wants(b)
          const file = f.file(b)
          return (
            <li key={b.id} id={`book-${b.id}`}>
              <button type="button" className="relative block w-full text-start" onClick={() => setOpen(b.id)} aria-label={t('books.open', { title: b.title })}>
                <Cover src={b.cover} title={b.title} />
                {file && (
                  <span className="absolute top-2 end-2 rounded-full bg-ok p-1 text-white shadow" title={t('common.onDisk')}>
                    <CircleCheck className="size-4" aria-label={t('common.onDisk')} />
                  </span>
                )}
              </button>
              <div className="mt-2 line-clamp-2 font-medium leading-snug" title={b.subtitle ? `${b.title}: ${b.subtitle}` : b.title}>
                {b.title}
              </div>
              <div className="mt-1 flex items-center justify-between gap-2 text-xs text-muted">
                <span>{b.year || t('artist.noDate')}</span>
                <button
                  type="button"
                  disabled={book.isPending}
                  aria-pressed={on}
                  aria-label={on ? t(`books.${format}.stop`, { title: b.title }) : t(`books.${format}.want`, { title: b.title })}
                  onClick={() => book.mutate({ id: b.id, wanted: !on })}
                  className={`rounded-full border px-2 py-1 sm:py-0.5 ${on ? 'border-gold text-ink' : 'border-line hover:text-ink'}`}
                >
                  {on ? t('common.wanted') : t('artist.notWanted')}
                </button>
              </div>
            </li>
          )
        })}
      </ul>

      {opened && (
        <BookDialog
          format={format}
          book={opened}
          locked={a.locked}
          busy={book.isPending}
          onWant={(wanted) => book.mutate({ id: opened.id, wanted })}
          onReleases={() => {
            setOpen(undefined)
            setReleasesFor(opened)
          }}
          onClose={() => setOpen(undefined)}
        />
      )}
      {releasesFor && (
        <Releases path={`/books/${releasesFor.id}/releases?format=${format}`} title={t(`books.${format}.releasesTitle`, { title: releasesFor.title })} onClose={() => setReleasesFor(undefined)} />
      )}

      {removing && (
        <Modal title={t('artist.removeTitle', { name: a.name })} onClose={() => setRemoving(false)}>
          <p className="text-sm">{t('books.removeBody', { name: a.name })}</p>
          <ErrorNote error={remove.error} />
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setRemoving(false)}>
              {t('artist.keep')}
            </Button>
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              {t('title.remove')}
            </Button>
          </div>
        </Modal>
      )}
    </div>
  )
}

// BookDialog is one book in one format: whether it's on disk, and looking
// for it.
function BookDialog({
  format,
  book: b,
  locked,
  busy,
  onWant,
  onReleases,
  onClose,
}: {
  format: BookFormat
  book: Book
  locked: boolean
  busy: boolean
  onWant: (wanted: boolean) => void
  onReleases: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const f = formats[format]
  const qc = useQueryClient()
  const file = f.file(b)
  const on = f.wants(b)
  const search = useMutation({
    mutationFn: () => post(`/books/${b.id}/search`, { format }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  })
  return (
    <Modal title={b.title} onClose={onClose}>
      <div className="flex gap-4">
        <div className="w-24 shrink-0 sm:w-28">
          <Cover src={b.cover} title={b.title} />
        </div>
        <div className="min-w-0 flex-1 text-sm">
          {b.subtitle && <p className="mb-1 text-muted">{b.subtitle}</p>}
          <p className="text-muted">{[b.year, b.editions ? t('books.editions', { count: b.editions }) : ''].filter(Boolean).join(' · ')}</p>
          <div className="mt-3 flex flex-wrap gap-2">
            {file ? <Badge tone="ok">{t('common.onDisk')}</Badge> : on ? <Badge tone="gold">{t('common.wanted')}</Badge> : <Badge>{t('artist.notWanted')}</Badge>}
          </div>
          {file && (
            <p className="mt-2 break-all text-xs text-muted">
              {file.path} · {bytes(file.size)}
            </p>
          )}
        </div>
      </div>
      <div className="mt-5 flex flex-col gap-4">
        <Toggle checked={on} disabled={busy} onChange={onWant} label={t(`books.${format}.wantIt`)} />
        {locked ? (
          <p className="text-sm text-muted">{t('books.lockedNote')}</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            <Button variant="primary" busy={search.isPending} disabled={search.isSuccess} onClick={() => search.mutate()}>
              <Search className="size-4" aria-hidden /> {search.isSuccess ? t('books.lookingFor') : t('books.lookFor')}
            </Button>
            <Button onClick={onReleases}>
              <ListFilter className="size-4" aria-hidden /> {t('title.choose')}
            </Button>
          </div>
        )}
        {search.isSuccess && <p className="text-sm text-muted">{t(`books.${format}.searching`)}</p>}
        {!file && on && !locked && !search.isSuccess && <p className="text-sm text-muted">{t('books.alsoLooks')}</p>}
        <ErrorNote error={search.error} />
      </div>
    </Modal>
  )
}

// BookLink opens a book's author page at the book, from a link that only
// knows the book (a download's, say).
export function BookLink({ format }: { format: BookFormat }) {
  const id = Number(useParams().id)
  const book = useQuery({ queryKey: ['book', id], queryFn: () => get<Book>(`/books/${id}`) })
  if (book.isPending) return <Spinner />
  if (book.isError) return <ErrorNote error={book.error} />
  return <Navigate to={`${formats[format].path}/${book.data.authorId}#book-${id}`} replace />
}

// Cover is a book's cover, or its title when there's none.
function Cover({ src, title }: { src?: string; title: string }) {
  const [failed, setFailed] = useState(false)
  return (
    <div className="aspect-[2/3] overflow-hidden rounded-lg bg-line">
      {src && !failed ? (
        <img src={src} alt="" loading="lazy" className="size-full object-cover" onError={() => setFailed(true)} />
      ) : (
        <div className="flex size-full items-center justify-center p-3 text-center font-display text-sm text-muted">{title}</div>
      )}
    </div>
  )
}
