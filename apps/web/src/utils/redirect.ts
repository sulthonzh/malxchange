// Where a successful sign-in lands. The login and register flows accept a
// `redirect` query parameter written by the `member` route middleware, but
// only a path inside this site is ever honored: an absolute URL or a
// protocol-relative `//host` value would turn the parameter into an
// open redirect to somebody else's site.

/** A same-origin site path, or `fallback` when the value is not one. */
export function safeRedirectTarget(value: unknown, fallback = '/exchange'): string {
  if (typeof value !== 'string' || value === '') {
    return fallback
  }
  if (!value.startsWith('/')) {
    return fallback
  }
  // `//host` and `/\host` are browser shorthand for another origin.
  if (value.startsWith('//') || value.startsWith('/\\')) {
    return fallback
  }
  return value
}
