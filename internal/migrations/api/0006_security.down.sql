-- 0006_security down: drop the sensitive-flow state in dependency order.
DROP TABLE IF EXISTS security_audit;
DROP TABLE IF EXISTS kyc_applications;
DROP TABLE IF EXISTS authenticator_bindings;
