# Architecture

## Overview

Skriva is one Go program. The program contains the HTTP server, the HTML
templates, the style sheet and the database migrations. It needs two
external services:

- A PostgreSQL database (version 15 or later).
- An OpenID Connect provider, for example Keycloak.

```
Browser ──HTTP──> Skriva ──SQL──> PostgreSQL
   │                 │
   └──── OIDC ───────┴──────────> Identity provider (Keycloak)
```

## Packages

| Package             | Function                                                  |
|---------------------|-----------------------------------------------------------|
| `cmd/skriva`        | Reads the configuration, starts and stops the server.     |
| `internal/config`   | Reads the settings from environment variables.            |
| `internal/db`       | Opens the connection pool. Applies the migrations.        |
| `internal/store`    | Contains all SQL queries.                                 |
| `internal/rbac`     | Defines the permissions and the permission set.           |
| `internal/auth`     | Connects to the OIDC provider. Verifies the ID token.     |
| `internal/markdown` | Converts Markdown to safe HTML. Makes slugs from titles.  |
| `internal/web`      | Contains the HTTP handlers, middleware and templates.     |
| `internal/testdb`   | Makes temporary databases for the integration tests.      |
| `e2e`               | Contains the end-to-end test.                             |

## Data model

| Table              | Contents                                                 |
|--------------------|----------------------------------------------------------|
| `users`            | One row for each user. The key is (issuer, subject).     |
| `groups`           | Groups. The source is `oidc` or `local`.                 |
| `group_members`    | The members of each group.                               |
| `roles`            | Roles. Built-in roles have `builtin = true`.             |
| `role_permissions` | The permissions of each role.                            |
| `role_bindings`    | Each row gives one role to one user or to one group.     |
| `pages`            | The current version of each page.                        |
| `page_revisions`   | All versions of each page.                               |
| `sessions`         | Browser sessions. The table keeps only a token hash.     |
| `oidc_states`      | Data for logins that are in progress.                    |
| `schema_migrations`| The migrations that the server applied.                  |

## Pages

The URL of a page is `/wiki/<slug>`. A slug contains only `a-z`, `0-9`
and `-`. The server makes the slug from the title of a new page. For
example, the title "Översikt 2026" gives the slug `oversikt-2026`. The
slug does not change when a user changes the title.

Each save makes a new row in `page_revisions`. The `version` column
increases by one at each save.

### Lost updates

The edit form contains the version that the user started from. The
server saves only if the page still has that version. If a different
user saved first, the server shows an error. Thus no user can overwrite
the changes of a different user without knowledge.

### Markdown

The server uses [goldmark](https://github.com/yuin/goldmark) with these
extensions:

- GitHub Flavored Markdown (tables, task lists, strikethrough, links).
- Footnotes.
- Wiki links: `[[Page title]]` and `[[Page title#Section]]`.
- Automatic heading IDs.

The server does not permit raw HTML in Markdown. After the conversion,
[bluemonday](https://github.com/microcosm-cc/bluemonday) removes all
unsafe HTML a second time.

### Search

The `pages.search` column contains a `tsvector`. PostgreSQL calculates it
from the title and the body. A GIN index makes the search fast. The
search uses the `simple` text configuration. Thus the search works
equally for all languages, but it does not find word forms.

## Migrations

The files in `internal/db/migrations` are SQL files with the name
`NNNN_description.sql`. At start, the server applies each file that it
did not apply before. Each file runs in one transaction. An advisory
lock makes sure that only one server applies migrations at a time.

Do not change a migration file after a release. Add a new file.
