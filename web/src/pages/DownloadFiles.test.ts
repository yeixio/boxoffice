import { describe, expect, it } from 'vitest'
import { guess } from './DownloadFiles'

describe('guess', () => {
  it('ranks the title a release is named for first', () => {
    const name = 'The.Matrix.1999.1080p.BluRay.x264'
    expect(guess(name, 'The Matrix', 1999)).toBeGreaterThan(guess(name, 'The Matrix Reloaded', 2003))
    expect(guess(name, 'The Matrix', 1999)).toBeGreaterThanOrEqual(1.5)
    expect(guess(name, 'Heat', 1995)).toBe(0)
  })
})
