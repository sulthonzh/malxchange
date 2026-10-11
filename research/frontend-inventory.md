# Legacy Frontend Inventory for the Nuxt 3 Port (Issue #12)

Two Vue 2 apps live in the repo: `05_Web_Front/` (public trading front, iView 3, vue-resource) and
`04_Web_Admin/` (element… actually iView 2 admin, axios). Both are webpack template builds; neither
has any test or lint setup worth porting.

Legend: WS = SockJS + STOMP endpoint subscription. "—" = none. All line refs are current `main`.

---

## A. 05_Web_Front — public trading front

### Shared infrastructure
- **Stack**: Vue 2.5, vue-router 3 (history mode), vuex, vue-resource (NOT axios), iView 3.1, vue-i18n 7, moment, jquery, vue-clipboard2, vue-qriously/vue-qart, stompjs + sockjs-client (`05_Web_Front/package.json`).
- **Entry**: `src/main.js` — registers plugins, sets `Vue.prototype.host = "https://api.xxxx.com"` and `rootHost` (hardcoded placeholders, main.js:25-26); HTTP interceptor injects `x-auth-token` header from localStorage and redirects to `/login` on code 4000/3000 (main.js:60-79).
- **API base**: `src/config/api.js` exports a map of paths (uc / market / exchange / otc / activity); most pages also hardcode `this.host + "/..."` literals inline.
- **WS**: SockJS to `host + /market/market-ws` (market) and `host + /chat/chat-webSocket` (OTC chat), STOMP subscriptions (see tables).
- **i18n**: `VueI18n` with `zh`/`en` dicts in `src/assets/lang/{zh,en}.js`, default `zh` (main.js:55-60).
- **Charts**: TradingView **charting_library** vendored at `src/assets/js/charting_library/` with a custom STOMP datafeed `datafeed/bitrade.js` (Exchange.vue:672,1700-1701).
- **Build**: legacy webpack template (`config/index.js`), dev server localhost:8080, proxyTable commented out — all API calls go directly to the hardcoded host. No dev proxy to replicate; Nuxt port should use runtime env config.

### Routes / pages table

| Page | Route | APIs called | WS | Screenshot (09_DOC/PC端运行截图) |
|---|---|---|---|---|
| index/Index.vue | `/`, `/index`, `*` (catch-all) | `/uc/announcement/page`, `/market/symbol-thumb-trend`, `/exchange/favor/find|add|delete` | `/topic/market/thumb` | 01_首页.png |
| uc/Login.vue | `/login`, `/login/returnUrl/:returnUrl` | `/uc/login`, `/uc/start/captcha` | — | 登录.png |
| uc/Register.vue | `/register` | `/uc/support/country` | — | 注册.png |
| uc/MobileRegister.vue | `/reg` | `/uc/support/country` | — | (同注册) |
| uc/FindPwd.vue | `/findPwd` | `/uc/start/captcha` + reset literals | — | — |
| uc/AppDownload.vue | `/app` | `/uc/ancillary/system/app/version/0` | — | — |
| exchange/Exchange.vue | `/exchange`, `/exchange/:pair` | `/market/symbol-thumb`, `-info`, `coin-info`, `exchange-plate-full`, `-mini`, `/market/latest-trade`, `/exchange/order/add|current|history|cancel|detail`, `/exchange/favor/*`, `/uc/asset/wallet/` | `/topic/market/thumb`, `/topic/market/trade/:symbol`, `/topic/market/trade-plate/:symbol`, `/topic/market/order-canceled|order-completed|order-trade/:symbol/:memberId` | 02_币币交易.png |
| ctc/Ctc.vue | `/ctc` | `/market/ctc-usdt`, `/uc/ctc/page-query|new-ctc-order|detail|pay-ctc-order|cancel-ctc-order`, `/uc/approve/account|security/setting`, `/uc/mobile/ctc/code` | — | 03_法币交易CTC.png |
| activity/Activity.vue | `/lab` | `/uc/activity/page-query` | — | 04_创新实验室.png |
| activity/ActivityDetail.vue | `/lab/detail/:id` | `/uc/activity/detail|code|attend`, `/uc/activity/getmemberrecords`, wallet | — | 创新实验室详情.png |
| activity/Partner.vue | `/partner` | (via Invite endpoints /uc/promotion/*) | — | 07_推广合伙人.png |
| activity/Bzb.vue | `/bzb` | literal `rootHost` static page | — | — |
| cms/Notice.vue | `/notice` | `/uc/announcement/page` | — | 05_公告.png |
| cms/NoticeItem.vue | `/announcement/:id` | `/uc/announcement/page` (detail by id) | — | 公告详情.png |
| cms/Help.vue | `/help` | `/uc/ancillary/more/help` | — | 06_帮助中心.png |
| cms/HelpList.vue | `/helplist` | `/uc/ancillary/more/help/page` | — | 06_帮助中心.png |
| cms/HelpDetail.vue | `/helpdetail` | `/uc/ancillary/more/help/detail`, `.../page/top` | — | — |
| cms/WhitePaper.vue | `/whitepaper` | static | — | — |
| cms/AboutUs.vue | `/about-us` | static | — | — |
| invite/Invite.vue | `/invite` | `/uc/promotion/mypromotion|toprank|monthtoprank|weektoprank|record`, `promotioncard/*`, `/uc/member/my-info` | — | 07_推广合伙人.png |
| envelope/Envelope.vue | `/envelope/:eno` | `/uc/redenvelope/query|query-detail|receive|code` | — | — |
| otc/Main.vue | `/otc` (parent) | `/otc/coin/all` | — | — |
| otc/Trade.vue | `/otc/trade/*` (child) | `/otc/advertise/page-by-unit` | — | — |
| otc/TradeInfo.vue | `/otc/tradeInfo` | `/otc/order/sell|detail|pay|release|cancel|appeal`, chat history | `/user/:myId/:orderId` (via Chatline.vue) | — |
| otc/Chat.vue | `/chat` | `/otc/order/detail|pay|release|cancel|appeal`, `/chat/getHistoryMessage` | `/chat/chat-webSocket`, send `/app/message/chat` | — |
| otc/CheckUser.vue | `/checkuser` | `/otc/advertise/member` | — | — |
| otc/AdPublish.vue | `/uc/ad/create`, `/uc/ad/update` | `/uc/ad/create`, `/uc/approve/certified/business/status` | — | — |
| otc/index.vue | **not routed (dead)** | `/api/advertise/excellent` | — | — |
| uc/MemberCenter.vue | `/uc` (layout) | user info | — | — |
| uc/IdentBusiness.vue | `/identbusiness` | `/uc/approve/certified/business/apply|status` | — | — |
| uc children — components/uc/Safe.vue | `/uc`, `/uc/safe` | `/uc/approve/real/name`, `transaction/password`, `update/password`, `bind/email|phone`, mobile codes | — | — |
| components/uc/Account.vue | `/uc/account` | `/uc/approve/bind|update/ali|bank|wechat`, account/setting | — | — |
| components/uc/MoneyIndex.vue | `/uc/money` | `/uc/asset/wallet`, `/match`, `/match-check`, `/reset-address` | — | — |
| components/uc/Record.vue | `/uc/record` | `/uc/asset/transaction/all`, `/uc/withdraw/support/coin/info` | — | — |
| components/uc/Recharge.vue | `/uc/recharge` | wallet + deposit literals | — | — |
| components/uc/Withdraw.vue | `/uc/withdraw` | `/uc/approve/security/setting`, `/uc/mobile/withdraw/code`, withdraw literals | — | — |
| components/uc/WithdrawAddress.vue | `/uc/withdraw/address` | `/uc/add/address/code` + literals | — | — |
| components/otc/MyAd.vue | `/uc/ad` | `/otc/advertise/all`, `/uc/ad/create` | — | — |
| components/uc/myorder.vue | `/uc/order` | `/otc/order/self` | — | — |
| components/uc/EntrustCurrent.vue | `/uc/entrust/current` | `/exchange/order/personal/current`, `/exchange/order/cancel`, `/market/symbol-thumb` | — | — |
| components/uc/EntrustHistory.vue | `/uc/entrust/history` | history literals, `/market/symbol-thumb` | — | — |
| components/uc/MinTrade.vue | `/uc/trade` | `/uc/mine/detail/` | — | — |
| components/uc/InvitingMin.vue | `/uc/invitingmining` | `/uc/mine/detail/`, `/uc/asset/transaction/all` | — | — |
| components/uc/PayDividends.vue | `/uc/paydividends` | `/uc/bonus/user/page` | — | — |
| components/uc/PromotionMyCards.vue | `/uc/promotion/mycards` | `/uc/promotion/promotioncard/mycard|exchangecard` | — | — |
| components/uc/MyPromotion.vue | `/uc/promotion/mypromotion` | `/uc/promotion/record` | — | — |
| components/uc/InnovationOrders.vue | `/uc/innovation/myorders` | `/uc/activity/getmyorders` | — | — |
| components/uc/InnovationMinings.vue | `/uc/innovation/myminings` | `/uc/miningorder/my-minings` | — | — |

### Dead / vestigial (front)
- `pages/otc/index.vue` — no route; calls `/api/advertise/excellent`.
- `routes.js` has ~30 commented-out imports/routes (`/newhelp`, `/question`, `/agreement`, `/about-fee`, etc.) — skip in port.
- Components never imported anywhere: `components/exchange/{BZCountDown,expand,SvgLine,DepthGraph}.vue`, `components/uc/TradeExpand.vue`, `components/cms/Noticeindex.vue`. (`otc/Chatline.vue` IS used by Chat.vue/TradeInfo.)
- `json-server` in dependencies — dev-only mock, drop.
- Catch-all `path: '*'` → Index.vue: Nuxt should use a 404/error page instead.

---

## B. 04_Web_Admin — admin console

### Shared infrastructure
- **Stack**: Vue 2, vue-router (**hash mode**), axios 0.17 + qs, iView **2.13** (NOT element-ui), echarts 3.8, @antv/data-set, js-cookie, html2canvas, cropperjs, iview-area (`04_Web_Admin/package.json`).
- **Entry**: `src/main.js`; axios baseURL hardcoded `http://49.234.13.106:8801/` in `src/service/http.js:3` (⚠ public IP baked in — replace with env config in port; same IP in `src/config/api.js:2`).
- **API layer**: `src/service/getData.js` — ~190 named `admin/**` endpoint wrappers used by views; `src/config/api.js` is a small legacy subset (login, system, otc) mostly unused.
- **WS**: none. No WebSocket/stomp usage anywhere in admin (grep confirmed).
- **i18n**: `src/locale/index.js` merges iview locales zh-CN / en-US / zh-TW from `navigator.language` + localStorage; UI chrome is Chinese.
- **Charts**: echarts 3 (home dashboard), @antv/data-set (G2 stack in home components).
- **Build**: webpack template in `build/`, dev server 127.0.0.1:9527, `src/template/index.ejs`.
- **Menu is data-driven**: `src/router/router.js` builds `appRouter` by joining a persisted `leftSidebarList` (from backend permissions, localStorage) with the static `findSlideArr` table (router.js:69-160) — the route table a Nuxt port must reproduce is exactly `findSlideArr`.

### Routes / pages table
Top-level nav sections (router.js findSlideArr ids): system(9) member(11) otc(66) exchange(60) finance(93) content(18) businessAuth(177) activity(162) ctc(203) envelope(220) Invitation(214). All are children of `views/Main.vue`; hash routes are `/<section>/<child path>`.

| Page (view) | Route path (under Main) | Key APIs (getData.js) | WS | Screenshot (09_DOC/管理后台运行截图) |
|---|---|---|---|---|
| login.vue | `/login` (standalone) | `admin/system/employee/sign/in` | — | (登录在 00_首页 前流程) |
| home/home.vue | `home` (otherRouter) | `admin/system/statistics/dashboard`, `admin/index/statistics/*` (member/otc/exchange charts) | — | 00_首页 |
| member/Authenticate.vue | `memberaudit` | `admin/member/member-application/page-query|detail`, check pass/not-pass | — | 01_会员管理 |
| member/AuthenticateDetail.vue | `member/memberaudit/auditdetail` | same as above | — | 01_会员管理 |
| member/MemberDetail.vue | `member/memberdetail` | `admin/member/detail`, `member-wallet/*` | — | 01_会员管理 |
| member/MemberAsset.vue | `memberasset` | `admin/member/member-wallet/balance`, lock/unlock/recharge, reset-address | — | 01_会员管理 |
| member/MemberManage.vue⚠ | `membermanage` | `admin/member/page-query`, alter-status, alter-transaction-status | — | 01_会员管理 |
| member/RechargeList.vue | (unrouted) | `admin/legal-wallet-recharge/page` | — | — |
| member/DrawList.vue | (unrouted) | `admin/legal-wallet-withdraw/page` | — | — |
| member/Parnter.vue | (unrouted) | `admin/promotion/member/page-query` | — | — |
| invitation/InvitationRecord.vue | `InvitationRecord` | `admin/invite/management/query` | — | 02_邀请管理 |
| invitation/InvitationRank.vue | `inviteRankList` | `admin/invite/management/rank|update-rank` | — | 02_邀请管理 |
| invitation/SecondRecord.vue | (unrouted) | `admin/promotion/reward/page-query|merge|detail` | — | — |
| ctc/list.vue | `ctcorderlist` | `admin/ctc/order/page-query|detail|confirm|pay|cancel|complete` | — | 03_CTC管理 |
| ctc/acceptor.vue | `ctcacceptorlist` | `admin/ctc/acceptor/page-query|switch` | — | 03_CTC管理 |
| content/AnnounceManage.vue | `announcemanage` | `admin/system/announcement/*` | — | 04_内容管理 |
| content/AddAnnouncement.vue | `content/announceManage/addAnnounce` | same | — | 04_内容管理 |
| content/HelpManage.vue | `helpManage` | `admin/cms/system-help/*` | — | 04_内容管理 |
| content/AddHelpManage.vue | `content/helpManage/addhelpmanage` | same | — | 04_内容管理 |
| content/AddAdvertisement.vue | `advertise` | `admin/cms/system-advertise/*` | — | 04_内容管理 |
| finance/WithdrawalsExamine.vue | `userwithdrawals` | `admin/finance/withdraw-record/page-query|audit-pass|audit-no-pass|remittance|add-transaction-number` | — | 05_财务管理 |
| finance/WithdrawDetail.vue | `withdrawdetail` | withdraw-record/*, `admin/system/coin/all-name-and-unit` | — | 05_财务管理 |
| finance/AuditDetail.vue | `finance/auditdetail` | withdraw-record detail | — | 05_财务管理 |
| finance/ChargeCoinDetail.vue | `changecoindetail` | `admin/finance/member-deposit/page-query` | — | 05_财务管理 |
| finance/TradeDetail.vue | `tradedetail` | `admin/finance/member-transaction/page-query|all` | — | 05_财务管理 |
| finance/FeeManage.vue | `feemanage` | `admin/finance/member-transaction/*`, dividend fee info | — | 05_财务管理 |
| finance/FinanceStatistic.vue | `statistic` | `admin/finance/statistics/fee|recharge-or-withdraw-amount|turnover-all` | — | 05_财务管理 |
| exchange/Order.vue | `bborder` | `admin/exchange/exchange-order/page-query|detail|cancel` | — | 06_币币管理 |
| exchange/OrderDetail.vue | `exchange/bborder/detail` | same | — | 06_币币管理 |
| exchange/Setting.vue | `bbsetting` | `admin/exchange/exchange-coin/*` (page-query, robot-config, start/stop-trader, merge, cancel-all-order…) | — | 06_币币管理 |
| activity/Activity.vue | `activity` | `admin/activity/activity/*`, sign records | — | 07_活动管理 |
| activity/AddActivity.vue | `activity/activity/addActivity` | same | — | 07_活动管理 |
| redenvelope/RedEnvelope.vue | `envelopeList` | `admin/envelope/page-query|detail|receive-detail` | — | 08_红包管理 |
| redenvelope/AddRedEnvelope.vue | `envelope/addEnvelope` | `admin/envelope/add|modify` | — | 08_红包管理 |
| system/Employee.vue | `employee` | `admin/system/employee/*` | — | 09_系统管理 |
| system/AddAuditEmployee.vue | `system/employee/auditEmployee` | same + role/department | — | 09_系统管理 |
| system/Role.vue | `rolemanage` | `admin/system/role/*`, permission/* | — | 09_系统管理 |
| system/Permission.vue | `permission` | `admin/system/permission/*` | — | 09_系统管理 |
| system/Department.vue | `departmanage` | `admin/system/department/*` | — | 09_系统管理 |
| system/Dictionary.vue | `dictionary` | `admin/system/data-dictionary` | — | 09_系统管理 |
| system/AccessLog.vue | `accesslog` | (realName list reused as log — see dead flags) | — | 09_系统管理 |
| system/Coin.vue | `coin` | `admin/system/coin/*`, transfer cold wallet, sms-provider code | — | 09_系统管理 |
| system/AppReversion.vue | `apprevision` | `admin/system/app-revision/page-query` | — | 09_系统管理 |
| system/CoinTransferDetail.vue | `system/coin/transferdetail` | `admin/system/coin/hot-transfer-record/page-query` | — | 09_系统管理 |
| bond/BondManage.vue | `manage` | business-auth + member-wallet pages (mixed) | — | 10_保证金管理 |
| otc/BusinessAudit.vue | `businessaudit` | `admin/business-auth/*` | — | 11_OTC管理 |
| otc/BusinessDetail.vue | `otc/businessaudit/businessdetail` | same | — | 11_OTC管理 |
| otc/ExitBond.vue | `cancelapply` | `admin/business/cancel-apply/*` | — | 11_OTC管理 |
| otc/ExitBondDetail.vue | `otc/adManage/exitbonddetail` | same | — | 11_OTC管理 |
| otc/CoinManage.vue⚠ | `coinManage` | `admin/otc/otc-coin/*` | — | 11_OTC管理 |
| otc/AdManage.vue⚠ | `adManage` | `admin/otc/advertise/*` | — | 11_OTC管理 |
| otc/AdvertiseDetail.vue | `otc/adManage/advertisedetail` | advertise detail, otc order | — | 11_OTC管理 |
| otc/OrderManage.vue⚠ | `orderManage` | `admin/otc/order/*` | — | 11_OTC管理 |
| otc/AppealManage.vue | `appealManage` | `admin/otc/appeal/*` (release-coin, cancel-order) | — | 11_OTC管理 |
| otc/AppealDetail.vue | `otc/appealmanage/appealdetail` | same | — | 11_OTC管理 |
| user/PersonalCenter.vue | `/user/personalcenter` (otherRouter) | `admin/system/employee/detail`, update-password | — | — |
| error-page/403/404/500.vue | `/403`, `*`, `/500` | — | — | — |
| main-components/* (lockscreen, tags, theme-switch…) | chrome of Main.vue | — | — | — |

⚠ = **filename case mismatch**: router requires `MemberManage.vue` / `CoinManage.vue` / `AdManage.vue` / `OrderManage.vue` but files on disk are `memberManage.vue` / `coinManage.vue` / `adManage.vue` / `orderManage.vue`. Works only on case-insensitive macOS/Windows; breaks on Linux builds. Fix casing in the port (or during any re-packaging).

### Dead / vestigial (admin)
- `views/home/components/*` (inforCard, map, visiteVolume, dataSourcePie, toDoListItem, userFlow, serviceRequests, countUp, mapDataTable) — iview-admin template demo parts; home.vue uses only a subset. Verify before deleting, but none are routed.
- `views/own-space/{own-space,message}.vue`, `views/error-page/error-page.vue` wrapper, `views/invitation/SecondRecord.vue`, `views/member/{Parnter,RechargeList,DrawList}.vue` — not referenced by any router entry.
- `src/smeditor/*` rich-text editor is used by content add/edit pages (keep).
- `src/config/api.js` largely duplicated by `src/service/getData.js`; only `common.login` is load-bearing.
- `getData.js` contains a real bug: `'admin/member/alter-publish-advertisement-status\t'` with a trailing tab — Nuxt port should normalize.

---

## C. Port notes for Nuxt 3
1. Front WS is mandatory (DEVELOP.md): two SockJS/STOMP channels — `/market/market-ws` (topics `market/thumb`, `market/trade|trade-plate|order-* per symbol+member`) and `/chat/chat-webSocket` (user queue `/user/{myId}/{orderId}`, send `/app/message/chat`). Nuxt 3 should use a STOMP client plugin; SockJS fallback URL preserved.
2. Auth: front uses `x-auth-token` header + localStorage; admin uses cookie `user` + backend permission menu (`leftSidebarList`). Port to one mechanism (cookie/session recommended) with route middleware replacing both guards.
3. All hardcoded hosts (`api.xxxx.com`, `49.234.13.106:8801`, `rootHost`) must become runtime config — no secrets copied forward.
4. Charting: front needs the TradingView charting_library bundle (vendored, license-limited) or a replacement; admin needs echarts.
5. Screenshot mapping above shows every major screenshot has a live page, except admin `10_保证金管理` (bond) which maps to `bond/BondManage.vue` and front `创新实验室详情` → ActivityDetail.
