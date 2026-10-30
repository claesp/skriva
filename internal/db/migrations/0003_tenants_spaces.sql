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

INSERT INTO tenants (code, name, is_default) VALUES ('dflt', 'Default', true);

-- A space is a tree of folders and documents in a tenant.
CREATE TABLE spaces (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    slug        text        NOT NULL CHECK (slug ~ '^[a-z0-9]{8}$'),
    name        text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

INSERT INTO spaces (tenant_id, slug, name)
SELECT id, 'default0', 'Default' FROM tenants WHERE is_default;

-- Users. A NULL tenant_id identifies a system administrator.
ALTER TABLE users ADD COLUMN tenant_id bigint REFERENCES tenants (id) ON DELETE CASCADE;
UPDATE users SET tenant_id = (SELECT id FROM tenants WHERE is_default);
ALTER TABLE users DROP CONSTRAINT users_issuer_subject_key;
ALTER TABLE users ADD CONSTRAINT users_identity_key UNIQUE NULLS NOT DISTINCT (tenant_id, issuer, subject);
ALTER TABLE users ADD CONSTRAINT users_tenant_id_key UNIQUE (tenant_id, id);

-- Groups.
ALTER TABLE groups ADD COLUMN tenant_id bigint REFERENCES tenants (id) ON DELETE CASCADE;
UPDATE groups SET tenant_id = (SELECT id FROM tenants WHERE is_default);
ALTER TABLE groups ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE groups DROP CONSTRAINT groups_source_name_key;
ALTER TABLE groups ADD CONSTRAINT groups_tenant_source_name_key UNIQUE (tenant_id, source, name);
ALTER TABLE groups ADD CONSTRAINT groups_tenant_id_key UNIQUE (tenant_id, id);

-- The composite foreign keys make sure that a member and a group are in
-- the same tenant.
ALTER TABLE group_members ADD COLUMN tenant_id bigint;
UPDATE group_members SET tenant_id = (SELECT id FROM tenants WHERE is_default);
ALTER TABLE group_members ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE group_members DROP CONSTRAINT group_members_group_id_fkey;
ALTER TABLE group_members DROP CONSTRAINT group_members_user_id_fkey;
ALTER TABLE group_members ADD CONSTRAINT group_members_group_fkey
    FOREIGN KEY (tenant_id, group_id) REFERENCES groups (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE group_members ADD CONSTRAINT group_members_user_fkey
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE;

-- Roles. Built-in roles have no tenant. Custom roles belong to a tenant.
ALTER TABLE roles ADD COLUMN tenant_id bigint REFERENCES tenants (id) ON DELETE CASCADE;
UPDATE roles SET tenant_id = (SELECT id FROM tenants WHERE is_default) WHERE NOT builtin;
ALTER TABLE roles DROP CONSTRAINT roles_name_key;
CREATE UNIQUE INDEX roles_tenant_name_idx ON roles (COALESCE(tenant_id, 0), name);
ALTER TABLE roles ADD CONSTRAINT roles_builtin_tenant_check CHECK (builtin = (tenant_id IS NULL));

-- Role bindings.
ALTER TABLE role_bindings ADD COLUMN tenant_id bigint;
UPDATE role_bindings SET tenant_id = (SELECT id FROM tenants WHERE is_default);
ALTER TABLE role_bindings ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_tenant_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE;
ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_user_id_fkey;
ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_group_id_fkey;
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_user_fkey
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE role_bindings ADD CONSTRAINT role_bindings_group_fkey
    FOREIGN KEY (tenant_id, group_id) REFERENCES groups (tenant_id, id) ON DELETE CASCADE;
CREATE INDEX role_bindings_tenant_idx ON role_bindings (tenant_id);

-- New permission names.
UPDATE role_permissions SET permission = 'doc.' || substr(permission, 6) WHERE permission LIKE 'page.%';
UPDATE role_permissions SET permission = 'tenant.manage' WHERE permission = 'admin.manage';
INSERT INTO role_permissions (role_id, permission)
SELECT id, 'space.manage' FROM roles WHERE name = 'admin' AND builtin;

-- Pages become nodes. A node is a folder or a document in a space.
ALTER TABLE pages RENAME TO nodes;
ALTER TABLE page_revisions RENAME TO revisions;
ALTER TABLE revisions RENAME COLUMN page_id TO node_id;

ALTER TABLE nodes DROP CONSTRAINT pages_slug_key;
ALTER TABLE nodes DROP CONSTRAINT pages_slug_check;
ALTER TABLE nodes ADD CONSTRAINT nodes_slug_check CHECK (slug ~ '^([a-z0-9]+(-[a-z0-9]+)*)?$');

ALTER TABLE nodes ADD COLUMN space_id bigint REFERENCES spaces (id) ON DELETE CASCADE;
UPDATE nodes SET space_id = (SELECT s.id FROM spaces s JOIN tenants t ON t.id = s.tenant_id WHERE t.is_default);
ALTER TABLE nodes ALTER COLUMN space_id SET NOT NULL;

-- The public ID is in the URL. It is random, thus nobody can guess it.
ALTER TABLE nodes ADD COLUMN public_id text;
UPDATE nodes SET public_id = substr(translate(encode(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid()), 'base64'), '+/=', ''), 1, 12);
ALTER TABLE nodes ALTER COLUMN public_id SET NOT NULL;
ALTER TABLE nodes ADD CONSTRAINT nodes_public_id_key UNIQUE (public_id);
ALTER TABLE nodes ADD CONSTRAINT nodes_public_id_check CHECK (public_id ~ '^[0-9A-Za-z]{12}$');

ALTER TABLE nodes ADD COLUMN kind text NOT NULL DEFAULT 'document' CHECK (kind IN ('folder', 'document'));
ALTER TABLE nodes ALTER COLUMN kind DROP DEFAULT;

-- The parent must be in the same space. A NULL parent is the root.
ALTER TABLE nodes ADD COLUMN parent_id bigint;
ALTER TABLE nodes ADD CONSTRAINT nodes_space_id_key UNIQUE (space_id, id);
ALTER TABLE nodes ADD CONSTRAINT nodes_parent_fkey
    FOREIGN KEY (space_id, parent_id) REFERENCES nodes (space_id, id) ON DELETE CASCADE;
ALTER TABLE nodes ADD CONSTRAINT nodes_not_own_parent_check CHECK (parent_id <> id);

CREATE INDEX nodes_parent_idx ON nodes (space_id, parent_id);
CREATE INDEX nodes_slug_idx ON nodes (space_id, slug);
CREATE INDEX nodes_space_updated_idx ON nodes (space_id, updated_at DESC);
DROP INDEX pages_updated_idx;

-- Sessions and login states now belong to a tenant. A NULL tenant_id
-- identifies a system administrator session. Users must sign in again.
DELETE FROM sessions;
ALTER TABLE sessions ADD COLUMN tenant_id bigint REFERENCES tenants (id) ON DELETE CASCADE;
DELETE FROM oidc_states;
ALTER TABLE oidc_states ADD COLUMN tenant_id bigint REFERENCES tenants (id) ON DELETE CASCADE;
