-- 0002_wallet down: drop the Ledger and custody domain in dependency order.
DROP TABLE IF EXISTS incidents;
DROP TABLE IF EXISTS utxos;
DROP TABLE IF EXISTS watch_cursors;
DROP TABLE IF EXISTS cold_transfers;
DROP TABLE IF EXISTS hot_transfers;
DROP TABLE IF EXISTS sweeps;
DROP TABLE IF EXISTS withdrawals;
DROP TABLE IF EXISTS deposits;
DROP TABLE IF EXISTS deposit_addresses;
DROP TABLE IF EXISTS order_holds;
DROP TABLE IF EXISTS journal;
DROP TABLE IF EXISTS ledger_accounts;
DROP TABLE IF EXISTS coins;
