// The audit page's search query builder: the wire contract of
// GET /api/audit-log (internal/admin/auditsearch.go). Filters are
// trimmed; empty ones select everything; the range sides are
// independent and render as UTC ISO timestamps — the RFC 3339 the api
// demands. Returns '' when nothing filters, so the page calls the
// bare path.

export interface AuditFilters {
  actor: string
  action: string
  target: string
  /** [from, to]; either side may be missing while the picker is half-set. */
  range: [Date | null, Date | null] | null
}

export function auditQuery(filters: AuditFilters): string {
  const params = new URLSearchParams()
  const actor = filters.actor.trim()
  if (actor !== '') {
    params.set('actor', actor)
  }
  const action = filters.action.trim()
  if (action !== '') {
    params.set('action', action)
  }
  const target = filters.target.trim()
  if (target !== '') {
    params.set('target', target)
  }
  if (filters.range) {
    const [from, to] = filters.range
    if (from !== null) {
      params.set('from', from.toISOString())
    }
    if (to !== null) {
      params.set('to', to.toISOString())
    }
  }
  const query = params.toString()
  return query === '' ? '' : `?${query}`
}
