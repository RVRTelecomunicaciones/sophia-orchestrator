BEGIN;
DROP INDEX IF EXISTS idx_reeval_run_item_run;
DROP INDEX IF EXISTS idx_reeval_run_created;
DROP TABLE IF EXISTS reeval_run_item;
DROP TABLE IF EXISTS reeval_run;
COMMIT;
