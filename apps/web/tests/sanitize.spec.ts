// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'

import { sanitizeCmsHtml } from '../src/utils/sanitize'

// The content contract passes CMS-stored HTML through literally, so the
// render path is: sanitize, then v-html. These expectations pin the
// security behavior (what must die) and the publishing behavior (what
// the admin CMS legitimately writes and members must still see).

describe('sanitizeCmsHtml', () => {
  it('strips script tags and inline event handlers', () => {
    expect(
      sanitizeCmsHtml('<p>ok</p><script>alert("x")</script>'),
    ).toBe('<p>ok</p>')
    expect(sanitizeCmsHtml('<img src="x.png" onerror="alert(1)">')).toBe(
      '<img src="x.png">',
    )
  })

  it('strips javascript: URLs but keeps ordinary links', () => {
    expect(sanitizeCmsHtml('<a href="javascript:alert(1)">bad</a>')).toBe(
      '<a>bad</a>',
    )
    expect(
      sanitizeCmsHtml('<a href="https://example.com/a">guide</a>'),
    ).toBe('<a href="https://example.com/a">guide</a>')
  })

  it('keeps the rich text the CMS publishes', () => {
    const html =
      '<h2>Maintenance window</h2><p>Read-only mode <strong>Sunday</strong>.</p>' +
      '<ul><li>Deposits pause</li><li>Withdrawals pause</li></ul>' +
      '<p><em>See</em> <a href="/notice">announcements</a>.</p>' +
      '<img src="https://example.com/diagram.png" alt="deposit flow">'
    expect(sanitizeCmsHtml(html)).toBe(html)
  })

  it('returns plain text untouched', () => {
    expect(sanitizeCmsHtml('Deposits credit after confirmations.')).toBe(
      'Deposits credit after confirmations.',
    )
  })
})
