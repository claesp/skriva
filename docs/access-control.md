# Access control (RBAC)

## Terms

| Term         | Meaning |
|--------------|---------|
| Permission   | The right to do one action, for example `page.edit`. |
| Role         | A named set of permissions. |
| Group        | A set of users. |
| Role binding | A connection that gives one role to one user or to one group. |
| Default role | A role that all signed-in users get. |

## Permissions

| Permission     | Allows |
|----------------|--------|
| `page.read`    | Read pages, history, source and search results. |
| `page.create`  | Create new pages. |
| `page.edit`    | Edit pages. Restore old versions. |
| `page.delete`  | Delete pages and their history. |
| `admin.manage` | Manage users, groups, roles and role bindings. |

The permissions are part of the program code. An administrator cannot
add permissions.

## Built-in roles

| Role         | Permissions |
|--------------|-------------|
| `viewer`     | `page.read` |
| `editor`     | `page.read`, `page.create`, `page.edit` |
| `maintainer` | `page.read`, `page.create`, `page.edit`, `page.delete` |
| `admin`      | All permissions |

You cannot change or delete the built-in roles. An administrator can
make custom roles on the page "Administration > Roles".

## Effective permissions

The server calculates the permissions of a user at each request. The
permissions are the union of the permissions of these roles:

1. The default role (`SKRIVA_DEFAULT_ROLE`).
2. The roles with a binding to the user.
3. The roles with a binding to a group of the user.

Thus a change of a role binding has an effect at the next request. The
user does not have to sign in again.

## Groups

There are two types of groups.

### OIDC groups

OIDC groups come from the identity provider. At each sign-in, the server
reads the groups claim of the ID token. Then it updates the OIDC groups
of the user:

- The server adds the user to each group in the claim.
- The server removes the user from each OIDC group that is not in the
  claim.

If the claim contains a new group name, the server makes the group. An
administrator cannot change the members of an OIDC group.

A change at the identity provider has an effect at the next sign-in of
the user.

### Local groups

An administrator makes local groups and adds members on the page
"Administration > Groups". The sign-in does not change local groups.

## Start configuration

A new installation has no role bindings. Use these settings to give
roles to OIDC groups at start:

```sh
SKRIVA_ADMIN_GROUP=skriva-admins
SKRIVA_GROUP_ROLES=skriva-editors=editor,skriva-maintainers=maintainer
```

The server makes these bindings at each start. If an administrator
removes a binding, the binding comes back at the next start. Thus a
member of the admin group can always get access again.

## Disabled users

An administrator can disable a user. The server then deletes all
sessions of the user at once. A disabled user cannot sign in. An
administrator cannot disable his or her own account.

## Limits of the current design

- The roles apply to all pages. The server does not have permissions for
  one page or one part of the wiki.
- The server does not delete users. Disable a user to stop access.
