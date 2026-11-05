import { useEffect } from 'react'
import { applyLanguage, readStored } from './index'

// Each account can pick a language, which the server keeps so it follows them
// to every browser; '' follows the browser. A language picked on the sign-in
// page, before the account had one, becomes the account's.

export function LanguageSync({ saved, adopt }: { saved?: string; adopt?: (language: string) => void }) {
  useEffect(() => {
    if (saved === undefined) return
    const picked = readStored()
    if (saved === '' && picked && adopt) {
      adopt(picked)
      return
    }
    void applyLanguage(saved)
    if (saved) return
    const onChange = () => void applyLanguage('')
    window.addEventListener('languagechange', onChange)
    return () => window.removeEventListener('languagechange', onChange)
  }, [saved, adopt])
  return null
}
