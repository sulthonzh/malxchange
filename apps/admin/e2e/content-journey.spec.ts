// The deferred content proof (ticket #63, discharged by #64): content
// the back office manages is what members actually see. A support
// Employee publishes an announcement and a help article over the
// admin surface; the member web app — through the member edge, in a
// real browser — lists and serves the announcement, the member HTTP
// surface serves the help article, and a draft stays invisible to
// members (published rows only). Every mutation leaves its audit row.
import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'

import {
  auditRows,
  loginHttp,
  MEMBER_API,
  pageApi,
  provisionEmployee,
  readAdminState,
  roleIdOf,
  totpCode,
  seatPage,
} from './helpers'

interface AnnouncementRow {
  id: string
  title: string
}

function isAnnouncementRow(value: unknown): value is AnnouncementRow {
  if (typeof value !== 'object' || value === null || !('id' in value) || !('title' in value)) {
    return false
  }
  return typeof value.id === 'string' && typeof value.title === 'string'
}

async function createAnnouncement(
  page: Page,
  body: { title: string; content: string; published: boolean },
): Promise<string> {
  const answer = await pageApi(page, '/api/announcements', 'POST', body)
  expect(answer.status, JSON.stringify(answer.body)).toBe(200)
  if (!isAnnouncementRow(answer.body)) {
    throw new Error('announcement create served no row')
  }
  return answer.body.id
}

test('admin-managed content reaches members through the member edge', async ({ page, request }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await seatPage(page)

  const support = await provisionEmployee(page, root!, 'support')
  // Support's session owns the content mutations below; the admin-
  // journey's grant-strip test revoked support's matrix grants on a
  // shared stack, so re-grant the content strings idempotently.
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const supportRole = await roleIdOf(page, 'support')
  for (const permission of ['view:announcement', 'create:announcement', 'edit:announcement', 'view:help-category', 'create:help-category', 'view:help-article', 'create:help-article']) {
    const granted = await pageApi(page, `/api/roles/${supportRole}/grants`, 'POST', { permission, granted: true })
    expect(granted.status, permission).toBe(200)
  }
  await loginHttp(page, support.email, support.password, await totpCode(support.totpSecret))
  const marker = String(Date.now())

  // Support owns content (ADR-0012): a published announcement and a
  // help center article, plus a draft that must never reach members.
  const publishedTitle = `E2E announcement ${marker}`
  const publishedID = await createAnnouncement(page, {
    title: publishedTitle,
    content: `<p>Served by the back office ${marker}</p>`,
    published: true,
  })
  const draftTitle = `E2E draft ${marker}`
  await createAnnouncement(page, {
    title: draftTitle,
    content: '<p>Members never see drafts</p>',
    published: false,
  })

  const category = await pageApi(page, '/api/help/categories', 'POST', {
    title: `E2E category ${marker}`,
  })
  expect(category.status, JSON.stringify(category.body)).toBe(200)
  const categoryBody = category.body as { id?: unknown }
  if (typeof categoryBody.id !== 'string') {
    throw new Error('help category create served no id')
  }
  const article = await pageApi(page, '/api/help/articles', 'POST', {
    title: `E2E article ${marker}`,
    content: `<p>Help from the back office ${marker}</p>`,
    published: true,
    category_id: categoryBody.id,
  })
  expect(article.status, JSON.stringify(article.body)).toBe(200)

  await pageApi(page, '/api/logout', 'POST')
  // Support cannot search the audit log (ADR-0012: super-admin,
  // finance, compliance only) — the super-admin re-authenticates for
  // the audit assertions.
  await loginHttp(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const announceAudit = await auditRows(page, { actor: support.email, action: 'create:announcement' })
  expect(announceAudit.some((row) => row.target === publishedID)).toBe(true)
  const articleAudit = await auditRows(page, { actor: support.email, action: 'create:help-article' })
  expect(articleAudit.length).toBeGreaterThanOrEqual(1)

  // The member web app, through the member edge in a real browser:
  // the notice page lists the published announcement and the detail
  // page serves its body. The draft never appears.
  await page.goto('https://exchange.example.com/notice')
  const listed = page.locator('.list .item', { hasText: publishedTitle })
  await expect(listed).toBeVisible()
  expect(await page.locator('.list .item', { hasText: draftTitle }).count()).toBe(0)
  await listed.locator('span.text').click()
  await expect(page).toHaveURL(new RegExp(`/announcement/${publishedID}$`))
  await expect(page.locator('.content-wrap .content')).toContainText(
    `Served by the back office ${marker}`,
  )

  // The member HTTP surface serves the published help article — the
  // help center api's members read.
  const help = await request.get(`${MEMBER_API}/help`)
  expect(help.status()).toBe(200)
  const helpText = await help.text()
  expect(helpText).toContain(`E2E article ${marker}`)
})
