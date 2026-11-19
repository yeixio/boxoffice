// @vitest-environment jsdom
import { afterEach, expect, test, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import './i18n'
import { PageBoundary } from './Broken'
import { Toggle } from './ui'

afterEach(cleanup)

function Bad(): never {
  throw new Error('no such field')
}

// A page that breaks shows what went wrong in its own place, leaves what's
// around it alone, and is forgotten on moving to another page.
test('a broken page is caught, and cleared by moving on', () => {
  const quiet = vi.spyOn(console, 'error').mockImplementation(() => {})
  const view = render(
    <div>
      <nav>menu</nav>
      <PageBoundary at="/movies">
        <Bad />
      </PageBoundary>
    </div>,
  )
  expect(screen.getByRole('alert').textContent).toContain('Error: no such field')
  expect(screen.getByRole('button', { name: 'Reload the page' })).toBeTruthy()
  expect(screen.getByText('menu')).toBeTruthy()
  view.rerender(
    <div>
      <nav>menu</nav>
      <PageBoundary at="/shows">
        <p>shows</p>
      </PageBoundary>
    </div>,
  )
  expect(screen.queryByRole('alert')).toBeNull()
  expect(screen.getByText('shows')).toBeTruthy()
  quiet.mockRestore()
})

test('a switch is named by its text', () => {
  render(<Toggle checked onChange={() => {}} label="Watch for it" />)
  expect(screen.getByRole('switch', { name: 'Watch for it' }).getAttribute('aria-checked')).toBe('true')
})
