import DOMPurify from 'dompurify'

// CMS bodies are stored HTML that the content contract passes through
// literally, so a detail body reaches this app as untrusted markup and
// must never reach v-html unsanitized.
//
// Library choice (ticket #50): DOMPurify, pinned at 3.4.17. It is the
// OWASP-referenced standard for HTML sanitization in the browser, has no
// dependencies, and ships its own types — unlike sanitize-html, which
// drags a server-oriented parser stack into the client bundle. A
// text-only render was rejected because the admin CMS legitimately
// publishes rich text (headings, lists, links, images), and flattening
// it would degrade every article and announcement detail.
//
// The default profile already removes scripts, event handlers, and
// javascript:/data: URLs; the defaults are deliberately not widened.
// These pages fetch client-side (onMounted), so sanitization always runs
// in a real DOM — never during SSR, where no body has been fetched.
export function sanitizeCmsHtml(html: string): string {
  return DOMPurify.sanitize(html)
}
