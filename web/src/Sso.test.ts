import { describe, expect, it } from 'vitest'
import { outcome } from './Sso'
import { lines } from './pages/Sso'

describe('single sign-on', () => {
  it('reads how a trip to the provider ended', () => {
    expect(outcome('?sso=refused')).toBe('refused')
    expect(outcome('?a=1&sso=notallowed')).toBe('notallowed')
    expect(outcome('?sso=failed')).toBe('failed')
    expect(outcome('?sso=anything-else')).toBeNull()
    expect(outcome('')).toBeNull()
  })

  it('reads one account on each line', () => {
    expect(lines(' mike@example.com \n\nmike, sam\n')).toEqual(['mike@example.com', 'mike', 'sam'])
    expect(lines('')).toEqual([])
  })
})
