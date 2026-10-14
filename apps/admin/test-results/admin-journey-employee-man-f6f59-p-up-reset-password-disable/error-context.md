# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: admin-journey.spec.ts >> employee management from the UI: create with Step-up, reset password, disable
- Location: e2e/admin-journey.spec.ts:216:1

# Error details

```
Error: expect(locator).toHaveCount(expected) failed

Locator:  locator('.audit-table tbody tr').filter({ hasText: 'create:employee' })
Expected: 1
Received: 15
Timeout:  5000ms

Call log:
  - Expect "toHaveCount" locator('.audit-table tbody tr').filter({ hasText: 'create:employee' }) with timeout 5000ms
  - waiting for locator('.audit-table tbody tr').filter({ hasText: 'create:employee' })
    14 × locator resolved to 15 elements
       - unexpected value "15"

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
          - heading "Audit log" [level=1] [ref=f2e28]
          - paragraph [ref=f2e29]: One row per mutating admin request and sign-in event, written by the server's middleware — never by handlers. Actions are verb:resource strings; before/after carry the redacted row JSON (credentials never enter the log).
        - generic [ref=f2e30]:
          - generic [ref=f2e31]:
            - generic [ref=f2e32]: Actor
            - textbox "Actor" [ref=f2e36]:
              - /placeholder: employee@exchange.example.com
          - generic [ref=f2e38]:
            - generic [ref=f2e39]: Action
            - textbox "Action" [ref=f2e43]:
              - /placeholder: verb:resource
              - text: create:employee
          - generic [ref=f2e45]:
            - generic [ref=f2e46]: Target
            - textbox "Target" [ref=f2e50]:
              - /placeholder: Affected id
          - generic [ref=f2e52]:
            - generic [ref=f2e53]: Time range
            - generic [ref=f2e55]:
              - combobox "Time range" [ref=f2e61]
              - generic [ref=f2e62]: "-"
              - combobox "To" [ref=f2e63]
          - button "Search" [active] [ref=f2e66] [cursor=pointer]
        - generic [ref=f2e69]:
          - table [ref=f2e71]:
            - rowgroup [ref=f2e79]:
              - row [ref=f2e80]:
                - columnheader "Time" [ref=f2e81]
                - columnheader "Actor" [ref=f2e83]
                - columnheader "Action" [ref=f2e85]
                - columnheader "Target" [ref=f2e87]
                - columnheader "Origin IP" [ref=f2e89]
                - columnheader "Before → After" [ref=f2e91]
          - table [ref=f2e97]:
            - rowgroup [ref=f2e105]:
              - row [ref=f2e106]:
                - cell "Oct 14, 2026, 2:38 PM" [ref=f2e107]
                - cell "admin@exchange.example.com" [ref=f2e109]
                - cell "create:employee" [ref=f2e111]
                - cell "e0c51489-2bbc-4bbe-9faf-cd9d17a798ef" [ref=f2e113]
                - cell "172.19.0.1" [ref=f2e115]
                - 'cell "— {\"id\":\"e0c51489-2bbc-4bbe-9faf-cd9d17a798ef\",\"email\":\"ui-staff-9f9501c9@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e117]':
                  - generic [ref=f2e118]:
                    - generic [ref=f2e119]: —
                    - generic [ref=f2e120]: "{\"id\":\"e0c51489-2bbc-4bbe-9faf-cd9d17a798ef\",\"email\":\"ui-staff-9f9501c9@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e121]:
                - cell "Oct 14, 2026, 2:38 PM" [ref=f2e122]
                - cell "admin@exchange.example.com" [ref=f2e124]
                - cell "create:employee" [ref=f2e126]
                - cell "d5d3d73b-6ce6-4733-a6fd-9349d6e5a495" [ref=f2e128]
                - cell "172.19.0.1" [ref=f2e130]
                - 'cell "— {\"id\":\"d5d3d73b-6ce6-4733-a6fd-9349d6e5a495\",\"email\":\"support-e2e-c92861f2@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e132]':
                  - generic [ref=f2e133]:
                    - generic [ref=f2e134]: —
                    - generic [ref=f2e135]: "{\"id\":\"d5d3d73b-6ce6-4733-a6fd-9349d6e5a495\",\"email\":\"support-e2e-c92861f2@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e136]:
                - cell "Oct 14, 2026, 2:36 PM" [ref=f2e137]
                - cell "admin@exchange.example.com" [ref=f2e139]
                - cell "create:employee" [ref=f2e141]
                - cell "8d87890d-4404-416b-b522-ec77afa8f9bb" [ref=f2e143]
                - cell "172.19.0.1" [ref=f2e145]
                - 'cell "— {\"id\":\"8d87890d-4404-416b-b522-ec77afa8f9bb\",\"email\":\"ui-staff-36946ec6@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e147]':
                  - generic [ref=f2e148]:
                    - generic [ref=f2e149]: —
                    - generic [ref=f2e150]: "{\"id\":\"8d87890d-4404-416b-b522-ec77afa8f9bb\",\"email\":\"ui-staff-36946ec6@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e151]:
                - cell "Oct 14, 2026, 2:36 PM" [ref=f2e152]
                - cell "admin@exchange.example.com" [ref=f2e154]
                - cell "create:employee" [ref=f2e156]
                - cell "d3dcae0f-22e9-480d-b182-0c8f56c36c2a" [ref=f2e158]
                - cell "172.19.0.1" [ref=f2e160]
                - 'cell "— {\"id\":\"d3dcae0f-22e9-480d-b182-0c8f56c36c2a\",\"email\":\"ui-staff-cbc4ca73@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e162]':
                  - generic [ref=f2e163]:
                    - generic [ref=f2e164]: —
                    - generic [ref=f2e165]: "{\"id\":\"d3dcae0f-22e9-480d-b182-0c8f56c36c2a\",\"email\":\"ui-staff-cbc4ca73@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e166]:
                - cell "Oct 14, 2026, 2:36 PM" [ref=f2e167]
                - cell "admin@exchange.example.com" [ref=f2e169]
                - cell "create:employee" [ref=f2e171]
                - cell "e280db85-c498-4534-8ebb-4c143e762c94" [ref=f2e173]
                - cell "172.19.0.1" [ref=f2e175]
                - 'cell "— {\"id\":\"e280db85-c498-4534-8ebb-4c143e762c94\",\"email\":\"support-e2e-71ced7e2@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e177]':
                  - generic [ref=f2e178]:
                    - generic [ref=f2e179]: —
                    - generic [ref=f2e180]: "{\"id\":\"e280db85-c498-4534-8ebb-4c143e762c94\",\"email\":\"support-e2e-71ced7e2@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e181]:
                - cell "Oct 14, 2026, 2:28 PM" [ref=f2e182]
                - cell "admin@exchange.example.com" [ref=f2e184]
                - cell "create:employee" [ref=f2e186]
                - cell "b5cda768-daa8-4b2a-8469-17e4931a626c" [ref=f2e188]
                - cell "172.19.0.1" [ref=f2e190]
                - 'cell "— {\"id\":\"b5cda768-daa8-4b2a-8469-17e4931a626c\",\"email\":\"ui-staff-aa8c7ef5@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e192]':
                  - generic [ref=f2e193]:
                    - generic [ref=f2e194]: —
                    - generic [ref=f2e195]: "{\"id\":\"b5cda768-daa8-4b2a-8469-17e4931a626c\",\"email\":\"ui-staff-aa8c7ef5@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e196]:
                - cell "Oct 14, 2026, 2:26 PM" [ref=f2e197]
                - cell "admin@exchange.example.com" [ref=f2e199]
                - cell "create:employee" [ref=f2e201]
                - cell "da15d57e-cdab-41ee-83ef-a86d39ff9bd7" [ref=f2e203]
                - cell "172.19.0.1" [ref=f2e205]
                - 'cell "— {\"id\":\"da15d57e-cdab-41ee-83ef-a86d39ff9bd7\",\"email\":\"ui-staff-2a36ee14@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e207]':
                  - generic [ref=f2e208]:
                    - generic [ref=f2e209]: —
                    - generic [ref=f2e210]: "{\"id\":\"da15d57e-cdab-41ee-83ef-a86d39ff9bd7\",\"email\":\"ui-staff-2a36ee14@exchange.example.com\",\"enabled\":true,\"role_id\":\"da33290b-f681-4894-8108-5b2b91295c18\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e211]:
                - cell "Oct 14, 2026, 2:26 PM" [ref=f2e212]
                - cell "admin@exchange.example.com" [ref=f2e214]
                - cell "create:employee" [ref=f2e216]
                - cell "d73b22ff-0d03-4723-ad01-c282dfae091d" [ref=f2e218]
                - cell "172.19.0.1" [ref=f2e220]
                - 'cell "— {\"id\":\"d73b22ff-0d03-4723-ad01-c282dfae091d\",\"email\":\"support-e2e-76a8a432@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e222]':
                  - generic [ref=f2e223]:
                    - generic [ref=f2e224]: —
                    - generic [ref=f2e225]: "{\"id\":\"d73b22ff-0d03-4723-ad01-c282dfae091d\",\"email\":\"support-e2e-76a8a432@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e226]:
                - cell "Oct 14, 2026, 2:16 PM" [ref=f2e227]
                - cell "admin@exchange.example.com" [ref=f2e229]
                - cell "create:employee" [ref=f2e231]
                - cell "5d715b46-eb01-4654-8b9b-39217f5cb2b3" [ref=f2e233]
                - cell "172.19.0.1" [ref=f2e235]
                - 'cell "— {\"id\":\"5d715b46-eb01-4654-8b9b-39217f5cb2b3\",\"email\":\"support-e2e-089329e5@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e237]':
                  - generic [ref=f2e238]:
                    - generic [ref=f2e239]: —
                    - generic [ref=f2e240]: "{\"id\":\"5d715b46-eb01-4654-8b9b-39217f5cb2b3\",\"email\":\"support-e2e-089329e5@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e241]:
                - cell "Oct 14, 2026, 1:09 PM" [ref=f2e242]
                - cell "admin@exchange.example.com" [ref=f2e244]
                - cell "create:employee" [ref=f2e246]
                - cell "dadcc9fd-ecb9-450a-9b9d-6b596ba5c09d" [ref=f2e248]
                - cell "172.19.0.1" [ref=f2e250]
                - 'cell "— {\"id\":\"dadcc9fd-ecb9-450a-9b9d-6b596ba5c09d\",\"email\":\"support-e2e-8ce2ef5f@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e252]':
                  - generic [ref=f2e253]:
                    - generic [ref=f2e254]: —
                    - generic [ref=f2e255]: "{\"id\":\"dadcc9fd-ecb9-450a-9b9d-6b596ba5c09d\",\"email\":\"support-e2e-8ce2ef5f@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e256]:
                - cell "Oct 14, 2026, 1:09 PM" [ref=f2e257]
                - cell "admin@exchange.example.com" [ref=f2e259]
                - cell "create:employee" [ref=f2e261]
                - cell "26aed2e1-e394-4fb3-876d-bc834eba519d" [ref=f2e263]
                - cell "172.19.0.1" [ref=f2e265]
                - 'cell "— {\"id\":\"26aed2e1-e394-4fb3-876d-bc834eba519d\",\"email\":\"support-e2e-b8dfaee0@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e267]':
                  - generic [ref=f2e268]:
                    - generic [ref=f2e269]: —
                    - generic [ref=f2e270]: "{\"id\":\"26aed2e1-e394-4fb3-876d-bc834eba519d\",\"email\":\"support-e2e-b8dfaee0@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e271]:
                - cell "Oct 14, 2026, 1:09 PM" [ref=f2e272]
                - cell "admin@exchange.example.com" [ref=f2e274]
                - cell "create:employee" [ref=f2e276]
                - cell "0dc246f7-cb38-4dae-ae60-b9e4b2379cc6" [ref=f2e278]
                - cell "172.19.0.1" [ref=f2e280]
                - 'cell "— {\"id\":\"0dc246f7-cb38-4dae-ae60-b9e4b2379cc6\",\"email\":\"support-e2e-a8b5d268@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e282]':
                  - generic [ref=f2e283]:
                    - generic [ref=f2e284]: —
                    - generic [ref=f2e285]: "{\"id\":\"0dc246f7-cb38-4dae-ae60-b9e4b2379cc6\",\"email\":\"support-e2e-a8b5d268@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e286]:
                - cell "Oct 14, 2026, 1:09 PM" [ref=f2e287]
                - cell "admin@exchange.example.com" [ref=f2e289]
                - cell "create:employee" [ref=f2e291]
                - cell "64fd18a9-90e7-4347-8b95-9af5d9876fe7" [ref=f2e293]
                - cell "172.19.0.1" [ref=f2e295]
                - 'cell "— {\"id\":\"64fd18a9-90e7-4347-8b95-9af5d9876fe7\",\"email\":\"support-e2e-5f3c3959@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e297]':
                  - generic [ref=f2e298]:
                    - generic [ref=f2e299]: —
                    - generic [ref=f2e300]: "{\"id\":\"64fd18a9-90e7-4347-8b95-9af5d9876fe7\",\"email\":\"support-e2e-5f3c3959@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e301]:
                - cell "Oct 14, 2026, 1:09 PM" [ref=f2e302]
                - cell "admin@exchange.example.com" [ref=f2e304]
                - cell "create:employee" [ref=f2e306]
                - cell "7a4ee6b9-d1d2-4b86-9142-13281e2d9fe0" [ref=f2e308]
                - cell "172.19.0.1" [ref=f2e310]
                - 'cell "— {\"id\":\"7a4ee6b9-d1d2-4b86-9142-13281e2d9fe0\",\"email\":\"support-e2e-7ccf378e@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e312]':
                  - generic [ref=f2e313]:
                    - generic [ref=f2e314]: —
                    - generic [ref=f2e315]: "{\"id\":\"7a4ee6b9-d1d2-4b86-9142-13281e2d9fe0\",\"email\":\"support-e2e-7ccf378e@exchange.example.com\",\"enabled\":true,\"role_id\":\"1e0c0b8f-1757-4f8a-9c1a-05aaf43a8063\",\"totp_bound\":false,\"must_rotate\":true}"
              - row [ref=f2e316]:
                - cell "Oct 14, 2026, 1:03 PM" [ref=f2e317]
                - cell "system:bootstrap" [ref=f2e319]
                - cell "create:employee" [ref=f2e321]
                - cell "8531dbbe-d22d-4447-8f5c-317abe4ef0ce" [ref=f2e323]
                - cell "127.0.0.1" [ref=f2e325]
                - 'cell "— {\"id\":\"8531dbbe-d22d-4447-8f5c-317abe4ef0ce\",\"email\":\"admin@exchange.example.com\",\"enabled\":true,\"role_id\":\"f7a3c971-4fd9-4da5-b3f6-98b6f84fa84f\",\"totp_bound\":false,\"must_rotate\":true}" [ref=f2e327]':
                  - generic [ref=f2e328]:
                    - generic [ref=f2e329]: —
                    - generic [ref=f2e330]: "{\"id\":\"8531dbbe-d22d-4447-8f5c-317abe4ef0ce\",\"email\":\"admin@exchange.example.com\",\"enabled\":true,\"role_id\":\"f7a3c971-4fd9-4da5-b3f6-98b6f84fa84f\",\"totp_bound\":false,\"must_rotate\":true}"
        - generic [ref=f2e331]:
          - button "Go to previous page" [disabled] [ref=f2e332]
          - list [ref=f2e333]:
            - listitem "page 1" [ref=f2e334]: "1"
          - button "Go to next page" [disabled] [ref=f2e335]
          - generic [ref=f2e336]: Total 15
```

# Test source

```ts
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
  213 |   throw new Error('no super-admin column in the grant editor')
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
> 274 |   await expect(createRow).toHaveCount(1)
      |                           ^ Error: expect(locator).toHaveCount(expected) failed
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
  314 |     .nth(columnIndex)
  315 |     .locator('.el-switch')
  316 |   const [grantResponse] = await Promise.all([
  317 |     page.waitForResponse((response) => response.url().includes('/grants') && response.request().method() === 'POST'),
  318 |     grantSwitchAgain.click(),
  319 |   ])
  320 |   expect(grantResponse.status()).toBe(200)
  321 |   await expect(grantSwitchAgain).toHaveClass(/is-checked/)
  322 |   await page.goto('/system/employees')
  323 |   await expect(page.locator('.employees-table')).toBeVisible()
  324 | })
  325 | 
```