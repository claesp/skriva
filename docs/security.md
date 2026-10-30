# Security

This document describes the security controls of Skriva.

## Authentication

- The server uses the OIDC authorization code flow.
- The server sends PKCE (`S256`), a `state` value and a `nonce`.
- The server keeps the login data in the database. A cookie binds the
  `state` value to the browser that started the login. The server
  accepts each `state` value only one time, and for a maximum of 10
  minutes.
- The server verifies the signature, issuer, audience, expiry and nonce
  of the ID token.
- The server identifies a user by the pair (issuer, subject). It does
  not use the e-mail address or the user name for identification.

## Sessions

- The session token is a random value with 128 bits.
- The database keeps only the SHA-256 hash of the token. Thus a copy of
  the database does not give access to sessions.
- The cookie has `HttpOnly` and `SameSite=Lax`. With an `https` base URL,
  the cookie also has `Secure` and the `__Host-` prefix.
- A session has a fixed maximum life (`SKRIVA_SESSION_TTL`).
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
- The Markdown converter does not permit raw HTML.
- The bluemonday sanitizer removes unsafe HTML from the result. It
  permits only the `http`, `https` and `mailto` URL schemes.
- The `Content-Security-Policy` header does not permit scripts. Thus a
  browser does not run a script, also if a script gets into a page.

## HTTP headers

The server sends these headers with each response:

| Header | Value |
|--------|-------|
| `Content-Security-Policy` | `default-src 'none'; style-src 'self'; img-src 'self' https:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'` |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `same-origin` |
| `Cross-Origin-Opener-Policy` | `same-origin` |
| `Cross-Origin-Resource-Policy` | `same-origin` |
| `Cache-Control` | `no-store` (not for static files) |
| `Strict-Transport-Security` | Only with an `https` base URL |

## Open redirects

The server accepts a `return_to` value only if it is a path on the same
site. The value must start with one `/`. It must not contain `\` or
control characters.

## Limits

- The maximum request body is 2 MiB.
- The maximum page text is 1 MiB.
- The maximum time for a request is 30 seconds.
- The HTTP server has timeouts for headers, reading, writing and idle
  connections.

## SQL injection

All queries use parameters. No query contains text from a user.

## Container

The container image contains only the program and CA certificates. The
program runs as user 65532, not as root.

## Known limits

- The server does not limit the rate of requests. Use a reverse proxy to
  limit the rate of requests to `/login` and `/auth/callback`.
- The server does not encrypt the connection. Use a reverse proxy with
  TLS, or a load balancer with TLS.
- Images from external `https` sites can show in pages. The external
  site can see the IP address of the reader.

## Vulnerability check

Run this command to find known vulnerabilities in the dependencies:

```sh
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```
