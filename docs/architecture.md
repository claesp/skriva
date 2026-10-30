# Architecture

## Overview

Skriva is one Go program. The program contains the HTTP server, the HTML
templates, the style sheet and the database migrations. It needs these
external services:

- A PostgreSQL database (version 15 or later).
- One or more OpenID Connect (OIDC) providers, for example Keycloak.

```
Browser ──HTTP──> Skriva ──SQL──> PostgreSQL
   │                 │
   └──── OIDC ───────┴──────────> Identity providers (one for each tenant,
                                  and one for the system administration)
```

## Organization

| Term     | Meaning |
|----------|---------|
| Tenant   | The main organizational unit. A tenant has its own identity provider, users, groups, custom roles, settings and spaces. |
| Space    | A collection of folders and documents in a tenant. |
| Folder   | An item in a space that contains other items. It has no text. |
| Document | An item in a space with Markdown text and a history of versions. |
| Node     | A folder or a document. The code and the database use this term. |

```
Tenant "acme"
├── Space "handbook"
│   ├── Folder "Guides"
│   │   ├── Folder "Setup"
│   │   │   └── Document "Install"
│   │   │       └── Document "Install on Linux"
│   │   └── Document "Style"
│   └── Document "Home"
└── Space "projects"
```

These rules apply to the tree:

- A document can be in a folder, in a different document, or at the top
  of the space.
- A folder can be in a folder, or at the top of the space. A folder
  cannot be in a document.
- There is no limit to the depth of the tree.
- No item can be in its own subtree.

## URLs

| URL | Page |
|-----|------|
| `/` | Goes to the default tenant. |
| `/admin/` | System administration. |
| `/{tenant}/` | The spaces of the tenant. |
| `/{tenant}/admin/` | Tenant administration. |
| `/{tenant}/{space}/` | The tree of the space. |
| `/{tenant}/{space}/d/{id}/{slug}` | One folder or document. |
| `/{tenant}/{space}/d/{id}` | Short link. It goes to the full link. |
| `/{tenant}/{space}/w/{slug}` | The target of a wiki link. |

- The tenant code has exactly 4 characters from `a-z` and `0-9`, for
  example `acme`.
- The space code has exactly 8 characters from `a-z` and `0-9`, for
  example `handbook`.
- An administrator selects the tenant code and the space code. The codes
  do not change after they are made.
- The `{id}` is a random value with 12 characters from `0-9`, `A-Z` and
  `a-z`, for example `Hq7mZp2Kx4aB`. The value has approximately 71 bits
  of entropy. Thus nobody can guess the ID of a document.
- The `{slug}` comes from the title, for example `install-on-linux`. It
  makes the URL easier to read. When the title changes, the old URL
  goes to the new URL.

The default installation has the tenant `dflt` ("Default") with the
space `default0` ("Default").

## Packages

| Package             | Function |
|---------------------|----------|
| `cmd/skriva`        | Reads the configuration. Prepares the default tenant. Starts and stops the server. |
| `internal/config`   | Reads the installation settings from environment variables. |
| `internal/db`       | Opens the connection pool. Applies the migrations. |
| `internal/store`    | Contains all SQL queries. |
| `internal/rbac`     | Defines the permissions and the permission set. |
| `internal/auth`     | Connects to the OIDC providers. Keeps one client for each tenant. |
| `internal/secret`   | Encrypts the client secrets of the tenants. |
| `internal/markdown` | Converts Markdown to safe HTML. Makes slugs from titles. |
| `internal/web`      | Contains the HTTP handlers, middleware and templates. |
| `internal/testdb`   | Makes temporary databases for the integration tests. |
| `e2e`               | Contains the end-to-end test. |

## Request flow

1. The middleware adds the security headers, the limits and the CSRF
   protection.
2. The dispatcher reads the first part of the path.
   - `/admin/...` goes to the system router.
   - A 4-character code goes to the tenant. The dispatcher loads the
     tenant and the session for that tenant.
3. If the second part of the path has 8 characters, the dispatcher loads
   the space and the permission data of the user in that space. The
   data contains the cached access paths, the default roles of the
   items with their own default role and the item bindings of the user.
   It does not contain the tree. Then the space router handles the
   request. The handler calculates the permissions of an item from its
   access path, in memory. Otherwise the
   tenant router handles the request.
4. The handler checks the permission. Then it reads or changes the data.

## Data model

| Table              | Contents |
|--------------------|----------|
| `tenants`          | Tenants and their settings. The client secret is encrypted. |
| `spaces`           | Spaces. Each space belongs to one tenant. A space can have its own default role. |
| `nodes`            | Folders and documents. `parent_id` makes the tree. An item can have its own default role. `access_path_id` points to the cached access path. |
| `access_paths`     | A cache: the access paths of the items of each space. See [Access control](access-control.md). |
| `revisions`        | All versions of each node. |
| `users`            | Users. The key is (tenant, issuer, subject). A user with no tenant is a system administrator. |
| `groups`           | Groups of a tenant. The source is `oidc` or `local`. |
| `group_members`    | The members of each group. |
| `roles`            | Built-in roles (no tenant) and custom roles (one tenant). |
| `role_permissions` | The permissions of each role. |
| `role_bindings`    | Each row gives one role to one user or to one group of a tenant, in all spaces, in one space or in one item. |
| `sessions`         | Browser sessions. The table keeps only a token hash. |
| `oidc_states`      | Data for logins that are in progress. |
| `schema_migrations`| The migrations that the server applied. |

### Tenant isolation

The database prevents data from different tenants in one relation:

- Composite foreign keys make sure that a group member and its group are
  in the same tenant. The same applies to role bindings and to the space
  of a role binding.
- A composite foreign key makes sure that the parent of a node is in the
  same space.
- Each store function that reads or changes tenant data has the tenant
  ID as a parameter. Each query uses it.

### Lost updates

The edit form contains the version that the user started from. The
server saves only if the item still has that version. If a different
user saved first, the server shows an error.

### Changes to the tree

A create, move or delete operation locks the row of the space. Thus two
operations on the tree of one space cannot run at the same time. This
prevents a cycle when two users move two folders into each other at the
same time. A move checks the subtree with a recursive query.

The deletion of a folder or a document also deletes its subtree.

### Markdown

The server uses [goldmark](https://github.com/yuin/goldmark) with these
extensions:

- GitHub Flavored Markdown (tables, task lists, strikethrough, links).
- Footnotes.
- Wiki links: `[[Title]]` and `[[Title#Section]]`. A wiki link points to
  `/{tenant}/{space}/w/{slug}`. That page finds the item with the slug in
  the same space.
- Automatic heading IDs.

The server does not permit raw HTML in Markdown. After the conversion,
[bluemonday](https://github.com/microcosm-cc/bluemonday) removes all
unsafe HTML a second time.

### Large spaces

- The permission data of a request does not depend on the number of
  items in the space. See "The permission cache" in
  [Access control](access-control.md).
- If a space has more than 2000 items, the tree at the left side shows
  only the top level and the levels on the path to the current item.
  The page of a folder or a document always shows its children.
- The benchmark `BenchmarkAccessLargeSpace` in `internal/store` measures
  the permission data for a space with 50000 items and 100 items with
  their own settings. On a development computer, it takes approximately
  2 ms.

### Search

The `nodes.search` column contains a `tsvector`. PostgreSQL calculates it
from the title and the text. A GIN index makes the search fast. Users can
search in one space or in all spaces of the tenant. The search uses the
`simple` text configuration. Thus the search works equally for all
languages, but it does not find word forms.

## Migrations

The files in `internal/db/migrations` are SQL files with the name
`NNNN_description.sql`. At start, the server applies each file that it
did not apply before. Each file runs in one transaction. An advisory
lock makes sure that only one server applies migrations at a time.

Migration `0003` moved the existing pages to the default space. It also
deleted all sessions. Thus all users had to sign in again.

Do not change a migration file after a release. Add a new file.
