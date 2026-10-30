-- The ID token from the login. The logout request to the identity
-- provider sends it as id_token_hint.
ALTER TABLE sessions ADD COLUMN id_token text NOT NULL DEFAULT '';
