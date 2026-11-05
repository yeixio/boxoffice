import { describe, expect, it } from 'vitest'

// Checks the shared catalog in i18n/ at the repository root: every
// language file is valid JSON without duplicate keys, has only keys English
// has, keeps English's placeholders, and has complete plural forms; and every
// key the code uses exists in English.

// Ported from Matinee's.

// The catalog and the code, read as text, so JSON problems JSON.parse would
// hide, such as duplicate keys, can be seen.
const catalogFiles = import.meta.glob<string>('../../../i18n/locales/*/*.json', { eager: true, query: '?raw', import: 'default' })
const languagesFile = Object.values(
  import.meta.glob<string>('../../../i18n/languages.json', { eager: true, query: '?raw', import: 'default' }),
)[0]
const sourceFiles = import.meta.glob<string>(['../**/*.{ts,tsx}', '!../**/*.test.{ts,tsx}'], {
  eager: true,
  query: '?raw',
  import: 'default',
})

const source = 'en'
const pluralSuffix = /_(zero|one|two|few|many|other)$/

type Flat = Record<string, string>

function flatten(value: unknown, prefix = '', out: Flat = {}): Flat {
  if (typeof value === 'string') {
    out[prefix] = value
  } else if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const [k, v] of Object.entries(value)) flatten(v, prefix ? `${prefix}.${k}` : k, out)
  } else {
    throw new Error(`${prefix || '(root)'} must be text or an object of keys`)
  }
  return out
}

/** Keys repeated within one JSON object, which JSON.parse silently drops. */
function duplicateKeys(raw: string): string[] {
  const dups: string[] = []
  const stack: Set<string>[] = []
  const token = /"((?:[^"\\]|\\.)*)"\s*(:)?|[{}]/g
  for (const m of raw.matchAll(token)) {
    if (m[0] === '{') stack.push(new Set())
    else if (m[0] === '}') stack.pop()
    else if (m[2]) {
      const keys = stack[stack.length - 1]
      if (keys?.has(m[1])) dups.push(m[1])
      keys?.add(m[1])
    }
  }
  return dups
}

const placeholders = (text: string) => [...text.matchAll(/\{\{\s*([^},\s]+)[^}]*\}\}/g)].map((m) => m[1]).sort()
const baseKey = (key: string) => key.replace(pluralSuffix, '')

/**
 * Whether a plural category stands for one number only in this language,
 * such as Arabic's _two, so its text may spell the number out instead of
 * using {{count}}. Russian's _one also covers 21, 31 and so on, so it
 * may not.
 */
function singleNumber(language: string, category: string): boolean {
  const rules = new Intl.PluralRules(language)
  let numbers = 0
  for (let n = 0; n <= 200; n++) if (rules.select(n) === category) numbers++
  return numbers === 1
}

const fileOf = (filePath: string) => /locales\/([^/]+)\/([^/]+)\.json$/.exec(filePath)

function readLocale(language: string): Record<string, Flat> {
  const out: Record<string, Flat> = {}
  for (const [filePath, raw] of Object.entries(catalogFiles)) {
    const m = fileOf(filePath)
    if (!m || m[1] !== language) continue
    const file = `${m[2]}.json`
    let parsed: unknown
    expect(() => (parsed = JSON.parse(raw)), `${language}/${file} is not valid JSON`).not.toThrow()
    expect(duplicateKeys(raw), `${language}/${file} has duplicate keys`).toEqual([])
    out[file.replace(/\.json$/, '')] = flatten(parsed)
  }
  return out
}

/** Every way a language's files differ from English that would show wrong text. */
function compareToEnglish(language: string, en: Record<string, Flat>, translated: Record<string, Flat>): string[] {
  const problems: string[] = []
  // Every form the language uses, such as _many in Spanish for 1,000,000:
  // one that is missing would fall back to English.
  const categories = new Intl.PluralRules(language).resolvedOptions().pluralCategories
  for (const [ns, keys] of Object.entries(translated)) {
    if (!en[ns]) {
      problems.push(`${language}/${ns}.json has no English file`)
      continue
    }
    const englishBases = new Set(Object.keys(en[ns]).map(baseKey))
    for (const [key, text] of Object.entries(keys)) {
      if (!englishBases.has(baseKey(key))) {
        problems.push(`${language} ${ns}:${key} is not in English`)
        continue
      }
      const englishText = en[ns][key] ?? en[ns][`${baseKey(key)}_other`]
      const category = pluralSuffix.exec(key)?.[1]
      const own = placeholders(text)
      const expected = placeholders(englishText).filter(
        (name) => !(name === 'count' && !own.includes('count') && category && singleNumber(language, category)),
      )
      if (own.join() !== expected.join()) {
        problems.push(`${language} ${ns}:${key} has placeholders ${placeholders(text).join(',') || 'none'}, English has ${placeholders(englishText).join(',') || 'none'}`)
      }
      if (pluralSuffix.test(key)) {
        for (const category of categories) {
          if (keys[`${baseKey(key)}_${category}`] === undefined) {
            problems.push(`${language} ${ns}:${baseKey(key)} has no _${category} form`)
          }
        }
      }
    }
  }
  return [...new Set(problems)]
}

/**
 * English keys a language lacks, which show in English. A language
 * marked partial in languages.json may lack them; the others may not.
 */
function missingFromEnglish(language: string, en: Record<string, Flat>, translated: Record<string, Flat>): string[] {
  const missing: string[] = []
  for (const [ns, keys] of Object.entries(en)) {
    const have = new Set(Object.keys(translated[ns] ?? {}).map(baseKey))
    for (const base of new Set(Object.keys(keys).map(baseKey))) {
      if (!have.has(base)) missing.push(`${language} ${ns}:${base} is missing, so it shows in English`)
    }
  }
  return missing
}

const localeNames = [...new Set(Object.keys(catalogFiles).map((p) => fileOf(p)?.[1]).filter((l): l is string => Boolean(l)))]
const english = readLocale(source)

describe('the shared catalog', () => {
  it('has English, the source', () => {
    expect(localeNames).toContain(source)
    expect(Object.keys(english).length).toBeGreaterThan(0)
  })

  it('lists every language that has a catalog, with a direction and status', () => {
    const listed = JSON.parse(languagesFile) as { code: string; name: string; dir: string; status: string }[]
    expect(listed.map((l) => l.code).sort()).toEqual([...localeNames].sort())
    for (const l of listed) {
      expect(l.name, l.code).toBeTruthy()
      expect(['ltr', 'rtl'], l.code).toContain(l.dir)
      expect(['source', 'machine', 'partial', 'reviewed', 'community'], l.code).toContain(l.status)
    }
  })

  it('gives English both plural forms wherever it uses plurals', () => {
    for (const [ns, keys] of Object.entries(english)) {
      for (const key of Object.keys(keys).filter((k) => pluralSuffix.test(k))) {
        const base = baseKey(key)
        expect(keys[`${base}_one`], `${ns}:${base}_one`).toBeDefined()
        expect(keys[`${base}_other`], `${ns}:${base}_other`).toBeDefined()
      }
    }
  })

  const statuses = new Map((JSON.parse(languagesFile) as { code: string; status: string }[]).map((l) => [l.code, l.status]))
  for (const language of localeNames.filter((l) => l !== source)) {
    it(`${language} matches English`, () => {
      expect(compareToEnglish(language, english, readLocale(language))).toEqual([])
    })
    if (statuses.get(language) !== 'partial') {
      it(`${language} has every key English has`, () => {
        expect(missingFromEnglish(language, english, readLocale(language))).toEqual([])
      })
    }
  }
})

describe('the catalog checks', () => {
  const en = { common: { title: 'Models', count_one: '{{count}} model', count_other: '{{count}} models', hello: 'Hi {{name}}' } }

  it('find keys English lacks, changed placeholders, and missing plural forms', () => {
    const de = { common: { title: 'Modelle', extra: 'x', count_one: '{{anzahl}} Modell', hello: 'Hallo {{name}}' }, other: { a: 'b' } }
    expect(compareToEnglish('de', en, de)).toEqual([
      'de common:extra is not in English',
      'de common:count_one has placeholders anzahl, English has count',
      'de common:count has no _other form',
      'de/other.json has no English file',
    ])
  })

  it('find plural forms the language uses that are missing', () => {
    const es = { common: { count_one: '{{count}} modelo', count_other: '{{count}} modelos' } }
    expect(compareToEnglish('es', en, es)).toEqual(['es common:count has no _many form'])
    const ja = { common: { count_other: '{{count}} 個のモデル' } }
    expect(compareToEnglish('ja', en, ja)).toEqual([])
  })

  it('accept a partial translation and extra plural forms', () => {
    const ru = { common: { count_one: '{{count}} модель', count_few: '{{count}} модели', count_many: '{{count}} моделей', count_other: '{{count}} модели' } }
    expect(compareToEnglish('ru', en, ru)).toEqual([])
  })

  it('find English keys a language lacks, whatever its plural forms', () => {
    const de = { common: { title: 'Modelle', count_one: '{{count}} Modell', count_other: '{{count}} Modelle' } }
    expect(missingFromEnglish('de', en, de)).toEqual(['de common:hello is missing, so it shows in English'])
    const ja = { common: { title: 'モデル', count_other: '{{count}} 個', hello: 'こんにちは {{name}}' } }
    expect(missingFromEnglish('ja', en, ja)).toEqual([])
  })

  it('find duplicate keys in the same object only', () => {
    expect(duplicateKeys('{"a": "1", "b": {"a": "2"}, "a": "3"}')).toEqual(['a'])
    expect(duplicateKeys('{"a": {"x": "1"}, "b": {"x": "2"}}')).toEqual([])
  })
})

// Literal keys in the code, such as t('nav.home') or i18n.t('common:sort.title'),
// must exist in English, so nobody sees a raw key.
describe('keys used in the code', () => {
  const exists = (ns: string, key: string) => {
    const keys = english[ns]
    return Boolean(keys && (keys[key] !== undefined || keys[`${key}_one`] !== undefined || keys[`${key}_other`] !== undefined))
  }

  it('exist in English', () => {
    const missing: string[] = []
    for (const [file, code] of Object.entries(sourceFiles)) {
      const fileNs = /useTranslation\(\s*['"]([\w-]+)['"]/.exec(code)?.[1] ?? 'web'
      for (const m of code.matchAll(/['"`]((?:([\w-]+):)?([a-z][a-zA-Z0-9]*(?:\.[a-zA-Z0-9_]+)+))['"`]/g)) {
        const ns = m[2] ?? fileNs
        const key = m[3]
        const top = key.split('.')[0]
        // Only strings that start like a key of that namespace are keys.
        const isCatalogKey = english[ns] && Object.keys(english[ns]).some((k) => k.split('.')[0] === top)
        if (isCatalogKey && !exists(ns, key)) missing.push(`${file.replace(/^\.\.\//, '')}: ${ns}:${key}`)
      }
    }
    expect(missing).toEqual([])
  })

})
