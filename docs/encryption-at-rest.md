# Encryption at rest: research

This document compares methods to encrypt the documents in the database
at rest. It gives a recommendation. The data is from 2026-11-01. This is
research. Skriva now implements method 5 (envelope encryption) for the
titles and the content of the documents, the files and the audit log of
the tenants, with the KEK from the configuration. See
[Security: encryption at rest](security.md#encryption-at-rest).
A KMS for the KEK is not done yet.

## The data to protect

Before the encryption, the content of the documents was in these
columns. The current columns are in
[Security: encryption at rest](security.md#encryption-at-rest).

| Table | Columns | Contents |
|-------|---------|----------|
| `nodes` | `title`, `slug`, `body`, `plain_text`, `search` | The current version. `search` was a `tsvector`: it contained the words of the title and the text. |
| `revisions` | `title`, `body` | All old versions. |
| `files` | `data` | Uploaded images and draw.io diagrams. |
| `node_links` | `target_slug` | The titles of the targets of wiki links. |
| `audit_events` | `target`, `details` | The titles of items. |

Other data (user names, group names, role bindings, the names of the
spaces) is metadata. It is less sensitive, but it is not encrypted
either.

PostgreSQL writes these values into its data files, into the
write-ahead log (WAL), into temporary files, and into backups. A
`pg_dump` file contains them as plain text.

## Threats

The methods protect against different threats:

| Threat | Example |
|--------|---------|
| T1. Stolen storage | A stolen disk, a disk that the provider throws away, a copy of a volume snapshot. |
| T2. Stolen backup | A copy of a backup file or of a backup repository. |
| T3. Access to the database server | A database administrator, or an attacker with a database login or with access to the operating system of the database server. |
| T4. Access to the application server | An attacker that controls the Skriva process. |

No method protects against T4: the application must read the text to
show it. The difference between the methods is T3.

## Methods

### 1. Encryption of the storage (disk or volume)

The operating system or the provider encrypts the volume of the
database: LUKS (dm-crypt) on Linux, ZFS native encryption, or the
encryption of the cloud volumes (for example Amazon EBS, Azure managed
disks, Google persistent disks). Most cloud providers encrypt all
volumes by default.

- Protects against: T1.
- Does not protect against: T2 (a `pg_dump` file is plain text), T3.
- Changes to Skriva: none.
- Performance: the CPU encrypts with AES-NI. The cost is very small.
- Key management: the provider or the operating system. LUKS needs a
  passphrase or a key at boot (for example from a TPM or from Tang/Clevis).

### 2. Encrypted backups

The backup tool encrypts the backups. pgBackRest encrypts its full
repository with AES-256 (`repo1-cipher-type=aes-256-cbc` and
`repo1-cipher-pass`). The encryption must be set when the repository is
made: a change needs a new repository. A `pg_dump` file can be
encrypted with a tool such as `age` or `gpg`.

- Protects against: T2.
- Changes to Skriva: none. The operator configures the backup tool.

### 3. Transparent data encryption (TDE) in PostgreSQL

The database server encrypts the data files and the WAL. The SQL
queries do not change.

- Community PostgreSQL (version 18) has no TDE. There are proposals,
  but no TDE is in the core.
- **Percona pg_tde** is an open source extension. Percona made it
  generally available on 2025-07-01 in Percona Distribution for
  PostgreSQL. It encrypts tables (the access method `tde_heap`), their
  indexes and the WAL. It gets the keys from a key management system
  (for example HashiCorp Vault or a KMIP server). It needs the server of
  the Percona distribution: the community server does not have the
  necessary hooks.
- Other products: EDB Postgres Advanced Server (commercial), Fujitsu
  Enterprise Postgres (commercial), Cybertec PostgreSQL TDE (a patched
  server), open_pg_tde of Command Prompt (needs a patch of the core),
  pg_vault_tde (an extension).

- Protects against: T1, and T2 for physical backups of the data files.
- Does not protect against: T3 (the server decrypts the data for each
  query; a database administrator sees plain text), and `pg_dump`
  files (plain text).
- Changes to Skriva: small. The tables use the access method of the
  extension (`CREATE TABLE ... USING tde_heap`). The full text search,
  the sort order and all queries continue to work.
- Disadvantages: the installation needs a special PostgreSQL server, not
  the official image. The operator depends on one vendor and must test
  each upgrade. Temporary files are not encrypted in all products.

### 4. Encryption in SQL with pgcrypto

The extension `pgcrypto` has functions such as `pgp_sym_encrypt`. The
query sends the key to the server.

- Protects against: T1 and T2 for the encrypted columns.
- Does not protect against: T3. The key is in the text of the queries.
  It can be in the server log, in `pg_stat_statements` and in
  `pg_stat_activity`.
- Not recommended. Encryption in the application (method 5) gives more
  protection with the same changes to the queries.

### 5. Encryption in the application (envelope encryption)

Skriva encrypts the content before it sends it to the database, and
decrypts it after it reads it. The database contains only ciphertext.

The design:

1. A **key encryption key (KEK)** is outside the database: in a key
   management system (HashiCorp Vault Transit, AWS KMS, Azure Key
   Vault, Google Cloud KMS) or, as the simplest form, in the
   configuration, as the current `SKRIVA_ENCRYPTION_KEY`.
2. Each tenant has a **data encryption key (DEK)**: 32 random bytes. The
   database keeps the DEK encrypted with the KEK (a new table
   `tenant_keys`, with a key version). Skriva decrypts the DEK one time
   and keeps it in memory.
3. Skriva encrypts each value with AES-256-GCM and the DEK of the
   tenant. The package `internal/secret` already does this for the
   client secrets. The additional data (AAD) of GCM contains the table,
   the column and the row. The implementation uses
   `skriva:<tenant>:revisions.body:<space>:<public ID>:<version>`.
   Thus an attacker cannot move a ciphertext to a different row.
4. Each ciphertext starts with the version of the key. Thus a new key
   can come into use, and a background task can encrypt the old values
   again (key rotation).

- Protects against: T1, T2 (also `pg_dump` files), and T3. A database
  administrator sees only ciphertext.
- Crypto-shredding: when a tenant is deleted, the deletion of its DEK
  makes its data unreadable in the live database. Old backups contain
  the wrapped DEK. The KEK of that time can decrypt it. Change the KEK
  to make old backups unreadable.
- Changes to Skriva: large. See the next section.
- Performance: AES-GCM with AES-NI encrypts more than 1 GB/s on one CPU
  core. A document is at most 1 MiB. The cost is small. The KMS call
  happens only when a DEK is loaded.

#### Effects of method 5 on the functions of Skriva

| Function | Before the encryption | With encrypted content |
|----------|-----|------------------------|
| Full text search | `tsvector` in PostgreSQL with a GIN index. | Not possible on ciphertext. A plain `tsvector` would contain all words of the text. See "Search" below. |
| Title search for links, wiki links (slug), sort by title | SQL (`ILIKE`, `lower(title)`, `slug = $1`). | The titles must stay plain text, or the application sorts and filters in memory. A slug can be a keyed hash (HMAC) of the title. |
| Backlinks (`node_links.target_slug`) | Plain slug. | HMAC of the slug. |
| Audit log (`target`) | Plain titles. | Encrypt the column, or keep only IDs. |
| Comparison of versions, Markdown, HTML | In the application. | No change. |
| `body` as `jsonb` | The type `jsonb`. | The type `bytea`. The server checks the JSON before the encryption, as now. |
| Uploaded files | `bytea`. | `bytea` with ciphertext. No other change. |

#### Search with encrypted content

There are three options:

1. **Blind index.** For each word of a document, Skriva stores a keyed
   hash: HMAC-SHA-256 with a search key of the tenant, of the normalized
   word, cut to 8 bytes. A `bigint[]` column with a GIN index keeps the
   hashes. A search makes the hashes of the search words and finds the
   documents with all hashes. This supports whole words, not word parts
   or phrases, and it has no ranking. It tells the database
   administrator which documents have the same words, and how frequent
   a word is. It does not tell the words. Skriva uses this method. It
   also keeps hashes of the first 3 to 20 letters of each word, for a
   search for the start of a word, and hashes of the English stems of
   the words, for a search for other forms of a word. It puts the
   results with whole words first. See [Architecture: search](architecture.md#search).
2. **Search in the application.** Skriva decrypts the readable documents
   of a space and searches in memory. This is correct, but slow for
   large spaces.
3. **Keep the search index in plain text.** This leaks the words of all
   documents, thus it removes most of the protection.

Option 1 is the best compromise.

### 6. Other methods

- **Client-side (end-to-end) encryption:** the browser encrypts the
  documents. Skriva then cannot make HTML, search or compare versions on
  the server, and the editor would need more JavaScript. This does not
  agree with the design of Skriva.
- **PostgreSQL in a confidential VM** (AMD SEV-SNP, Intel TDX): the
  memory of the database server is encrypted. This protects against a
  cloud provider, but not against a database login.

## Comparison

| Method | T1 storage | T2 backups | T3 database server | Changes to Skriva | Operation |
|--------|:---:|:---:|:---:|---|---|
| 1. Encrypted storage | Yes | No | No | None | Easy |
| 2. Encrypted backups | No | Yes | No | None | Easy |
| 3. TDE (pg_tde) | Yes | Physical backups only | No | Small | Special server, one vendor |
| 4. pgcrypto | Yes | Yes | No (key in queries) | Large | Easy |
| 5. Envelope encryption in Skriva | Yes | Yes | Yes | Large (search, titles, links) | KMS or key in the configuration |

## Recommendation

The project implemented recommendation 2 (method 5). The notes at each
step tell what the implementation does.

1. **Now, without changes to Skriva:** require encrypted storage
   (method 1) and encrypted backups (method 2) in the operation guide.
   This protects against the most frequent losses: disks, snapshots and
   backup files. Do not use TDE (method 3): it gives almost the same
   protection as method 1, but it needs a special PostgreSQL server.
2. **If a database administrator or an attacker on the database server
   must not read the documents (T3):** implement envelope encryption in
   Skriva (method 5), in steps:
   1. Keys: the table `tenant_keys`, a KEK from the configuration first,
      and an interface for a KMS (Vault Transit) later. Key versions and
      rotation from the start. Done, except the KMS: the interface
      `store.KEK` exists, but only the KEK from the configuration
      implements it.
   2. Encrypt `files.data`, `revisions.body` and `nodes.body`. These have
      no SQL functions on them. Remove `plain_text`. Done. The
      implementation keeps `plain_text`, encrypted, for the parts of the
      text in the search results.
   3. Replace the `tsvector` with a blind index (search option 1), and
      the plain slugs with HMAC values. Done.
   4. Decide about the titles. Encrypted titles need sorting and title
      search in the application. Plain titles are simpler, but they can
      be sensitive. A per-tenant setting is possible. The implementation
      encrypts all titles.
   5. Encrypt `audit_events.target` and `details`, or keep only IDs.
      Done: the column `sealed` contains them, encrypted.
3. Make a decision about the information classification: for example,
   documents with a high level could require method 5, while the other
   levels use method 1 only. This is open. Now all documents use
   method 5.

## Sources

- Percona: [pg_tde general availability, press release](https://www.percona.com/press/percona-launches-first-ever-open-source-transparent-data-encryption-for-postgresql/),
  [pg_tde blog](https://www.percona.com/blog/your-datas-not-safe-until-its-tde-safe-heres-how/).
- [open_pg_tde (Command Prompt)](https://github.com/commandprompt/open_pg_tde),
  [pg_vault_tde](https://pgxn.org/dist/pg_vault_tde/).
- [Status update on TDE in PostgreSQL, PGConf.DE 2026](https://www.postgresql.eu/events/pgconfde2026/sessions/session/7747/slides/871/03%20Jan%20Wieremjewicz%20-%20Status%20update%20on%20TDE%20in%20PG.pdf).
- [Percona: encrypt the pgBackRest repository](https://www.percona.com/blog/enhancing-postgresql-security-how-to-encrypt-the-pgbackrest-repository/).
- PostgreSQL documentation: [pgcrypto](https://www.postgresql.org/docs/current/pgcrypto.html),
  [encryption options](https://www.postgresql.org/docs/current/encryption-options.html).
