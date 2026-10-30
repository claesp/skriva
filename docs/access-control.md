# Access control (RBAC)

## Terms

| Term         | Meaning |
|--------------|---------|
| Permission   | The right to do one action, for example `doc.edit`. |
| Role         | A named set of permissions. |
| Group        | A set of users of one tenant. |
| Role binding | A connection that gives one role to one user or to one group. The binding applies in all spaces or in one space. |
| Default role | A role that all signed-in users of a tenant get. A space can have its own default role. |
| Space permission | A document permission (`doc.*`). Only these permissions can apply to one space. |

All roles, groups and role bindings apply to one tenant. A user of one
tenant has no access to a different tenant. The same person can be a
user in more than one tenant. Then each tenant has its own user record
and its own roles for that person.

## Permissions

| Permission      | Allows |
|-----------------|--------|
| `doc.read`      | Read spaces, folders, documents, history and search results. |
| `doc.create`    | Create folders and documents. |
| `doc.edit`      | Edit, rename and move folders and documents. Restore old versions. |
| `doc.delete`    | Delete folders and documents with their subtrees. |
| `space.manage`  | Create, change and delete spaces. |
| `tenant.manage` | Manage users, groups, roles and role bindings of the tenant. |

The permissions are part of the program code. An administrator cannot
add permissions.

The `doc.*` permissions are space permissions. They can apply to all
spaces or to one space. `space.manage` and `tenant.manage` always apply
to the full tenant.

## Built-in roles

| Role         | Permissions |
|--------------|-------------|
| `viewer`     | `doc.read` |
| `editor`     | `doc.read`, `doc.create`, `doc.edit` |
| `maintainer` | `doc.read`, `doc.create`, `doc.edit`, `doc.delete` |
| `admin`      | All permissions |

All tenants have the built-in roles. You cannot change or delete them. A
tenant administrator can make custom roles on the page
"Administration > Roles". A custom role belongs to one tenant. It cannot
have the name of a built-in role.

## Scope of a role binding

A role binding has one of two scopes:

| Scope      | Effect |
|------------|--------|
| All spaces | The binding gives all permissions of the role, in all spaces of the tenant. |
| One space  | The binding gives only the space permissions (`doc.*`) of the role, and only in that space. |

Thus a binding for one space never gives `space.manage` or
`tenant.manage`. For example, the `admin` role for one space gives
`doc.read`, `doc.create`, `doc.edit` and `doc.delete` in that space,
and no other permissions.

## Default role of a space

Each space has one of these default role settings:

| Setting            | Effect |
|--------------------|--------|
| Same as the tenant | The default role of the tenant applies in the space. This is the initial setting. |
| None               | No default role applies in the space. Only users with a role binding have access. |
| A role             | This role applies in the space, in place of the default role of the tenant. |

The default role of a space gives only space permissions.

### Make a private space

1. Open "Administration > Spaces".
2. Click **Access** in the row of the space.
3. Set the default role to "None".
4. Give roles for this space to users or groups.

Users with a role binding for all spaces also have access to a private
space. For example, a member of the admin group has access to all
spaces.

## Effective permissions

The server calculates the permissions of a user at each request.

In a space, the permissions are the union of:

1. The space permissions of the default role of the space. If the space
   uses the setting "Same as the tenant", this is the default role of
   the tenant.
2. All permissions of the roles with a binding for all spaces, to the
   user or to a group of the user.
3. The space permissions of the roles with a binding for this space, to
   the user or to a group of the user.
4. The permissions `space.manage` and `tenant.manage` from the default
   role of the tenant.

Outside a space (for example on the administration pages), the
permissions are the union of the default role of the tenant and the
bindings for all spaces.

The start page of the tenant and the search in all spaces show only the
spaces in which the user has `doc.read`.

A change of a role binding has an effect at the next request. The user
does not have to sign in again.

## Groups

There are two types of groups.

### OIDC groups

OIDC groups come from the identity provider of the tenant. At each
sign-in, the server reads the groups claim of the ID token. Then it
updates the OIDC groups of the user:

- The server adds the user to each group in the claim.
- The server removes the user from each OIDC group that is not in the
  claim.

If the claim contains a new group name, the server makes the group. An
administrator cannot change the members of an OIDC group. A change at
the identity provider has an effect at the next sign-in of the user.

### Local groups

A tenant administrator makes local groups and adds members on the page
"Administration > Groups". The sign-in does not change local groups.

## Admin group

Each tenant can have an admin group. Its members always have the `admin`
role. The server makes the role binding when a system administrator
saves the tenant settings, and at each start. If a tenant administrator
removes the binding, the binding comes back at the next start. Thus a
member of the admin group can always get access again.

## System administrators

System administrators are not users of a tenant. They sign in at
`/admin/` with the system identity provider. The server lets a user sign
in there only if the groups claim contains `SKRIVA_SYSTEM_ADMIN_GROUP`.
The server checks the group at each sign-in. A system administrator
session has no permissions in a tenant.

## Disabled users and tenants

- A tenant administrator can disable a user. The server then deletes all
  sessions of the user at once. A disabled user cannot sign in. An
  administrator cannot disable the account that the administrator uses.
- A system administrator can disable a tenant. The server then deletes
  all sessions of the tenant. Nobody can use a disabled tenant.

## Limits of the current design

- The server does not have permissions for one folder or one document.
  The smallest unit is a space.
- A user with `space.manage` can make, change and delete spaces. Only a
  user with `tenant.manage` can change the access to a space.
- The server does not delete users. Disable a user to stop access.
