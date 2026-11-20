import { describe, expect, it } from 'vitest'
import { decode, deviceName, encode } from './passkey'
import { groups, qrPath } from './pages/SignInWays'

describe('passkeys', () => {
  it('sends bytes the way the server reads them', () => {
    const b = new Uint8Array([251, 255, 190, 0, 1])
    expect(encode(b.buffer)).toBe('-_--AAE')
    expect([...decode('-_--AAE')]).toEqual([...b])
    expect([...decode(encode(new Uint8Array([7]).buffer))]).toEqual([7])
  })

  it('names a passkey after the browser it was made in', () => {
    expect(deviceName('Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36')).toBe('Chrome · Android')
    expect(deviceName('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1')).toBe('Safari · iPhone')
    expect(deviceName('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 Edg/130.0')).toBe('Edge · Windows')
    expect(deviceName('Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:132.0) Gecko/20100101 Firefox/132.0')).toBe('Firefox · Mac')
    expect(deviceName('')).toBe('')
  })
})

describe('the one-time code', () => {
  it('draws the key as a QR code', () => {
    const { d, size } = qrPath('otpauth://totp/Box%20Office:admin?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=Box%20Office')
    // A QR code is a square of 21 squares or more, 4 more a step, and
    // starts with a finder pattern: seven dark squares along the top.
    expect(size).toBeGreaterThanOrEqual(21)
    expect((size - 21) % 4).toBe(0)
    for (let x = 0; x < 7; x++) expect(d).toContain(`M${x} 0h1v1h-1z`)
    expect(d).not.toContain('M7 0h')
  })

  it('breaks the key into fours', () => {
    expect(groups('GEZDGNBVGY3TQOJQ')).toBe('GEZD GNBV GY3T QOJQ')
    expect(groups('ABCDE')).toBe('ABCD E')
  })
})
