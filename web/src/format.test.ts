import { describe, expect, it } from 'vitest'
import { bytes, episodeCode, relativeDay } from './format'

describe('format', () => {
  it('sizes', () => {
    expect(bytes(0)).toBe('0 B')
    expect(bytes(1536)).toBe('1.5 KB')
    expect(bytes(5 * 1024 ** 3)).toBe('5.0 GB')
  })
  it('episode codes', () => {
    expect(episodeCode(1, 2)).toBe('S01E02')
  })
  it('relative days', () => {
    const now = new Date(2026, 10, 4, 12)
    expect(relativeDay(new Date(2026, 10, 4, 20).toISOString(), now)).toBe('Today')
    expect(relativeDay(new Date(2026, 10, 5, 1).toISOString(), now)).toBe('Tomorrow')
  })
})
