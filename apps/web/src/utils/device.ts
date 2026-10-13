// The device label a new session carries (ADR-0006: per-device sessions
// the Member can list and revoke). The label is a short noun phrase, not
// copy — it renders verbatim in the session list, so it never goes
// through sentence templates.

const BROWSERS: Array<[string, RegExp]> = [
  ['Edge', /Edg\//],
  ['Chrome', /Chrome\//],
  ['Firefox', /Firefox\//],
  // Safari reports itself as Chrome-less "Version/x … Safari".
  ['Safari', /Safari/],
]

const SYSTEMS: Array<[string, RegExp]> = [
  ['iOS', /iPhone|iPad/],
  ['Android', /Android/],
  ['Windows', /Windows/],
  ['macOS', /Macintosh|Mac OS X/],
  ['Linux', /Linux|X11/],
]

/** "Chrome on macOS" for a browser user agent; "Web browser" otherwise. */
export function deviceLabel(userAgent: string | undefined): string {
  if (userAgent === undefined || userAgent === '') {
    return 'Web browser'
  }
  const browser = BROWSERS.find(([, pattern]) => pattern.test(userAgent))?.[0]
  const system = SYSTEMS.find(([, pattern]) => pattern.test(userAgent))?.[0]
  if (browser !== undefined && system !== undefined) {
    return `${browser} on ${system}`
  }
  return browser ?? system ?? 'Web browser'
}
