// The languages Box Office can show, from the shared catalog in i18n/ at the
// repository root (see i18n/README.md).
import listed from '../../../i18n/languages.json'

export type TranslationStatus = 'source' | 'machine' | 'partial' | 'reviewed' | 'community'

export interface Language {
  /** BCP 47 tag, such as en or pt-BR. */
  code: string
  /** The language's name in itself, such as Español. */
  name: string
  dir: 'ltr' | 'rtl'
  status: TranslationStatus
}

export const languages = listed as Language[]

/** The source language, and the last fallback. */
export const sourceLanguage = 'en'

/** Generated from English to find text that isn't translated or doesn't fit. */
export const pseudoLocale = 'en-XA'

/**
 * Generated from English and written right to left, to find layout that
 * doesn't mirror: Arabic's direction with English words reversed, as
 * Android's ar-XB does.
 */
export const pseudoRtlLocale = 'ar-XB'

export const pseudoLocales: readonly string[] = [pseudoLocale, pseudoRtlLocale]

// Scripts written right to left, so a language not listed still gets the
// right direction.
const rtlLanguages = new Set(['ar', 'ckb', 'dv', 'fa', 'he', 'iw', 'ks', 'ku', 'ps', 'sd', 'ug', 'ur', 'yi'])

/** The text direction of a language tag. */
export function directionOf(tag: string): 'ltr' | 'rtl' {
  if (tag === pseudoRtlLocale) return 'rtl'
  const listedLanguage = languages.find((l) => l.code.toLowerCase() === tag.toLowerCase())
  if (listedLanguage) return listedLanguage.dir
  return rtlLanguages.has(tag.split('-')[0].toLowerCase()) ? 'rtl' : 'ltr'
}
