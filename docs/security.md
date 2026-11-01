# Security

This document describes the security controls of Skriva.

## Tenant isolation

- Each tenant has its own identity provider, users, groups, roles and
  sessions.
- The session cookie has the name `skriva_<tenant code>`. A session is
  valid only for its tenant. The server checks the tenant of the
  session and of the user at each request.
- Each store function for tenant data has the tenant ID as a parameter,
  and each query uses it. Composite foreign keys in the database also
  make sure that group members, role bindings and parent nodes are in
  the correct tenant or space.
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
  that the user can read.
- The server checks the permissions for each item. If a user cannot
  read an item, the server sends status 404 and not 403. Thus a user
  cannot find out that the item exists. The tree, the lists and the
  search do not show the titles of items that the user cannot read.
- A deletion also removes the subtree. Thus the server requires
  `doc.delete` for each item in the subtree.
- A move requires `doc.create` at the new place. Thus a user cannot move
  an item into a folder where the user cannot make items.

## Client secrets

- The server encrypts the OIDC client secret of each tenant with
  AES-256-GCM. The key is `SKRIVA_ENCRYPTION_KEY`.
- The associated data of the encryption contains the tenant code. Thus a
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
  the database does not give access to sessions.
- The cookie has `HttpOnly` and `SameSite=Lax`.
- Each tenant has its own cookie. Thus a browser can have sessions in
  more than one tenant. With an `https` base URL,
  the cookie also has `Secure` and the `__Host-` prefix.
- A session has a fixed maximum life. Each tenant has its own value. The
  system administration uses `SKRIVA_SESSION_TTL`.
- At each sign-in, the server makes a new token. This prevents session
  fixation.
- Logout deletes the session in the database. Then the server sends the
  browser to the logout endpoint of the identity provider.

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
- A link can go only to `http`, `https`, `mailto`, `wiki:` and paths on
  the same site. An image can come only from an uploaded file or an
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
  request. It accepts only PNG, JPEG, GIF and WebP. It does not accept
  SVG, because an SVG file can contain scripts.
- A user can read a file only if the user can read a document that uses
  it. Before a document uses the file, only the user that uploaded it
  can read it. A file that no document uses after one day is deleted.
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
| `Cache-Control` | `no-store` (not for static files) |
| `Strict-Transport-Security` | Only with an `https` base URL |

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
  `Store.RebuildAccessPaths` calculates the cache of a space again.
- The HTTP server has timeouts for headers, reading, writing and idle
  connections.

## SQL injection

All queries use parameters. No query contains text from a user.

## Container

The container image contains only the program and CA certificates. The
program runs as user 65532, not as root.

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
