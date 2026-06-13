BEGIN;
DROP INDEX IF EXISTS idx_webhook_outbox_pending;
DROP TABLE IF EXISTS webhook_outbox;
COMMIT;
