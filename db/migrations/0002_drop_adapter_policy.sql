-- The organization adapter policy (the glob list of gated tools hooks and
-- the gateway once read before contacting the provider) is gone: every
-- intercepted call becomes a request and the pipeline decides.
ALTER TABLE organization_policies DROP COLUMN adapter_policy;
