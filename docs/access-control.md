# Access control (RBAC)

## Terms

| Term         | Meaning |
|--------------|---------|
| Permission   | The right to do one action, for example `doc.edit`. |
| Role         | A named set of permissions. |
| Group        | A set of users of one tenant. |
| Role binding | A connection that gives one role to one user or to one group. The binding applies in all spaces, in one space or in one folder. |
| Default role | A role that all signed-in users of a tenant get. A space or a folder can have its own default role. |
| Space permission | A document permission (`doc.*`). Only these permissions can apply to one space or one folder. |

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
spaces, to one space or to one folder. `space.manage` and `tenant.manage`
always apply to the full tenant.

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
| One folder | The binding gives only the space permissions (`doc.*`) of the role, for the folder and all items in it. |

Thus a binding for one space or one folder never gives `space.manage`
or `tenant.manage`. For example, the `admin` role for one space gives
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

## Folder permissions

A folder can have its own default role and its own role bindings. They
apply to the folder and to all items in it: documents, sub-documents
and folders. A document does not have its own permissions. It has the
permissions of its nearest folder.

The default role of a folder has one of these settings:

| Setting                | Effect |
|------------------------|--------|
| Same as the parent     | The nearest folder above with its own setting applies. If there is none, the default role of the space applies. This is the initial setting. |
| None                   | No default role applies in the folder. |
| A role                 | This role applies in the folder. |

A folder inside a private folder can have a different default role
again. For example, the folder "HR" can be private, and its subfolder
"HR / Public forms" can have the default role `viewer`.

### Make a private folder

1. Open the folder.
2. Click **Access**. Only users with `tenant.manage` see this link.
3. Set the default role to "None".
4. Give roles for this folder to users or groups.

### Bindings at a higher level

Bindings add permissions. A default role "None" does not remove the
permissions from bindings at a higher level. Thus these users also have
access to a private folder:

- users with a binding for all spaces,
- users with a binding for the space,
- users with a binding for a parent folder.

## Effective permissions

The server calculates the permissions of a user at each request.

For an item in a space, the permissions are the union of:

1. The space permissions of the nearest default role. The server looks
   at the folder of the item, then at its parent folders, then at the
   space, then at the tenant. The first setting that is not "Same as
   the parent" (or "Same as the tenant") applies.
2. All permissions of the roles with a binding for all spaces, to the
   user or to a group of the user.
3. The space permissions of the roles with a binding for this space.
4. The space permissions of the roles with a binding for the folder of
   the item or for a parent folder.
5. The permissions `space.manage` and `tenant.manage` from the default
   role of the tenant.

The server uses these permissions:

| Action | Permission |
|--------|------------|
| Open an item, its history or its source | `doc.read` for the item |
| Make an item | `doc.create` for the new parent (or for the top level of the space) |
| Edit or rename an item | `doc.edit` for the item |
| Move an item | `doc.edit` for the item, and `doc.create` for the new parent |
| Delete an item | `doc.delete` for the item and for each item in it |
| Change the access to a folder | `tenant.manage` |

If a user cannot read an item, the server shows "not found". Thus the
server does not tell that the item exists. The tree, the path above the
title, the lists, "Recent changes", the search and the wiki links show
only items that the user can read. If the user can read an item but not
its parent folder, the tree shows the item at a higher level, without
the title of the folder.

Outside a space (for example on the administration pages), the
permissions are the union of the default role of the tenant and the
bindings for all spaces.

The start page of the tenant shows a space if the user can read the top
level of the space or at least one folder in it.

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

- A document does not have its own permissions. The smallest unit is a
  folder.
- A user who moves a folder also moves its subtree. Then the items in it
  get the permissions of the new place, except where the folder has its
  own settings.
- A user with `space.manage` can make, change and delete spaces. Only a
  user with `tenant.manage` can change the access to a space.
- The server does not delete users. Disable a user to stop access.
