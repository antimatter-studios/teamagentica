-- Copy plugin configs from old tool-* owner_id to new agent-* owner_id.
-- Run against kernel DB after installing renamed plugins.
--
-- Usage:
--   sqlite3 data/kernel/database.db < scripts/migrate-agent-configs.sql
--
-- Safe to re-run: INSERT OR REPLACE overwrites existing empty rows,
-- and the DELETE at end cleans up the old tool-* rows.

BEGIN TRANSACTION;

INSERT OR REPLACE INTO configs (owner_id, key, value, is_secret, created_at, updated_at)
SELECT 'agent-nanobanana', key, value, is_secret, created_at, CURRENT_TIMESTAMP
FROM configs WHERE owner_id = 'tool-nanobanana';

INSERT OR REPLACE INTO configs (owner_id, key, value, is_secret, created_at, updated_at)
SELECT 'agent-stability', key, value, is_secret, created_at, CURRENT_TIMESTAMP
FROM configs WHERE owner_id = 'tool-stability';

INSERT OR REPLACE INTO configs (owner_id, key, value, is_secret, created_at, updated_at)
SELECT 'agent-seedance', key, value, is_secret, created_at, CURRENT_TIMESTAMP
FROM configs WHERE owner_id = 'tool-seedance';

INSERT OR REPLACE INTO configs (owner_id, key, value, is_secret, created_at, updated_at)
SELECT 'agent-veo', key, value, is_secret, created_at, CURRENT_TIMESTAMP
FROM configs WHERE owner_id = 'tool-veo';

-- Remove the old rows once copied.
DELETE FROM configs WHERE owner_id IN ('tool-nanobanana', 'tool-stability', 'tool-seedance', 'tool-veo');

COMMIT;
