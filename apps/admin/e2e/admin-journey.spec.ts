// The admin app journey (spec #25, issue #56) over the real compose
// stack, through the Caddy edge:
//
//   1. the first super-admin walks the forced first-login rotation
//      (password → TOTP) in the browser and lands in the shell, whose
//      sidebar reflects the live permission set;
//   2. a grants-free Employee (support, whose matrix grants belong to
//      later slices) sees the sidebar drop the routes they lack, and
//      the server refuses the api surface regardless of the UI;
//   3. unknown routes answer the 404 page.
//
// Repeatability: the first Employee can only be bootstrapped once, so
// `make admin-e2e` hands the one-time password over as
// E2E_BOOTSTRAP_*; this suite rotates it in the browser and persists
// the rotated credentials in e2e/.admin-state.json (gitignored) for
// later runs.
import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'

import { bootstrapPassword, pageApi, readAdminState, totpCode, writeAdminState } from './helpers'

function suffix(): string {
  return Math.random().toString(16).slice(2, 10)
}

interface RoleView {
  id: string
  name: string
  permissions: string[]
}

function isRoleView(value: unknown): value is RoleView {
  return (
    typeof value === 'object' &&
    value !== null &&
    'id' in value &&
    'name' in value &&
    'permissions' in value &&
    Array.isArray(value.permissions)
  )
}

function permissionsOf(body: unknown): string[] {
  if (typeof body === 'object' && body !== null && 'permissions' in body && Array.isArray(body.permissions)) {
    return body.permissions
  }
  return []
}

async function loginThroughUi(
  page: Page,
  email: string,
  password: string,
  code = '',
): Promise<void> {
  await page.goto('/login')
  await page.fill('input[name="email"]', email)
  await page.fill('input[name="password"]', password)
  if (code) {
    await page.fill('input[name="code"]', code)
  }
  await page.click('button:has-text("Sign in")')
}

async function rotateThroughUi(
  page: Page,
  current: string,
  next: string,
): Promise<void> {
  await page.fill('input[name="current_password"]', current)
  await page.fill('input[name="new_password"]', next)
  await page.fill('input[name="confirm_password"]', next)
  await page.click('button:has-text("Change password")')
  await expect(page).toHaveURL(/\/login\?rotated=1/)
  await expect(page.getByText('Password changed. Sign in with your new password.')).toBeVisible()
}

/** Enrolls in the browser and returns the staged secret. */
async function enrollTotpThroughUi(page: Page): Promise<string> {
  const secret = await page.locator('code.secret').first().textContent()
  expect(secret).toBeTruthy()
  await page.fill('input[name="code"]', await totpCode(secret!.replace(/\s+/g, '')))
  await page.click('button:has-text("Confirm")')
  return secret!.replace(/\s+/g, '')
}

test('super-admin: forced first-login rotation yields the permission-derived shell', async ({ page }) => {
  const state = readAdminState()
  let email: string
  let password: string

  if (state?.totpSecret) {
    // Later runs: the rotated credentials from the first run.
    email = state.email
    password = state.password
    await loginThroughUi(page, email, password, await totpCode(state.totpSecret))
  } else {
    if (state) {
      // A earlier run rotated but did not finish enrollment — resume.
      email = state.email
      password = state.password
      await loginThroughUi(page, email, password)
    } else {
      // First run ever: the installer's one-time credential.
      email = process.env.E2E_BOOTSTRAP_EMAIL ?? ''
      password = bootstrapPassword()
      expect(email, 'no state and no E2E_BOOTSTRAP_* — run `make admin-e2e`').not.toBe('')
      await loginThroughUi(page, email, password)
      await expect(page).toHaveURL('/setup/password')
      const rotated = `E2e-rotated-${suffix()}`
      await rotateThroughUi(page, password, rotated)
      // Persist before enrollment so a crash here can resume.
      writeAdminState({ email, password: rotated, totpSecret: '' })
      password = rotated
      await loginThroughUi(page, email, password)
    }
    await expect(page).toHaveURL('/setup/totp')
    const secret = await enrollTotpThroughUi(page)
    writeAdminState({ email, password, totpSecret: secret })
  }

  // The forced rotation completed into a normal session: home.
  await expect(page).toHaveURL(/\/$/)
  await expect(page.locator('.topbar')).toContainText(email)

  // The sidebar reflects the super-admin's live permission set.
  const nav = page.locator('.sidebar-nav')
  await expect(nav).toContainText('System')
  await expect(nav.locator('a[href="/system/employees"]')).toBeVisible()
  await expect(nav.locator('a[href="/system/roles"]')).toBeVisible()
  await expect(page.locator('.home')).toContainText('view:employee')
})

test('grants-free Employee: sidebar hides ungranted routes and the server refuses them', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')

  // Staff provisioning goes through the admin-api itself — Step-up
  // included, which is the caller's fresh TOTP code. The calls run
  // inside the browser page so the edge, the host mapping, and the
  // cookie jar all apply.
  await page.goto('/login')
  const login = await pageApi(page, '/api/login', 'POST', {
    email: root!.email,
    password: root!.password,
    code: await totpCode(root!.totpSecret),
  })
  expect(login.status).toBe(200)
  expect(permissionsOf(login.body)).toContain('view:employee')

  const rolesAnswer = await pageApi(page, '/api/roles')
  expect(rolesAnswer.status).toBe(200)
  const roles = (Array.isArray(rolesAnswer.body) ? rolesAnswer.body : []).filter(isRoleView)
  // A grants-free Employee needs a Role holding no grants: the
  // journey strips the seeded support Role down to the empty set
  // (every later test provisions its own Employees, and #57's grant
  // editor journey covers the populated matrix).
  const support = roles.find((role) => role.name === 'support')
  expect(support, 'the support Role is seeded').toBeTruthy()
  for (const permission of support!.permissions) {
    const revoked = await pageApi(page, `/api/roles/${support!.id}/grants`, 'POST', {
      permission,
      granted: false,
    })
    expect(revoked.status, JSON.stringify(revoked.body)).toBe(200)
  }

  const staffEmail = `support-e2e-${suffix()}@exchange.example.com`
  const oneTime = `E2e-one-time-${suffix()}`
  const created = await pageApi(page, '/api/employees', 'POST', {
    email: staffEmail,
    password: oneTime,
    role_id: support!.id,
    code: await totpCode(root!.totpSecret),
  })
  expect(created.status, JSON.stringify(created.body)).toBe(200)

  // The fresh Employee walks the same forced rotation in the browser.
  await loginThroughUi(page, staffEmail, oneTime)
  await expect(page).toHaveURL('/setup/password')
  const staffPassword = `E2e-staff-${suffix()}`
  await rotateThroughUi(page, oneTime, staffPassword)
  await loginThroughUi(page, staffEmail, staffPassword)
  await expect(page).toHaveURL('/setup/totp')
  await enrollTotpThroughUi(page)

  // A normal session whose permission set intersects the route table
  // emptily: Home alone, no System section.
  await expect(page).toHaveURL(/\/$/)
  const nav = page.locator('.sidebar-nav')
  await expect(nav.locator('a[href="/"]')).toBeVisible()
  await expect(nav).not.toContainText('System')
  await expect(nav.locator('a[href="/system/employees"]')).toHaveCount(0)

  // The sidebar is presentation only: the server refuses the surface.
  const refused = await pageApi(page, '/api/employees')
  expect(refused.status).toBe(403)
  expect(refused.body).toEqual({ message: 'You do not have permission to do that.' })
})

test('unknown routes answer the 404 page', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await loginThroughUi(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  const response = await page.goto('/no-such-page')
  expect(response?.status()).toBe(404)
  await expect(page.getByText('The page you are looking for does not exist.')).toBeVisible()
})

// ---- issue #57: Employee management, the grant editor, audit search ----

/** The super-admin column's index in the grant editor (roles arrive name-sorted). */
async function superAdminColumnIndex(page: Page): Promise<number> {
  const headers = page.locator('.editor-table thead th')
  // The editor renders after the roles + catalog fetches land.
  await expect(headers.filter({ hasText: 'super-admin' })).toBeVisible()
  const count = await headers.count()
  for (let i = 0; i < count; i += 1) {
    if ((await headers.nth(i).innerText()).trim() === 'super-admin') {
      return i
    }
  }
  throw new Error('no super-admin column in the grant editor')
}

test('employee management from the UI: create with Step-up, reset password, disable', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await loginThroughUi(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  await expect(page).toHaveURL(/\/$/)
  await page.goto('/system/employees')
  await expect(page.locator('h1')).toHaveText('Employees')

  // The guards are inert client-side: self-disable and the sole
  // super-admin's own row refuse before any request.
  const selfRow = page.locator('.employees-table tbody tr', { hasText: root!.email })
  await expect(selfRow.locator('button:has-text("Disable")')).toBeDisabled()
  await expect(selfRow.locator('button:has-text("Change role")')).toBeDisabled()

  // Create behind the Step-up prompt: an empty code is refused with a
  // visible sentence before anything is sent.
  const email = `ui-staff-${suffix()}@exchange.example.com`
  // Closed dialogs stay in the DOM hidden — every interaction scopes
  // to the open dialog's class, never the bare .el-dialog.
  await page.click('button:has-text("Create Employee")')
  await page.fill('.dialog-create input[name="email"]', email)
  await page.fill('.dialog-create input[name="password"]', 'E2e-one-time-1')
  await page.click('.dialog-create .el-select__wrapper')
  await page.click('.el-select-dropdown__item:has-text("finance")')
  await page.click('.dialog-create button:has-text("Create")')
  await expect(page.locator('.dialog-create .dialog-error')).toContainText(
    'Enter a fresh authenticator code.',
  )
  await page.fill('.dialog-create input[name="code"]', await totpCode(root!.totpSecret))
  await page.click('.dialog-create button:has-text("Create")')
  const row = page.locator('.employees-table tbody tr', { hasText: email })
  await expect(row).toBeVisible()

  // Reset password (Step-up): the generated one-time credential shows once.
  await row.locator('button:has-text("Reset password")').click()
  await page.fill('.dialog-reset-password input[name="code"]', await totpCode(root!.totpSecret))
  await page.click('.dialog-reset-password button:has-text("Reset")')
  await expect(page.locator('.dialog-reset-password .one-time-result')).toBeVisible()
  await page.click('.dialog-reset-password button:has-text("Close")')

  // Disable the new Employee; the server's refusal for a guarded row
  // would render in the page alert (the self row above never sends).
  await row.locator('button:has-text("Disable")').click()
  await expect(row).toContainText('Disabled')

  // The audit log renders the trail with before/after JSON. The action
  // filter's response decides the table — wait for it, then assert the
  // one create row (the unfiltered table also shows this Employee's
  // reset and disable rows).
  await page.goto('/audit/log')
  await expect(page.locator('.audit-table tbody tr').first()).toBeVisible()
  await page.fill('input[name="action"]', 'create:employee')
  const [searchResponse] = await Promise.all([
    page.waitForResponse((response) => response.url().includes('action=create%3Aemployee')),
    page.click('button:has-text("Search")'),
  ])
  expect(searchResponse.status()).toBe(200)
  // Every create ever logged matches the action filter (the database
  // keeps prior runs' rows); this run's row is the one whose after view
  // carries this Employee's email.
  const createRow = page
    .locator('.audit-table tbody tr')
    .filter({ hasText: 'create:employee' })
    .filter({ hasText: email })
  await expect(createRow).toHaveCount(1)
  await expect(createRow.locator('.after-json')).toContainText(email)
  await expect(createRow.locator('.before-json')).toContainText('—')
})

test('grant editor: a revoked grant bites the live session on its next request', async ({ page }) => {
  const root = readAdminState()
  test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  await loginThroughUi(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  await expect(page).toHaveURL(/\/$/)
  await page.goto('/system/employees')
  await expect(page.locator('.employees-table')).toBeVisible()

  // Toggle view:employee off the super-admin Role in the matrix; the
  // switch persists immediately (one POST, no save step).
  await page.goto('/system/roles')
  const columnIndex = await superAdminColumnIndex(page)
  const editorRow = page.locator('.editor-table tbody tr', { hasText: 'view:employee' })
  const grantSwitch = editorRow.locator('td').nth(columnIndex).locator('.el-switch')
  await expect(grantSwitch).toHaveClass(/is-checked/)
  const [revokeResponse] = await Promise.all([
    page.waitForResponse((response) => response.url().includes('/grants') && response.request().method() === 'POST'),
    grantSwitch.click(),
  ])
  expect(revokeResponse.status()).toBe(200)
  await expect(grantSwitch).not.toHaveClass(/is-checked/)

  // The bite: the SAME live session's next request is refused, and the
  // refusal renders as a visible error — never a silent empty table.
  await page.goto('/system/employees')
  await expect(page.locator('.action-error')).toContainText('You do not have permission to do that.')
  const refused = await pageApi(page, '/api/employees')
  expect(refused.status).toBe(403)

  // Re-grant through the same switch; the next request goes through.
  await page.goto('/system/roles')
  const grantSwitchAgain = page
    .locator('.editor-table tbody tr', { hasText: 'view:employee' })
    .locator('td')
    .nth(columnIndex)
    .locator('.el-switch')
  const [grantResponse] = await Promise.all([
    page.waitForResponse((response) => response.url().includes('/grants') && response.request().method() === 'POST'),
    grantSwitchAgain.click(),
  ])
  expect(grantResponse.status()).toBe(200)
  await expect(grantSwitchAgain).toHaveClass(/is-checked/)
  await page.goto('/system/employees')
  await expect(page.locator('.employees-table')).toBeVisible()
})
