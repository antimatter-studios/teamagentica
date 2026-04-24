-- One-time migration for tool-* → agent-* renames (2026-04-24).
-- Run against both registry DBs after the renamed plugins are built & deployed.
--
-- Usage:
--   sqlite3 data/infra-agent-registry/personas.db < scripts/migrate-agent-renames.sql
--   sqlite3 data/infra-alias-registry/aliases.db  < scripts/migrate-agent-renames.sql
--
-- The file is idempotent — rows already on the new names are left untouched.

BEGIN TRANSACTION;

-- infra-agent-registry: personas table
UPDATE personas SET plugin = 'agent-nanobanana' WHERE plugin = 'tool-nanobanana';
UPDATE personas SET plugin = 'agent-stability'  WHERE plugin = 'tool-stability';
UPDATE personas SET plugin = 'agent-seedance'   WHERE plugin = 'tool-seedance';
UPDATE personas SET plugin = 'agent-veo'        WHERE plugin = 'tool-veo';

-- infra-alias-registry: aliases table
UPDATE aliases SET plugin = 'agent-nanobanana' WHERE plugin = 'tool-nanobanana';
UPDATE aliases SET plugin = 'agent-stability'  WHERE plugin = 'tool-stability';
UPDATE aliases SET plugin = 'agent-seedance'   WHERE plugin = 'tool-seedance';
UPDATE aliases SET plugin = 'agent-veo'        WHERE plugin = 'tool-veo';

COMMIT;
