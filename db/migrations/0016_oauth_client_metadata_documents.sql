-- An OAuth client may identify itself by the HTTPS URL of its metadata
-- document instead of registering (draft-ietf-oauth-client-id-metadata-document,
-- the MCP authorization spec's preferred registration). Such a client keeps
-- a row like a registered one, found by that URL, so keys and codes refer
-- to it the same way.
ALTER TABLE public.oauth_clients ADD COLUMN metadata_url text;

CREATE UNIQUE INDEX oauth_clients_metadata_url_idx ON public.oauth_clients USING btree (metadata_url) WHERE (metadata_url IS NOT NULL);
