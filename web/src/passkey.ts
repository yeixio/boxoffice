// Passkeys in the browser: making one for the account that's signed in,
// and signing in with one. The server (internal/passkey) checks them.

const b64 = (b: ArrayBuffer) => {
  let s = ''
  for (const x of new Uint8Array(b)) s += String.fromCharCode(x)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

const bytes = (s: string) => {
  const raw = atob(s.replace(/-/g, '+').replace(/_/g, '/'))
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

export const encode = b64
export const decode = bytes

// supported says whether this browser can use passkeys here at all.
export const supported = () => typeof window !== 'undefined' && typeof window.PublicKeyCredential === 'function' && !!navigator.credentials

// Calls is how a page reaches its server: the admin's API or the request
// site's.
export type Calls = { post: <T>(path: string, body?: unknown) => Promise<T> }

export type Passkey = { id: number; name: string; host: string; createdAt: string; usedAt?: string }

// Why a passkey wasn't made or used, for the page to say in its own words:
// the person said no or it timed out, the device has one for this account
// already, or the browser is too old.
export class PasskeyProblem extends Error {
  constructor(public why: 'cancelled' | 'exists' | 'unsupported') {
    super(why)
  }
}

const problem = (err: unknown): unknown => {
  const name = err instanceof DOMException ? err.name : ''
  if (name === 'NotAllowedError' || name === 'AbortError') return new PasskeyProblem('cancelled')
  if (name === 'InvalidStateError') return new PasskeyProblem('exists')
  if (name === 'NotSupportedError' || name === 'SecurityError') return new PasskeyProblem('unsupported')
  return err
}

type Start = {
  challenge: string
  rp: { id: string; name: string }
  user: { id: string; name: string; displayName: string }
  algorithms: number[]
  exclude: string[]
}

// makePasskey has the browser make a passkey for the signed-in account
// and hands it to the server to keep.
export async function makePasskey(calls: Calls, name: string): Promise<Passkey> {
  if (!supported()) throw new PasskeyProblem('unsupported')
  const o = await calls.post<Start>('/passkeys/start')
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.create({
      publicKey: {
        challenge: bytes(o.challenge),
        rp: o.rp,
        user: { id: bytes(o.user.id), name: o.user.name, displayName: o.user.displayName },
        pubKeyCredParams: o.algorithms.map((alg) => ({ type: 'public-key' as const, alg })),
        // Kept on the authenticator, so signing in needs no username, and
        // only after it has checked who's using it.
        authenticatorSelection: { residentKey: 'required', requireResidentKey: true, userVerification: 'required' },
        attestation: 'none',
        excludeCredentials: o.exclude.map((id) => ({ type: 'public-key' as const, id: bytes(id) })),
        timeout: 120000,
      },
    })) as PublicKeyCredential | null
  } catch (err) {
    throw problem(err)
  }
  if (!cred) throw new PasskeyProblem('cancelled')
  const r = cred.response as AuthenticatorAttestationResponse
  // Browsers hand the key over ready to read; ones too old to don't.
  const key = typeof r.getPublicKey === 'function' ? r.getPublicKey() : null
  if (!key || typeof r.getAuthenticatorData !== 'function') throw new PasskeyProblem('unsupported')
  return calls.post<Passkey>('/passkeys', {
    id: b64(cred.rawId),
    clientDataJSON: b64(r.clientDataJSON),
    authenticatorData: b64(r.getAuthenticatorData()),
    publicKey: b64(key),
    publicKeyAlgorithm: r.getPublicKeyAlgorithm(),
    name,
  })
}

// passkeySignIn asks for a passkey and signs in with the one picked.
export async function passkeySignIn<T>(calls: Calls): Promise<T> {
  if (!supported()) throw new PasskeyProblem('unsupported')
  const o = await calls.post<{ challenge: string; rpId: string }>('/passkeys/challenge')
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.get({
      publicKey: { challenge: bytes(o.challenge), rpId: o.rpId, userVerification: 'required', timeout: 120000 },
    })) as PublicKeyCredential | null
  } catch (err) {
    throw problem(err)
  }
  if (!cred) throw new PasskeyProblem('cancelled')
  const r = cred.response as AuthenticatorAssertionResponse
  return calls.post<T>('/passkeys/login', {
    id: b64(cred.rawId),
    clientDataJSON: b64(r.clientDataJSON),
    authenticatorData: b64(r.authenticatorData),
    signature: b64(r.signature),
  })
}

// deviceName guesses what to call a passkey made here: "Chrome on Android".
export function deviceName(ua: string = typeof navigator === 'undefined' ? '' : navigator.userAgent): string {
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : ''
  const system = /iPhone/.test(ua)
    ? 'iPhone'
    : /iPad/.test(ua)
      ? 'iPad'
      : /Android/.test(ua)
        ? 'Android'
        : /Mac OS X/.test(ua)
          ? 'Mac'
          : /Windows/.test(ua)
            ? 'Windows'
            : /Linux/.test(ua)
              ? 'Linux'
              : ''
  return [browser, system].filter(Boolean).join(' · ')
}
