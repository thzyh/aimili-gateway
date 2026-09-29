-- A fresh deployment starts with source restriction disabled.  Migration 006
-- predates the installer prompt and seeded enabled=1 without a CIDR, which
-- makes the first Gateway reconcile fail before the installer can apply the
-- user's choice.  Only normalize that unconfigured seed; a failed or applied
-- policy is deliberate state and must remain untouched.
UPDATE mixed_source_policy
SET enabled = 0,
    apply_status = 'pending'
WHERE id = 1
  AND enabled = 1
  AND apply_status = 'pending'
  AND NOT EXISTS (SELECT 1 FROM mixed_source_cidrs);
