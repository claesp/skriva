-- Users come from the OpenID Connect provider.
-- The pair (issuer, subject) identifies one user.
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    issuer        text        NOT NULL,
    subject       text        NOT NULL,
    username      text        NOT NULL,
    email         text        NOT NULL DEFAULT '',
    display_name  text        NOT NULL DEFAULT '',
    disabled      boolean     NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    UNIQUE (issuer, subject)
);

-- A group has the source "oidc" (from the token claim) or "local"
-- (made in the administration pages).
CREATE TABLE groups (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    source      text        NOT NULL CHECK (source IN ('oidc', 'local')),
    description text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, name)
);

CREATE TABLE group_members (
    group_id bigint NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX group_members_user_idx ON group_members (user_id);

CREATE TABLE roles (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text    NOT NULL UNIQUE CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    description text    NOT NULL DEFAULT '',
    builtin     boolean NOT NULL DEFAULT false
);

CREATE TABLE role_permissions (
    role_id    bigint NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission text   NOT NULL,
    PRIMARY KEY (role_id, permission)
);

-- A role binding gives a role to one user or to one group.
CREATE TABLE role_bindings (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    role_id    bigint      NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    user_id    bigint      REFERENCES users (id) ON DELETE CASCADE,
    group_id   bigint      REFERENCES groups (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((user_id IS NULL) <> (group_id IS NULL)),
    UNIQUE NULLS NOT DISTINCT (role_id, user_id, group_id)
);
CREATE INDEX role_bindings_user_idx ON role_bindings (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX role_bindings_group_idx ON role_bindings (group_id) WHERE group_id IS NOT NULL;

CREATE TABLE pages (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug       text        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    title      text        NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body       text        NOT NULL,
    version    integer     NOT NULL DEFAULT 1,
    created_by bigint      REFERENCES users (id) ON DELETE SET NULL,
    updated_by bigint      REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    search     tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', title), 'A') ||
        setweight(to_tsvector('simple', body), 'B')
    ) STORED
);
CREATE INDEX pages_search_idx ON pages USING gin (search);
CREATE INDEX pages_updated_idx ON pages (updated_at DESC);

-- Each saved version of a page.
CREATE TABLE page_revisions (
    page_id    bigint      NOT NULL REFERENCES pages (id) ON DELETE CASCADE,
    version    integer     NOT NULL,
    title      text        NOT NULL,
    body       text        NOT NULL,
    author_id  bigint      REFERENCES users (id) ON DELETE SET NULL,
    comment    text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, version)
);

-- The database keeps only a SHA-256 hash of the session token.
CREATE TABLE sessions (
    token_hash bytea       PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Data for login attempts that are in progress.
CREATE TABLE oidc_states (
    state_hash    bytea       PRIMARY KEY,
    nonce         text        NOT NULL,
    code_verifier text        NOT NULL,
    return_to     text        NOT NULL,
    expires_at    timestamptz NOT NULL
);
CREATE INDEX oidc_states_expires_idx ON oidc_states (expires_at);

-- Built-in roles. The application does not let users change these.
INSERT INTO roles (name, description, builtin) VALUES
    ('viewer',     'Read pages',                          true),
    ('editor',     'Read, create and edit pages',         true),
    ('maintainer', 'Read, create, edit and delete pages', true),
    ('admin',      'All permissions',                     true);

INSERT INTO role_permissions (role_id, permission)
SELECT r.id, p.permission
FROM roles r
JOIN (VALUES
    ('viewer',     'page.read'),
    ('editor',     'page.read'),
    ('editor',     'page.create'),
    ('editor',     'page.edit'),
    ('maintainer', 'page.read'),
    ('maintainer', 'page.create'),
    ('maintainer', 'page.edit'),
    ('maintainer', 'page.delete'),
    ('admin',      'page.read'),
    ('admin',      'page.create'),
    ('admin',      'page.edit'),
    ('admin',      'page.delete'),
    ('admin',      'admin.manage')
) AS p (role, permission) ON p.role = r.name;
