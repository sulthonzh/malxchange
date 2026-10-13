-- 0002_member down: drop the member identity core in dependency order.
DROP TABLE IF EXISTS login_failures;
DROP TABLE IF EXISTS verification_codes;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS members;
