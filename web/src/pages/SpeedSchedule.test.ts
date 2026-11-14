import { describe, expect, it } from 'vitest'
import { segments, toKB, toMB } from './SpeedSchedule'

describe('speed schedule', () => {
  it('splits a time past midnight across two days', () => {
    // Saturday 23:00 to 07:00 runs into Sunday, the week's first day.
    expect(segments({ days: [6], from: '23:00', to: '07:00', mode: 'full', limit: 0 })).toEqual([
      [6, 23 * 60, 24 * 60],
      [0, 0, 7 * 60],
    ])
  })

  it('reads the same From and To as all day', () => {
    expect(segments({ days: [1, 2], from: '00:00', to: '00:00', mode: 'pause', limit: 0 })).toEqual([
      [1, 0, 24 * 60],
      [2, 0, 24 * 60],
    ])
  })

  it('types MB/s and saves KB/s', () => {
    expect(toKB(5)).toBe(5120)
    expect(toMB(5120)).toBe(5)
    expect(toKB(1.5)).toBe(1536)
    expect(toKB(-1)).toBe(0)
  })
})
