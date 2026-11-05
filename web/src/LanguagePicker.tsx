import { useTranslation } from 'react-i18next'
import { Languages } from 'lucide-react'
import i18n, { applyLanguage, availableLanguages, languageName, languages, pseudoLocale, pseudoRtlLocale, readStored, resolveLanguage, systemLanguages } from './i18n'

// The languages to offer: those with a catalog, plus the pseudo-languages
// for checking layouts while developing.
function choices() {
  const shown = languages.filter((l) => availableLanguages.includes(l.code))
  const pseudo = import.meta.env.DEV ? [pseudoLocale, pseudoRtlLocale] : []
  return { shown, pseudo }
}

// LanguagePicker picks the language for an account, which follows it to every
// browser, or leaves it to each browser ('').
export function LanguagePicker({ saved, onPick, busy, id }: { saved?: string; onPick: (code: string) => void; busy?: boolean; id?: string }) {
  const { t } = useTranslation('common')
  const { shown, pseudo } = choices()
  const browser = resolveLanguage('', systemLanguages())
  const current = shown.find((l) => l.code === i18n.language)
  const value = pseudo.includes(i18n.language) ? i18n.language : (saved ?? '')
  const change = (code: string) => {
    // Pseudo-languages stay in this browser.
    if (pseudo.includes(code)) void applyLanguage(code)
    else onPick(code)
  }
  return (
    <div className="flex flex-col gap-1.5">
      <select
        id={id}
        className="w-full rounded-md border border-line bg-paper px-3 py-2 text-sm"
        value={value}
        disabled={saved === undefined || busy}
        onChange={(e) => change(e.target.value)}
      >
        <option value="">{t('language.automatic', { language: languageName(browser) })}</option>
        {shown.map((l) => (
          <option key={l.code} value={l.code} lang={l.code}>
            {l.name}
          </option>
        ))}
        {pseudo.map((code) => (
          <option key={code} value={code}>
            {code}
          </option>
        ))}
      </select>
      {current?.status === 'machine' && (
        <p className="text-xs text-muted">
          {t('language.machine')}{' '}
          <a href="https://github.com/yeixio/boxoffice/blob/main/i18n/README.md" target="_blank" rel="noreferrer" className="underline hover:text-ink">
            {t('language.help')}
          </a>
        </p>
      )}
    </div>
  )
}

// LanguageMenu is the small picker on a sign-in page, before there's an
// account to keep the choice: it's kept in this browser, and becomes the
// account's language on signing in.
export function LanguageMenu() {
  const { t } = useTranslation('common')
  const { shown, pseudo } = choices()
  const browser = resolveLanguage('', systemLanguages())
  const value = pseudo.includes(i18n.language) ? i18n.language : readStored()
  return (
    <label className="inline-flex items-center gap-2 text-sm text-muted">
      <Languages className="size-4" aria-hidden />
      <span className="sr-only">{t('language.label')}</span>
      <select className="rounded-md border border-line bg-paper px-2 py-1 text-sm" value={value} onChange={(e) => void applyLanguage(e.target.value)}>
        <option value="">{t('language.automatic', { language: languageName(browser) })}</option>
        {shown.map((l) => (
          <option key={l.code} value={l.code} lang={l.code}>
            {l.name}
          </option>
        ))}
        {pseudo.map((code) => (
          <option key={code} value={code}>
            {code}
          </option>
        ))}
      </select>
    </label>
  )
}
