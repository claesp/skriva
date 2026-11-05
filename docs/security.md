# Security

This document describes the security controls of Skriva.

## Tenant isolation

- Each tenant has its own identity provider, users, groups, roles and
  sessions.
- The session cookie has the name `skriva_t<tenant ID>` (with the
  `__Host-` prefix for an `https` base URL). A session is valid only for
  its tenant. The server checks the tenant of the
  session and of the user at each request.
- The store functions for users, groups, roles, spaces and settings
  have the tenant ID as a parameter, and each query uses it.
- The functions for the data in a space (documents, files, the trash,
  search) have the space ID as a parameter. A space ID is an internal
  number (`spaces.id`), not the slug in the URL. The database makes it,
  and it is unique in all tenants. The server gets the space with the
  tenant ID and the slug (`Store.Space`). Thus a request can get only a
  space ID of its own tenant. The public ID of a document or a file is
  also unique in all tenants, and each query uses it together with the
  space ID.
- Composite foreign keys in the database also make sure that group
  members, role bindings and parent nodes are in the correct tenant or
  space.
- The system administration has its own cookie (`skriva_system`) and its
  own sessions. A system session has no permissions in a tenant. A
  tenant session gives no access to the system administration.

## Space permissions

- In a space, the server calculates the permissions for that space at
  each request. A handler cannot use the tenant-level permissions by
  mistake: the request data contains only the space permissions.
- A role binding for one space gives only `doc.*` permissions. Thus a
  user who has the `admin` role in one space cannot change the access
  control of the tenant.
- The tenant page and the search in all spaces show only the spaces
  that the user can read. If a user can read nothing in a space, all
  pages of the space send status 404. The page does not show the name
  of the space.
- The server checks the permissions for each item. If a user cannot
  read an item, the server sends status 404 and not 403. Thus a user
  cannot find out that the item exists. The tree, the lists and the
  search do not show the titles of items that the user cannot read.
- A deletion also removes the subtree. Thus the server requires
  `doc.delete` for each item in the subtree.
- The list of pages for a link (`link-targets`) and the list "Links to
  this page" show only the pages that the user can read. The list of
  pages also shows the title of the parent only if the user can read
  the parent. A `doc:` link to a page that the user cannot read goes to
  "not found".
- A deleted item goes into the trash. The item keeps its access path.
  Thus the trash uses the permissions of the old place. A restored item
  goes back to its old place only. If the old parent is in the trash,
  the server refuses the restore. Thus a restore cannot give an item
  different permissions. The trash shows the title of the old parent
  only if the user can read the parent.
- A move requires `doc.create` at the new place. Thus a user cannot move
  an item into a folder where the user cannot make items.

## Client secrets

- The server encrypts the OIDC client secret of each tenant with
  AES-256-GCM. The key is `SKRIVA_ENCRYPTION_KEY` (the KEK, see
  [Encryption at rest](#encryption-at-rest)).
- The associated data of the encryption contains the tenant ID. Thus a
  person who can write to the database cannot move a secret to a
  different tenant.
- The tenant page never shows the secret. A system administrator can
  only replace or remove it.

## Document IDs

The ID of a document is a random value with 14 characters from a set of
36 characters, `0-9` and `a-z` (approximately 72 bits). The server makes it with
`crypto/rand`. Thus nobody can find documents with a list of guessed
IDs. The ID is not the access control: the server checks the permissions
at each request.

## Authentication

- The server uses the OIDC authorization code flow.
- The server sends PKCE (`S256`), a `state` value and a `nonce`.
- The server keeps the login data in the database. A cookie binds the
  `state` value to the browser that started the login. The server
  accepts each `state` value only one time, and for a maximum of 10
  minutes.
- The server verifies the signature, issuer, audience, expiry and nonce
  of the ID token.
- The server identifies a user by the tenant, the issuer and the
  subject. It does not use the e-mail address or the user name for
  identification.
- The server accepts a `state` value only for the tenant (or the system
  administration) that started the login.
- An issuer URL must use `https`. An `http` URL is permitted only for
  `localhost`.

## Sessions

- The session token is a random value with 128 bits.
- The database keeps only the SHA-256 hash of the token. Thus a copy of
  the database does not give access to sessions. The ID token for the
  logout is encrypted with a key from the session token. See
  [Encryption](#encryption-at-rest).
- The cookie has `HttpOnly` and `SameSite=Lax`.
- Each tenant has its own cookie. Thus a browser can have sessions in
  more than one tenant. With an `https` base URL,
  the cookie also has `Secure` and the `__Host-` prefix.
- A session has a fixed maximum life. Each tenant has its own value. The
  system administration uses `SKRIVA_SESSION_TTL`.
- At each sign-in, the server makes a new token. This prevents session
  fixation.
- Logout deletes the session in the database, also if the identity
  provider is not available. Then the server sends a page that goes to
  the logout endpoint of the identity provider (a meta refresh). The
  server does not send a redirect, because the policy `form-action
  'self'` does not permit a redirect to a different site after a form.

## Cross-site request forgery (CSRF)

The server uses two controls:

1. Each form contains a CSRF token. The token is different for each
   session. The server compares the token in constant time.
2. The Go `http.CrossOriginProtection` middleware refuses cross-site
   requests that change data. It uses the `Sec-Fetch-Site` and `Origin`
   headers.

All requests that change data use `POST`. A `GET` request never changes
data.

## Cross-site scripting (XSS)

- The `html/template` package escapes all data in the templates.
- The server checks each document against a list of permitted nodes,
  marks and attributes before it saves the document. It removes all
  other items. A document cannot contain HTML: the converter makes all
  HTML from the list, and escapes all text. See
  [Document format](architecture.md#document-format).
- A link can go only to `http`, `https`, `mailto`, `wiki:`, `doc:`,
  fragments (`#…`) and paths on the same site. An image can come only from an uploaded file or an
  `https` URL. Thus a document cannot contain `javascript:` or `data:`
  URLs.
- The bluemonday sanitizer removes unsafe HTML from the result a second
  time. It permits only the `http`, `https` and `mailto` URL schemes.
- The `Content-Security-Policy` header does not permit scripts, except
  the editor script on the form of a document. Thus a browser does not
  run a script, also if a script gets into a page.
- A fuzz test (`FuzzParse` in `internal/doc`) makes sure that no input
  gives a script, an event handler or a script URL in the HTML.

## JavaScript

Only the form of a document uses JavaScript: the editor. These controls
apply:

- The script comes from the same site (`/static/editor.js`). The page
  does not load scripts from other sites, and it has no inline scripts.
- The form of a document has its own security policy. It adds only
  `script-src 'self'` and `connect-src 'self'` (for uploads). It does
  not add `'unsafe-inline'` or `'unsafe-eval'`. All other pages keep the
  policy without scripts.
- The editor does not add a style element or style attributes. The
  alignment uses CSS classes.
- The build installs the exact versions and checksums in
  `web/editor/package-lock.json`. The packages cannot run install
  scripts. The build stops if `npm audit` finds a known vulnerability.
- The server does not trust the editor. It checks all data again. See
  [Cross-site scripting](#cross-site-scripting-xss).

## Uploaded files

- A user must have the permission to edit the document (or to make a
  document in the parent) to upload an image.
- The maximum size is 10 MiB. The body limit of the request is larger
  only for uploads.
- The server finds the type from the data, not from the name or the
  request. It accepts PNG, JPEG, GIF and WebP, and draw.io diagrams in
  SVG format. It does not accept other SVG files, because an SVG file
  can contain scripts.
- A draw.io SVG file has three layers of protection:
  1. The upload check (`checkDrawioSVG`). The file must be well-formed
     XML with a root `svg` element that contains a draw.io diagram (the
     `content` attribute with an `mxfile`). The check refuses script,
     iframe, object, embed and similar elements, animation elements
     (`set`, `animate`), processing instructions (for example
     `xml-stylesheet`), all event attributes (`on…`), `javascript:` and
     `vbscript:` URLs, `data:` URLs that are not raster images, entity
     definitions, and more than 256 levels.
  2. The page shows the diagram in an `img` element. A browser does not
     run scripts and does not load other resources in an image.
  3. The server sends the file with the policy `default-src 'none';
     style-src 'unsafe-inline'; img-src data:; font-src data:; sandbox`.
     Thus a script cannot run also if a user opens the file directly.
     The diagram can use its inline styles and embedded images.
- A user can read a file only if the user can read a document that uses
  it. Before a document uses the file, only the user that uploaded it
  can read it. A file that no document uses after one day is deleted.
- A document uses a file only if the author can read the file when the
  author saves the document. Thus a user cannot get access to the file
  of a private document with a copy of its URL in a new document.
- The server sends a file with its type, `X-Content-Type-Options:
  nosniff` and the security policy `default-src 'none'; sandbox`. Thus a
  browser does not run content from a file, also if a user opens the
  file directly.
- The ID of a file is random, as the ID of a document. The server checks
  the permission for each request.

## HTTP headers

The server sends these headers with each response:

| Header | Value |
|--------|-------|
| `Content-Security-Policy` | `default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self' https:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'`. The form of a document adds `script-src 'self'; connect-src 'self'`. An uploaded file has `default-src 'none'; sandbox`. |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `same-origin` |
| `Cross-Origin-Opener-Policy` | `same-origin` |
| `Cross-Origin-Resource-Policy` | `same-origin` |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=(), interest-cohort=()` |
| `Cache-Control` | `no-store`. Static files: `public, max-age=300`, or one year (`immutable`) for the fonts and for the files with a version in the URL. Uploaded files and diagram images: `private, max-age=3600`. |
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains`, only with an `https` base URL |

## Destructive actions

A GET request never changes data. Each deletion of a tenant, a space, a
group, a role, a folder or a document opens a page that asks the user to
confirm. The deletion of a tenant or a space removes much data. The user
must also type the code of the tenant or the space to confirm. Only the
removal of a role binding or a group member, and the change of the
status of a user, are one-click actions. These actions are easy to undo.

## Open redirects

The server accepts a `return_to` value only if it is a path on the same
site. The value must start with one `/`. It must not contain `\` or
control characters.

## Limits

- The maximum request body is 2 MiB. For an upload, it is 10 MiB.
- All uploaded files of a space together have a quota (default 1024
  MiB). An administrator of the space can change it.
- A search reads at most 10000 matches. It removes the matches that the
  user cannot read before it applies the limit of results of each
  space. Thus the matches that a user cannot read do not hide the
  matches that the user can read. Only the text of the results is
  decrypted.
- The maximum document text is 1 MiB (the JSON text). Each tenant can
  set a lower limit. A document can have a maximum of 64 levels and
  200000 nodes.
- The maximum time for a request is 30 seconds.
- The server permits a maximum of 20 login starts per second, for all
  clients together. Each login start writes one row to the database.
  The limit prevents that anonymous clients fill the database.
- The server accepts only document versions that fit in 32 bits.
- The tree of a space shows a maximum of 10000 items. A space with more
  than 2000 items shows only the path to the current item.
- The server keeps a cache of the access paths in the database. The
  server updates the cache in the same transaction as each change to
  the tree or to the access settings of an item. The integration tests
  compare the cache with a full calculation after many random changes.
- The HTTP server has timeouts for headers, reading, writing and idle
  connections.

## Export

An export needs the permission `doc.read`, the same as the page. The
HTML file and the PDF file contain the images that the user can read
(the same check as for `/files/{id}`).

- **HTML**: the body goes through the same converter and sanitizer as
  the page. The sanitizer of the export also permits images in `data:`
  URIs of five image types. A Content Security Policy in the file
  permits no scripts.
- **PDF**: the server runs the [Typst](https://typst.app) program. The
  text of the document is never Typst code. Skriva writes the document
  as JSON data, and a fixed template (`internal/export/pdf.typ`) reads
  it. Typst shows strings from JSON as text. The process:
  - can read only the files in its temporary folder (`--root`),
  - uses only the fonts in the program (`--ignore-system-fonts`),
  - has empty package folders, thus it cannot load packages from the
    network,
  - has an empty environment, one thread (`--jobs 1`) and a time limit
    of 25 seconds,
  - can use 1 GiB of data memory (`RLIMIT_DATA`, `SKRIVA_TYPST_MEMORY`).
    If Typst needs more memory, the allocation fails and the export
    gives an error.
  - At most two processes run at the same time. Other requests wait.

  The server removes the temporary folder after each export.

## Mermaid diagrams

The server runs the mmdr program for the text of a diagram. The text
comes from a user.

- The SVG of mmdr is not safe in a page. It escapes the text of labels,
  but a `click` line makes a link, also a `javascript:` link. Thus the
  page shows the SVG only in an `img` element. A browser does not run
  scripts or follow links in an image. The image response has the same
  policy as an SVG file: `Content-Security-Policy` with `sandbox`, and
  `X-Content-Type-Options: nosniff`. Thus the links do not work, also
  when a user opens the image directly.
- The image URL needs the permission `doc.read` on the document. The
  server sends only the images of the diagrams in the document.
- The process gets the text on the standard input. It has an empty
  environment and no arguments from the user. It has a time limit of 5
  seconds and 256 MiB of data memory (`RLIMIT_DATA`,
  `SKRIVA_MMDR_MEMORY`). At most two processes run at the same time. The
  server keeps the result in a cache, also a time-out, an error or a
  diagram that needs too much memory. Thus a slow diagram does not run
  again at each view.
- The server sets the memory limit with `prlimit(2)` immediately after
  the start of the process. The limit is not available on systems other
  than Linux.
- The text can have a maximum of 32 KB. The image can have a maximum of
  4 MB. The error message has a maximum of 300 characters.
- The page shows the error message of mmdr as text. The message can
  contain parts of the diagram, but the HTML converter escapes it.

## SQL injection

All queries use parameters. No query contains text from a user.

## Container

The container image contains only the program, the Typst program for
the PDF export, the mmdr program for Mermaid diagrams, an empty
temporary folder and CA certificates. The programs run as user 65532,
not as root. The Containerfile gets Typst from its official image. The
project of mmdr has no static binary. Thus the Containerfile builds mmdr
from source, with a fixed version and the lock file of the crate
(`cargo install --locked`).

Each base image has a tag and a digest (`name:tag@sha256:...`), also
the images in the scripts. The digest is the digest of the multi-arch
index. Thus a build always uses the same image, also if the tag moves to
a new image. The tag shows only the version for the reader. To update
an image, change the tag and the digest together:

1. Pull the image, and show its digests:

   ```sh
   podman pull docker.io/library/golang:1.26-alpine
   podman image inspect --format '{{json .RepoDigests}}' docker.io/library/golang:1.26-alpine
   ```

2. The list has two digests: the index and the image of your platform.
   Find the index. For the index, this command shows `image.index` or
   `manifest.list` as the media type:

   ```sh
   podman manifest inspect docker.io/library/golang@sha256:... | head -3
   ```

## Information classification

- A document can have only a level of its own tenant. The store checks
  the level with a join on the space of the document.
- The classification does not change the permissions. It is a label
  for the readers. Use the access settings to control who can read a
  document.
- The audit log records each change of the classification of a
  document, each change to the levels and each change to the
  classification limits of a space or a folder.
- A folder can only make the limits of the space and of its parents
  stricter. The store calculates the limits from all parents at each
  check. Thus a folder setting cannot permit a level that a parent does
  not permit, also after a move.
- The limits of a folder need `space.manage`. A role binding for a
  folder gives only document permissions, thus only a space manager can
  change them.

## Audit log

The audit log records the changes to access rights (users, groups,
roles, role bindings, default roles), to spaces and tenants, the moves,
the restored versions, the trash actions and the sign-ins. See
[Administration: audit log](administration.md#audit-log).

- The server writes each event in the transaction of the change. If the
  server cannot write the event, the change fails.
- Only users with `tenant.manage` can read the audit log of a tenant.
  Only system administrators can read the system audit log.
- The application has no function to change or delete an event. The
  cleanup task deletes events after 365 days.
- The log does not contain IP addresses, tokens or secrets.

## Encryption at rest

Skriva encrypts the content of the documents in the database (envelope
encryption). See [Encryption at rest: research](encryption-at-rest.md)
for the methods that the project compared.

- **Keys.** Each tenant has a data key and a search key: 32 random bytes
  each. The table `tenant_keys` keeps them encrypted (wrapped) with the
  key encryption key (KEK), `SKRIVA_ENCRYPTION_KEY`. The KEK is not in
  the database. The server unwraps the keys of a tenant when it needs
  them, and keeps them in memory.
- **Encrypted data.** The title and the text of each folder and
  document (`nodes.title`, `nodes.body`), its plain text for the search
  results (`nodes.plain_text`), the titles and the text of all old
  versions (`revisions.title`, `revisions.body`), the uploaded files
  (`files.data`), and the target and the details of the audit events of
  the tenants (`audit_events.sealed`). The algorithm is AES-256-GCM. The associated data contains the tenant, the
  table, the column and the row. Thus a person who can write to the
  database cannot move a value to a different row or tenant.
- **Search.** The search index contains keyed hashes of the words
  (HMAC-SHA-256 with the search key, 8 bytes), not the words. The index
  tells which documents contain the same words, and how frequent a word
  is, but not the words. The search finds whole words only.
- **Titles.** The database cannot sort or filter encrypted titles. Thus
  the server decrypts the titles and sorts them in memory, for the tree,
  the lists and the page picker of the editor. Wiki links and backlinks
  use a keyed hash of the slug of the title (`nodes.slug_token`,
  `node_links.target_token`), not the slug. The search ranks the results
  with a separate index of the words of the title (`nodes.title_tokens`).
  This tells a database administrator which word hashes are in titles,
  but not the words.
- **Slugs in URLs.** The URL of a document contains the slug of its
  title, for example `/d/abc…/project-plan`. The server makes it from
  the decrypted title. It is not in the database, but it can be in the
  logs of a proxy and in the history of the browser.
- **Not encrypted.** The names of users, groups, roles and spaces, the
  structure of the tree, the times, the classification names of the
  versions, and the events of the system administration (tenant names and
  codes).
- **ID tokens.** The session keeps the ID token for the logout
  (`sessions.id_token`). The ID token contains the name, the email
  address and the groups of the user. The server encrypts it with
  AES-256-GCM. The key comes from the session token (HKDF-SHA256), and
  the database keeps only a hash of the token. Thus only a request with
  the cookie can decrypt the ID token. The key is not a tenant key, thus
  the ID token is also encrypted for system administrator sessions.
- **Key rotation.** A system administrator can make a new data key for a
  tenant (tenant page, **Encryption**). New values use the new key at
  once. The cleanup task encrypts the old values again, and then deletes
  the old key. The search key does not change.
- **KEK change.** Set the new key in `SKRIVA_ENCRYPTION_KEY` and the old
  key in `SKRIVA_ENCRYPTION_KEY_PREVIOUS`. At start, the server wraps all
  keys again with the new KEK. Then remove the old key.
- **Backups.** A backup of the database contains only encrypted content.
  To restore a backup, you need the KEK of the time of the backup. Keep
  the KEK in a different place than the backups.
- **Deletion of a tenant.** The keys of the tenant are deleted, also from
  the memory of the server that deletes the tenant. Old
  backups still contain the wrapped keys. They can be decrypted with the
  KEK of that time. Change the KEK to make old backups unreadable.
- **Limits.** The server decrypts the content for each request. Thus an
  attacker that controls the Skriva process or knows the KEK can read
  the content. A database administrator without the KEK cannot.

Also use encrypted storage and encrypted backups. They protect the
metadata that is not encrypted.

## Known limits

- The login limit applies to all clients together. Thus a flood of
  requests can stop the login for all users for a short time. Use a
  reverse proxy to limit the rate of requests for each client.
- The server does not encrypt the connection. Use a reverse proxy with
  TLS, or a load balancer with TLS.
- Images from external `https` sites can show in pages. The external
  site can see the IP address of the reader.

## Security checks

Run these commands before a release:

```sh
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go run github.com/securego/gosec/v2/cmd/gosec@latest -exclude-dir=e2e ./...
```

The first command finds known vulnerabilities in the dependencies. The
second command does a static analysis of the code. Each `#nosec` comment
in the code gives the reason for the exception.
