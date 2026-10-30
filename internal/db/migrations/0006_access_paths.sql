-- Per-document permissions and a cache for the permission calculation.

-- Documents can now have a default role and role bindings, as folders.
ALTER TABLE nodes DROP CONSTRAINT nodes_default_role_folder_check;

-- The access path of a node is the list of the nodes from the top level
-- to the node (the node included) that have their own access settings:
-- a default role or at least one role binding. The permissions of a user
-- for a node depend only on the access path. Many nodes have the same
-- access path, thus a space has few rows here. The table is a cache:
-- the function refresh_access_paths calculates it again from the tree.
CREATE TABLE access_paths (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    space_id bigint   NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,
    path     bigint[] NOT NULL,
    UNIQUE (space_id, path)
);

ALTER TABLE nodes ADD COLUMN access_path_id bigint REFERENCES access_paths (id);
CREATE INDEX nodes_access_path_idx ON nodes (access_path_id);
CREATE INDEX nodes_default_role_idx ON nodes (space_id) WHERE default_role IS NOT NULL;

-- refresh_access_paths calculates the access paths of a node and of its
-- subtree. It uses the access path of the parent. The caller must hold
-- the lock on the row of the space.
CREATE FUNCTION refresh_access_paths(root bigint) RETURNS void
LANGUAGE sql AS $$
    WITH RECURSIVE sub (id, space_id, path) AS (
        SELECT n.id, n.space_id,
               COALESCE((SELECT ap.path FROM nodes p JOIN access_paths ap ON ap.id = p.access_path_id
                         WHERE p.id = n.parent_id), '{}'::bigint[])
               || CASE WHEN n.default_role IS NOT NULL
                         OR EXISTS (SELECT 1 FROM role_bindings rb WHERE rb.node_id = n.id)
                       THEN ARRAY[n.id] ELSE '{}'::bigint[] END
        FROM nodes n WHERE n.id = root
        UNION ALL
        SELECT c.id, c.space_id,
               sub.path
               || CASE WHEN c.default_role IS NOT NULL
                         OR EXISTS (SELECT 1 FROM role_bindings rb WHERE rb.node_id = c.id)
                       THEN ARRAY[c.id] ELSE '{}'::bigint[] END
        FROM nodes c JOIN sub ON c.parent_id = sub.id
    ),
    ins AS (
        INSERT INTO access_paths (space_id, path)
        SELECT DISTINCT space_id, path FROM sub
        ON CONFLICT (space_id, path) DO NOTHING
        RETURNING id, space_id, path
    ),
    -- The statement cannot see the rows from "ins" in the table. Thus
    -- use the new rows and the old rows together.
    paths AS (
        SELECT id, space_id, path FROM ins
        UNION ALL
        SELECT a.id, a.space_id, a.path FROM access_paths a
        WHERE a.space_id = (SELECT space_id FROM nodes WHERE id = root)
    )
    UPDATE nodes n SET access_path_id = paths.id
    FROM sub JOIN paths ON paths.space_id = sub.space_id AND paths.path = sub.path
    WHERE n.id = sub.id AND n.access_path_id IS DISTINCT FROM paths.id;
$$;

-- Fill the cache for the existing nodes.
SELECT refresh_access_paths(id) FROM nodes WHERE parent_id IS NULL;

ALTER TABLE nodes ALTER COLUMN access_path_id SET NOT NULL;
