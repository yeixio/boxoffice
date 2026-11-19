import { useEffect, useRef, useState, type RefObject } from 'react'

// A big library has thousands of poster cards, and drawing them all at once
// makes a phone wait seconds for the page and for each letter typed in the
// filter. So a list draws its first cards, and more as the end of them nears
// the screen.

// step is how many cards are drawn at first, and added each time: a few
// screens' worth on a wide monitor.
export const step = 120

// ahead is how far below the screen the end of the cards may be before more
// are drawn, so scrolling never catches up with them.
const ahead = '1500px'

// drawn remembers how far each list was scrolled, so coming back from a title
// finds the list as it was left.
const drawn = new Map<string, { view: string; n: number }>()

// useWindowed gives the part of items to draw now. name tells one list from
// another; view is what the list is showing (its filter and sort), and the
// list starts from the top again when that changes. End goes after the cards.
export function useWindowed<T>(name: string, items: T[], view = ''): { items: T[]; end: RefObject<HTMLDivElement | null>; more: boolean } {
  // Without a way to tell what's near the screen, draw everything.
  const all = typeof IntersectionObserver === 'undefined'
  const [n, setN] = useState(() => {
    const d = drawn.get(name)
    return d && d.view === view ? d.n : step
  })
  const [was, setWas] = useState(view)
  if (was !== view) {
    setWas(view)
    setN(step)
  }
  useEffect(() => {
    drawn.set(name, { view, n })
  }, [name, view, n])
  const more = !all && items.length > n
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = end.current
    if (!el || !more) return
    // Watching afresh after each batch draws another when the end is still
    // near the screen.
    const io = new IntersectionObserver((seen) => seen.some((e) => e.isIntersecting) && setN((n) => n + step), { rootMargin: ahead })
    io.observe(el)
    return () => io.disconnect()
  }, [more, n])
  return { items: more ? items.slice(0, n) : items, end, more }
}

// End marks where the drawn cards stop; more are drawn as it nears the screen.
export function End({ list }: { list: { end: RefObject<HTMLDivElement | null>; more: boolean } }) {
  return list.more ? <div ref={list.end} className="h-px" aria-hidden /> : null
}
