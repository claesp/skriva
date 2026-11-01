-- A tenant is the main organizational unit. Each tenant has its own
-- identity provider, users, groups, custom roles and spaces.
CREATE TABLE tenants (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code                text        NOT NULL UNIQUE CHECK (code ~ '^[a-z0-9]{4}$'),
    name                text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    is_default          boolean     NOT NULL DEFAULT false,
    disabled            boolean     NOT NULL DEFAULT false,
    oidc_issuer         text        NOT NULL DEFAULT '',
    oidc_client_id      text        NOT NULL DEFAULT '',
    -- The client secret, encrypted with AES-256-GCM. NULL means no secret.
    oidc_client_secret  bytea,
    oidc_scopes         text        NOT NULL DEFAULT 'openid profile email',
    oidc_groups_claim   text        NOT NULL DEFAULT 'groups',
    -- An OIDC group that always has the admin role in this tenant.
    admin_group         text        NOT NULL DEFAULT '',
    -- The role of all signed-in users of this tenant. Empty means none.
    default_role        text        NOT NULL DEFAULT 'viewer',
    session_ttl_seconds integer     NOT NULL DEFAULT 43200 CHECK (session_ttl_seconds BETWEEN 60 AND 2592000),
    max_doc_bytes       integer     NOT NULL DEFAULT 1048576 CHECK (max_doc_bytes BETWEEN 1024 AND 1048576),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
-- Only one tenant can be the default tenant.
CREATE UNIQUE INDEX tenants_one_default_idx ON tenants (is_default) WHERE is_default;

-- A space is a tree of folders and documents in a tenant.
CREATE TABLE spaces (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    slug         text        NOT NULL CHECK (slug ~ '^[a-z0-9]{8}$'),
    name         text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description  text        NOT NULL DEFAULT '',
    -- The default role in the space. NULL means that the space uses the
    -- default role of the tenant. An empty value means no default role.
    default_role text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug),
    UNIQUE (tenant_id, id)
);

-- Users come from the OpenID Connect provider of their tenant. The
-- triple (tenant, issuer, subject) identifies one user. A NULL tenant_id
-- identifies a system administrator.
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint      REFERENCES tenants (id) ON DELETE CASCADE,
    issuer        text        NOT NULL,
    subject       text        NOT NULL,
    username      text        NOT NULL,
    email         text        NOT NULL DEFAULT '',
    display_name  text        NOT NULL DEFAULT '',
    disabled      boolean     NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CONSTRAINT users_identity_key UNIQUE NULLS NOT DISTINCT (tenant_id, issuer, subject),
    UNIQUE (tenant_id, id)
);

-- A group has the source "oidc" (from the token claim) or "local"
-- (made in the administration pages).
CREATE TABLE groups (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    source      text        NOT NULL CHECK (source IN ('oidc', 'local')),
    description text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source, name),
    UNIQUE (tenant_id, id)
);

-- The composite foreign keys make sure that a member and a group are in
-- the same tenant.
CREATE TABLE group_members (
    tenant_id bigint NOT NULL,
    group_id  bigint NOT NULL,
    user_id   bigint NOT NULL,
    PRIMARY KEY (group_id, user_id),
    FOREIGN KEY (tenant_id, group_id) REFERENCES groups (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX group_members_user_idx ON group_members (user_id);

-- Built-in roles have no tenant. Custom roles belong to a tenant.
CREATE TABLE roles (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint  REFERENCES tenants (id) ON DELETE CASCADE,
    name        text    NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    description text    NOT NULL DEFAULT '',
    builtin     boolean NOT NULL DEFAULT false,
    CHECK (builtin = (tenant_id IS NULL))
);
CREATE UNIQUE INDEX roles_tenant_name_idx ON roles (COALESCE(tenant_id, 0), name);

CREATE TABLE role_permissions (
    role_id    bigint NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission text   NOT NULL,
    PRIMARY KEY (role_id, permission)
);

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

-- A node is a folder or a document in a space. A NULL parent is the top
-- level of the space. The parent must be in the same space.
CREATE TABLE nodes (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    space_id       bigint      NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,
    -- The public ID is in the URL. It is random, thus nobody can guess it.
    public_id      text        NOT NULL UNIQUE CHECK (public_id ~ '^[0-9a-z]{14}$'),
    parent_id      bigint,
    kind           text        NOT NULL CHECK (kind IN ('folder', 'document')),
    title          text        NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    slug           text        NOT NULL CHECK (slug ~ '^([a-z0-9]+(-[a-z0-9]+)*)?$'),
    -- The text of a document in the JSON format of the editor. The
    -- server checks the format before it saves the text.
    body           jsonb       NOT NULL DEFAULT '{"type":"doc"}',
    -- The plain text of the body for the search index.
    plain_text     text        NOT NULL DEFAULT '',
    version        integer     NOT NULL DEFAULT 1,
    -- The default role of the node. NULL means that the node uses the
    -- default role of its parent (or of the space). An empty value means
    -- no default role.
    default_role   text,
    access_path_id bigint      NOT NULL REFERENCES access_paths (id),
    created_by     bigint      REFERENCES users (id) ON DELETE SET NULL,
    updated_by     bigint      REFERENCES users (id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    -- A node in the trash has a value in deleted_at. trash_id is the node
    -- that the user deleted: the node itself, or an ancestor of the node.
    -- The subtree of a deleted node goes into the trash with it.
    deleted_at     timestamptz,
    deleted_by     bigint      REFERENCES users (id) ON DELETE SET NULL,
    trash_id       bigint,
    search         tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', title), 'A') ||
        setweight(to_tsvector('simple', plain_text), 'B')
    ) STORED,
    UNIQUE (space_id, id),
    CONSTRAINT nodes_parent_fkey
        FOREIGN KEY (space_id, parent_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE,
    CHECK (parent_id <> id),
    CHECK ((deleted_at IS NULL) = (trash_id IS NULL))
);
CREATE INDEX nodes_search_idx ON nodes USING gin (search);
CREATE INDEX nodes_parent_idx ON nodes (space_id, parent_id);
CREATE INDEX nodes_slug_idx ON nodes (space_id, slug);
CREATE INDEX nodes_space_updated_idx ON nodes (space_id, updated_at DESC);
CREATE INDEX nodes_access_path_idx ON nodes (access_path_id);
CREATE INDEX nodes_default_role_idx ON nodes (space_id) WHERE default_role IS NOT NULL;
CREATE INDEX nodes_trash_idx ON nodes (trash_id) WHERE trash_id IS NOT NULL;
-- The items in the trash: the nodes that the users deleted.
CREATE INDEX nodes_trash_root_idx ON nodes (space_id, deleted_at) WHERE trash_id = id;

-- Each saved version of a node.
CREATE TABLE revisions (
    node_id    bigint      NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    version    integer     NOT NULL,
    title      text        NOT NULL,
    body       jsonb       NOT NULL,
    author_id  bigint      REFERENCES users (id) ON DELETE SET NULL,
    comment    text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, version)
);

-- An uploaded file, for example an image in a document. A file belongs
-- to a space. The data is in the database, thus a backup of the database
-- contains all data.
CREATE TABLE files (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    space_id     bigint      NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,
    public_id    text        NOT NULL UNIQUE CHECK (public_id ~ '^[0-9a-z]{14}$'),
    content_type text        NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/gif', 'image/webp')),
    data         bytea       NOT NULL,
    created_by   bigint      REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (space_id, id)
);
CREATE INDEX files_created_idx ON files (created_at);

-- The nodes that use a file, in one or more of their revisions. A user
-- can read a file if the user can read one of these nodes. The server
-- adds rows when it saves a node, and it does not remove them. Thus old
-- revisions continue to show their images.
CREATE TABLE file_refs (
    file_id  bigint NOT NULL,
    node_id  bigint NOT NULL,
    space_id bigint NOT NULL,
    PRIMARY KEY (file_id, node_id),
    FOREIGN KEY (space_id, file_id) REFERENCES files (space_id, id) ON DELETE CASCADE,
    FOREIGN KEY (space_id, node_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE
);
CREATE INDEX file_refs_node_idx ON file_refs (node_id);

-- The links in the current version of each node. A "doc:" link has the
-- target node. A wiki link has the slug of the target title, because the
-- target can be made later. The server replaces the rows of a node when
-- it saves the node. The page of a node uses the rows to show the nodes
-- that link to it.
CREATE TABLE node_links (
    space_id    bigint NOT NULL,
    source_id   bigint NOT NULL,
    target_id   bigint,
    target_slug text,
    CHECK ((target_id IS NULL) <> (target_slug IS NULL)),
    FOREIGN KEY (space_id, source_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE,
    FOREIGN KEY (space_id, target_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE,
    UNIQUE NULLS NOT DISTINCT (source_id, target_id, target_slug)
);
CREATE INDEX node_links_target_idx ON node_links (target_id) WHERE target_id IS NOT NULL;
CREATE INDEX node_links_slug_idx ON node_links (space_id, target_slug) WHERE target_slug IS NOT NULL;

-- A role binding gives a role to one user or to one group. A binding
-- without a space applies in all spaces of the tenant. A binding with a
-- space applies only in that space. A binding with a node applies to that
-- node and its subtree. The composite foreign keys make sure that all
-- parts are in the same tenant and in the same space.
CREATE TABLE role_bindings (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    role_id    bigint      NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    user_id    bigint,
    group_id   bigint,
    space_id   bigint,
    node_id    bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((user_id IS NULL) <> (group_id IS NULL)),
    CHECK (node_id IS NULL OR space_id IS NOT NULL),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, group_id) REFERENCES groups (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, space_id) REFERENCES spaces (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (space_id, node_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE,
    UNIQUE NULLS NOT DISTINCT (role_id, user_id, group_id, space_id, node_id)
);
CREATE INDEX role_bindings_tenant_idx ON role_bindings (tenant_id);
CREATE INDEX role_bindings_user_idx ON role_bindings (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX role_bindings_group_idx ON role_bindings (group_id) WHERE group_id IS NOT NULL;
CREATE INDEX role_bindings_space_idx ON role_bindings (space_id) WHERE space_id IS NOT NULL;
CREATE INDEX role_bindings_node_idx ON role_bindings (node_id) WHERE node_id IS NOT NULL;

-- The audit log: changes to access rights, to the trash and to the tree,
-- restored versions, and logins. A NULL tenant_id is an event of the
-- system administration. The columns keep the names at the time of the
-- event, thus the log stays readable after a user, a group or an item
-- is deleted.
CREATE TABLE audit_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  bigint      REFERENCES tenants (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    actor_id   bigint      REFERENCES users (id) ON DELETE SET NULL,
    actor_name text        NOT NULL,
    action     text        NOT NULL,
    space_slug text        NOT NULL DEFAULT '',
    target     text        NOT NULL DEFAULT '',
    target_id  text        NOT NULL DEFAULT '',
    details    text        NOT NULL DEFAULT ''
);
CREATE INDEX audit_events_tenant_idx ON audit_events (tenant_id, id DESC);
CREATE INDEX audit_events_created_idx ON audit_events (created_at);

-- The database keeps only a SHA-256 hash of the session token. A NULL
-- tenant_id identifies a system administrator session.
CREATE TABLE sessions (
    token_hash bytea       PRIMARY KEY,
    tenant_id  bigint      REFERENCES tenants (id) ON DELETE CASCADE,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token text        NOT NULL,
    -- The ID token from the login. The logout request to the identity
    -- provider sends it as id_token_hint.
    id_token   text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Data for login attempts that are in progress.
CREATE TABLE oidc_states (
    state_hash    bytea       PRIMARY KEY,
    tenant_id     bigint      REFERENCES tenants (id) ON DELETE CASCADE,
    nonce         text        NOT NULL,
    code_verifier text        NOT NULL,
    return_to     text        NOT NULL,
    expires_at    timestamptz NOT NULL
);
CREATE INDEX oidc_states_expires_idx ON oidc_states (expires_at);

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

-- The default installation: the tenant "dflt" with the space "default0".
INSERT INTO tenants (code, name, is_default) VALUES ('dflt', 'Default', true);
INSERT INTO spaces (tenant_id, slug, name)
SELECT id, 'default0', 'Default' FROM tenants WHERE is_default;

-- Built-in roles. The application does not let users change these.
INSERT INTO roles (name, description, builtin) VALUES
    ('viewer',     'Read documents',                          true),
    ('editor',     'Read, create and edit documents',         true),
    ('maintainer', 'Read, create, edit and delete documents', true),
    ('admin',      'All permissions',                         true);

INSERT INTO role_permissions (role_id, permission)
SELECT r.id, p.permission
FROM roles r
JOIN (VALUES
    ('viewer',     'doc.read'),
    ('editor',     'doc.read'),
    ('editor',     'doc.create'),
    ('editor',     'doc.edit'),
    ('maintainer', 'doc.read'),
    ('maintainer', 'doc.create'),
    ('maintainer', 'doc.edit'),
    ('maintainer', 'doc.delete'),
    ('admin',      'doc.read'),
    ('admin',      'doc.create'),
    ('admin',      'doc.edit'),
    ('admin',      'doc.delete'),
    ('admin',      'space.manage'),
    ('admin',      'tenant.manage')
) AS p (role, permission) ON p.role = r.name;
