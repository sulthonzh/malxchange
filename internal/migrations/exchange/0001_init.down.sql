DROP TABLE IF EXISTS inbox;
DROP TRIGGER IF EXISTS outbox_insert_notify ON outbox;
DROP FUNCTION IF EXISTS outbox_wakeup();
DROP TABLE IF EXISTS outbox;
