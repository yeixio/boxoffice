import { describe, expect, it } from 'vitest'
import { sortList, words, type SortOption } from './sort'

type T = { title: string; size?: number; added: string }
const list: T[] = [
  { title: 'Alien', size: 5, added: '2024-03-01T00:00:00Z' },
  { title: 'Brazil', added: '2025-01-01T00:00:00Z' },
  { title: 'casablanca', size: 9, added: '2023-01-01T00:00:00Z' },
  { title: 'Dune', size: 5, added: '2024-03-01T00:00:00Z' },
  { title: 'Alien 10', size: 1, added: '2022-01-01T00:00:00Z' },
  { title: 'Alien 2', size: 1, added: '2022-01-01T00:00:00Z' },
]
const title: SortOption<T> = { key: 'title', label: 'Title', value: (t) => t.title, dir: 'asc', says: words.text }
const size: SortOption<T> = { key: 'size', label: 'Size', value: (t) => t.size, dir: 'desc', says: words.size }
const added: SortOption<T> = { key: 'added', label: 'Added', value: (t) => t.added, dir: 'desc', says: words.date }
const names = (l: T[]) => l.map((t) => t.title)

describe('sortList', () => {
  it('sorts titles as people read them, ignoring case and counting numbers', () => {
    expect(names(sortList(list, title, 'asc'))).toEqual(['Alien', 'Alien 2', 'Alien 10', 'Brazil', 'casablanca', 'Dune'])
  })
  it('puts titles with nothing to sort on last, whichever way it runs', () => {
    expect(names(sortList(list, size, 'desc', (t) => t.title)).at(-1)).toBe('Brazil')
    expect(names(sortList(list, size, 'asc', (t) => t.title)).at(-1)).toBe('Brazil')
  })
  it('keeps ties in title order both ways', () => {
    expect(names(sortList(list, size, 'desc', (t) => t.title))).toEqual(['casablanca', 'Alien', 'Dune', 'Alien 2', 'Alien 10', 'Brazil'])
    expect(names(sortList(list, size, 'asc', (t) => t.title))).toEqual(['Alien 2', 'Alien 10', 'Alien', 'Dune', 'casablanca', 'Brazil'])
  })
  it('sorts dates newest first', () => {
    expect(names(sortList(list, added, 'desc')).slice(0, 2)).toEqual(['Brazil', 'Alien'])
  })
  it('leaves the list it was given alone', () => {
    const copy = [...list]
    sortList(list, size, 'desc')
    expect(list).toEqual(copy)
  })
})
