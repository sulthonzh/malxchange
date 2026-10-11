# Legacy schema inventory

- 63 tables recovered from 63 @Entity classes (core: 60 shared, exchange-core: 3).
- 1 patch-only tables (no entity class): `admin_role_permission`.
- Naming: Spring Boot 1.5.9 `SpringPhysicalNamingStrategy` — camelCase to snake_case everywhere.
- DDL best-effort: `schema.sql` next to this file. Column types follow Hibernate 5.0 defaults unless `columnDefinition` overrides (verified on `member_wallet`).
- Money columns: `member_wallet.balance`/`frozen_balance` are `decimal(26,16)`, `to_released` `decimal(18,8)` — inconsistent precision, evidence for the money-representation ticket.

| # | table | entity | module | cols | id | notable columns |
|---|---|---|---|---|---|---|
| 1 | `activity` | Activity | core | 39 | `id` bigint | status, type, price |
| 2 | `activity_order` | ActivityOrder | core | 12 | `id` bigint | member_id, type, amount, price, turnover |
| 3 | `admin` | Admin | core | 14 | `id` bigint | status |
| 4 | `admin_access_log` | AdminAccessLog | core | 8 | `id` bigint |  |
| 5 | `admin_permission` | SysPermission | core | 6 | `id` bigint |  |
| 6 | `admin_role` | SysRole | core | 3 | `id` bigint |  |
| 7 | `advertise` | Advertise | core | 26 | `id` bigint | price, member_id, coin_id, status |
| 8 | `announcement` | Announcement | core | 10 | `id` bigint |  |
| 9 | `app_revision` | AppRevision | core | 6 | `id` bigint |  |
| 10 | `appeal` | Appeal | core | 10 | `id` bigint | order_id, status |
| 11 | `business_auth_apply` | BusinessAuthApply | core | 11 | `id` bigint | member_id, amount |
| 12 | `business_auth_deposit` | BusinessAuthDeposit | core | 6 | `id` bigint | coin_id, amount, status |
| 13 | `bussiness_cancel_apply` | BusinessCancelApply | core | 8 | `id` bigint | member_id, status |
| 14 | `coin` | Coin | core | 27 | `name` varchar(255) | status |
| 15 | `country` | Country | core | 6 | `zh_name` varchar(255) |  |
| 16 | `ctc_acceptor` | CtcAcceptor | core | 7 | `id` bigint | member_id, status |
| 17 | `ctc_order` | CtcOrder | core | 22 | `id` bigint | member_id, price, money, amount, status |
| 18 | `data_dictionary` | DataDictionary | core | 6 | `id` bigint |  |
| 19 | `department` | Department | core | 6 | `id` bigint |  |
| 20 | `deposit_record` | DepositRecord | core | 5 | `id` varchar(255) | member_id, coin_id, amount, status |
| 21 | `dividend_start_record` | DividendStartRecord | core | 10 | `id` bigint | amount |
| 22 | `exchange_coin` | ExchangeCoin | exchange-core | 28 | `symbol` varchar(255) | symbol, fee |
| 23 | `exchange_favor_symbol` | FavorSymbol | exchange-core | 4 | `id` bigint | symbol, member_id |
| 24 | `exchange_order` | ExchangeOrder | exchange-core | 16 | `order_id` varchar(255) | order_id, member_id, type, amount, symbol, turnover, status, price |
| 25 | `feedback` | Feedback | core | 4 | `id` bigint | member_id |
| 26 | `financial_item` | FinancialItem | core | 11 | `id` bigint |  |
| 27 | `financial_order` | FinancialOrder | core | 13 | `id` bigint | member_id |
| 28 | `hot_transfer_record` | HotTransferRecord | core | 9 | `id` bigint | amount, balance |
| 29 | `init_plate` | InitPlate | core | 6 | `id` bigint | symbol |
| 30 | `legal_wallet_recharge` | LegalWalletRecharge | core | 12 | `id` bigint | member_id, amount |
| 31 | `legal_wallet_withdraw` | LegalWalletWithdraw | core | 12 | `id` bigint | member_id, amount, status |
| 32 | `member` | Member | core | 43 | `id` bigint | status |
| 33 | `member_address` | MemberAddress | core | 8 | `id` bigint | coin_id, status, member_id |
| 34 | `member_application` | MemberApplication | core | 11 | `id` bigint | member_id |
| 35 | `member_application_config` | MemberApplicationConfig | core | 5 | `id` bigint |  |
| 36 | `member_bonus` | MemberBonusDTO | core | 7 | `id` bigint | member_id, coin_id |
| 37 | `member_deposit` | MemberDeposit | core | 7 | `id` bigint | member_id, amount |
| 38 | `member_invite_stastic` | MemberInviteStastic | core | 13 | `id` bigint | member_id |
| 39 | `member_invite_stastic_rank` | MemberInviteStasticRank | core | 8 | `id` bigint | member_id, type |
| 40 | `member_level` | MemberLevel | core | 4 | `id` bigint |  |
| 41 | `member_promotion` | MemberPromotion | core | 5 | `id` bigint |  |
| 42 | `member_sign_record` | MemberSignRecord | core | 6 | `id` bigint | member_id, amount |
| 43 | `member_transaction` | MemberTransaction | core | 11 | `id` bigint | member_id, amount, type, symbol, fee |
| 44 | `member_wallet` | MemberWallet | core | 9 | `id` bigint | member_id, coin_id, balance |
| 45 | `mining_order` | MiningOrder | core | 18 | `id` bigint | type, member_id |
| 46 | `mining_order_detail` | MiningOrderDetail | core | 6 | `id` bigint | member_id |
| 47 | `otc_coin` | OtcCoin | core | 10 | `id` bigint | status |
| 48 | `otc_order` | Order | core | 30 | `id` bigint | member_id, coin_id, price, money, status |
| 49 | `promotion_card` | PromotionCard | core | 15 | `id` bigint | member_id, coin_id, amount |
| 50 | `promotion_card_order` | PromotionCardOrder | core | 9 | `id` bigint | member_id, amount |
| 51 | `red_envelope` | RedEnvelope | core | 19 | `id` bigint | member_id, type |
| 52 | `red_envelope_detail` | RedEnvelopeDetail | core | 7 | `id` bigint | member_id, amount |
| 53 | `reward_activity_setting` | RewardActivitySetting | core | 7 | `id` bigint | coin_id, status, type |
| 54 | `reward_promotion_setting` | RewardPromotionSetting | core | 8 | `id` bigint | coin_id, status, type |
| 55 | `reward_record` | RewardRecord | core | 7 | `id` bigint | coin_id, type, amount, member_id |
| 56 | `sign` | Sign | core | 6 | `id` bigint | amount, status |
| 57 | `sys_advertise` | SysAdvertise | core | 14 | `serial_number` varchar(255) | status |
| 58 | `sys_help` | SysHelp | core | 11 | `id` bigint | status |
| 59 | `tb_sms` | SmsDTO | core | 7 | `id` bigint |  |
| 60 | `transfer_address` | TransferAddress | core | 6 | `id` bigint | coin_id, status |
| 61 | `transfer_record` | TransferRecord | core | 9 | `id` bigint | amount, member_id, coin_id, fee |
| 62 | `website_information` | WebsiteInformation | core | 11 | `id` bigint |  |
| 63 | `withdraw_record` | WithdrawRecord | core | 15 | `id` bigint | member_id, coin_id, fee, status |
| 64 | `admin_role_permission` | (patch SQL) | patch | 11 | see schema.sql | |
