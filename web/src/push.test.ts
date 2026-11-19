import { expect, test } from 'vitest'
import { blocker, keyBytes, onApple } from './push'

const fine = { secure: true, worker: true, push: true, notification: true, permission: 'default', apple: false, installed: false }

test('says the first thing in the way of notifications', () => {
  expect(blocker(fine)).toBeNull()
  expect(blocker({ ...fine, permission: 'granted' })).toBeNull()
  // Opened over http: nothing else matters until that's fixed.
  expect(blocker({ ...fine, secure: false, worker: false, push: false })).toBe('insecure')
  // An iPhone's Safari tab has no push until the site is on the Home Screen.
  expect(blocker({ ...fine, apple: true, push: false })).toBe('apple')
  expect(blocker({ ...fine, apple: true, installed: true })).toBeNull()
  // Installed on an iPhone too old for it.
  expect(blocker({ ...fine, apple: true, installed: true, push: false })).toBe('unsupported')
  expect(blocker({ ...fine, notification: false })).toBe('unsupported')
  expect(blocker({ ...fine, permission: 'denied' })).toBe('blocked')
})

test('tells an iPhone and an iPad from a Mac', () => {
  expect(onApple({ userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X)', platform: 'iPhone', maxTouchPoints: 5 })).toBe(true)
  // An iPad says it's a Mac, but has a touch screen.
  expect(onApple({ userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', platform: 'MacIntel', maxTouchPoints: 5 })).toBe(true)
  expect(onApple({ userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', platform: 'MacIntel', maxTouchPoints: 0 })).toBe(false)
  expect(onApple({ userAgent: 'Mozilla/5.0 (Linux; Android 14; Pixel 8)', platform: 'Linux armv81', maxTouchPoints: 5 })).toBe(false)
})

test("reads the server's key as browsers want it", () => {
  // base64url, as the server gives it: - and _ and no padding.
  expect([...keyBytes('BP-_AQ')]).toEqual([4, 255, 191, 1])
  expect(keyBytes('BGlRBNmTR8iGoByteejtUtkIkgbycsqPKixpNOnHvBqVN8A_a0k4Yt282YQXx5yxDLK1uHMIcEAUHQ96iky-mAs')).toHaveLength(65)
})
