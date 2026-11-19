import { expect, test } from 'vitest'
import { monthWeeks, weekStart } from './Calendar'

const iso = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

test('a month grid is whole weeks around the month', () => {
  // November 2026 starts on a Sunday.
  const sunday = monthWeeks(2026, 10, 0)
  expect(sunday.length).toBe(35)
  expect(iso(sunday[0])).toBe('2026-11-01')
  expect(iso(sunday[34])).toBe('2026-12-05')
  const monday = monthWeeks(2026, 10, 1)
  expect(monday.length).toBe(42)
  expect(iso(monday[0])).toBe('2026-10-26')
  expect(iso(monday[41])).toBe('2026-12-06')
  // February 2027 starts on a Monday and has 28 days: four weeks exactly.
  const feb = monthWeeks(2027, 1, 1)
  expect(feb.length).toBe(28)
  expect(iso(feb[0])).toBe('2027-02-01')
  expect(iso(feb[27])).toBe('2027-02-28')
  for (const cells of [sunday, monday, feb]) expect(cells.every((d, i) => i === 0 || d.getTime() > cells[i - 1].getTime())).toBe(true)
})

test('weeks start on the day the locale says, or Monday', () => {
  expect([0, 1]).toContain(weekStart('en-US'))
  expect(weekStart('not a locale')).toBe(1)
})
