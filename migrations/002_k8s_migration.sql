-- 002_k8s_migration.sql
-- Track GitOps commit references and sync status for K8s-managed resources.

ALTER TABLE fleets ADD COLUMN IF NOT EXISTS git_commit_sha TEXT DEFAULT '';
ALTER TABLE fleets ADD COLUMN IF NOT EXISTS git_manifest_path TEXT DEFAULT '';
ALTER TABLE fleets ADD COLUMN IF NOT EXISTS sync_status TEXT DEFAULT 'unknown';

ALTER TABLE routes ADD COLUMN IF NOT EXISTS git_commit_sha TEXT DEFAULT '';
ALTER TABLE routes ADD COLUMN IF NOT EXISTS git_manifest_path TEXT DEFAULT '';
ALTER TABLE routes ADD COLUMN IF NOT EXISTS sync_status TEXT DEFAULT 'unknown';
