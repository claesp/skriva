-- Per-space permissions.
--
-- A role binding with a space applies only in that space. A role binding
-- without a space applies in all spaces of the tenant.
ALTER TABLE spaces ADD CONSTRAINT spaces_tenant_id_key UNIQUE (tenant_id, id);

-- The default role in the space. NULL means that the space uses the
-- default role of the tenant. An empty value means no default role.
ALTER TABLE spaces ADD COLUMN default_role text;

ALTER TABLE role_bindings ADD COLUMN space_id bigint;
-- The composite foreign key makes sure that the space is in the tenant of
-- the binding.
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_space_fkey
    FOREIGN KEY (tenant_id, space_id) REFERENCES spaces (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_role_id_user_id_group_id_key;
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_subject_key
    UNIQUE NULLS NOT DISTINCT (role_id, user_id, group_id, space_id);
CREATE INDEX role_bindings_space_idx ON role_bindings (space_id) WHERE space_id IS NOT NULL;
