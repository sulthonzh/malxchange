-- Legacy CoinExchange schema, recovered from Hibernate entities (Spring Boot 1.5.9,
-- SpringPhysicalNamingStrategy: camelCase -> snake_case on implicit AND explicit names).
-- Best-effort DDL equivalent of what ddl-auto=update generated on MySQL 5.5.
-- 63 entity tables + 1 patch-only tables; entities: core + exchange-core.

-- Activity (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Activity.java)
CREATE TABLE `activity` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `status` integer,
  `step` integer,
  `progress` integer,
  `start_time` varchar(30) default '2000-01-01 01:00:00'  comment '开始时间',
  `end_time` varchar(30) default '2000-01-01 01:00:00'  comment '结束时间',
  `type` integer,
  `levelone_count` integer,
  `total_supply` decimal(24,8) DEFAULT 0 ,
  `traded_amount` decimal(24,8) DEFAULT 0 ,
  `freeze_amount` decimal(26,8) DEFAULT 0 ,
  `price` decimal(18,8) DEFAULT 0 ,
  `price_scale` integer,
  `unit` varchar(255),
  `accept_unit` varchar(255),
  `amount_scale` integer,
  `max_limit_amout` decimal(24,8) DEFAULT 0 ,
  `min_limit_amout` decimal(24,8) DEFAULT 0 ,
  `hold_limit` decimal(24,8) DEFAULT 0 ,
  `hold_unit` varchar(255),
  `limit_times` integer,
  `mining_period` integer,
  `mining_days` integer,
  `mining_daysprofit` decimal(24,8) DEFAULT 0 ,
  `mining_unit` varchar(255),
  `mining_invite` decimal(24,8) DEFAULT 0 ,
  `mining_invitelimit` decimal(24,8) DEFAULT 0 ,
  `settings` varchar(255),
  `title` varchar(255),
  `title_en` varchar(255),
  `detail` varchar(255),
  `detail_en` varchar(255),
  `content` TEXT,
  `content_en` TEXT,
  `small_image_url` varchar(255),
  `banner_image_url` varchar(255),
  `notice_link` varchar(255),
  `activity_link` varchar(255),
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- ActivityOrder (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/ActivityOrder.java)
CREATE TABLE `activity_order` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `activity_id` bigint,
  `member_id` bigint,
  `state` integer,
  `type` integer,
  `freeze_amount` decimal(24,8) DEFAULT 0 ,
  `amount` decimal(24,8) DEFAULT 0 ,
  `price` decimal(24,8) DEFAULT 0 ,
  `turnover` decimal(26,16) DEFAULT 0 ,
  `coin_symbol` varchar(255),
  `base_symbol` varchar(255),
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Admin (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Admin.java)
CREATE TABLE `admin` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `username` varchar(255) UNIQUE NOT NULL,
  `password` varchar(255),
  `enable` integer,
  `last_login_time` datetime,
  `last_login_ip` varchar(255),
  `role_id` bigint,
  `department_id` bigint,
  `real_name` varchar(255),
  `mobile_phone` varchar(255),
  `qq` varchar(255),
  `email` varchar(255),
  `avatar` varchar(255),
  `status` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- AdminAccessLog (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/AdminAccessLog.java)
CREATE TABLE `admin_access_log` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `admin_id` bigint,
  `uri` varchar(255),
  `module` integer,
  `operation` varchar(255),
  `access_ip` varchar(255),
  `access_method` varchar(255),
  `access_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- SysPermission (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/SysPermission.java)
CREATE TABLE `admin_permission` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `title` varchar(255),
  `description` varchar(255),
  `parent_id` bigint,
  `sort` integer,
  `name` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- SysRole (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/SysRole.java)
CREATE TABLE `admin_role` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `role` varchar(255),
  `description` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Advertise (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Advertise.java)
CREATE TABLE `advertise` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `price` decimal(18,2) comment '交易价格',
  `advertise_type` integer,
  `member_id` bigint,
  `create_time` datetime,
  `update_time` datetime,
  `coin_id` bigint,
  `country` bigint,
  `min_limit` decimal(18,2) comment '最低单笔交易额',
  `max_limit` decimal(18,2) comment '最高单笔交易额',
  `remark` varchar(255),
  `time_limit` integer,
  `premise_rate` decimal(18,6) comment '溢价百分比',
  `level` integer,
  `pay_mode` varchar(255),
  `version` bigint,
  `status` integer,
  `price_type` integer,
  `number` decimal(18,8) comment '计划数量',
  `deal_amount` decimal(18,8) comment '交易中数量',
  `remain_amount` decimal(18,8) comment '计划剩余数量',
  `auto` integer,
  `autoword` varchar(255),
  `limit_money` varchar(255),
  `username` varchar(255),
  `coin_unit` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Announcement (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Announcement.java)
CREATE TABLE `announcement` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `title` varchar(255),
  `content` TEXT,
  `create_time` datetime,
  `announcement_classification` integer,
  `is_show` bit(1),
  `lang` varchar(255),
  `is_top` varchar(255),
  `img_url` varchar(255),
  `sort` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- AppRevision (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/AppRevision.java)
CREATE TABLE `app_revision` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `publish_time` datetime,
  `remark` varchar(255),
  `version` varchar(255),
  `download_url` varchar(255),
  `platform` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Appeal (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Appeal.java)
CREATE TABLE `appeal` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `order_id` bigint,
  `create_time` datetime,
  `deal_with_time` datetime,
  `remark` varchar(500),
  `initiator_id` bigint,
  `associate_id` bigint,
  `is_success` integer,
  `status` integer,
  `admin_id` bigint
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- BusinessAuthApply (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/BusinessAuthApply.java)
CREATE TABLE `business_auth_apply` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `certified_business_status` integer,
  `detail` varchar(255),
  `create_time` datetime,
  `auditing_time` datetime,
  `auth_info` TEXT,
  `business_auth_deposit_id` bigint,
  `deposit_record_id` varchar(255),
  `amount` decimal(19,2),
  `update_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- BusinessAuthDeposit (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/BusinessAuthDeposit.java)
CREATE TABLE `business_auth_deposit` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `coin_id` bigint,
  `amount` decimal(18,8) comment '保证金数额',
  `create_time` datetime,
  `admin_id` bigint,
  `status` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- BusinessCancelApply (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/BusinessCancelApply.java)
CREATE TABLE `bussiness_cancel_apply` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `status` integer,
  `deposit_record_id` varchar(255),
  `cancel_apply_time` datetime,
  `handle_time` datetime,
  `reason` varchar(255),
  `detail` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Coin (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Coin.java)
CREATE TABLE `coin` (
  `name` varchar(255) PRIMARY KEY,
  `name_cn` varchar(255),
  `unit` varchar(255),
  `status` integer,
  `min_tx_fee` integer,
  `cny_rate` decimal(18,2) default 0.00 comment '人民币汇率',
  `max_tx_fee` integer,
  `usd_rate` decimal(18,2) default 0.00 comment '美元汇率',
  `enable_rpc` integer,
  `sort` integer,
  `can_withdraw` integer,
  `can_recharge` integer,
  `can_transfer` integer,
  `can_auto_withdraw` integer,
  `withdraw_threshold` decimal(18,8) comment '提现阈值',
  `min_withdraw_amount` decimal(18,8) comment '最小提币数量',
  `max_withdraw_amount` decimal(18,8) comment '最大提币数量',
  `min_recharge_amount` decimal(18,8) comment '最小充值数量',
  `is_platform_coin` integer,
  `has_legal` bit default 0 NOT NULL,
  `cold_wallet_address` varchar(255),
  `miner_fee` decimal(18,8) default 0 comment '矿工费',
  `withdraw_scale` int default 4 comment '提币精度',
  `infolink` varchar(255),
  `information` varchar(255),
  `account_type` int default 0 comment '币种账户类型',
  `deposit_address` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Country (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Country.java)
CREATE TABLE `country` (
  `zh_name` varchar(255) PRIMARY KEY,
  `en_name` varchar(255),
  `area_code` varchar(255),
  `language` varchar(255),
  `local_currency` varchar(255),
  `sort` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- CtcAcceptor (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/CtcAcceptor.java)
CREATE TABLE `ctc_acceptor` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `usdt_out` decimal(18,2) default 0.00 comment '售出USDT',
  `usdt_in` decimal(18,2) default 0.00 comment '买入USDT',
  `cny_out` decimal(18,2) default 0.00 comment '售出CNY',
  `cny_in` decimal(18,2) default 0.00 comment '买入CNY',
  `status` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- CtcOrder (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/CtcOrder.java)
CREATE TABLE `ctc_order` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `order_sn` varchar(255) UNIQUE NOT NULL,
  `member_id` bigint,
  `real_name` varchar(255),
  `acceptor_id` bigint,
  `price` decimal(18,2) comment '价格',
  `money` decimal(18,2) comment '交易金额',
  `amount` decimal(19,2),
  `unit` varchar(255),
  `status` integer,
  `confirm_time` datetime,
  `cancel_reason` varchar(255),
  `remark` varchar(255),
  `cancel_time` datetime,
  `pay_time` datetime,
  `complete_time` datetime,
  `direction` integer,
  `pay_mode` varchar(255),
  `alipay` integer,
  `bank_info` integer,
  `wechat_pay` integer,
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- DataDictionary (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/DataDictionary.java)
CREATE TABLE `data_dictionary` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `bond` varchar(255),
  `value` varchar(255),
  `comment` varchar(255),
  `creation_time` datetime,
  `update_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Department (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Department.java)
CREATE TABLE `department` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `name` varchar(255) UNIQUE NOT NULL,
  `remark` varchar(255),
  `leader_id` bigint,
  `create_time` datetime,
  `update_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- DepositRecord (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/DepositRecord.java)
CREATE TABLE `deposit_record` (
  `id` varchar(255) PRIMARY KEY,
  `member_id` bigint,
  `coin_id` bigint,
  `amount` decimal(19,2),
  `status` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- DividendStartRecord (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/DividendStartRecord.java)
CREATE TABLE `dividend_start_record` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `start` bigint,
  `start_date` datetime,
  `end` bigint,
  `end_date` datetime,
  `date` datetime,
  `unit` varchar(255),
  `rate` decimal(18,2) comment '比例',
  `amount` decimal(18,6) comment '数量',
  `admin_id` bigint
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- ExchangeCoin (exchange-core: 00_framework/exchange-core/src/main/java/com/bizzan/bitrade/entity/ExchangeCoin.java)
CREATE TABLE `exchange_coin` (
  `symbol` varchar(255) PRIMARY KEY,
  `coin_symbol` varchar(255),
  `base_symbol` varchar(255),
  `enable` integer,
  `fee` decimal(8,4) comment '交易手续费',
  `sort` integer,
  `coin_scale` integer,
  `base_coin_scale` integer,
  `min_sell_price` decimal(18,8) default 0 comment '卖单最低价格',
  `max_buy_price` decimal(18,8) default 0 comment '最高买单价',
  `enable_market_sell` int(11) default 1 comment '是否启用市价卖',
  `enable_market_buy` int(11) default 1 comment '是否启用市价买',
  `max_trading_time` int(11) default 0 comment '委托超时自动下架时间，单位为秒，0表示不过期',
  `max_trading_order` int(11) default 0 comment '最大允许同时交易的订单数，0表示不限制',
  `robot_type` int(11) default 0 comment '机器人类型',
  `flag` int(11) default 0 ,
  `min_turnover` decimal(18,8) default 0 comment '最小成交额',
  `zone` int(11) default 0 ,
  `min_volume` decimal(18,8) default 0 comment '最小下单量',
  `max_volume` decimal(18,8) default 0 comment '最大下单量',
  `publish_type` int(11) default 1  comment '发行活动类型 1:无活动,2:抢购发行,3:分摊发行',
  `start_time` varchar(30) default '2000-01-01 01:00:00'  comment '开始时间',
  `end_time` varchar(30) default '2000-01-01 01:00:00'  comment '结束时间',
  `clear_time` varchar(30) default '2000-01-01 01:00:00'  comment '清盘时间',
  `publish_price` decimal(18,8) default 0 comment ' 分摊发行价格',
  `publish_amount` decimal(18,8) default 0 comment ' 活动发行数量',
  `visible` int(11) default 1 comment ' 前台可见状态',
  `exchangeable` int(11) default 1 comment ' 是否可交易'
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- FavorSymbol (exchange-core: 00_framework/exchange-core/src/main/java/com/bizzan/bitrade/entity/FavorSymbol.java)
CREATE TABLE `exchange_favor_symbol` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `symbol` varchar(255),
  `member_id` bigint,
  `add_time` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- ExchangeOrder (exchange-core: 00_framework/exchange-core/src/main/java/com/bizzan/bitrade/entity/ExchangeOrder.java)
CREATE TABLE `exchange_order` (
  `order_id` varchar(255) PRIMARY KEY,
  `member_id` bigint,
  `type` integer,
  `amount` decimal(18,8) DEFAULT 0 ,
  `symbol` varchar(255),
  `traded_amount` decimal(26,16) DEFAULT 0 ,
  `turnover` decimal(26,16) DEFAULT 0 ,
  `coin_symbol` varchar(255),
  `base_symbol` varchar(255),
  `status` integer,
  `direction` integer,
  `price` decimal(18,8) DEFAULT 0 ,
  `time` bigint,
  `completed_time` bigint,
  `canceled_time` bigint,
  `use_discount` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Feedback (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Feedback.java)
CREATE TABLE `feedback` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `create_time` datetime,
  `remark` varchar(255),
  `member_id` bigint
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- FinancialItem (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/FinancialItem.java)
CREATE TABLE `financial_item` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `item_id` varchar(255),
  `item_name` varchar(255),
  `item_desc` varchar(255),
  `yield` double,
  `deadline` integer,
  `coin_minnum` decimal(19,2),
  `coin_name` varchar(255),
  `create_time` datetime,
  `update_time` datetime,
  `item_state` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- FinancialOrder (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/FinancialOrder.java)
CREATE TABLE `financial_order` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `order_no` bigint,
  `member_id` bigint,
  `item_id` varchar(255),
  `coin_name` varchar(255),
  `create_time` datetime,
  `coin_num` decimal(19,2),
  `update_time` datetime,
  `order_state` integer,
  `frozen_days` integer,
  `order_usdt_rate` double,
  `plan_revenue_time` datetime,
  `real_income` decimal(19,2)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- HotTransferRecord (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/HotTransferRecord.java)
CREATE TABLE `hot_transfer_record` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `admin_id` bigint,
  `transfer_time` datetime,
  `amount` decimal(18,8) default 0 comment '转账金额',
  `balance` decimal(18,8) default 0 comment '热钱包余额',
  `miner_fee` decimal(18,8) default 0 comment '矿工费',
  `unit` varchar(255),
  `cold_address` varchar(255),
  `transaction_number` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- InitPlate (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/InitPlate.java)
CREATE TABLE `init_plate` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `symbol` varchar(255),
  `final_price` varchar(255),
  `init_price` varchar(255),
  `relative_time` varchar(255),
  `interference_factor` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- LegalWalletRecharge (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/LegalWalletRecharge.java)
CREATE TABLE `legal_wallet_recharge` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `amount` decimal(18,2) comment '充值金额',
  `payment_instrument` varchar(255),
  `coin_name` bigint,
  `state` integer,
  `pay_mode` integer,
  `remark` varchar(255),
  `creation_time` datetime,
  `deal_time` datetime,
  `admin_id` bigint,
  `update_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- LegalWalletWithdraw (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/LegalWalletWithdraw.java)
CREATE TABLE `legal_wallet_withdraw` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `amount` decimal(18,8) comment '申请总数量',
  `create_time` datetime,
  `deal_time` datetime,
  `status` integer,
  `admin_id` bigint,
  `remark` varchar(255),
  `pay_mode` integer,
  `coin_name` bigint,
  `payment_instrument` varchar(255),
  `remit_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Member (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Member.java)
CREATE TABLE `member` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `salt` varchar(255),
  `username` varchar(255) UNIQUE,
  `password` varchar(255),
  `margin` varchar(255),
  `google_key` varchar(255),
  `google_state` integer,
  `google_date` datetime,
  `jy_password` varchar(255),
  `real_name` varchar(255),
  `id_number` varchar(255),
  `email` varchar(255) UNIQUE,
  `mobile_phone` varchar(255) UNIQUE,
  `location` integer,
  `member_level` integer,
  `status` integer,
  `registration_time` datetime,
  `last_login_time` datetime,
  `token` varchar(255),
  `super_partner` varchar(255),
  `transactions` integer,
  `bank_info` integer,
  `alipay` integer,
  `wechat_pay` integer,
  `appeal_times` integer,
  `appeal_success_times` integer,
  `inviter_id` bigint,
  `promotion_code` varchar(255),
  `first_level` integer,
  `second_level` integer,
  `third_level` integer,
  `local` bigint,
  `real_name_status` integer,
  `login_count` integer,
  `certified_business_status` integer,
  `certified_business_apply_time` datetime,
  `application_time` datetime,
  `certified_business_check_time` datetime,
  `avatar` varchar(255),
  `token_expire_time` datetime,
  `publish_advertise` integer,
  `transaction_status` integer,
  `sign_in_ability` bit default 1 NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberAddress (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberAddress.java)
CREATE TABLE `member_address` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `create_time` datetime,
  `delete_time` datetime,
  `coin_id` bigint,
  `address` varchar(255),
  `status` integer,
  `member_id` bigint,
  `remark` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberApplication (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberApplication.java)
CREATE TABLE `member_application` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `real_name` varchar(255),
  `id_card` varchar(255),
  `identity_card_img_front` varchar(255),
  `identity_card_img_reverse` varchar(255),
  `identity_card_img_in_hand` varchar(255),
  `audit_status` integer,
  `member_id` bigint,
  `reject_reason` varchar(255),
  `create_time` datetime,
  `update_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberApplicationConfig (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberApplicationConfig.java)
CREATE TABLE `member_application_config` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `withdraw_coin_on` integer,
  `recharge_coin_on` integer,
  `promotion_on` integer,
  `transaction_on` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberBonusDTO (core: 00_framework/core/src/main/java/com/bizzan/bitrade/dto/MemberBonusDTO.java)
CREATE TABLE `member_bonus` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `have_time` varchar(255),
  `arrive_time` varchar(255),
  `total` decimal(18,8) comment '当天总手续费',
  `mem_bouns` decimal(18,8) comment '分红数量',
  `coin_id` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberDeposit (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberDeposit.java)
CREATE TABLE `member_deposit` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `amount` decimal(18,8) default 0,
  `unit` varchar(255),
  `create_time` datetime,
  `txid` varchar(255),
  `address` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberInviteStastic (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberInviteStastic.java)
CREATE TABLE `member_invite_stastic` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `user_identify` varchar(255),
  `btc_reward` decimal(24,8) DEFAULT 0 ,
  `eth_reward` decimal(24,8) DEFAULT 0 ,
  `usdt_reward` decimal(24,8) DEFAULT 0 ,
  `other_reward` varchar(255),
  `estimated_reward` decimal(24,8) DEFAULT 0 ,
  `extra_reward` decimal(24,8) DEFAULT 0 ,
  `level_one` integer,
  `level_two` integer,
  `is_robot` integer,
  `stastic_date` varchar(30) default '2000-01-01 01:00:00'  comment '统计日期'
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberInviteStasticRank (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberInviteStasticRank.java)
CREATE TABLE `member_invite_stastic_rank` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `user_identify` varchar(255),
  `level_one` integer,
  `level_two` integer,
  `type` integer,
  `is_robot` integer,
  `stastic_date` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberLevel (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberLevel.java)
CREATE TABLE `member_level` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `name` varchar(255),
  `is_default` bit(1),
  `remark` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberPromotion (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberPromotion.java)
CREATE TABLE `member_promotion` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `inviter_id` bigint,
  `invitees_id` bigint,
  `level` integer,
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberSignRecord (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberSignRecord.java)
CREATE TABLE `member_sign_record` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `sign_id` bigint,
  `coin_name` bigint,
  `amount` decimal(19,2),
  `creation_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberTransaction (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberTransaction.java)
CREATE TABLE `member_transaction` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `amount` decimal(26,16) comment '充币金额',
  `create_time` datetime,
  `type` integer,
  `symbol` varchar(255),
  `address` varchar(255),
  `fee` decimal(26,16),
  `flag` int default 0 NOT NULL,
  `real_fee` varchar(255),
  `discount_fee` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MemberWallet (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MemberWallet.java)
CREATE TABLE `member_wallet` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `coin_id` bigint,
  `balance` decimal(26,16) comment '可用余额',
  `frozen_balance` decimal(26,16) comment '冻结余额',
  `to_released` decimal(18,8) comment '待释放总量',
  `address` varchar(255),
  `version` integer,
  `is_lock` int default 0 comment '钱包不是锁定'
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MiningOrder (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MiningOrder.java)
CREATE TABLE `mining_order` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `type` integer,
  `image` varchar(255),
  `title` varchar(255),
  `member_id` bigint,
  `activity_id` bigint,
  `period` integer,
  `mining_status` integer,
  `mining_days` integer,
  `mininged_days` integer,
  `mining_unit` varchar(255),
  `mining_daysprofit` decimal(18,8) comment '矿机原始日产出',
  `current_daysprofit` decimal(18,8) comment '矿机当前日产出',
  `total_profit` decimal(18,8) comment '矿机总产出',
  `mining_invite` decimal(24,8) DEFAULT 0 ,
  `mining_invitelimit` decimal(24,8) DEFAULT 0 ,
  `end_time` varchar(30) default '2000-01-01 01:00:00'  comment '结束时间',
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- MiningOrderDetail (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/MiningOrderDetail.java)
CREATE TABLE `mining_order_detail` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `mining_order_id` bigint,
  `output` decimal(18,8) comment '矿机当期产出',
  `mining_unit` varchar(255),
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- OtcCoin (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/OtcCoin.java)
CREATE TABLE `otc_coin` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `name` varchar(255),
  `name_cn` varchar(255),
  `unit` varchar(255),
  `status` integer,
  `jy_rate` decimal(18,6) comment '交易手续费率',
  `sell_min_amount` decimal(18,8) comment '卖出广告最低发布数量',
  `buy_min_amount` decimal(18,8) comment '买入广告最低发布数量',
  `sort` integer,
  `is_platform_coin` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Order (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Order.java)
CREATE TABLE `otc_order` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `order_sn` varchar(255) UNIQUE NOT NULL,
  `advertise_type` integer,
  `create_time` datetime,
  `member_id` bigint,
  `member_name` varchar(255),
  `member_real_name` varchar(255),
  `customer_id` bigint,
  `customer_name` varchar(255),
  `customer_real_name` varchar(255),
  `coin_id` bigint,
  `price` decimal(18,2) comment '价格',
  `max_limit` decimal(18,2) comment '最高交易额',
  `country` varchar(255),
  `min_limit` decimal(18,2) comment '最低交易额',
  `remark` varchar(255),
  `time_limit` integer,
  `money` decimal(18,2) comment '交易金额',
  `number` decimal(18,8) comment '交易数量',
  `commission` decimal(18,8) comment '手续费',
  `status` integer,
  `pay_time` datetime,
  `pay_mode` varchar(255),
  `advertise_id` bigint,
  `cancel_time` datetime,
  `release_time` datetime,
  `alipay` integer,
  `bank_info` integer,
  `wechat_pay` integer,
  `version` bigint
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- PromotionCard (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/PromotionCard.java)
CREATE TABLE `promotion_card` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `is_enabled` integer,
  `card_name` varchar(255),
  `card_no` varchar(255) UNIQUE NOT NULL,
  `card_desc` varchar(255),
  `coin_id` bigint,
  `count` integer,
  `exchange_count` integer,
  `amount` decimal(18,8) comment '单个卡券金额',
  `total_amount` decimal(18,8) comment '所有卡券总金额',
  `is_free` integer,
  `is_lock` integer,
  `lock_days` integer,
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- PromotionCardOrder (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/PromotionCardOrder.java)
CREATE TABLE `promotion_card_order` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `card_id` bigint,
  `member_id` bigint,
  `is_free` integer,
  `is_lock` integer,
  `lock_days` integer,
  `state` integer,
  `amount` decimal(18,8) comment '兑换金额',
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- RedEnvelope (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/RedEnvelope.java)
CREATE TABLE `red_envelope` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `envelope_no` varchar(255) UNIQUE NOT NULL,
  `member_id` bigint,
  `type` integer,
  `invite` integer,
  `plateform` integer,
  `max_rand` decimal(18,8) comment '最大随机领取额',
  `total_amount` decimal(18,8) comment '红包总额',
  `receive_amount` decimal(18,8) comment '领取总额',
  `count` integer,
  `logo_image` varchar(255),
  `bg_image` varchar(255),
  `name` varchar(255),
  `detail` TEXT,
  `unit` varchar(255),
  `receive_count` integer,
  `state` integer,
  `expired_hours` integer,
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- RedEnvelopeDetail (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/RedEnvelopeDetail.java)
CREATE TABLE `red_envelope_detail` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `envelope_id` bigint,
  `amount` decimal(18,8) comment '领取数额',
  `cate` integer,
  `create_time` datetime,
  `user_identify` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- RewardActivitySetting (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/RewardActivitySetting.java)
CREATE TABLE `reward_activity_setting` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `coin_id` bigint,
  `status` integer,
  `type` integer,
  `info` varchar(255),
  `update_time` datetime,
  `admin_id` bigint
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- RewardPromotionSetting (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/RewardPromotionSetting.java)
CREATE TABLE `reward_promotion_setting` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `coin_id` bigint,
  `status` integer,
  `type` integer,
  `info` varchar(255),
  `effective_time` integer,
  `update_time` datetime,
  `admin_id` bigint
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- RewardRecord (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/RewardRecord.java)
CREATE TABLE `reward_record` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `coin_id` bigint,
  `remark` varchar(255),
  `type` integer,
  `amount` decimal(18,8) comment '数目',
  `member_id` bigint,
  `create_time` datetime
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- Sign (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/Sign.java)
CREATE TABLE `sign` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `coin_name` bigint,
  `amount` decimal(19,2),
  `end_date` datetime,
  `creation_time` datetime,
  `status` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- SysAdvertise (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/SysAdvertise.java)
CREATE TABLE `sys_advertise` (
  `serial_number` varchar(255) PRIMARY KEY,
  `name` varchar(255),
  `sys_advertise_location` integer,
  `lang` varchar(255),
  `start_time` varchar(255),
  `end_time` varchar(255),
  `url` varchar(255),
  `link_url` varchar(255),
  `remark` varchar(255),
  `status` integer,
  `create_time` datetime,
  `content` TEXT,
  `author` varchar(255),
  `sort` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- SysHelp (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/SysHelp.java)
CREATE TABLE `sys_help` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `title` varchar(255),
  `sys_help_classification` integer,
  `img_url` varchar(255),
  `create_time` datetime,
  `status` integer,
  `content` TEXT,
  `author` varchar(255),
  `sort` integer,
  `is_top` varchar(255),
  `lang` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- SmsDTO (core: 00_framework/core/src/main/java/com/bizzan/bitrade/dto/SmsDTO.java)
CREATE TABLE `tb_sms` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `key_id` varchar(255),
  `key_secret` varchar(255),
  `sign_id` varchar(255),
  `template_id` varchar(255),
  `sms_status` varchar(255),
  `sms_name` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- TransferAddress (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/TransferAddress.java)
CREATE TABLE `transfer_address` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `coin_id` bigint,
  `address` varchar(255),
  `transfer_fee` decimal(18,6) comment '转账手续费率',
  `min_amount` decimal(18,2) comment '最低转账数目',
  `status` integer
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- TransferRecord (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/TransferRecord.java)
CREATE TABLE `transfer_record` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `amount` decimal(19,2),
  `member_id` bigint,
  `coin_id` bigint,
  `create_time` datetime,
  `fee` decimal(18,8) comment '手续费',
  `order_sn` varchar(255),
  `address` varchar(255),
  `remark` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- WebsiteInformation (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/WebsiteInformation.java)
CREATE TABLE `website_information` (
  `id` bigint PRIMARY KEY,
  `name` varchar(255),
  `logo` varchar(255),
  `address_icon` varchar(255),
  `url` varchar(255),
  `keywords` varchar(255),
  `description` varchar(255),
  `copyright` varchar(255),
  `postcode` varchar(255),
  `contact` varchar(255),
  `other_information` TEXT
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- WithdrawRecord (core: 00_framework/core/src/main/java/com/bizzan/bitrade/entity/WithdrawRecord.java)
CREATE TABLE `withdraw_record` (
  `id` bigint PRIMARY KEY AUTO_INCREMENT,
  `member_id` bigint,
  `coin_id` bigint,
  `total_amount` decimal(18,8) comment '申请总数量',
  `fee` decimal(18,8) comment '手续费',
  `arrived_amount` decimal(18,8) comment '预计到账数量',
  `create_time` datetime,
  `deal_time` datetime,
  `status` integer,
  `is_auto` integer,
  `admin_id` bigint,
  `can_auto_withdraw` integer,
  `transaction_number` varchar(255),
  `address` varchar(255),
  `remark` varchar(255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

-- ---- patch-only tables (no @Entity; authoritative DDL from patch SQL) ----

-- from 00_framework/sql/db_patch.sql
CREATE TABLE `admin_role_permission` (
  `role_id` bigint(20) NOT NULL,,
  `rule_id` bigint(20) NOT NULL,,
  UNIQUE KEY `UKplesprlvm1sob8nl9yc5rgh3m` (`role_id`,`rule_id`),,
  KEY `FK52rddd3qje4p49iubt08gplb5` (`role_id`) USING BTREE,,
  KEY `FKqf3fhgl5mjqqb0jeupx7yafh0` (`rule_id`) USING BTREE,,
  CONSTRAINT `admin_role_permission_ibfk_1` FOREIGN KEY (`role_id`) REFERENCES `admin_role` (`id`),,
  CONSTRAINT `admin_role_permission_ibfk_2` FOREIGN KEY (`rule_id`) REFERENCES `admin_permission` (`id`),,
  CONSTRAINT `admin_role_permission_ibfk_3` FOREIGN KEY (`role_id`) REFERENCES `admin_role` (`id`),,
  CONSTRAINT `admin_role_permission_ibfk_4` FOREIGN KEY (`rule_id`) REFERENCES `admin_permission` (`id`),,
  CONSTRAINT `FK52rddd3qje4p49iubt08gplb5` FOREIGN KEY (`role_id`) REFERENCES `admin_role` (`id`),,
  CONSTRAINT `FKqf3fhgl5mjqqb0jeupx7yafh0` FOREIGN KEY (`rule_id`) REFERENCES `admin_permission` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

