# Administration

There are two levels of administration.

| Level  | URL                | Who |
|--------|--------------------|-----|
| System | `/admin/`          | Members of `SKRIVA_SYSTEM_ADMIN_GROUP` at the system identity provider. |
| Tenant | `/{tenant}/admin/` | Users of the tenant with the permission `tenant.manage` or `space.manage`. |

A system administrator does not have access to the documents of a
tenant. A tenant administrator does not have access to the system
administration. Each level has its own sign-in and its own session.

## Layout of the administration pages

The administration pages use the same layout on the two levels:

- The menu at the left side shows the main sections, for example
  **Users**, **Groups**, **Roles**, **Role bindings** and **Spaces**.
- Each page has one function: a list, the data of one item, or one
  form.
- The toolbar at the top right side contains icon buttons. Put the mouse
  pointer on a button to see its name. The first group of buttons opens
  the pages of the item, for example **Details**, **Role bindings** and
  **Permissions** of a user. The current page has a line below its
  button. The next group of buttons does actions, for example
  **New role binding** (+) or **Delete** (waste bin).
- A form opens on its own page. The buttons are at the bottom right
  side: **Cancel** and **Save**.
- **Remove** (circle with x) in a table row removes that role binding or
  group member immediately.
- **Delete** opens a page that asks you to confirm. To delete a space or
  a tenant, you must also type its code.

| Icon | Name | Action |
|------|------|--------|
| Plus | New | Open the form for a new item. |
| Circle with x | Remove | Remove the role binding or the member in the row. |
| Waste bin | Delete | Open the page to delete the item. |
| Pencil | Change the default role | Open the form for the default role. |
| Lock | Access | Open the default role and the role bindings of a space. |
| Sliders | Settings | Open the settings of a space or a tenant. |
| Arrow out of a box | Open | Open the space or the tenant in the wiki. |
| Circle with a line | Disable | Disable the user. |
| Circle with a check mark | Enable | Enable the user again. |

## System administration

### Make a tenant

1. Sign in at `/admin/`.
2. Click **New tenant** (+).
3. Type the code. The code has exactly 4 characters from `a-z` and
   `0-9`. It is part of all URLs of the tenant. You cannot change it
   later.
4. Type the name.
5. Type the settings of the identity provider. See the table below.
6. Type the admin group. The members of this OIDC group get the `admin`
   role in the tenant.
7. Click **Save**.
8. Add the redirect URI and the post-logout redirect URI to the client
   at the identity provider. The settings page of the tenant shows the
   two URIs.
9. Click **Spaces** (four squares). Then click **New space** (+).
10. Type the name and the code of the space. The code has exactly 8
    characters from `a-z` and `0-9`. Click **Save**.

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

1. Open the tenant.
2. Click **Delete** (waste bin).
3. Type the tenant code in the confirmation field.
4. Click **Delete**.

This deletes all users, groups, roles, spaces and documents of the
tenant. You cannot undo this. You cannot delete the default tenant.

## Tenant administration

Users with the permission `tenant.manage` manage these items:

- **Users.** Users appear after their first sign-in. The pages of a
  user are **Details**, **Role bindings** and **Permissions**. An
  administrator can disable a user. The server then deletes all
  sessions of the user.
- **Groups.** OIDC groups and local groups. The pages of a group are
  **Members** and **Role bindings**. Only local groups have
  **New member** and **Delete**. See [Access control](access-control.md).
- **Roles.** Custom roles for the tenant.
- **Role bindings.** Roles for users and groups, for all spaces or for
  one space.
- **Access to a space.** The default role and the role bindings of one
  space. Open **Spaces**, then click **Access** (lock) in the row of
  the space. See [Access control](access-control.md).
- **Access to a folder or a document.** The default role and the role
  bindings of one item. Open the item and click **Access** (lock).

The form for a new role binding opens from the list of all role
bindings, and from the pages of a user, a group, a space or an item.
From the page of a user or a group, the form gives the role to that user
or group. From the access page of a space or an item, the form gives
the role for that space or item.

Users with the permission `space.manage` manage the spaces:

- Make a space. The code has exactly 8 characters from `a-z` and `0-9`.
  You cannot change the code later.
- Change the name and the description of a space.
- Delete a space. Type the space code to confirm. This deletes all
  folders and documents in the space.
