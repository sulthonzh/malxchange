import { readdirSync, readFileSync } from "node:fs"
import { describe, expect, it } from 'vitest'

// Nuxt passes route params as props only to pages that opt in with
// `definePageMeta({ props: true })` — without it, `defineProps<{ id }>`
// binds undefined and the page fetches `/undefined` (spec-25 session,
// issue #64: announcement and help detail pages shipped this way; the
// page-mount unit tests pass props directly, so only this filesystem
// check can catch it).
function walkPages(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = dir + "/" + entry.name
    if (entry.isDirectory()) {
      out.push(...walkPages(path))
    } else if (entry.name.endsWith('.vue')) {
      out.push(path)
    }
  }
  return out
}

describe('param pages opt into route-params-as-props', () => {
  const pages = walkPages('src/pages')

  it('found the param pages this check exists for', () => {
    expect(pages.filter((page) => /\[\w+\]\.vue$/.test(page)).length).toBeGreaterThanOrEqual(3)
  })

  it('every page declaring defineProps carries props: true', () => {
    for (const page of pages) {
      const source = readFileSync(page, 'utf8')
      if (!source.includes('defineProps')) {
        continue
      }
      expect(source, `${page} declares defineProps but no definePageMeta({ props: true })`).toMatch(
        /props:\s*true/,
      )
    }
  })
})
