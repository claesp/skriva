# User guide

## Sign in

1. Open the URL of your tenant, for example `https://wiki.example.com/acme/`.
2. Sign in at the identity provider.

The wiki then shows the page that you opened. Each tenant has its own
sign-in. If you use more than one tenant, sign in to each tenant.

## Spaces, folders and documents

- The start page of a tenant shows its **spaces** in a table, with the
  number of items and the date of the last change.
- A space contains **folders** and **documents** in a tree.
- A folder contains documents and other folders.
- Dates show as a relative time, for example "24 minutes ago" or
  "Yesterday". A date that is more than 10 days ago shows as a date, for
  example "2026-10-31". Put the mouse pointer on a date to see the full
  time in UTC.
- A document contains text. It can also contain other documents, for
  example a main document with sub-documents.

The pages of a space, a folder and a document show the tree of the
space at the left side of the window. The start page shows the list of
spaces there. A folder has a folder icon. A document has a page icon.
Click the small arrow at the left of an item to open or close it in the
tree. The page of a space and the page of a folder show their items in a
table.

## Buttons for new items

The page header contains **New document** (page with +) and **New
folder** (folder with +), at the left of the search field. The new item
goes into the space, the folder or the document that you have open.
**New folder** is not available on a document page, because a folder
cannot be in a document.

## Toolbar

The toolbar is below the page header. It covers the full width of the
page. The path to the item is at the left side and the buttons are at
the right side. The toolbar stays at the top when you scroll. Each button
is an icon. Put the mouse pointer on a button to see its name. The
toolbar shows only the buttons for the actions that you have permission
for.

| Icon         | Name         | Action |
|--------------|--------------|--------|
| Pencil       | Edit         | Change the text of the document. For a folder, the name is **Rename**. |
| Clock        | History      | Show the old versions of the document. |
| `< >`        | Source       | Show the text of the document in Markdown. See [Source](#source). |
| Folder with arrow | Move    | Move the item to a different place. |
| Waste bin    | Delete       | Delete the item and the items in it. |
| Lock         | Access       | Change the permissions of the item. |

**Edit**, **History**, **Move**, **Delete** and **Access** open a page
with the same tree, path and toolbar. On that page, the button has a
line below it. Click the button again to go back to the document. From
an old version, **History** goes back to the list of versions.

## Access

The administrators can give access to a full space, to one folder or to
one document. A document has the access of its parent, except if an
administrator changes it. If
you cannot read a folder, it is not in the tree and the search does not
find its documents. If you have a link to a document that you cannot
read, the wiki shows "not found".

## Links to a document

Each document has two links:

- The full link, for example `/acme/handbook/d/k3x9p2qa7tm4zb/install`.
- The short link, for example `/acme/handbook/d/k3x9p2qa7tm4zb`. The
  bottom of the page shows it.

The two links stay valid when the title changes or when the document
moves. Only users with access to the tenant can open them.

## Make a document or a folder

1. Open the space, a folder or a document.
2. Click **New document** or **New folder** in the page header.
3. Type the title.
4. For a document, type the text in the editor. See [The editor](#the-editor).
5. Click **Save**.

The new item is in the folder or the document that you opened. A folder
cannot be in a document.

You can also make a document from a wiki link. Write `[[Title of new
document]]` in a document and save it. Click the link. Then click **New
document**.

## Edit a document

1. Click **Edit** (pencil) in the toolbar.
2. Change the title or the text.
3. Optionally, type a comment about the change in **Comment**.
4. Click **Save**.

The edit page has the same tree, path and toolbar as the page of the
document. The **Edit** button has a line below it. To stop without a
save, click **Edit** again.

If a different user saved the document after you started, the wiki does
not save your text. Copy your text. Then open the document again and add
your changes.

If you try to leave the page with changes that are not saved, the
browser asks you to confirm.

To change the name of a folder, click **Rename** (pencil).

## Move a document or a folder

1. Click **Move** (folder with arrow) in the toolbar.
2. Select the new place. "(Top of the space)" is the top level.
3. Click **Save**.

The list does not show the item itself and the items in it. For a
folder, the list shows only folders.

## Delete a document or a folder

1. Click **Delete** (waste bin) in the toolbar.
2. Read the number of items in it. The wiki also deletes these items.
3. Click **Delete**.

You cannot undo this.

## Restore an old version

1. Click **History** (clock) in the toolbar.
2. Click **Restore** (arrow in a circle) in the row of the version.
3. Click **Save**.

The wiki saves the old text as a new version. The history does not
change.

## The editor

The editor shows the text with its formats, as the document page shows
it. The editor needs JavaScript. Without JavaScript, you can change only
the title of a document. All other pages work without JavaScript.

The toolbar of the editor is above the text. It stays visible when you
scroll. Put the mouse pointer on a button to see its name. A button for
a format that applies at the cursor has a gray background.

| Button | Action |
|--------|--------|
| Undo, Redo | Undo or redo a change. |
| Type of text | Normal text, or a heading of level 1, 2 or 3. |
| Bold, Italic, Underline, Strikethrough, Code | Format the selected text. |
| Align left, Align center, Align right | Align the paragraph or the heading. |
| Bulleted list, Numbered list, Task list | Start or stop a list. Tab and Shift+Tab change the level of an item. |
| Quote, Code block, Horizontal line | Add a quotation, a block of code or a line. |
| Link | Add, change or remove a link. See [Links](#links). |
| Image | Add an image from your computer. See [Images](#images). |
| Table | Add a table with 3 rows and 3 columns. The first row is the header row. |

When the cursor is in a table, the toolbar shows more buttons: **Add
row**, **Remove row**, **Add column**, **Remove column**, **Merge or split
cells**, **Header row** and **Remove table**. To merge cells, select
them with the mouse or with Shift and the arrow keys. Then click **Merge
or split cells**. Tab goes to the next cell.

### Shortcuts

You can also use the keyboard and some Markdown syntax:

| Type or press | Result |
|---------------|--------|
| Ctrl+B, Ctrl+I, Ctrl+U | Bold, italic, underline. |
| Ctrl+K | Add or change a link. |
| Ctrl+Z, Ctrl+Shift+Z | Undo, redo. |
| `#`, `##` or `###` and a space at the start of a line | A heading. |
| `-` or `*` and a space | A bulleted list. |
| `1.` and a space | A numbered list. |
| `[ ]` and a space | A task list. |
| `>` and a space | A quotation. |
| ```` ``` ```` | A code block. |
| `**text**`, `*text*`, `` `text` ``, `~~text~~` | Bold, italic, code, strikethrough. |
| `[[Title]]` | A wiki link. |

On a Mac, use Cmd in place of Ctrl.

### Links

Click **Link** or press Ctrl+K. Type the address and click **Apply** or
press Enter. If you selected text, the text becomes the link. These
addresses are permitted:

- `https://…` and `http://…`: a page on a different site.
- `mailto:…`: an e-mail address.
- `wiki:Title` or `wiki:Title#Heading`: a document in the same space.
- A path that starts with `/`, for example the short link of a
  document.

Type `[[Title]]` in the text to make a wiki link quickly. Wiki links are
case insensitive. `[[Install]]`, `[[install]]` and `[[INSTALL]]` go to
the same document. This is also true for the heading in
`[[Title#Heading]]`. If more than one document in the space has the
title of a wiki link, the wiki shows a list. Then select the correct
document.

### Images

Click **Image** and select a file. You can also paste an image, or drag
an image file into the text. The wiki keeps the image with the document.

- The file must be a PNG, JPEG, GIF or WebP image. SVG is not permitted.
- The maximum size is 10 MiB.
- The name of the file becomes the description of the image (the
  alternative text).
- A user who can read the document can see its images.

If you add an image and do not save the document, the wiki deletes the
image after one day.

### Source

**Source** (`< >`) shows the text of the document in Markdown. You can
copy it to a different program. Markdown does not have all the formats
of the editor. The source does not show the alignment, the underline and
merged cells.
