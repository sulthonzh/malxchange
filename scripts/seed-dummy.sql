-- malxchange dummy seed data for fresh installs
-- Usage: psql -U malxchange -d malxchange -f seed-dummy.sql
--
-- Creates:
--   - Admin account: admin@example.com / Admin123!
--   - 5 demo traders: trader1@example.com ... trader5@example.com
--     passwords: Trader1123! ... Trader5123!
--   - Each trader funded with 10 BTC + 1,000,000 USDT
--   - Sample deposit records, withdraw addresses, favorites
--
-- NOTE: Change all passwords after install!

BEGIN;

-- Admin account (password: Admin123!)
INSERT INTO admin.employees (id, email, password_hash, role_id, enabled, must_rotate)
VALUES (
  gen_random_uuid(),
  'admin@example.com',
  '$2b$12$2lmse6qybYZ96RE6HexSn.O/U9ZJyfpVByltaSkyvDV45A59uSgai',
  (SELECT id FROM admin.roles WHERE name = 'super-admin' LIMIT 1),
  true,
  true
)
ON CONFLICT (email) DO NOTHING;

-- Demo traders with real bcrypt hashes
INSERT INTO api.members (id, email, password_hash, created_at) VALUES
  (gen_random_uuid(), 'trader1@example.com', '$2b$12$qJRruuHesL.Ne9/ZT.yNfecljmVfbwS9OzwyydDMN9WDYqTE4exQ.', now()),
  (gen_random_uuid(), 'trader2@example.com', '$2b$12$I39RyAv1VSec9uNre8YZJesGeL9U0a2yyCjt0Ijv7JKfr8LK0a.cy', now()),
  (gen_random_uuid(), 'trader3@example.com', '$2b$12$G1eMIbNLAGl8e6UGfpSNquDcc.oyt2nvAp16r6xDcLyMqdG8wswKS', now()),
  (gen_random_uuid(), 'trader4@example.com', '$2b$12$.rKTErDqoKW3VwhaAPtvBOFxHh65Ls15CxjtjU9XLlEWICae.IvmS', now()),
  (gen_random_uuid(), 'trader5@example.com', '$2b$12$XqSelsVMHJD93wP5l6HkJeuGW8vkOXApjJ0JPumu2BNaZA.ivo4.6', now())
ON CONFLICT (email) DO NOTHING;

-- Fund traders: 10 BTC + 1,000,000 USDT each
INSERT INTO wallet.ledger_accounts (member_id, coin, available)
SELECT id, 'BTC', 10 FROM api.members WHERE email LIKE 'trader%@example.com'
ON CONFLICT (member_id, coin) DO UPDATE SET available = 10;

INSERT INTO wallet.ledger_accounts (member_id, coin, available)
SELECT id, 'USDT', 1000000 FROM api.members WHERE email LIKE 'trader%@example.com'
ON CONFLICT (member_id, coin) DO UPDATE SET available = 1000000;

-- Journal entries for reconciliation (keeps Ledger == Journal invariant)
INSERT INTO wallet.journal (id, member_id, coin, kind, avail_delta, amount, reason, idem_key)
SELECT gen_random_uuid(), member_id, coin, 'adjustment', available, available, 'dummy seed', 'dummy-seed-' || member_id || '-' || coin
FROM wallet.ledger_accounts
WHERE member_id IN (SELECT id FROM api.members WHERE email LIKE 'trader%@example.com')
ON CONFLICT DO NOTHING;

-- Sample deposit records
INSERT INTO wallet.deposits (id, coin, member_id, address, txid, amount, detected_height, confirmations, status, credited_at)
SELECT gen_random_uuid(), 'BTC', id, 'tb1qdummy' || substr(id::text, 1, 8),
       encode(gen_random_bytes(32), 'hex'), 10, 100, 6, 'credited', now()
FROM api.members WHERE email LIKE 'trader%@example.com'
ON CONFLICT DO NOTHING;

-- Sample withdraw addresses
INSERT INTO api.withdraw_addresses (id, member_id, coin, address, label)
SELECT gen_random_uuid()::text, id, 'BTC', 'tb1qwithdraw' || substr(id::text, 1, 8), 'My wallet'
FROM api.members WHERE email LIKE 'trader%@example.com'
ON CONFLICT DO NOTHING;

-- Sample favorites
INSERT INTO api.market_favorites (member_id, market)
SELECT id, 'BTC/USDT' FROM api.members WHERE email LIKE 'trader%@example.com'
ON CONFLICT DO NOTHING;

COMMIT;

-- Credentials summary
SELECT 'Admin: admin@example.com / Admin123!' AS credentials
UNION ALL
SELECT 'Trader ' || g || ': trader' || g || '@example.com / Trader' || g || '123!'
FROM generate_series(1, 5) g;
