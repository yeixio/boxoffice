// The en-XA pseudo-locale: every English string accented, padded
// by about 30%, and bracketed, so text that is not translated, is cut off, or
// is built from pieces stands out. Placeholders and markup stay as they are.

const accented: Record<string, string> = {
  a: 'á', b: 'ƀ', c: 'ç', d: 'ð', e: 'é', f: 'ƒ', g: 'ĝ', h: 'ĥ', i: 'í', j: 'ĵ', k: 'ķ', l: 'ļ', m: 'ɱ',
  n: 'ñ', o: 'ó', p: 'þ', q: 'ǫ', r: 'ŕ', s: 'š', t: 'ţ', u: 'ú', v: 'ṽ', w: 'ŵ', x: 'ẋ', y: 'ý', z: 'ž',
  A: 'Á', B: 'Ɓ', C: 'Ç', D: 'Ð', E: 'É', F: 'Ƒ', G: 'Ĝ', H: 'Ĥ', I: 'Í', J: 'Ĵ', K: 'Ķ', L: 'Ļ', M: 'Ṁ',
  N: 'Ñ', O: 'Ó', P: 'Þ', Q: 'Ǫ', R: 'Ŕ', S: 'Š', T: 'Ţ', U: 'Ú', V: 'Ṽ', W: 'Ŵ', X: 'Ẋ', Y: 'Ý', Z: 'Ž',
}

const vowels = new Set('aeiouAEIOU')

// {{placeholders}}, <tags>, and $t(nested) references are kept verbatim.
const protectedPart = /(\{\{[^}]*\}\}|<[^>]+>|\$t\([^)]*\))/

// Right-to-left override … pop directional formatting: the letters inside
// show in reverse order, so English reads as if written right to left.
const RLO = '\u202e'
const PDF = '\u202c'

/**
 * Turns English text into ar-XB pseudo-RTL text: each word written right to
 * left, in a page laid out right to left. Text that is not from the catalog
 * keeps reading left to right, and layout that does not mirror stands out.
 */
export function pseudoRtlLocalize(text: string): string {
  if (!text) return text
  return text
    .split(protectedPart)
    .map((part, i) => (i % 2 === 1 ? part : part.replace(/[^\s]+/g, (word) => `${RLO}${word}${PDF}`)))
    .join('')
}

/** Turns English text into pseudo-localized text. */
export function pseudoLocalize(text: string): string {
  if (!text) return text
  const parts = text.split(protectedPart)
  const body = parts
    .map((part, i) => {
      if (i % 2 === 1) return part
      let out = ''
      for (const ch of part) {
        out += accented[ch] ?? ch
        // Doubling vowels lengthens text about as much as many languages do.
        if (vowels.has(ch)) out += accented[ch]
      }
      return out
    })
    .join('')
  return `[!! ${body} !!]`
}
