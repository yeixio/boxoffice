import { describe, expect, it } from 'vitest'
import { browserCountry } from './WhereToWatch'

describe('browserCountry', () => {
  it('takes the first language that names a country', () => {
    expect(browserCountry(['en', 'en-GB', 'de-DE'])).toBe('GB')
    expect(browserCountry(['pt_br'])).toBe('BR')
    expect(browserCountry(['zh-Hant-TW'])).toBe('TW')
  })
  it('is empty when none does', () => {
    expect(browserCountry(['en', 'fr'])).toBe('')
    expect(browserCountry([])).toBe('')
  })
})
