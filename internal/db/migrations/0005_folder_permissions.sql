-- Per-folder permissions.
--
-- A folder can have its own default role. NULL means that the folder
-- uses the default role of its parent (or of the space). An empty value
-- means no default role.
ALTER TABLE nodes ADD COLUMN default_role text;
ALTER TABLE nodes ADD CONSTRAINT nodes_default_role_folder_check
    CHECK (default_role IS NULL OR kind = 'folder');

-- A role binding with a node applies to that folder and its subtree. The
-- binding must also have the space of the folder.
ALTER TABLE role_bindings ADD COLUMN node_id bigint;
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_node_space_check
    CHECK (node_id IS NULL OR space_id IS NOT NULL);
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_node_fkey
    FOREIGN KEY (space_id, node_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE;
ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_subject_key;
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_subject_key
    UNIQUE NULLS NOT DISTINCT (role_id, user_id, group_id, space_id, node_id);
CREATE INDEX role_bindings_node_idx ON role_bindings (node_id) WHERE node_id IS NOT NULL;
