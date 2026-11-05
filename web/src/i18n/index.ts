import i18n, { type FormatterModule, type Resource, type ResourceLanguage } from 'i18next'
import { initReactI18next } from 'react-i18next'
import { directionOf, languages, pseudoLocale, pseudoLocales, pseudoRtlLocale, sourceLanguage } from './languages'
import { pseudoLocalize, pseudoRtlLocalize } from './pseudo'

// Box Office's text comes from the catalog in i18n/locales at the repository
// root: one folder per language, one JSON file per namespace (web for the
// admin UI, request for the request site, common for both).
// English is the source and is always loaded; another language loads when
// it's picked. A key missing in a language falls back through its base
// language to English (es-MX → es → en). See i18n/README.md.

const english = import.meta.glob<Record<string, unknown>>('../../../i18n/locales/en/*.json', { eager: true, import: 'default' })
const others = import.meta.glob<Record<string, unknown>>(['../../../i18n/locales/*/*.json', '!../../../i18n/locales/en/*.json'], {
  import: 'default',
})

const fileOf = (path: string) => /locales\/([^/]+)\/([^/]+)\.json$/.exec(path)

function englishResources(): Resource {
  const en: ResourceLanguage = {}
  for (const [path, keys] of Object.entries(english)) {
    const m = fileOf(path)
    if (m) en[m[2]] = keys
  }
  return { [sourceLanguage]: en }
}

/** Languages that have a catalog, which a profile can pick. */
export const availableLanguages = [
  ...new Set([sourceLanguage, ...Object.keys(others).map((p) => fileOf(p)?.[1]).filter((l): l is string => Boolean(l))]),
]

/** Loads a language's catalog, once. */
async function load(language: string): Promise<void> {
  if (language === sourceLanguage || pseudoLocales.includes(language) || i18n.hasResourceBundle(language, 'common')) return
  const files = Object.entries(others).filter(([path]) => fileOf(path)?.[1] === language)
  await Promise.all(
    files.map(async ([path, read]) => {
      const m = fileOf(path)
      if (m) i18n.addResourceBundle(language, m[2], await read(), true, true)
    }),
  )
}

/** Where the last shown language is kept, so the next visit starts in it. */
const storedKey = 'boxoffice.language'

/** The language last picked in this browser, or '' for none. */
export function readStored(): string {
  try {
    return localStorage.getItem(storedKey) ?? ''
  } catch {
    return ''
  }
}

/** A tag's script, such as Hant for zh-TW, or '' when Intl can't tell. */
function scriptOf(tag: string): string {
  try {
    return new Intl.Locale(tag).maximize().script ?? ''
  } catch {
    return ''
  }
}

// Old or other names for listed languages: Norwegian is written in Bokmål.
const aliases: Record<string, string> = { no: 'nb', nn: 'nb', iw: 'he', in: 'id' }

/**
 * Picks the language to show, as the server does: a picked language wins;
 * otherwise the first of the browser's languages that has a catalog, matched
 * exactly, by base language (de-AT → de), by script (zh-TW → zh-Hant), or by
 * another region in the same script (pt-PT → pt-BR, never Simplified for
 * Traditional Chinese); otherwise English.
 */
export function resolveLanguage(saved: string, system: readonly string[], available: readonly string[] = availableLanguages): string {
  if (pseudoLocales.includes(saved)) return saved
  const lower = available.map((a) => a.toLowerCase())
  const match = (tag: string): string | null => {
    const [first, ...rest] = tag.split(/[-_]/)
    const language = aliases[first.toLowerCase()] ?? first.toLowerCase()
    tag = [language, ...rest].join('-')
    const exact = lower.indexOf(tag.toLowerCase())
    if (exact >= 0) return available[exact]
    const base = lower.indexOf(language)
    if (base >= 0) return available[base]
    const script = scriptOf(tag)
    const byScript = lower.indexOf(`${language}-${script.toLowerCase()}`)
    if (script && byScript >= 0) return available[byScript]
    const sibling = available.find((a) => a.split('-')[0].toLowerCase() === language && scriptOf(a) === script)
    return sibling ?? null
  }
  for (const tag of saved ? [saved, ...system] : system) {
    const found = match(tag)
    if (found) return found
  }
  return sourceLanguage
}

/** The browser's preferred languages. */
export function systemLanguages(): string[] {
  if (typeof navigator === 'undefined') return []
  if (navigator.languages?.length) return [...navigator.languages]
  return navigator.language ? [navigator.language] : []
}

/** A tag Intl can format in, or '' for one it can't. */
function canonical(tag: string): string {
  try {
    return Intl.getCanonicalLocales(tag)[0] ?? ''
  } catch {
    return ''
  }
}

// The locale dates and numbers are formatted in: the picked language, or the
// browser's first. A German browser shows 30.09.2026 even in English text.
let requested = ''

function request(saved: string) {
  requested = canonical(saved) || canonical(systemLanguages()[0] ?? '') || sourceLanguage
}
request(readStored())

/** The locale Intl formats dates, numbers and sizes in. */
export function formatLocale(): string {
  return pseudoLocales.includes(i18n.language) ? sourceLanguage : requested
}

/** Numbers in text, such as {{count}}, in the UI's number format: 1,234 or 1.234. */
const numberFormatter: FormatterModule = {
  type: 'formatter',
  init() {},
  add() {},
  addCached() {},
  format(value, _format, _lng, options) {
    if (typeof value !== 'number' || ['year', 'season', 'episode', 'version', 'port', 'id'].includes(String(options?.interpolationkey ?? ''))) return value
    return new Intl.NumberFormat(formatLocale()).format(value)
  },
}

/** "Radarr and Sonarr": a list in the page's language. */
export function formatList(items: string[]): string {
  return new Intl.ListFormat(formatLocale(), { type: 'conjunction' }).format(items)
}

/** Marks the page with its language and direction, for screen readers and right-to-left layout. */
function markDocument(language: string) {
  if (typeof document === 'undefined') return
  document.documentElement.lang = language
  document.documentElement.dir = directionOf(language)
}

void i18n
  .use(initReactI18next)
  .use(numberFormatter)
  .use({
    type: 'postProcessor',
    name: 'pseudo',
    process: (value: string) =>
      i18n.language === pseudoLocale ? pseudoLocalize(value) : i18n.language === pseudoRtlLocale ? pseudoRtlLocalize(value) : value,
  })
  .init({
    resources: englishResources(),
    lng: sourceLanguage,
    fallbackLng: sourceLanguage,
    // i18next checks a tag's base language too, so pt-BR and zh-Hans need
    // pt and zh listed or they'd fall back to English.
    supportedLngs: [...new Set([...availableLanguages, ...availableLanguages.map((code) => code.split('-')[0]), ...pseudoLocales])],
    nonExplicitSupportedLngs: true,
    defaultNS: 'web',
    ns: Object.keys(englishResources()[sourceLanguage]),
    postProcess: ['pseudo'],
    // React escapes; numbers are formatted for the locale (numberFormatter).
    interpolation: { escapeValue: false, alwaysFormat: true },
    returnNull: false,
    initAsync: false,
    // Never show a raw key: missing text falls back to English, and the
    // catalog test catches keys English lacks.
    parseMissingKeyHandler: (key) => {
      if (import.meta.env.DEV) console.warn(`Missing translation: ${key}`)
      const last = key.split(/[.:]/).pop() ?? key
      return last.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/^./, (c) => c.toUpperCase())
    },
    react: { useSuspense: false },
  })

markDocument(i18n.language)
i18n.on('languageChanged', markDocument)

/** Shows the page in a language now: a picked one, or '' for the browser's. */
export async function applyLanguage(saved: string): Promise<void> {
  const language = resolveLanguage(saved, systemLanguages())
  request(saved)
  try {
    if (saved) localStorage.setItem(storedKey, saved)
    else localStorage.removeItem(storedKey)
  } catch {
    // Storage can be unavailable; the server still keeps the account's pick.
  }
  await load(language)
  if (i18n.language !== language) await i18n.changeLanguage(language)
}

/** Resolves once the language last shown (or the browser's) is loaded, before the first render. */
export const ready: Promise<void> = applyLanguage(readStored()).catch(() => {})

/** The name of a language in itself, such as Deutsch, for the language picker. */
export function languageName(code: string): string {
  return languages.find((l) => l.code === code)?.name ?? code
}

export { directionOf, languages, pseudoLocale, pseudoRtlLocale, sourceLanguage }
export default i18n
