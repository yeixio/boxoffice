// @vitest-environment jsdom
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { act, cleanup, render } from '@testing-library/react'
import { End, step, useWindowed } from './windowed'

// near stands in for the browser saying the end of the cards is near the
// screen.
let near: (() => void) | undefined

beforeEach(() => {
  near = undefined
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      constructor(cb: (seen: { isIntersecting: boolean }[]) => void) {
        near = () => cb([{ isIntersecting: true }])
      }
      observe() {}
      disconnect() {
        near = undefined
      }
    },
  )
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const titles = (n: number) => Array.from({ length: n }, (_, i) => i)

function List({ name, items, view }: { name: string; items: number[]; view?: string }) {
  const cards = useWindowed(name, items, view)
  return (
    <>
      <ul>
        {cards.items.map((i) => (
          <li key={i}>{i}</li>
        ))}
      </ul>
      <End list={cards} />
    </>
  )
}

const count = (el: HTMLElement) => el.querySelectorAll('li').length

test('a big list draws its first cards, then more as the end nears', () => {
  const view = render(<List name="a" items={titles(step * 2 + 30)} />)
  expect(count(view.container)).toBe(step)
  act(() => near?.())
  expect(count(view.container)).toBe(step * 2)
  act(() => near?.())
  expect(count(view.container)).toBe(step * 2 + 30)
  // All drawn: nothing is watched any more.
  expect(near).toBeUndefined()
})

test('a small list is drawn whole', () => {
  const view = render(<List name="b" items={titles(40)} />)
  expect(count(view.container)).toBe(40)
  expect(near).toBeUndefined()
})

test('a new filter or sort starts from the top; coming back keeps the place', () => {
  const items = titles(step * 3)
  const view = render(<List name="c" items={items} view="all|title" />)
  act(() => near?.())
  expect(count(view.container)).toBe(step * 2)
  view.rerender(<List name="c" items={items} view="wanted|title" />)
  expect(count(view.container)).toBe(step)
  act(() => near?.())
  view.unmount()
  // Back from a title's page with the same view: as it was left.
  const back = render(<List name="c" items={items} view="wanted|title" />)
  expect(count(back.container)).toBe(step * 2)
  back.unmount()
  // Another view was left behind: from the top.
  const other = render(<List name="c" items={items} view="all|title" />)
  expect(count(other.container)).toBe(step)
})

test('without a way to watch the screen, everything is drawn', () => {
  vi.stubGlobal('IntersectionObserver', undefined)
  const view = render(<List name="d" items={titles(step * 2)} />)
  expect(count(view.container)).toBe(step * 2)
})
