-- 003_fleet_k8s_name.sql
-- Store the Kubernetes resource name (metadata.name) for each fleet separately
-- from the DB UUID. Existing fleets keep their UUID as the k8s_name so
-- running Services/Deployments are unaffected. New fleets use a human-readable
-- slug (e.g. "fleet-jpmm-markets") derived from the fleet display name.

ALTER TABLE fleets ADD COLUMN IF NOT EXISTS k8s_name TEXT DEFAULT '';

-- Backfill: existing fleets already have K8s resources named after their UUID.
UPDATE fleets SET k8s_name = id WHERE k8s_name = '' OR k8s_name IS NULL;
