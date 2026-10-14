# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: admin-journey.spec.ts >> grant editor: a revoked grant bites the live session on its next request
- Location: e2e/admin-journey.spec.ts:280:1

# Error details

```
Error: no super-admin column in the grant editor
```

# Page snapshot

```yaml
- generic [ref=f2e3]:
  - complementary [ref=f2e4]:
    - paragraph [ref=f2e5]: malxchangeadmin
    - navigation [ref=f2e6]:
      - link "Home" [ref=f2e7] [cursor=pointer]:
        - /url: /
      - generic [ref=f2e8]:
        - paragraph [ref=f2e9]: System
        - link "Employees" [ref=f2e10] [cursor=pointer]:
          - /url: /system/employees
        - link "Roles" [ref=f2e11] [cursor=pointer]:
          - /url: /system/roles
      - generic [ref=f2e12]:
        - paragraph [ref=f2e13]: Audit
        - link "Audit log" [ref=f2e14] [cursor=pointer]:
          - /url: /audit/log
      - generic [ref=f2e15]:
        - paragraph [ref=f2e16]: Config
        - link "Code channels" [ref=f2e17] [cursor=pointer]:
          - /url: /config/channels
  - generic [ref=f2e18]:
    - banner [ref=f2e19]:
      - generic [ref=f2e20]:
        - text: admin@exchange.example.com
        - generic [ref=f2e21]: super-admin
      - button "Sign out" [ref=f2e23] [cursor=pointer]
    - main [ref=f2e25]:
      - generic [ref=f2e26]:
        - generic [ref=f2e27]:
          - heading "Roles" [level=1] [ref=f2e28]
          - paragraph [ref=f2e29]: Every Employee holds exactly one Role; a Role's grants decide what its holders may do. Seeded Roles and Roles held by an Employee cannot be deleted — their grants stay editable.
        - generic [ref=f2e31]:
          - table [ref=f2e33]:
            - rowgroup [ref=f2e39]:
              - row [ref=f2e40]:
                - columnheader "Role" [ref=f2e41]
                - columnheader "Flags" [ref=f2e43]
                - columnheader "Grants" [ref=f2e45]
                - columnheader "Actions" [ref=f2e47]
          - table [ref=f2e53]:
            - rowgroup [ref=f2e59]:
              - row [ref=f2e60]:
                - cell "compliance" [ref=f2e61]
                - cell "Seeded" [ref=f2e63]
                - cell "1" [ref=f2e67]
                - cell [ref=f2e69]:
                  - button "Delete" [disabled] [ref=f2e72]
              - row [ref=f2e74]:
                - cell "finance" [ref=f2e75]
                - cell "Seeded In use" [ref=f2e77]:
                  - generic [ref=f2e78]:
                    - generic [ref=f2e79]: Seeded
                    - generic [ref=f2e81]: In use
                - cell "1" [ref=f2e83]
                - cell [ref=f2e85]:
                  - button "Delete" [disabled] [ref=f2e88]
              - row [ref=f2e90]:
                - cell "super-admin" [ref=f2e91]
                - cell "Seeded In use" [ref=f2e93]:
                  - generic [ref=f2e94]:
                    - generic [ref=f2e95]: Seeded
                    - generic [ref=f2e97]: In use
                - cell "15" [ref=f2e99]
                - cell [ref=f2e101]:
                  - button "Delete" [disabled] [ref=f2e104]
              - row [ref=f2e106]:
                - cell "support" [ref=f2e107]
                - cell "Seeded In use" [ref=f2e109]:
                  - generic [ref=f2e110]:
                    - generic [ref=f2e111]: Seeded
                    - generic [ref=f2e113]: In use
                - cell "0" [ref=f2e115]
                - cell [ref=f2e117]:
                  - button "Delete" [disabled] [ref=f2e120]
        - heading "Grant editor" [level=2] [ref=f2e122]
        - paragraph [ref=f2e123]: The rows are the server's permission catalog — display-only; strings exist because a handler checks them, never because the UI invented one. Toggling a grant persists immediately and bites on every holder's next request, mid-session.
        - generic [ref=f2e125]:
          - table [ref=f2e127]:
            - rowgroup [ref=f2e134]:
              - row [ref=f2e135]:
                - columnheader "Permission" [ref=f2e136]
                - columnheader "compliance" [ref=f2e138]
                - columnheader "finance" [ref=f2e140]
                - columnheader "super-admin" [ref=f2e142]
                - columnheader "support" [ref=f2e144]
          - table [ref=f2e150]:
            - rowgroup [ref=f2e157]:
              - row [ref=f2e158]:
                - cell [ref=f2e159]:
                  - code [ref=f2e161]: activate:channel
                - cell [ref=f2e162]:
                  - generic [ref=f2e164]:
                    - switch
                    - generic [ref=f2e165] [cursor=pointer]
                - cell [ref=f2e167]:
                  - generic [ref=f2e169]:
                    - switch
                    - generic [ref=f2e170] [cursor=pointer]
                - cell [ref=f2e172]:
                  - generic [ref=f2e174]:
                    - switch [checked]
                    - generic [ref=f2e175] [cursor=pointer]
                - cell [ref=f2e177]:
                  - generic [ref=f2e179]:
                    - switch
                    - generic [ref=f2e180] [cursor=pointer]
              - row [ref=f2e182]:
                - cell [ref=f2e183]:
                  - code [ref=f2e185]: assign:employee-role
                - cell [ref=f2e186]:
                  - generic [ref=f2e188]:
                    - switch
                    - generic [ref=f2e189] [cursor=pointer]
                - cell [ref=f2e191]:
                  - generic [ref=f2e193]:
                    - switch
                    - generic [ref=f2e194] [cursor=pointer]
                - cell [ref=f2e196]:
                  - generic [ref=f2e198]:
                    - switch [checked]
                    - generic [ref=f2e199] [cursor=pointer]
                - cell [ref=f2e201]:
                  - generic [ref=f2e203]:
                    - switch
                    - generic [ref=f2e204] [cursor=pointer]
              - row [ref=f2e206]:
                - cell [ref=f2e207]:
                  - code [ref=f2e209]: create:employee
                - cell [ref=f2e210]:
                  - generic [ref=f2e212]:
                    - switch
                    - generic [ref=f2e213] [cursor=pointer]
                - cell [ref=f2e215]:
                  - generic [ref=f2e217]:
                    - switch
                    - generic [ref=f2e218] [cursor=pointer]
                - cell [ref=f2e220]:
                  - generic [ref=f2e222]:
                    - switch [checked]
                    - generic [ref=f2e223] [cursor=pointer]
                - cell [ref=f2e225]:
                  - generic [ref=f2e227]:
                    - switch
                    - generic [ref=f2e228] [cursor=pointer]
              - row [ref=f2e230]:
                - cell [ref=f2e231]:
                  - code [ref=f2e233]: deactivate:channel
                - cell [ref=f2e234]:
                  - generic [ref=f2e236]:
                    - switch
                    - generic [ref=f2e237] [cursor=pointer]
                - cell [ref=f2e239]:
                  - generic [ref=f2e241]:
                    - switch
                    - generic [ref=f2e242] [cursor=pointer]
                - cell [ref=f2e244]:
                  - generic [ref=f2e246]:
                    - switch [checked]
                    - generic [ref=f2e247] [cursor=pointer]
                - cell [ref=f2e249]:
                  - generic [ref=f2e251]:
                    - switch
                    - generic [ref=f2e252] [cursor=pointer]
              - row [ref=f2e254]:
                - cell [ref=f2e255]:
                  - code [ref=f2e257]: delete:role
                - cell [ref=f2e258]:
                  - generic [ref=f2e260]:
                    - switch
                    - generic [ref=f2e261] [cursor=pointer]
                - cell [ref=f2e263]:
                  - generic [ref=f2e265]:
                    - switch
                    - generic [ref=f2e266] [cursor=pointer]
                - cell [ref=f2e268]:
                  - generic [ref=f2e270]:
                    - switch [checked]
                    - generic [ref=f2e271] [cursor=pointer]
                - cell [ref=f2e273]:
                  - generic [ref=f2e275]:
                    - switch
                    - generic [ref=f2e276] [cursor=pointer]
              - row [ref=f2e278]:
                - cell [ref=f2e279]:
                  - code [ref=f2e281]: disable:employee
                - cell [ref=f2e282]:
                  - generic [ref=f2e284]:
                    - switch
                    - generic [ref=f2e285] [cursor=pointer]
                - cell [ref=f2e287]:
                  - generic [ref=f2e289]:
                    - switch
                    - generic [ref=f2e290] [cursor=pointer]
                - cell [ref=f2e292]:
                  - generic [ref=f2e294]:
                    - switch [checked]
                    - generic [ref=f2e295] [cursor=pointer]
                - cell [ref=f2e297]:
                  - generic [ref=f2e299]:
                    - switch
                    - generic [ref=f2e300] [cursor=pointer]
              - row [ref=f2e302]:
                - cell [ref=f2e303]:
                  - code [ref=f2e305]: edit:role
                - cell [ref=f2e306]:
                  - generic [ref=f2e308]:
                    - switch
                    - generic [ref=f2e309] [cursor=pointer]
                - cell [ref=f2e311]:
                  - generic [ref=f2e313]:
                    - switch
                    - generic [ref=f2e314] [cursor=pointer]
                - cell [ref=f2e316]:
                  - generic [ref=f2e318]:
                    - switch [checked]
                    - generic [ref=f2e319] [cursor=pointer]
                - cell [ref=f2e321]:
                  - generic [ref=f2e323]:
                    - switch
                    - generic [ref=f2e324] [cursor=pointer]
              - row [ref=f2e326]:
                - cell [ref=f2e327]:
                  - code [ref=f2e329]: enable:employee
                - cell [ref=f2e330]:
                  - generic [ref=f2e332]:
                    - switch
                    - generic [ref=f2e333] [cursor=pointer]
                - cell [ref=f2e335]:
                  - generic [ref=f2e337]:
                    - switch
                    - generic [ref=f2e338] [cursor=pointer]
                - cell [ref=f2e340]:
                  - generic [ref=f2e342]:
                    - switch [checked]
                    - generic [ref=f2e343] [cursor=pointer]
                - cell [ref=f2e345]:
                  - generic [ref=f2e347]:
                    - switch
                    - generic [ref=f2e348] [cursor=pointer]
              - row [ref=f2e350]:
                - cell [ref=f2e351]:
                  - code [ref=f2e353]: reset:employee-password
                - cell [ref=f2e354]:
                  - generic [ref=f2e356]:
                    - switch
                    - generic [ref=f2e357] [cursor=pointer]
                - cell [ref=f2e359]:
                  - generic [ref=f2e361]:
                    - switch
                    - generic [ref=f2e362] [cursor=pointer]
                - cell [ref=f2e364]:
                  - generic [ref=f2e366]:
                    - switch [checked]
                    - generic [ref=f2e367] [cursor=pointer]
                - cell [ref=f2e369]:
                  - generic [ref=f2e371]:
                    - switch
                    - generic [ref=f2e372] [cursor=pointer]
              - row [ref=f2e374]:
                - cell [ref=f2e375]:
                  - code [ref=f2e377]: reset:employee-totp
                - cell [ref=f2e378]:
                  - generic [ref=f2e380]:
                    - switch
                    - generic [ref=f2e381] [cursor=pointer]
                - cell [ref=f2e383]:
                  - generic [ref=f2e385]:
                    - switch
                    - generic [ref=f2e386] [cursor=pointer]
                - cell [ref=f2e388]:
                  - generic [ref=f2e390]:
                    - switch [checked]
                    - generic [ref=f2e391] [cursor=pointer]
                - cell [ref=f2e393]:
                  - generic [ref=f2e395]:
                    - switch
                    - generic [ref=f2e396] [cursor=pointer]
              - row [ref=f2e398]:
                - cell [ref=f2e399]:
                  - code [ref=f2e401]: view:audit-log
                - cell [ref=f2e402]:
                  - generic [ref=f2e404]:
                    - switch [checked]
                    - generic [ref=f2e405] [cursor=pointer]
                - cell [ref=f2e407]:
                  - generic [ref=f2e409]:
                    - switch [checked]
                    - generic [ref=f2e410] [cursor=pointer]
                - cell [ref=f2e412]:
                  - generic [ref=f2e414]:
                    - switch [checked]
                    - generic [ref=f2e415] [cursor=pointer]
                - cell [ref=f2e417]:
                  - generic [ref=f2e419]:
                    - switch
                    - generic [ref=f2e420] [cursor=pointer]
              - row [ref=f2e422]:
                - cell [ref=f2e423]:
                  - code [ref=f2e425]: view:channel
                - cell [ref=f2e426]:
                  - generic [ref=f2e428]:
                    - switch
                    - generic [ref=f2e429] [cursor=pointer]
                - cell [ref=f2e431]:
                  - generic [ref=f2e433]:
                    - switch
                    - generic [ref=f2e434] [cursor=pointer]
                - cell [ref=f2e436]:
                  - generic [ref=f2e438]:
                    - switch [checked]
                    - generic [ref=f2e439] [cursor=pointer]
                - cell [ref=f2e441]:
                  - generic [ref=f2e443]:
                    - switch
                    - generic [ref=f2e444] [cursor=pointer]
              - row [ref=f2e446]:
                - cell [ref=f2e447]:
                  - code [ref=f2e449]: view:employee
                - cell [ref=f2e450]:
                  - generic [ref=f2e452]:
                    - switch
                    - generic [ref=f2e453] [cursor=pointer]
                - cell [ref=f2e455]:
                  - generic [ref=f2e457]:
                    - switch
                    - generic [ref=f2e458] [cursor=pointer]
                - cell [ref=f2e460]:
                  - generic [ref=f2e462]:
                    - switch [checked]
                    - generic [ref=f2e463] [cursor=pointer]
                - cell [ref=f2e465]:
                  - generic [ref=f2e467]:
                    - switch
                    - generic [ref=f2e468] [cursor=pointer]
              - row [ref=f2e470]:
                - cell [ref=f2e471]:
                  - code [ref=f2e473]: view:permission
                - cell [ref=f2e474]:
                  - generic [ref=f2e476]:
                    - switch
                    - generic [ref=f2e477] [cursor=pointer]
                - cell [ref=f2e479]:
                  - generic [ref=f2e481]:
                    - switch
                    - generic [ref=f2e482] [cursor=pointer]
                - cell [ref=f2e484]:
                  - generic [ref=f2e486]:
                    - switch [checked]
                    - generic [ref=f2e487] [cursor=pointer]
                - cell [ref=f2e489]:
                  - generic [ref=f2e491]:
                    - switch
                    - generic [ref=f2e492] [cursor=pointer]
              - row [ref=f2e494]:
                - cell [ref=f2e495]:
                  - code [ref=f2e497]: view:role
                - cell [ref=f2e498]:
                  - generic [ref=f2e500]:
                    - switch
                    - generic [ref=f2e501] [cursor=pointer]
                - cell [ref=f2e503]:
                  - generic [ref=f2e505]:
                    - switch
                    - generic [ref=f2e506] [cursor=pointer]
                - cell [ref=f2e508]:
                  - generic [ref=f2e510]:
                    - switch [checked]
                    - generic [ref=f2e511] [cursor=pointer]
                - cell [ref=f2e513]:
                  - generic [ref=f2e515]:
                    - switch
                    - generic [ref=f2e516] [cursor=pointer]
```

# Test source

```ts
  113 |       writeAdminState({ email, password: rotated, totpSecret: '' })
  114 |       password = rotated
  115 |       await loginThroughUi(page, email, password)
  116 |     }
  117 |     await expect(page).toHaveURL('/setup/totp')
  118 |     const secret = await enrollTotpThroughUi(page)
  119 |     writeAdminState({ email, password, totpSecret: secret })
  120 |   }
  121 | 
  122 |   // The forced rotation completed into a normal session: home.
  123 |   await expect(page).toHaveURL(/\/$/)
  124 |   await expect(page.locator('.topbar')).toContainText(email)
  125 | 
  126 |   // The sidebar reflects the super-admin's live permission set.
  127 |   const nav = page.locator('.sidebar-nav')
  128 |   await expect(nav).toContainText('System')
  129 |   await expect(nav.locator('a[href="/system/employees"]')).toBeVisible()
  130 |   await expect(nav.locator('a[href="/system/roles"]')).toBeVisible()
  131 |   await expect(page.locator('.home')).toContainText('view:employee')
  132 | })
  133 | 
  134 | test('grants-free Employee: sidebar hides ungranted routes and the server refuses them', async ({ page }) => {
  135 |   const root = readAdminState()
  136 |   test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  137 | 
  138 |   // Staff provisioning goes through the admin-api itself — Step-up
  139 |   // included, which is the caller's fresh TOTP code. The calls run
  140 |   // inside the browser page so the edge, the host mapping, and the
  141 |   // cookie jar all apply.
  142 |   await page.goto('/login')
  143 |   const login = await pageApi(page, '/api/login', 'POST', {
  144 |     email: root!.email,
  145 |     password: root!.password,
  146 |     code: await totpCode(root!.totpSecret),
  147 |   })
  148 |   expect(login.status).toBe(200)
  149 |   expect(permissionsOf(login.body)).toContain('view:employee')
  150 | 
  151 |   const rolesAnswer = await pageApi(page, '/api/roles')
  152 |   expect(rolesAnswer.status).toBe(200)
  153 |   const roles = (Array.isArray(rolesAnswer.body) ? rolesAnswer.body : []).filter(isRoleView)
  154 |   // support's matrix grants (member view/lock, content) belong to
  155 |   // endpoints later slices own — today its live set is empty.
  156 |   const support = roles.find((role) => role.name === 'support')
  157 |   expect(support, 'the support Role is seeded').toBeTruthy()
  158 |   expect(support!.permissions).toEqual([])
  159 | 
  160 |   const staffEmail = `support-e2e-${suffix()}@exchange.example.com`
  161 |   const oneTime = `E2e-one-time-${suffix()}`
  162 |   const created = await pageApi(page, '/api/employees', 'POST', {
  163 |     email: staffEmail,
  164 |     password: oneTime,
  165 |     role_id: support!.id,
  166 |     code: await totpCode(root!.totpSecret),
  167 |   })
  168 |   expect(created.status, JSON.stringify(created.body)).toBe(200)
  169 | 
  170 |   // The fresh Employee walks the same forced rotation in the browser.
  171 |   await loginThroughUi(page, staffEmail, oneTime)
  172 |   await expect(page).toHaveURL('/setup/password')
  173 |   const staffPassword = `E2e-staff-${suffix()}`
  174 |   await rotateThroughUi(page, oneTime, staffPassword)
  175 |   await loginThroughUi(page, staffEmail, staffPassword)
  176 |   await expect(page).toHaveURL('/setup/totp')
  177 |   await enrollTotpThroughUi(page)
  178 | 
  179 |   // A normal session whose permission set intersects the route table
  180 |   // emptily: Home alone, no System section.
  181 |   await expect(page).toHaveURL(/\/$/)
  182 |   const nav = page.locator('.sidebar-nav')
  183 |   await expect(nav.locator('a[href="/"]')).toBeVisible()
  184 |   await expect(nav).not.toContainText('System')
  185 |   await expect(nav.locator('a[href="/system/employees"]')).toHaveCount(0)
  186 | 
  187 |   // The sidebar is presentation only: the server refuses the surface.
  188 |   const refused = await pageApi(page, '/api/employees')
  189 |   expect(refused.status).toBe(403)
  190 |   expect(refused.body).toEqual({ message: 'You do not have permission to do that.' })
  191 | })
  192 | 
  193 | test('unknown routes answer the 404 page', async ({ page }) => {
  194 |   const root = readAdminState()
  195 |   test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  196 |   await loginThroughUi(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  197 |   const response = await page.goto('/no-such-page')
  198 |   expect(response?.status()).toBe(404)
  199 |   await expect(page.getByText('The page you are looking for does not exist.')).toBeVisible()
  200 | })
  201 | 
  202 | // ---- issue #57: Employee management, the grant editor, audit search ----
  203 | 
  204 | /** The super-admin column's index in the grant editor (roles arrive name-sorted). */
  205 | async function superAdminColumnIndex(page: Page): Promise<number> {
  206 |   const headers = page.locator('.editor-table thead th')
  207 |   const count = await headers.count()
  208 |   for (let i = 0; i < count; i += 1) {
  209 |     if ((await headers.nth(i).innerText()).trim() === 'super-admin') {
  210 |       return i
  211 |     }
  212 |   }
> 213 |   throw new Error('no super-admin column in the grant editor')
      |         ^ Error: no super-admin column in the grant editor
  214 | }
  215 | 
  216 | test('employee management from the UI: create with Step-up, reset password, disable', async ({ page }) => {
  217 |   const root = readAdminState()
  218 |   test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  219 |   await loginThroughUi(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  220 |   await expect(page).toHaveURL(/\/$/)
  221 |   await page.goto('/system/employees')
  222 |   await expect(page.locator('h1')).toHaveText('Employees')
  223 | 
  224 |   // The guards are inert client-side: self-disable and the sole
  225 |   // super-admin's own row refuse before any request.
  226 |   const selfRow = page.locator('.employees-table tbody tr', { hasText: root!.email })
  227 |   await expect(selfRow.locator('button:has-text("Disable")')).toBeDisabled()
  228 |   await expect(selfRow.locator('button:has-text("Change role")')).toBeDisabled()
  229 | 
  230 |   // Create behind the Step-up prompt: an empty code is refused with a
  231 |   // visible sentence before anything is sent.
  232 |   const email = `ui-staff-${suffix()}@exchange.example.com`
  233 |   // Closed dialogs stay in the DOM hidden — every interaction scopes
  234 |   // to the open dialog's class, never the bare .el-dialog.
  235 |   await page.click('button:has-text("Create Employee")')
  236 |   await page.fill('.dialog-create input[name="email"]', email)
  237 |   await page.fill('.dialog-create input[name="password"]', 'E2e-one-time-1')
  238 |   await page.click('.dialog-create .el-select__wrapper')
  239 |   await page.click('.el-select-dropdown__item:has-text("finance")')
  240 |   await page.click('.dialog-create button:has-text("Create")')
  241 |   await expect(page.locator('.dialog-create .dialog-error')).toContainText(
  242 |     'Enter a fresh authenticator code.',
  243 |   )
  244 |   await page.fill('.dialog-create input[name="code"]', await totpCode(root!.totpSecret))
  245 |   await page.click('.dialog-create button:has-text("Create")')
  246 |   const row = page.locator('.employees-table tbody tr', { hasText: email })
  247 |   await expect(row).toBeVisible()
  248 | 
  249 |   // Reset password (Step-up): the generated one-time credential shows once.
  250 |   await row.locator('button:has-text("Reset password")').click()
  251 |   await page.fill('.dialog-reset-password input[name="code"]', await totpCode(root!.totpSecret))
  252 |   await page.click('.dialog-reset-password button:has-text("Reset")')
  253 |   await expect(page.locator('.dialog-reset-password .one-time-result')).toBeVisible()
  254 |   await page.click('.dialog-reset-password button:has-text("Close")')
  255 | 
  256 |   // Disable the new Employee; the server's refusal for a guarded row
  257 |   // would render in the page alert (the self row above never sends).
  258 |   await row.locator('button:has-text("Disable")').click()
  259 |   await expect(row).toContainText('Disabled')
  260 | 
  261 |   // The audit log renders the trail with before/after JSON. The action
  262 |   // filter's response decides the table — wait for it, then assert the
  263 |   // one create row (the unfiltered table also shows this Employee's
  264 |   // reset and disable rows).
  265 |   await page.goto('/audit/log')
  266 |   await expect(page.locator('.audit-table tbody tr').first()).toBeVisible()
  267 |   await page.fill('input[name="action"]', 'create:employee')
  268 |   const [searchResponse] = await Promise.all([
  269 |     page.waitForResponse((response) => response.url().includes('action=create%3Aemployee')),
  270 |     page.click('button:has-text("Search")'),
  271 |   ])
  272 |   expect(searchResponse.status()).toBe(200)
  273 |   const createRow = page.locator('.audit-table tbody tr', { hasText: 'create:employee' })
  274 |   await expect(createRow).toHaveCount(1)
  275 |   await expect(createRow).toContainText(email)
  276 |   await expect(createRow.locator('.after-json')).toContainText(email)
  277 |   await expect(createRow.locator('.before-json')).toContainText('—')
  278 | })
  279 | 
  280 | test('grant editor: a revoked grant bites the live session on its next request', async ({ page }) => {
  281 |   const root = readAdminState()
  282 |   test.skip(!root?.totpSecret, 'needs the provisioned super-admin from the first journey')
  283 |   await loginThroughUi(page, root!.email, root!.password, await totpCode(root!.totpSecret))
  284 |   await expect(page).toHaveURL(/\/$/)
  285 |   await page.goto('/system/employees')
  286 |   await expect(page.locator('.employees-table')).toBeVisible()
  287 | 
  288 |   // Toggle view:employee off the super-admin Role in the matrix; the
  289 |   // switch persists immediately (one POST, no save step).
  290 |   await page.goto('/system/roles')
  291 |   const columnIndex = await superAdminColumnIndex(page)
  292 |   const editorRow = page.locator('.editor-table tbody tr', { hasText: 'view:employee' })
  293 |   const grantSwitch = editorRow.locator('td').nth(columnIndex).locator('.el-switch')
  294 |   await expect(grantSwitch).toHaveClass(/is-checked/)
  295 |   const [revokeResponse] = await Promise.all([
  296 |     page.waitForResponse((response) => response.url().includes('/grants') && response.request().method() === 'POST'),
  297 |     grantSwitch.click(),
  298 |   ])
  299 |   expect(revokeResponse.status()).toBe(200)
  300 |   await expect(grantSwitch).not.toHaveClass(/is-checked/)
  301 | 
  302 |   // The bite: the SAME live session's next request is refused, and the
  303 |   // refusal renders as a visible error — never a silent empty table.
  304 |   await page.goto('/system/employees')
  305 |   await expect(page.locator('.action-error')).toContainText('You do not have permission to do that.')
  306 |   const refused = await pageApi(page, '/api/employees')
  307 |   expect(refused.status).toBe(403)
  308 | 
  309 |   // Re-grant through the same switch; the next request goes through.
  310 |   await page.goto('/system/roles')
  311 |   const grantSwitchAgain = page
  312 |     .locator('.editor-table tbody tr', { hasText: 'view:employee' })
  313 |     .locator('td')
```