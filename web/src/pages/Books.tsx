import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { BookOpen, Headphones, Plus, RefreshCw, Search, Trash2 } from 'lucide-react'
import { del, get, patch, post, put, type Author, type AuthorDetail, type Book, type BookFormat, type LookupAuthor } from '../api'
import { day } from '../format'
import { useRootFolders } from '../hooks'
import { SortControl, sortList, useSort, words, type SortOption } from '../sort'
import { Badge, Button, Card, Empty, ErrorNote, Heading, Input, Modal, Spinner, Toggle } from '../ui'

// Ebooks and audiobooks share one list of authors and books; each page
// shows what's wanted in its own format.
const formats = {
  ebook: {
    title: 'Books',
    path: '/books',
    noun: 'ebook',
    nouns: 'ebooks',
    icon: BookOpen,
    wanted: (a: Author) => a.wantedEbooks,
    wants: (b: Book) => b.wantEbook,
    newFlag: 'newEbooks' as const,
  },
  audiobook: {
    title: 'Audiobooks',
    path: '/audiobooks',
    noun: 'audiobook',
    nouns: 'audiobooks',
    icon: Headphones,
    wanted: (a: Author) => a.wantedAudiobooks,
    wants: (b: Book) => b.wantAudiobook,
    newFlag: 'newAudiobooks' as const,
  },
}

const byName = (a: Author) => a.sortName || a.name

// Books lists the authors Box Office follows, for one format.
export function Books({ format }: { format: BookFormat }) {
  const f = formats[format]
  const authors = useQuery({ queryKey: ['authors'], queryFn: () => get<Author[]>('/authors') })
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState('')
  const sorts = useMemo<SortOption<Author>[]>(
    () => [
      { key: 'name', label: 'Name', value: byName, dir: 'asc', says: words.text },
      { key: 'added', label: 'Date added', value: (a) => a.addedAt, dir: 'desc', says: words.date },
      { key: 'wanted', label: `${f.title} wanted`, value: f.wanted, dir: 'desc', says: { asc: 'Fewest first', desc: 'Most first' } },
    ],
    [f],
  )
  const sort = useSort(`authors-${format}`, sorts)
  const shown = useMemo(() => {
    const t = text.trim().toLowerCase()
    return sortList(authors.data ?? [], sort.option, sort.dir, byName).filter((a) => !t || a.name.toLowerCase().includes(t))
  }, [authors.data, sort.option, sort.dir, text])
  const wanted = authors.data?.reduce((n, a) => n + f.wanted(a), 0) ?? 0
  const Icon = f.icon
  return (
    <>
      <Heading
        title={f.title}
        sub={authors.data ? `${authors.data.length} ${authors.data.length === 1 ? 'author' : 'authors'}, ${wanted} ${wanted === 1 ? f.noun : f.nouns} wanted` : undefined}
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus className="size-4" aria-hidden /> Add author
          </Button>
        }
      />
      {adding && <AddAuthor format={format} onClose={() => setAdding(false)} />}
      <ErrorNote error={authors.error} />
      {authors.isPending && <Spinner />}
      {authors.data?.length === 0 && (
        <Empty icon={<Icon className="size-8" />} title="No authors yet">
          Add an author and Box Office lists their books from Open Library. Ebooks and audiobooks share the one list of authors, so an author added here shows on both pages.
        </Empty>
      )}
      {!!authors.data?.length && (
        <>
          <div className="mb-5 flex flex-col gap-2 sm:flex-row sm:items-center">
            <Input type="search" className="sm:w-60 sm:flex-none" placeholder="Filter by name" aria-label="Filter by name" value={text} onChange={(e) => setText(e.target.value)} />
            <SortControl sort={sort} className="sm:ml-auto" />
          </div>
          {shown.length === 0 && <p className="py-10 text-center text-sm text-muted">No authors match.</p>}
          <ul className="flex flex-col divide-y divide-line">
            {shown.map((a) => (
              <li key={a.id}>
                <Link to={`${f.path}/${a.id}`} className="flex items-center gap-3 py-3 hover:text-marquee">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-full bg-line font-display font-semibold text-muted" aria-hidden>
                    {a.name.slice(0, 1).toUpperCase()}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{a.name}</span>
                    <span className="block truncate text-xs text-muted">{sort.option.key === 'added' ? `Added ${day(a.addedAt)}` : `${a.books} ${a.books === 1 ? 'book' : 'books'}`}</span>
                  </span>
                  {f.wanted(a) > 0 ? <Badge tone="gold">{f.wanted(a)} wanted</Badge> : <Badge>Nothing wanted</Badge>}
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
  return a.died ? `${a.born} – ${a.died}` : `Born ${a.born}`
}

// AddAuthor finds an author on Open Library and adds them.
function AddAuthor({ format, onClose }: { format: BookFormat; onClose: () => void }) {
  const f = formats[format]
  const [term, setTerm] = useState('')
  const [q, setQ] = useState('')
  const [picked, setPicked] = useState<LookupAuthor | null>(null)
  useEffect(() => {
    const t = setTimeout(() => setQ(term.trim()), 400)
    return () => clearTimeout(t)
  }, [term])
  const results = useQuery({ queryKey: ['lookup', 'authors', q], queryFn: () => get<LookupAuthor[]>(`/lookup/authors?q=${encodeURIComponent(q)}`), enabled: q.length > 1 })
  return (
    <Modal title={picked ? `Add ${picked.name}` : 'Add an author'} onClose={onClose} wide>
      {picked ? (
        <AddOptions format={format} author={picked} onBack={() => setPicked(null)} onDone={onClose} />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted" aria-hidden />
            <Input autoFocus className="pl-9" placeholder="Author name, or an Open Library id like OL1394865A" value={term} onChange={(e) => setTerm(e.target.value)} />
          </div>
          {results.isFetching && <Spinner label="Looking on Open Library" />}
          <ErrorNote error={results.error} />
          {results.data?.length === 0 && <p className="text-sm text-muted">Open Library has nobody by that name.</p>}
          <ul className="flex flex-col divide-y divide-line">
            {results.data?.map((a) => (
              <li key={a.key} className="flex items-center gap-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="font-display font-semibold">{a.name}</div>
                  <p className="truncate text-sm text-muted">
                    {[lifespan(a), a.topWork && `Known for ${a.topWork}`, a.works ? `${a.works} works` : ''].filter(Boolean).join(' · ')}
                  </p>
                </div>
                {a.id ? (
                  <Link className="shrink-0 text-sm font-medium text-marquee" to={`${f.path}/${a.id}`} onClick={onClose}>
                    On the list
                  </Link>
                ) : (
                  <Button onClick={() => setPicked(a)}>Pick</Button>
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
        Add {format === 'ebook' ? 'an ebook' : 'an audiobook'} root folder under{' '}
        <Link to="/settings" className="font-medium text-marquee" onClick={onDone}>
          Settings
        </Link>{' '}
        first, so Box Office knows where the {f.nouns} go.
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
            Want every book of theirs as {format === 'ebook' ? 'an ebook' : 'an audiobook'}
            <span className="block text-sm text-muted">Off, you pick the ones you want from the author's page. Open Library lists collections and translations too, so picking is often better.</span>
          </span>
        }
      />
      <Toggle checked={fresh} onChange={setFresh} label={`Want new books as ${f.nouns} when they come out`} />
      <p className="text-sm text-muted">Box Office looks the books up on Open Library, which takes a few seconds for authors with a lot of them.</p>
      <ErrorNote error={error} />
      <div className="flex justify-between">
        <Button variant="ghost" onClick={onBack}>
          Back
        </Button>
        <Button variant="primary" busy={busy} onClick={add}>
          Add author
        </Button>
      </div>
    </div>
  )
}

// AuthorPage shows an author's books and which are wanted in one format.
export function AuthorPage({ format }: { format: BookFormat }) {
  const f = formats[format]
  const id = Number(useParams().id)
  const qc = useQueryClient()
  const nav = useNavigate()
  const author = useQuery({ queryKey: ['author', id], queryFn: () => get<AuthorDetail>(`/authors/${id}`) })
  const [removing, setRemoving] = useState(false)
  const [text, setText] = useState('')
  const set = (a: AuthorDetail) => {
    qc.setQueryData(['author', id], a)
    qc.invalidateQueries({ queryKey: ['authors'] })
  }
  const edit = useMutation({ mutationFn: (body: Partial<Pick<Author, 'newEbooks' | 'newAudiobooks'>>) => patch<AuthorDetail>(`/authors/${id}`, body), onSuccess: set })
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
    const t = text.trim().toLowerCase()
    return (author.data?.bookList ?? []).filter((b) => !t || b.title.toLowerCase().includes(t))
  }, [author.data, text])

  if (author.isPending) return <Spinner />
  if (author.isError) return <ErrorNote error={author.error} />
  const a = author.data
  const wanted = f.wanted(a)
  return (
    <div>
      <h1 className="font-display text-4xl font-bold tracking-tight">{a.name}</h1>
      <div className="mt-2 flex flex-wrap gap-2">
        <Badge tone={wanted ? 'gold' : 'plain'}>
          {wanted} {wanted === 1 ? f.noun : f.nouns} wanted
        </Badge>
        {lifespan(a) && <Badge>{lifespan(a)}</Badge>}
      </div>
      {a.bio && <p className="mt-4 line-clamp-4 max-w-3xl text-sm leading-relaxed">{a.bio}</p>}
      <p className="mt-3 text-sm text-muted">Searching for and downloading books comes in the next Box Office update.</p>
      <div className="mt-5 flex flex-wrap gap-2">
        <Button busy={refresh.isPending} onClick={() => refresh.mutate()}>
          <RefreshCw className="size-4" aria-hidden /> Refresh from Open Library
        </Button>
        <Button variant="ghost" onClick={() => setRemoving(true)}>
          <Trash2 className="size-4" aria-hidden /> Remove
        </Button>
      </div>
      <ErrorNote error={refresh.error ?? edit.error ?? all.error ?? book.error} />

      <Card className="mt-6">
        <div className="flex flex-col gap-4">
          <Toggle checked={a[f.newFlag]} disabled={edit.isPending} onChange={(on) => edit.mutate({ [f.newFlag]: on })} label={`Want new books as ${f.nouns} when they come out`} />
          <div className="flex flex-wrap gap-2">
            <Button busy={all.isPending && all.variables === true} disabled={wanted === a.books} onClick={() => all.mutate(true)}>
              Want all {a.books}
            </Button>
            <Button variant="ghost" busy={all.isPending && all.variables === false} disabled={wanted === 0} onClick={() => all.mutate(false)}>
              Want none
            </Button>
          </div>
        </div>
      </Card>

      <div className="mt-8 mb-3 flex flex-col gap-2 sm:flex-row sm:items-center">
        <h2 className="font-display text-lg font-semibold">
          Books <span className="font-normal text-muted">{a.books}</span>
        </h2>
        {a.books > 12 && <Input type="search" className="sm:ml-auto sm:w-60" placeholder="Filter by title" aria-label="Filter by title" value={text} onChange={(e) => setText(e.target.value)} />}
      </div>
      {a.books === 0 && <p className="text-sm text-muted">Open Library lists no books for {a.name} yet.</p>}
      <p className="mb-4 text-xs text-muted">The books with the most editions come first.</p>
      <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
        {shown.map((b) => {
          const on = f.wants(b)
          return (
            <li key={b.id}>
              <Cover src={b.cover} title={b.title} />
              <div className="mt-2 line-clamp-2 font-medium leading-snug" title={b.subtitle ? `${b.title}: ${b.subtitle}` : b.title}>
                {b.title}
              </div>
              <div className="mt-1 flex items-center justify-between gap-2 text-xs text-muted">
                <span>{b.year || 'No date'}</span>
                <button
                  type="button"
                  disabled={book.isPending}
                  aria-pressed={on}
                  aria-label={`${on ? 'Stop wanting' : 'Want'} ${b.title} as ${format === 'ebook' ? 'an ebook' : 'an audiobook'}`}
                  onClick={() => book.mutate({ id: b.id, wanted: !on })}
                  className={`rounded-full border px-2 py-1 sm:py-0.5 ${on ? 'border-gold text-ink' : 'border-line hover:text-ink'}`}
                >
                  {on ? 'Wanted' : 'Not wanted'}
                </button>
              </div>
            </li>
          )
        })}
      </ul>

      {removing && (
        <Modal title={`Remove ${a.name}?`} onClose={() => setRemoving(false)}>
          <p className="text-sm">Box Office stops following {a.name} for ebooks and audiobooks alike, and forgets their books. Files already in your libraries stay where they are.</p>
          <ErrorNote error={remove.error} />
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setRemoving(false)}>
              Keep
            </Button>
            <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
              Remove
            </Button>
          </div>
        </Modal>
      )}
    </div>
  )
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
