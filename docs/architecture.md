# Architecture

## Overview

Skriva is one Go program. The program contains the HTTP server, the HTML
templates, the style sheet, the script of the editor and the database
migrations. It needs these external services:

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
| Document | An item in a space with formatted text and a history of versions. |
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
| `/{tenant}/{space}/files/{id}` | An uploaded image. A `POST` to `/{tenant}/{space}/files` uploads an image. |

- The tenant code has exactly 4 characters from `a-z` and `0-9`, for
  example `acme`.
- The space code has exactly 8 characters from `a-z` and `0-9`, for
  example `handbook`.
- An administrator selects the tenant code and the space code. The codes
  do not change after they are made.
- The `{id}` is a random value with 14 characters from `0-9` and `a-z`,
  for example `k3x9p2qa7tm4zb`. The value has approximately 72 bits of
  entropy. Thus nobody can guess the ID of a document. All parts of the
  URL are in lower case. The server accepts an ID in upper case and
  sends the browser to the URL in lower case.
- The `{slug}` comes from the title, for example `install-on-linux`. It
  makes the URL easier to read. When the title changes, the old URL
  goes to the new URL.

The slug algorithm:

1. Remove the diacritics. "Översikt" becomes "Oversikt".
2. Change the letters `A-Z` to lower case.
3. Replace each sequence of other characters with one `-`.
4. Remove `-` at the start and at the end. Keep a maximum of 100
   characters.

A slug in a URL is case insensitive. For a wiki link, the server changes
the slug to lower case before it finds the item. For a document URL, the
server sends the browser to the URL with the slug in lower case.

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
| `internal/doc`      | Checks the documents. Converts a document to HTML, Markdown and plain text. |
| `internal/slug`     | Makes slugs from titles. |
| `internal/web`      | Contains the HTTP handlers, middleware and templates. |
| `internal/testdb`   | Makes temporary databases for the integration tests. |
| `e2e`               | Contains the end-to-end test. |
| `web/editor`        | The source of the editor script (JavaScript). See [Development](development.md#editor). |

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
| `files`            | Uploaded images. The data is in the table. |
| `file_refs`        | The nodes that use each file, in one or more versions. |
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

### Document format

The text of a document is a tree of nodes in the JSON format of the
[Tiptap](https://tiptap.dev/) editor. This is the ProseMirror format, for
example:

```json
{"type": "doc", "content": [
  {"type": "heading", "attrs": {"level": 1}, "content": [{"type": "text", "text": "Install"}]},
  {"type": "paragraph", "attrs": {"textAlign": "center"}, "content": [
    {"type": "text", "text": "See "},
    {"type": "text", "text": "Configure", "marks": [{"type": "link", "attrs": {"href": "wiki:Configure"}}]}
  ]}
]}
```

The column `nodes.body` and the column `revisions.body` have the type
`jsonb`. The column `nodes.plain_text` contains the plain text of the
document. The search index uses the title and the plain text.

The package `internal/doc` has a list of the permitted nodes, marks and
attributes:

| Item | Permitted |
|------|-----------|
| Blocks | Paragraph, heading (levels 1 to 3), quotation, code block, horizontal line, bulleted list, numbered list, task list, image, table |
| Text formats (marks) | Bold, italic, underline, strikethrough, code, link |
| Attributes | Alignment (center, right) of paragraphs and headings, language of a code block, start of a numbered list, checked state of a task, the source and the descriptions of an image, the number of columns and rows that a table cell covers |
| Link destinations | `http`, `https`, `mailto`, `wiki:Title#Heading`, paths that start with one `/`, fragments |
| Image sources | `file:{id}` (an uploaded file), `https` URLs |

The server does not trust the editor. When it saves a document, it
removes all other nodes, marks and attributes. It refuses a document that
is not valid JSON, or that has more than 64 levels or more than 200000
nodes. The size limit of the tenant applies to the JSON text.

The package converts a checked document to:

- **HTML** for the document page. Alignment becomes a CSS class, for
  example `align-center`, not an inline style. Headings of levels 1 to 3
  become `h2` to `h4`, because the title of the page is the `h1`. The
  IDs of the headings use the slug algorithm. Thus `wiki:Title#Install &
  Run` and `wiki:Title#install & run` both go to the heading "Install &
  Run". After the conversion,
  [bluemonday](https://github.com/microcosm-cc/bluemonday) removes all
  unsafe HTML a second time.
- **Markdown** for the "Source" page. Markdown does not have all the
  formats. The conversion ignores the alignment and the underline. A cell
  that covers more than one column becomes one cell and empty cells.
- **Plain text** for the search index and the parts of the text in the
  search results.

A wiki link has the destination `wiki:Title`. The HTML has a link to
`/{tenant}/{space}/w/{slug}`. That page finds the item with the slug in
the same space.

### Uploaded files

An image in a document has the source `file:{id}`. The ID is a random
value with 14 characters, as the ID of a document. The editor shows the
URL `/{tenant}/{space}/files/{id}`. The server changes the URL to the
`file:` source when it saves the document, and back when it opens the
editor.

When the server saves a version of a document, it adds a row to
`file_refs` for each file in the text. It does not remove rows. Thus old
versions continue to show their images. A user can read a file if the
user can read one of the nodes in `file_refs`. Before a node uses a
file, only the user that uploaded it can read it. The cleanup task
deletes files that no node uses after one day.

### Editor

The form of a document contains the JSON of the text in a hidden field,
and a toolbar that the templates make. The script `static/editor.js`
starts the Tiptap editor, connects the toolbar buttons to the commands
and shows the toolbar. When the user saves, the script writes the JSON
into the hidden field. Only the form of a document loads the script.
See [Security](security.md#javascript).

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

The application has no release yet. Thus there is only one file,
`0001_init.sql`, with the full schema. Change this file for a schema
change, and make the development database again (see
[development.md](development.md)). After the first release, do not
change a migration file. Add a new file.
