// Searching Settings: what someone types is matched against the words on
// each card, in whichever language the page is in, ignoring case and
// accents, so "metadonnees" finds "Métadonnées" and "PORT" finds "Port".

// fold makes text comparable: lower case, no accents or other marks.
export const fold = (s: string) => s.normalize('NFD').replace(/\p{M}/gu, '').toLocaleLowerCase()

// words splits a query into the words that must all be found.
export const words = (query: string) => fold(query).split(/\s+/).filter(Boolean)

// matches is true when every word of the query is somewhere in the text.
export function matches(text: string, query: string[]) {
  if (query.length === 0) return true
  const t = fold(text)
  return query.every((w) => t.includes(w))
}

// spans finds where each query word sits in a piece of text, as
// [start, end) offsets into the text as it is written, for highlighting.
export function spans(text: string, query: string[]): [number, number][] {
  if (query.length === 0 || !text) return []
  // Folding can change a character's length, so keep where each folded
  // character came from.
  let folded = ''
  const from: number[] = []
  for (let i = 0; i < text.length; ) {
    const cp = text.codePointAt(i)!
    const ch = String.fromCodePoint(cp)
    const f = fold(ch)
    folded += f
    for (let k = 0; k < f.length; k++) from.push(i)
    i += ch.length
  }
  from.push(text.length)
  const out: [number, number][] = []
  for (const w of query) {
    for (let at = folded.indexOf(w); at !== -1; at = folded.indexOf(w, at + w.length)) {
      const start = from[at]
      const last = from[at + w.length - 1]
      const end = last + String.fromCodePoint(text.codePointAt(last)!).length
      out.push([start, end])
    }
  }
  return out.sort((a, b) => a[0] - b[0])
}
