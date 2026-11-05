// Sorting for Box Office's lists: the options each list offers, the
// control that picks one, and remembering the pick in this browser.
import { useId, useState } from 'react'
import { ArrowDownWideNarrow, ArrowUpDown, ArrowUpNarrowWide, ChevronDown } from 'lucide-react'

export type Dir = 'asc' | 'desc'
type Value = string | number | undefined | null

// How each kind of value says which way it's going, in words: "Newest
// first" says more than an arrow does.
export const words = {
  text: { asc: 'A to Z', desc: 'Z to A' },
  date: { asc: 'Oldest first', desc: 'Newest first' },
  soon: { asc: 'Soonest first', desc: 'Latest first' },
  size: { asc: 'Smallest first', desc: 'Largest first' },
  rating: { asc: 'Lowest first', desc: 'Highest first' },
  quality: { asc: 'Worst first', desc: 'Best first' },
  length: { asc: 'Shortest first', desc: 'Longest first' },
  count: { asc: 'Fewest first', desc: 'Most first' },
  percent: { asc: 'Least first', desc: 'Most first' },
} satisfies Record<string, Record<Dir, string>>

export type SortOption<T> = {
  key: string
  label: string
  // What to sort on. Empty values (undefined, null, '') always go last,
  // whichever way the list runs: a movie with no file has no size.
  value: (t: T) => Value
  // Which way it runs at first: newest, largest and best first; A to Z.
  dir: Dir
  // What each direction is called; a word set above, or the list's own.
  says: Record<Dir, string>
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

function compare(a: Value, b: Value): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b
  return collator.compare(String(a), String(b))
}

const empty = (v: Value) => v === undefined || v === null || v === '' || (typeof v === 'number' && Number.isNaN(v))

// sortList sorts a copy of list. Ties keep the order of tiebreak (usually
// the title), whichever way the list runs, so equal values stay readable.
export function sortList<T>(list: readonly T[], option: SortOption<T>, dir: Dir, tiebreak?: (t: T) => Value): T[] {
  const sign = dir === 'asc' ? 1 : -1
  const keyed = list.map((t, i) => ({ t, i, v: option.value(t), tie: tiebreak?.(t) }))
  keyed.sort((x, y) => {
    const xe = empty(x.v)
    const ye = empty(y.v)
    if (xe !== ye) return xe ? 1 : -1
    const c = xe ? 0 : sign * compare(x.v, y.v)
    if (c !== 0) return c
    if (tiebreak) {
      const d = compare(x.tie ?? '', y.tie ?? '')
      if (d !== 0) return d
    }
    return x.i - y.i
  })
  return keyed.map((k) => k.t)
}

type Saved = { key: string; dir: Dir }

function load(list: string): Saved | null {
  try {
    const raw = localStorage.getItem(`boxoffice.sort.${list}`)
    return raw ? (JSON.parse(raw) as Saved) : null
  } catch {
    return null
  }
}

function save(list: string, s: Saved) {
  try {
    localStorage.setItem(`boxoffice.sort.${list}`, JSON.stringify(s))
  } catch {
    // A private window can refuse; the sort still works until a reload.
  }
}

// useSort picks a list's sort, remembering it in this browser. A saved
// option the list no longer has falls back to the first one.
export function useSort<T>(list: string, options: SortOption<T>[]) {
  const [saved, setSaved] = useState<Saved | null>(() => load(list))
  const option = options.find((o) => o.key === saved?.key) ?? options[0]
  const dir: Dir = saved?.key === option.key && (saved.dir === 'asc' || saved.dir === 'desc') ? saved.dir : option.dir
  const set = (s: Saved) => {
    setSaved(s)
    save(list, s)
  }
  return {
    option,
    dir,
    // Picking another option starts it the way it reads best.
    pick: (key: string) => {
      const o = options.find((x) => x.key === key) ?? options[0]
      set({ key: o.key, dir: o.dir })
    },
    flip: () => set({ key: option.key, dir: dir === 'asc' ? 'desc' : 'asc' }),
    options,
  }
}

// SortControl is a native select, so a phone opens its own picker, and a
// button that says which way the list runs and turns it round.
export function SortControl<T>({ sort, className = '' }: { sort: ReturnType<typeof useSort<T>>; className?: string }) {
  const id = useId()
  const { option, dir } = sort
  const other: Dir = dir === 'asc' ? 'desc' : 'asc'
  const Arrow = dir === 'asc' ? ArrowUpNarrowWide : ArrowDownWideNarrow
  return (
    <div className={`flex min-w-0 items-center gap-1.5 ${className}`}>
      <label htmlFor={id} className="sr-only">
        Sort by
      </label>
      <div className="relative min-w-0 flex-1 sm:flex-none">
        <ArrowUpDown className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted" aria-hidden />
        <select
          id={id}
          value={option.key}
          onChange={(e) => sort.pick(e.target.value)}
          className="h-10 w-full min-w-0 cursor-pointer appearance-none truncate rounded-full border border-line bg-card pl-9 pr-8 text-sm font-medium hover:border-ink/40 sm:h-9"
        >
          {sort.options.map((o) => (
            <option key={o.key} value={o.key}>
              {o.label}
            </option>
          ))}
        </select>
        <ChevronDown className="pointer-events-none absolute right-2.5 top-1/2 size-4 -translate-y-1/2 text-muted" aria-hidden />
      </div>
      <button
        type="button"
        onClick={sort.flip}
        title={`Show ${option.says[other].toLowerCase()}`}
        aria-label={`${option.label}, ${option.says[dir].toLowerCase()}. Show ${option.says[other].toLowerCase()} instead`}
        className="flex h-10 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border border-line bg-card px-3 text-sm hover:border-ink/40 sm:h-9"
      >
        <Arrow className="size-4" aria-hidden />
        {option.says[dir]}
      </button>
    </div>
  )
}
