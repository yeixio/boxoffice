import { describe, expect, it } from 'vitest'
import { matches } from './Downloads'

describe('download search', () => {
  it('matches every word, however the name spells its spaces', () => {
    expect(matches('Bobs.Burgers.S01E08.DVDRip.XviD-REWARD', 'bobs burgers')).toBe(true)
    expect(matches('Bobs.Burgers.S01E08.DVDRip.XviD-REWARD', 'burgers s01e08')).toBe(true)
    expect(matches('Bobs_Burgers_S01E08', 'Bobs.Burgers')).toBe(true)
    expect(matches('Bobs.Burgers.S01E08', 'reward')).toBe(false)
    expect(matches('Anything', '  ')).toBe(true)
  })
})
