-- A tenant is the main organizational unit. Each tenant has its own
-- identity provider, users, groups, custom roles and spaces.
CREATE TABLE tenants (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code                text        NOT NULL UNIQUE CHECK (code ~ '^[a-z0-9][a-z0-9-]{0,14}[a-z0-9]$'),
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

-- The keys of a tenant for the encryption of the content (envelope
-- encryption). Each key is 32 random bytes. The table keeps each key
-- encrypted with the key encryption key (KEK), which is outside the
-- database. kek_id identifies the KEK. The purpose "data" encrypts the
-- documents and the files with AES-256-GCM. The purpose "search" makes
-- the keyed hashes of the search index. A rotation adds a new version
-- and retires the old version. The server deletes a retired version
-- when no data uses it.
CREATE TABLE tenant_keys (
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    purpose    text        NOT NULL CHECK (purpose IN ('data', 'search')),
    version    integer     NOT NULL CHECK (version > 0),
    wrapped    bytea       NOT NULL,
    kek_id     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    retired_at timestamptz,
    PRIMARY KEY (tenant_id, purpose, version)
);
-- Each tenant has at most one current key for each purpose.
CREATE UNIQUE INDEX tenant_keys_current_idx ON tenant_keys (tenant_id, purpose) WHERE retired_at IS NULL;

-- The levels of information classification of a tenant. Each document
-- has one level. "position" gives the order of the levels, from the
-- lowest to the highest. One level is the default for new documents.
CREATE TABLE classifications (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint  NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        text    NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    description text    NOT NULL DEFAULT '',
    position    integer NOT NULL DEFAULT 0,
    is_default  boolean NOT NULL DEFAULT false,
    UNIQUE (tenant_id, name)
);
-- Only one level of a tenant can be the default.
CREATE UNIQUE INDEX classifications_one_default_idx ON classifications (tenant_id) WHERE is_default;

-- add_default_classifications gives a tenant the default levels.
CREATE FUNCTION add_default_classifications(tenant bigint) RETURNS void
LANGUAGE sql AS $$
    INSERT INTO classifications (tenant_id, name, description, position, is_default) VALUES
        (tenant, 'Unclassified', 'The information has no classification.', 1, true),
        (tenant, 'Public',       'Everybody can read the information.', 2, false),
        (tenant, 'Restricted',   'Only the persons that need the information can read it.', 3, false),
        (tenant, 'Secret',       'Disclosure can cause serious damage.', 4, false),
        (tenant, 'Top Secret',   'Disclosure can cause very serious damage.', 5, false);
$$;

-- A space is a tree of folders and documents in a tenant.
CREATE TABLE spaces (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    slug         text        NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,14}[a-z0-9]$'),
    name         text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description  text        NOT NULL DEFAULT '',
    -- The default role in the space. NULL means that the space uses the
    -- default role of the tenant. An empty value means no default role.
    default_role text,
    -- The largest total size of the uploaded files in the space, in MiB.
    upload_quota_mb integer NOT NULL DEFAULT 1024 CHECK (upload_quota_mb BETWEEN 1 AND 1048576),
    -- The largest number of search results from the space. The search
    -- counts only the items that the user can read.
    search_results  integer NOT NULL DEFAULT 50 CHECK (search_results BETWEEN 10 AND 1000),
    -- The default classification of new documents in the space. NULL
    -- means the default level of the tenant.
    default_classification_id bigint REFERENCES classifications (id) DEFERRABLE INITIALLY DEFERRED,
    -- The lowest classification of the documents in the space. NULL means
    -- no limit. The store compares the levels by their position.
    min_classification_id     bigint REFERENCES classifications (id) DEFERRABLE INITIALLY DEFERRED,
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
    -- The title, encrypted with the data key of the tenant (version
    -- key_version). The server checks the length before it saves the
    -- title. The server sorts and filters the titles in memory.
    title          bytea       NOT NULL,
    -- The keyed hash (HMAC-SHA-256 with the search key of the tenant, 8
    -- bytes) of the slug of the title, for wiki links. NULL if the slug
    -- is empty. The server makes the slug in the URL from the title.
    slug_token     bigint,
    -- The text of a document in the JSON format of the editor,
    -- encrypted with the data key of the tenant (version key_version).
    -- The server checks the format before it saves the text.
    body           bytea       NOT NULL,
    -- The plain text of the body for the parts of the text in the search
    -- results, encrypted with the same key.
    plain_text     bytea       NOT NULL,
    key_version    integer     NOT NULL,
    -- The search index: keyed hashes (HMAC-SHA-256 with the search key
    -- of the tenant, 8 bytes) of the words of the title and the text.
    search_tokens  bigint[]    NOT NULL DEFAULT '{}',
    -- The keyed hashes of the words of the title only. The search shows
    -- the results with the words in the title first.
    title_tokens   bigint[]    NOT NULL DEFAULT '{}',
    -- The keyed hashes of the prefixes of the words (3 to 20 letters),
    -- with a different domain than the words. A search for a part of a
    -- word ("describe" finds "describes") uses them.
    prefix_tokens  bigint[]    NOT NULL DEFAULT '{}',
    -- The keyed hashes of the English stems of the words, with a
    -- different domain than the words. A search for a different form of a
    -- word ("describing" finds "describe") uses them.
    stem_tokens    bigint[]    NOT NULL DEFAULT '{}',
    -- The version of the format of the search index. The cleanup task
    -- makes the index again for rows with an older version.
    search_version smallint    NOT NULL DEFAULT 0,
    version        integer     NOT NULL DEFAULT 1,
    -- The default role of the node. NULL means that the node uses the
    -- default role of its parent (or of the space). An empty value means
    -- no default role.
    default_role   text,
    access_path_id bigint      NOT NULL REFERENCES access_paths (id),
    -- The information classification of a document. A folder has none.
    -- The store makes sure that the level belongs to the tenant of the
    -- space. A level that documents use cannot be deleted. The check is
    -- at the end of the transaction. Thus the deletion of a tenant can
    -- remove the levels and the documents in one statement.
    classification_id bigint   REFERENCES classifications (id) DEFERRABLE INITIALLY DEFERRED,
    -- The default and the lowest classification of the documents in a
    -- folder. NULL means the value of the parent folder or of the space.
    -- A folder can only make the limits of its parents stricter.
    default_classification_id bigint REFERENCES classifications (id) DEFERRABLE INITIALLY DEFERRED,
    min_classification_id     bigint REFERENCES classifications (id) DEFERRABLE INITIALLY DEFERRED,
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
    UNIQUE (space_id, id),
    CONSTRAINT nodes_parent_fkey
        FOREIGN KEY (space_id, parent_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE,
    CHECK (parent_id <> id),
    CHECK ((deleted_at IS NULL) = (trash_id IS NULL)),
    CHECK ((kind = 'document') = (classification_id IS NOT NULL)),
    CHECK (kind = 'folder' OR (default_classification_id IS NULL AND min_classification_id IS NULL))
);
CREATE INDEX nodes_search_idx ON nodes USING gin (search_tokens);
CREATE INDEX nodes_prefix_idx ON nodes USING gin (prefix_tokens);
CREATE INDEX nodes_stem_idx ON nodes USING gin (stem_tokens);
CREATE INDEX nodes_search_version_idx ON nodes (search_version);
CREATE INDEX nodes_parent_idx ON nodes (space_id, parent_id);
CREATE INDEX nodes_slug_idx ON nodes (space_id, slug_token) WHERE slug_token IS NOT NULL;
CREATE INDEX nodes_space_updated_idx ON nodes (space_id, updated_at DESC);
CREATE INDEX nodes_access_path_idx ON nodes (access_path_id);
CREATE INDEX nodes_default_role_idx ON nodes (space_id) WHERE default_role IS NOT NULL;
CREATE INDEX nodes_classification_idx ON nodes (classification_id);
CREATE INDEX nodes_default_classification_idx ON nodes (default_classification_id) WHERE default_classification_id IS NOT NULL;
CREATE INDEX nodes_min_classification_idx ON nodes (min_classification_id) WHERE min_classification_id IS NOT NULL;
CREATE INDEX nodes_trash_idx ON nodes (trash_id) WHERE trash_id IS NOT NULL;
-- The items in the trash: the nodes that the users deleted.
CREATE INDEX nodes_trash_root_idx ON nodes (space_id, deleted_at) WHERE trash_id = id;
-- The deletion of a user sets these columns to NULL. Without the indexes,
-- PostgreSQL reads the full table for each user of a deleted tenant.
CREATE INDEX nodes_created_by_idx ON nodes (created_by) WHERE created_by IS NOT NULL;
CREATE INDEX nodes_updated_by_idx ON nodes (updated_by) WHERE updated_by IS NOT NULL;
CREATE INDEX nodes_deleted_by_idx ON nodes (deleted_by) WHERE deleted_by IS NOT NULL;

-- Each saved version of a node.
CREATE TABLE revisions (
    node_id    bigint      NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    version    integer     NOT NULL,
    -- The title and the text, encrypted as nodes.title and nodes.body.
    title      bytea       NOT NULL,
    body       bytea       NOT NULL,
    key_version integer    NOT NULL,
    author_id  bigint      REFERENCES users (id) ON DELETE SET NULL,
    comment    text        NOT NULL DEFAULT '',
    -- The name of the classification at the time of the version.
    classification text    NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, version)
);
CREATE INDEX revisions_author_idx ON revisions (author_id) WHERE author_id IS NOT NULL;

-- An uploaded file, for example an image in a document. A file belongs
-- to a space. The data is in the database, thus a backup of the database
-- contains all data.
CREATE TABLE files (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    space_id     bigint      NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,
    public_id    text        NOT NULL UNIQUE CHECK (public_id ~ '^[0-9a-z]{14}$'),
    -- image/svg+xml is only for draw.io diagrams. The server checks them.
    content_type text        NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/svg+xml')),
    -- The data, encrypted with the data key of the tenant.
    data         bytea       NOT NULL,
    -- The size of the data before the encryption, in bytes. The upload
    -- quota of the space uses it.
    size         bigint      NOT NULL CHECK (size >= 0),
    key_version  integer     NOT NULL,
    created_by   bigint      REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (space_id, id)
);
CREATE INDEX files_created_idx ON files (created_at);
CREATE INDEX files_created_by_idx ON files (created_by) WHERE created_by IS NOT NULL;

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
-- target node. A wiki link has the keyed hash of the slug of the target
-- title (as nodes.slug_token), because the target can be made later. The
-- server replaces the rows of a node when it saves the node. The page of a node uses the rows to show the nodes
-- that link to it.
CREATE TABLE node_links (
    space_id    bigint NOT NULL,
    source_id   bigint NOT NULL,
    target_id   bigint,
    target_token bigint,
    CHECK ((target_id IS NULL) <> (target_token IS NULL)),
    FOREIGN KEY (space_id, source_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE,
    FOREIGN KEY (space_id, target_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE,
    UNIQUE NULLS NOT DISTINCT (source_id, target_id, target_token)
);
CREATE INDEX node_links_target_idx ON node_links (target_id) WHERE target_id IS NOT NULL;
CREATE INDEX node_links_token_idx ON node_links (space_id, target_token) WHERE target_token IS NOT NULL;

-- A role binding gives a role to one user or to one group. A binding
-- without a space applies in all spaces of the tenant. A binding with a
-- space applies only in that space. A binding with a node applies to that
-- node and its subtree. The composite foreign keys make sure that the
-- user, the group, the space and the node are in the same tenant and in
-- the same space. A built-in role has no tenant, thus the store checks
-- the tenant of the role.
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
    -- target and details are only for the events of the system
    -- administration. An event of a tenant keeps them in sealed: a JSON
    -- object, encrypted with the data key of the tenant (version
    -- key_version). They can contain titles and names.
    target     text        NOT NULL DEFAULT '',
    target_id  text        NOT NULL DEFAULT '',
    details    text        NOT NULL DEFAULT '',
    sealed     bytea,
    key_version integer,
    CHECK ((tenant_id IS NULL) = (sealed IS NULL)),
    CHECK ((sealed IS NULL) = (key_version IS NULL)),
    CHECK (sealed IS NULL OR (target = '' AND details = ''))
);
CREATE INDEX audit_events_tenant_idx ON audit_events (tenant_id, id DESC);
CREATE INDEX audit_events_created_idx ON audit_events (created_at);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_id) WHERE actor_id IS NOT NULL;

-- The database keeps only a SHA-256 hash of the session token. A NULL
-- tenant_id identifies a system administrator session.
CREATE TABLE sessions (
    token_hash bytea       PRIMARY KEY,
    tenant_id  bigint      REFERENCES tenants (id) ON DELETE CASCADE,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token text        NOT NULL,
    -- The ID token from the login, encrypted with a key from the session
    -- token, or NULL. The logout request to the identity provider sends
    -- it as id_token_hint.
    id_token   bytea,
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
SELECT add_default_classifications(id) FROM tenants WHERE is_default;
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
