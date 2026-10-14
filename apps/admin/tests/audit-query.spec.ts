import { describe, expect, it } from 'vitest'

import { auditQuery } from '../src/utils/auditQuery'

// The wire contract of GET /api/audit-log: trimmed non-empty filters
// only, independent range sides as UTC ISO timestamps (RFC 3339), ''
// when nothing filters.

describe('auditQuery', () => {
  it('renders the bare path query when nothing filters', () => {
    expect(auditQuery({ actor: '', action: '', target: '', range: null })).toBe('')
    expect(auditQuery({ actor: '  ', action: '', target: '', range: null })).toBe('')
  })

  it('carries the text filters, trimmed', () => {
    expect(
      auditQuery({ actor: ' root@x.test ', action: 'create:employee', target: '', range: null }),
    ).toBe('?actor=root%40x.test&action=create%3Aemployee')
  })

  it('carries each range side independently as ISO timestamps', () => {
    const from = new Date('2026-01-02T03:04:05.000Z')
    expect(
      auditQuery({ actor: '', action: '', target: '', range: [from, null] }),
    ).toBe('?from=2026-01-02T03%3A04%3A05.000Z')
    const to = new Date('2026-01-03T03:04:05.678Z')
    expect(
      auditQuery({ actor: '', action: '', target: '', range: [null, to] }),
    ).toBe('?to=2026-01-03T03%3A04%3A05.678Z')
    expect(
      auditQuery({ actor: '', action: '', target: '', range: [from, to] }),
    ).toBe('?from=2026-01-02T03%3A04%3A05.000Z&to=2026-01-03T03%3A04%3A05.678Z')
  })
})
