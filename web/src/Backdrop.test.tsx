import { describe, expect, it } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { Backdrop } from './ui'

describe('Backdrop', () => {
  it('hides the image when it fails to load', () => {
    const { container } = render(<Backdrop src="/backdrop.jpg" />)
    const img = container.querySelector('img')
    expect(img).not.toBeNull()
    fireEvent.error(img!)
    expect(container.querySelector('img')).toBeNull()
  })

  it('renders nothing when there is no backdrop', () => {
    const { container } = render(<Backdrop />)
    expect(container.querySelector('img')).toBeNull()
  })

  it('renders the image when the source is valid', () => {
    const { container } = render(<Backdrop src="/ok.jpg" />)
    expect(container.querySelector('img')?.getAttribute('src')).toBe('/ok.jpg')
  })
})
