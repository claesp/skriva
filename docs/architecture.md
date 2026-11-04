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
| `/{tenant}/login`, `/{tenant}/auth/callback` | The sign-in through the identity provider. |
| `/{tenant}/logout`, `/{tenant}/signed-out` | The sign-out (`POST`), and the page after it. |
| `/{tenant}/search?q=` | The search in all spaces that the user can read. |
| `/{tenant}/admin/` | Tenant administration. `/{tenant}/admin/audit` is the audit log. |
| `/{tenant}/{space}/` | The tree of the space. |
| `/{tenant}/{space}/d/{id}/{slug}` | One folder or document. |
| `/{tenant}/{space}/d/{id}` | Short link. It goes to the full link. |
| `/{tenant}/{space}/w/{slug}` | The target of a wiki link. |
| `/{tenant}/{space}/new`, `/edit/{id}`, `/move/{id}`, `/delete/{id}` | The forms to make, change, move and delete an item. |
| `/{tenant}/{space}/history/{id}` | The versions of a document. `/history/{id}/{version}` shows one version. |
| `/{tenant}/{space}/access/{id}` | The access settings of an item. |
| `/{tenant}/{space}/search?q=` | The search in the space. |
| `/{tenant}/{space}/compare/{id}?from=&to=` | The changes between two versions of a document. |
| `/{tenant}/{space}/export/{id}` | The export formats of a document. `/export/{id}/{format}` downloads the file. `/export/{id}/{format}/text` shows a text format as plain text. |
| `/{tenant}/{space}/trash` | The trash of the space. |
| `/{tenant}/{space}/link-targets?q=` | JSON: the pages for the list of pages in the link bar of the editor. |
| `/{tenant}/{space}/files/{id}` | An uploaded image. A `POST` to `/{tenant}/{space}/files` uploads an image. |
| `/{tenant}/{space}/diagram/{id}/{key}` | The image of a Mermaid diagram in a document. `?v=` selects an old version. See [Mermaid diagrams](#mermaid-diagrams). |

- The tenant code and the space code have 2 to 16 characters from
  `a-z`, `0-9` and `-`, for example `acme` and `handbook`. The first and
  the last character cannot be `-`.
- An administrator selects the codes, and can change them later. The
  session cookie and the encryption of the client secret use the ID of
  the tenant, not the code. Thus a new code does not end the sessions.
- The first segment of each route at the top level (for example
  `admin`) cannot be a tenant code. The first segment of each route at
  the tenant level (for example `login`) cannot be a space code. The
  server records these names when it registers the routes (`namesMux`
  in `internal/web/server.go`). A new route at the tenant level hides a
  space with the same code. Thus check the space codes before you add
  a route.
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
| `internal/secret`   | AES-256-GCM with the KEK. It encrypts the client secrets and wraps the keys of the tenants. |
| `internal/doc`      | Checks the documents. Converts a document to HTML, Markdown and plain text. |
| `internal/export`   | Makes the export files of a document. See [Export](#export). |
| `internal/diagram`  | Makes the images of Mermaid diagrams with the mmdr program. See [Mermaid diagrams](#mermaid-diagrams). |
| `internal/slug`     | Makes slugs from titles. |
| `internal/diff`     | Compares two sequences (the Myers algorithm). |
| `internal/web`      | Contains the HTTP handlers, middleware and templates. |
| `internal/testdb`   | Makes temporary databases for the integration tests. |
| `e2e`               | Contains the end-to-end test. |
| `web/editor`        | The source of the editor script (JavaScript). See [Development](development.md#editor). |

## Request flow

1. The middleware adds the security headers, the limits and the CSRF
   protection.
2. The dispatcher reads the first part of the path.
   - `/admin/...` goes to the system router.
   - A tenant code goes to the tenant. The dispatcher loads the tenant
     and the session for that tenant.
3. If the second part of the path is a space code and not the name of a
   tenant-level route, the dispatcher loads
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
| `nodes`            | Folders and documents. `parent_id` makes the tree. An item can have its own default role. `access_path_id` points to the cached access path. `deleted_at` and `trash_id` mark the items in the trash. |
| `access_paths`     | A cache: the access paths of the items of each space. See [Access control](access-control.md). |
| `revisions`        | All versions of each node, with the name of the classification of each version. |
| `tenant_keys`      | The data key and the search key of each tenant, wrapped with the KEK. |
| `classifications`  | The levels of information classification of each tenant. `nodes.classification_id` gives the level of a document. |
| `files`            | Uploaded images. The data is in the table, encrypted. |
| `file_refs`        | The nodes that use each file, in one or more versions. |
| `node_links`       | The links in the current version of each node: the target node of a `doc:` link, or the keyed hash of the slug of a wiki link. |
| `users`            | Users. The key is (tenant, issuer, subject). A user with no tenant is a system administrator. |
| `groups`           | Groups of a tenant. The source is `oidc` or `local`. |
| `group_members`    | The members of each group. |
| `roles`            | Built-in roles (no tenant) and custom roles (one tenant). |
| `role_permissions` | The permissions of each role. |
| `role_bindings`    | Each row gives one role to one user or to one group of a tenant, in all spaces, in one space or in one item. |
| `audit_events`     | The audit log. A row with no tenant is an event of the system administration. |
| `sessions`         | Browser sessions. The table keeps a hash of the token, not the token. It also keeps the CSRF token and the ID token for the logout. |
| `oidc_states`      | Data for logins that are in progress. |
| `schema_migrations`| The migrations that the server applied. |

### Tenant isolation

The database prevents data from different tenants in one relation:

- Composite foreign keys make sure that a group member and its group are
  in the same tenant. The same applies to the user, the group and the
  space of a role binding. A built-in role has no tenant, thus the store
  checks the tenant of the role of a binding.
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

### Trash

A deletion moves a node and its subtree into the trash. The server sets
`deleted_at`, `deleted_by` and `trash_id` on each node of the subtree.
`trash_id` is the node that the user deleted. Thus one trash item is
one node with `trash_id = id`, and a restore clears the columns of all
nodes with that `trash_id`. A node of the subtree that was in the trash
already keeps its own `trash_id`. Thus it stays a separate trash item.

All queries for the tree, the pages, the search, the wiki links and the
files use only nodes with `deleted_at IS NULL`. The nodes in the trash
keep their revisions, their files and their access paths.

A restore puts the nodes back at their old place. If the parent is in
the trash, the server refuses the restore. A permanent deletion removes
the node. The foreign keys then remove the subtree, the revisions and
the file references. The cleanup task permanently deletes the trash
items that are older than 30 days (`store.TrashDays`).

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

The columns `nodes.body` and `revisions.body` contain this JSON,
encrypted. The column `nodes.plain_text` contains the plain text of the
document, encrypted. The search index uses the title and the plain
text. See "Encryption of the content".

The package `internal/doc` has a list of the permitted nodes, marks and
attributes:

| Item | Permitted |
|------|-----------|
| Blocks | Paragraph, heading (levels 1 to 8), quotation, code block, horizontal line, bulleted list, numbered list, task list, image, table |
| Text formats (marks) | Bold, italic, underline, strikethrough, code, link |
| Attributes | Alignment (center, right) of paragraphs and headings, language of a code block, start of a numbered list, checked state of a task, the source and the descriptions of an image, the number of columns and rows that a table cell covers |
| Link destinations | `http`, `https`, `mailto`, `doc:ID#Heading`, `wiki:Title#Heading`, paths that start with one `/`, fragments |
| Image sources | `file:{id}` (an uploaded file), `https` URLs |

The server does not trust the editor. When it saves a document, it
removes all other nodes, marks and attributes. It refuses a document that
is not valid JSON, or that has more than 64 levels or more than 200000
nodes. The size limit of the tenant applies to the JSON text.

The package converts a checked document to:

- **HTML** for the document page. Alignment becomes a CSS class, for
  example `align-center`, not an inline style. Headings of levels 1 to 5
  become `h2` to `h6`, because the title of the page is the `h1`. HTML
  has no `h7` to `h9` elements. Thus headings of levels 6 to 8 become
  `p` elements with `role="heading"` and `aria-level` 7 to 9. Screen
  readers know the level from these attributes. Each heading has the
  class `level-1` to `level-8`, and the style sheet uses the class. The
  IDs of the headings use the slug algorithm. Thus `wiki:Title#Install &
  Run` and `wiki:Title#install & run` both go to the heading "Install &
  Run". After the conversion,
  [bluemonday](https://github.com/microcosm-cc/bluemonday) removes all
  unsafe HTML a second time.
- **Markdown** for the [export](#export). Markdown does not have all the
  formats. The conversion ignores the alignment and the underline. A cell
  that covers more than one column becomes one cell and empty cells.
- **Plain text** for the search index and the parts of the text in the
  search results.

A wiki link has the destination `wiki:Title`. The HTML has a link to
`/{tenant}/{space}/w/{slug}`. That page finds the item with the slug in
the same space.

A document link has the destination `doc:ID`, with the public ID of the
target. The HTML and the Markdown have a link to the short link
`/{tenant}/{space}/d/{id}`, and the short link goes to the full URL.
Thus a document link stays correct when the target gets a new title or
moves. The link bar of the editor makes these links: it gets a list of
pages from `/{tenant}/{space}/link-targets`. The list contains only the
pages that the user can read.

### Links between pages

When the server saves a node, it replaces the rows of the node in
`node_links`. A `doc:` link gives a row with the target node, if the
target is in the same space. A wiki link gives a row with the keyed
hash of the slug of the title (`target_token`), because the target can
be made later. The page of a node shows the nodes that link to it: the
rows with the node as target, and the rows with the hash of the current
slug of the node (`nodes.slug_token`). The query uses two indexes:
one for each type of row.

### Information classification

Each tenant has its levels in `classifications`. The function
`add_default_classifications` gives a new tenant the default levels.
Each document has one level (`nodes.classification_id`); a folder has
none (a check constraint). The table `nodes` has no tenant column, thus
a foreign key cannot make sure that the level belongs to the tenant of
the space. The store makes this check: it finds the level with a join
on the space (`classificationFor`). The database checks the foreign key
to the level at the end of the transaction (deferred). Thus the deletion
of a tenant
removes the levels and the documents in one statement, and the deletion
of a level that documents use fails.

### Export

The package `internal/export` has a list of formats (`export.Formats`).
Each format has a name for the URL, a file name extension, a media type
and a function that writes the file. The formats are Markdown, HTML and
PDF. To add a format, add a `Format` value to the list in
`export.NewFormats`. The export page shows
all formats in the list.

- The server makes the file from the current version. The permission
  `doc.read` is necessary, the same as for the page.
- The file has the title and the information classification as
  metadata (in Markdown, the YAML front matter; in HTML, `meta`
  elements). Thus the classification
  stays with the text outside of Skriva. The text does not repeat the
  classification.
- Links and images get absolute URLs with `SKRIVA_BASE_URL`, because
  the file is not on the server. Also a link to a path on the same site
  (`/tenant/...`) gets the base URL. Wiki links become normal links.
- The HTML file contains the images as `data:` URIs. The file server
  sends `Cross-Origin-Resource-Policy: same-origin`, thus a file on
  the disk of the user cannot load the images from Skriva. The export
  uses the same permission check as the file server
  (`Server.readableFile`). The images have a maximum total size of
  32 MB (`export.MaxEmbeddedBytes`). Each use of an image counts,
  because the file contains the data again for each use. Images after the limit, and images
  of other types, keep their URLs.
- The HTML body comes from the same converter and the same bluemonday
  policy as the page. The export policy also permits `data:` URIs of
  the types gif, jpeg, png, svg+xml and webp in base64
  (`doc.HTMLOptions.DataImages`). A browser does not run the scripts of
  an SVG file in an `img` element. The file has a Content Security
  Policy in a `meta` element that permits no scripts.
- The values in the YAML front matter are JSON strings. A JSON string
  is also a valid YAML string in double quotes. Thus a title cannot
  change the structure of the block.
- The PDF format runs [Typst](https://typst.app). Typst is one static
  program without other dependencies. It does not use JavaScript. The
  Go code writes `doc.json` (the cleaned document with absolute links),
  the images and the template `pdf.typ` into a temporary folder, and
  runs `typst compile`. The template reads the data with `json()`. Thus
  the text of the document is data, not Typst code. If Typst cannot
  read an image, the server makes the file again without the images.
  See [Security](security.md#export) for the limits of the process.
  Without the Typst program (`SKRIVA_TYPST`), the export page shows the
  PDF format with disabled buttons.
- A PDF page shows the classification at the top and at the bottom,
  the same as a printed page. The PDF metadata also contains it
  (subject and keywords).
- The download has `Content-Disposition: attachment` and the file name
  `{slug}{extension}` (or `{id}{extension}` for an empty slug). The responses have
  `Cache-Control: no-store`, the same as the pages.

### Mermaid diagrams

A Mermaid diagram is a code block with the language `mermaid`. The
document stores only the text. The server makes the image with the
[mmdr](https://github.com/1jehuang/mermaid-rs-renderer) program
(mermaid-rs-renderer), thus the browser needs no JavaScript. mmdr is
one static program in Rust. It does not use a browser or Node.js.

- The page shows each diagram as an `img` element. The URL contains the
  ID of the document and a key of the text (the first 16 bytes of its
  SHA-256 hash, `diagram.Key`). The page CSP does not permit `data:`
  images, thus the page refers to the image.
- The page renders each diagram before it sends the HTML. Thus it can
  show the error message of mmdr below the text of a bad diagram.
- `/diagram/{id}/{key}` reads the document (or the version in `?v=`),
  finds the diagram with the key, and sends its image. The server does
  not store images. Thus the URL works on each server of a cluster.
- The images have the style of the page (`diagram.DefaultConfig`): the
  font Inter, and the page color behind labels. mmdr gets the
  configuration on file descriptor 3 (`-c /dev/fd/3`), thus the server
  writes no files. An `%%{init}%%` line in a diagram changes these
  values. `Render` removes the rectangle of the background, thus the
  image is transparent.
- An image in an `img` element cannot use the fonts of the page. Thus
  the image response contains the Latin part of Inter as a `data:`
  font (approximately 64 KB). The SVG policy permits `data:` fonts.
  mmdr measures the text with its own estimate, because the container
  has no system fonts. The PDF export uses the fonts of Typst.
- `diagram.Renderer` keeps the recent images and error messages in a
  cache in memory (32 MB). A diagram that takes more than 5 seconds is
  also an error in the cache. A key is the same only for the same text.
  Thus the cache needs no invalidation.
- The HTML export contains the images as `data:` URIs. The PDF export
  writes the images into the temporary folder of Typst. The Markdown
  export keeps the text of the diagram.
- Without the mmdr program (`SKRIVA_MMDR`), a diagram is a code block.

See [Security](security.md#mermaid-diagrams) for the limits.

### Audit log

The store functions that change access rights, spaces, tenants, the
trash or the place of an item write an event into `audit_events`. They
use the transaction of the change. Thus the change and the event are
saved together, or not at all. The HTTP middleware puts the ID of the
user into the context of the request (`store.WithActor`). The store
reads the name of the user when it writes the event. Without a user in
the context, the event shows "System".

The event keeps the names and the codes from the time of the event: the
name of the user, the code of the space and the title of the item. Thus
the log does not need the rows of these items. The foreign keys set
`actor_id` to NULL when a user is deleted, and remove the events of a
deleted tenant. The deletion of a tenant is an event of the system
administration, thus it stays in the log.

### Comparison of versions

The comparison uses the Markdown of the two versions. The package
`internal/diff` compares the lines with the algorithm of Eugene W.
Myers. It removes the common start and end first. In each block of
changed lines, the server compares each removed line with the added
line at the same position, word by word. The page shows three equal
lines around each change.

The time of the algorithm grows with the number of changes. Thus a
small change in a long document is fast. These limits keep the time and
the memory of one request small:

- If two versions have more than 1000 changed lines, the result shows
  the middle part as removed and added, without a search for equal
  lines.
- A line with more than 2000 words, or with more than 200 changed words,
  shows as fully changed.
- The comparison word by word stops after 200000 words in all lines.
  The lines after this limit show as fully changed.

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

A draw.io diagram is an uploaded file of the type `image/svg+xml`.
draw.io can export a diagram as SVG with a copy of the diagram in the
`content` attribute of the root element (a `.drawio.svg` file). Thus
the browser shows the file as an image, and draw.io can open it again.
A `.drawio.png` file is a PNG image with the diagram in a text chunk;
the server accepts it as a normal PNG image. A plain `.drawio` file
contains only XML. A browser needs the script of draw.io to show it,
thus the server does not accept it. See [Security](security.md#uploaded-files).

### Editor

The form of a document contains the JSON of the text in a hidden field,
and a toolbar that the templates make. The script `static/editor.js`
starts the Tiptap editor, connects the toolbar buttons to the commands
and shows the toolbar. When the user saves, the script writes the JSON
into the hidden field. Only the form of a document loads the script.
See [Security](security.md#javascript).

The editor uses the same HTML elements as the document page, thus the
same styles apply. For example, a heading of level 1 is an `h2` element
in the editor and on the page.

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

### Encryption of the content

The store encrypts the content before it writes it, and decrypts it
after it reads it (`internal/store/keys.go`). The web package sees only
plain text.

- An encrypted value has the format: one byte for the format (1), the
  version of the data key (4 bytes), the nonce of GCM (12 bytes), and
  the encrypted data with the tag. The column `key_version` of the row
  repeats the version, thus the re-encryption can find the old values.
- The associated data is `skriva:<tenant>:<table>.<column>:<space>:<public
  ID>` (and the version for a revision). It uses public IDs, because the
  store knows them before the insert.
- The store makes the keys of a tenant when it needs them for the first
  time, in its own transaction. Thus a key that encrypted a value is
  always saved, also if the transaction of the value fails.
- The store reads the keys with a separate small connection pool (two
  connections). The store can need a key in a transaction, which holds a
  connection of the main pool. With one pool, many requests at the same
  time could wait for each other until all connections are in use.
- The keys stay in memory for 5 minutes. Then the store reads the
  current version again, because a different server can rotate the key.
  The store deletes a retired key only after one hour, when no value
  uses it. The re-encryption encrypts all values of one row with the
  same key. It does not stop for a value that it cannot decrypt: that
  value keeps its key, thus the key stays.
- The title of a node is encrypted as the text. The store decrypts the
  titles of a list and sorts them in memory: folders first, then the
  titles in the root collation of Unicode (CLDR), case ignored. The
  page picker of the editor (`FindLinkTargets`) decrypts the titles of
  the space (at most 10000) and filters them in memory. For a space with
  10000 items, this takes approximately 100 ms on a development
  computer. The tree of a large space (more than 2000 items) reads only
  the levels on the path.
- The audit events of a tenant keep the target and the details in the
  column `sealed`: a JSON object, encrypted with the place
  `audit_events.sealed:<id>`. The store takes the ID from the sequence
  before the insert. The events of the system administration are not
  encrypted.
- A new tenant gets its keys in the transaction that makes the tenant,
  because other connections cannot see the tenant before the commit.
- The KEK is an interface (`store.KEK`). The current implementation uses
  the key from the configuration. A key management system (for example
  HashiCorp Vault Transit) can implement the same interface.

### Search

The text is encrypted, thus PostgreSQL cannot search it. The column
`nodes.search_tokens` (`bigint[]`, with a GIN index) contains a keyed
hash of each word of the title and the text: the first 8 bytes of
HMAC-SHA-256 with the search key of the tenant. A word is a sequence of
letters and digits, in lower case. A search makes the hashes of the
search words and finds the rows that contain all of them (`@>`). Items
with the words in the title (`nodes.title_tokens`) come first. The store decrypts the plain
text of the results and makes the parts of the text with the words.
The search finds whole words only, not word parts or phrases. A word
with `-` before it (`-word`) excludes the items with that word.

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
