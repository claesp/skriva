# Administration

There are two levels of administration.

| Level  | URL                | Who |
|--------|--------------------|-----|
| System | `/admin/`          | Members of `SKRIVA_SYSTEM_ADMIN_GROUP` at the system identity provider. |
| Tenant | `/{tenant}/admin/` | Users of the tenant with the permission `tenant.manage` or `space.manage`. |

A system administrator does not have access to the documents of a
tenant. A tenant administrator does not have access to the system
administration. Each level has its own sign-in and its own session.

## System administration

### Make a tenant

1. Sign in at `/admin/`.
2. Click **New tenant**.
3. Type the code. The code has exactly 4 characters from `a-z` and
   `0-9`. It is part of all URLs of the tenant. You cannot change it
   later.
4. Type the name.
5. Type the settings of the identity provider. See the table below.
6. Type the admin group. The members of this OIDC group get the `admin`
   role in the tenant.
7. Click **Create tenant**.
8. Add the redirect URI and the post-logout redirect URI to the client
   at the identity provider. The tenant page shows the two URIs.
9. Make a space. Type the name and the code. The code has exactly 8
   characters from `a-z` and `0-9`.

### Tenant settings

| Setting | Description |
|---------|-------------|
| Name | The name in the page header. |
| Disabled | Nobody can use a disabled tenant. The server deletes all sessions of the tenant. |
| Issuer URL | The issuer of the identity provider. It must be an `https` URL. An `http` URL is permitted only for `localhost`. |
| Client ID | The client ID at the identity provider. |
| Client secret | The client secret. The server encrypts it. The page never shows it. An empty field keeps the current secret. |
| Scopes | The OIDC scopes. `openid` is always included. |
| Groups claim | The ID token claim with the group names. |
| Admin group | An OIDC group. Its members always have the `admin` role. |
| Default role | The role of all signed-in users of the tenant. "(None)" means that a user needs a role binding. |
| Session life | The maximum life of a session, for example `12h`. |
| Maximum document size | The maximum size of the text of one document, from 1 to 1024 KiB. |

### Delete a tenant

1. Open the tenant page.
2. Type the tenant code in the confirmation field.
3. Click **Delete tenant**.

This deletes all users, groups, roles, spaces and documents of the
tenant. You cannot undo this. You cannot delete the default tenant.

## Tenant administration

Users with the permission `tenant.manage` manage these items:

- **Users.** Users appear after their first sign-in. An administrator
  can disable a user. The server then deletes all sessions of the user.
- **Groups.** OIDC groups and local groups. See
  [Access control](access-control.md).
- **Roles.** Custom roles for the tenant.
- **Role bindings.** Roles for users and groups, for all spaces or for
  one space.
- **Access to a space.** The default role and the role bindings of one
  space. Open "Administration > Spaces" and click **Access**. See
  [Access control](access-control.md).
- **Access to a folder or a document.** The default role and the role
  bindings of one item. Open the item and click **Access**.

Users with the permission `space.manage` manage the spaces:

- Make a space. The code has exactly 8 characters from `a-z` and `0-9`.
  You cannot change the code later.
- Change the name and the description of a space.
- Delete a space. Type the space code to confirm. This deletes all
  folders and documents in the space.
