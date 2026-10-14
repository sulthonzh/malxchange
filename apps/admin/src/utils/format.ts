// en-US display formatters (ADR-0013), pinned to UTC so server and
// client renders agree regardless of machine timezone (apps/web's
// utils/format.ts pattern — this app needs timestamps only).

/** Oct 13, 2026, 8:15 AM — date and 12-hour time, en-US, pinned to UTC. */
export function formatDateTime(value: string | number | Date): string {
  return new Intl.DateTimeFormat('en-US', {
    timeZone: 'UTC',
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(new Date(value))
}
