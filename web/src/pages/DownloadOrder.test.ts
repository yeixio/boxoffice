import { describe, expect, it } from 'vitest'
import { defaultOrder, move } from './DownloadOrder'

describe('download order', () => {
  it('moves a group up and down, not past the ends', () => {
    const o = ['a', 'b', 'c']
    expect(move(o, 1, -1)).toEqual(['b', 'a', 'c'])
    expect(move(o, 1, 1)).toEqual(['a', 'c', 'b'])
    expect(move(o, 0, -1)).toBe(o)
    expect(move(o, 2, 1)).toBe(o)
  })
  it('has every group once', () => {
    expect(new Set(defaultOrder).size).toBe(12)
  })
})
