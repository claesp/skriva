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
  example a main document with sub-documents. Below the text, the page
  shows the list **Sub-documents** with the documents in it that you
  can read.

The pages of a space, a folder and a document show the tree of the
space at the left side of the window. The start page shows the list of
spaces there. A folder has a folder icon. A document has a page icon.
Click the small arrow at the left of an item to open or close it in the
tree. The page of a space and the page of a folder show their items in a
table. The table shows the name, the type, the classification, the time
of the last change and the user who made it. A folder has no
classification, thus its cell is empty. On a narrow screen, the table
does not show the type and the user.

## The New button

The page header contains the blue **New** button, at the left of the
search field. Click it to open a list of the items that you can make:
**Document** and **Folder**. The new item goes into the space, the
folder or the document that you have open. **Folder** is not in the
list on a document page, because a folder cannot be in a document.
Click out of the list, or click **New** again, to close the list.

## Toolbar

The toolbar is below the page header. It covers the full width of the
page. The path to the item is at the left side and the buttons are at
the right side. The toolbar stays at the top when you scroll. Each button
is an icon. Put the mouse pointer on a button to see its name. The
toolbar always shows all buttons at the same places. A button for an
action that you do not have permission for is gray and does nothing.

| Icon         | Name         | Action |
|--------------|--------------|--------|
| Pencil       | Edit         | Change the text of the document. For a folder, the name is **Rename**. |
| Clock        | History      | Show the old versions of the document. |
| Arrow up from a tray | Export | Download the document as a file. See [Export a document](#export-a-document). |
| Folder with arrow | Move    | Move the item to a different place. |
| Waste bin    | Delete       | Move the item and the items in it into the trash. |
| Lock         | Access       | Change the permissions of the item. |
| Box          | Trash        | On the space page: show the deleted items. See [The trash](#the-trash). |

The buttons are at the same places on the page of an item, on its
forms and on the form of a new item. On a form, **Save** is at the left
side of the toolbar, separate from the other buttons.

**Edit**, **History**, **Export**, **Move**, **Delete** and **Access** open a page
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

## Search

Type words in the search field of the page header and press Enter. In a
space, the search finds the items of that space. Click **Search in all
spaces** (the space icon) to search in all spaces of the tenant.

- The search finds the items with all the words, in the title or in the
  text. Upper case and lower case are the same.
- A word of 3 letters or more also finds the words that start with it.
  "describe" finds "describes" and "described", and "install" finds
  "installation". A shorter word finds only the whole word. The search
  does not find the end or the middle of a word: "scribe" does not find
  "describe".
- A word of 3 letters or more also finds the other forms of the English
  word: "describing" finds "describe", and "planning" finds "plans". The
  search uses the English stem of the word. For other languages, this
  does not work correctly, but the start of words works.
- The results with the whole words come first. Then come the results
  with other forms of the words, then the results with parts of words.
- Type `-word` to exclude the items with that word.
- Items with the whole words in the title come first, then the items
  with the whole words in the text.
- The results show only the items that you can read.
- Each space gives a maximum number of results (50 if the administrator
  did not change it). In all spaces, each space gives its own maximum.

The text of the documents is encrypted in the database. Thus the search
uses an index of keyed hashes of the words, not the words. See
[Security](security.md#encryption-at-rest).

## Links to this page

Below the text, a page shows the list **Links to this page**: the pages
in the same space that link to it. The list shows only the pages that
you can read. A link with the list of pages (`doc:`) always counts. A
wiki link counts if it has the current title of the page. Pages in the
trash do not count.

## Links to a document

Each document has two links:

- The full link, for example `/acme/handbook/d/k3x9p2qa7tm4zb/install`.
- The short link, for example `/acme/handbook/d/k3x9p2qa7tm4zb`. The
  bottom of the page shows it.

The two links stay valid when the title changes or when the document
moves. Only users with access to the tenant can open them.

## Make a document or a folder

1. Open the space, a folder or a document.
2. Click **New** in the page header, then **Document** or **Folder**.
3. Type the title.
4. For a document, type the text in the editor. See [The editor](#the-editor).
5. Click **Save** at the left side of the toolbar.

The form of a new item shows the same toolbar as a document page. The
buttons that need a saved item (for example **History**) are gray. The pencil button has a line below it. To stop without a
save, click the pencil.

The new item is in the folder or the document that you opened. A folder
cannot be in a document.

You can also make a document from a wiki link. Write `[[Title of new
document]]` in a document and save it. Click the link. Then click **New
document**.

## Information classification

Each document has an information classification, for example
"Restricted". The administrators of the tenant define the levels. All
spaces of the tenant use the same levels. The default levels are
Unclassified, Public, Restricted, Secret and Top Secret. A new document
gets the default level of its place (see below). Without limits, this is
the default level of the tenant (Unclassified).

Below the toolbar, each page of a document shows the bar "This document
is classified as RESTRICTED". The bar stays visible when you scroll. The page of an old version shows the
classification of that version. A printed document also shows the bar.

To change the classification, open the document with **Edit** and
select the level in **Classification**, at the right side of the
**Mermaid diagram** button in the toolbar of the editor. The bar
changes when you select a level. Click **Save**. The history keeps the
classification of each version, and the audit log records the change.
Folders do not have a classification.

### Classification limits

A space and a folder can have classification limits. Use them for a
space or a folder that is only for documents of a high level.

- **Lowest classification**: the documents must have this level or a
  higher level. The editor shows only the permitted levels.
- **Default classification**: the level of a new document.

A user with the permission `space.manage` sets the limits:

- For a space: **Administration**, **Spaces**, the space, then
  **Change the settings** (pencil).
- For a folder: open the folder and click **Classification limits** in
  the toolbar. The button is disabled for a document.

- The limits of a folder apply to all items in it, also in subfolders.
- A folder can only make the limits stricter. Its lowest level must be
  the same as the lowest level of the space and of the parent folders,
  or higher. Its default must not be lower than the lowest level.
- If the default of a parent is lower than the lowest level of a
  folder, a new document in the folder gets the lowest level.
- You cannot set a lowest level while documents in the space or the
  folder have a lower level. Give these documents a higher level first.
  The message tells how many documents have a lower level.
- You cannot move a document or a folder to a place where a document in
  it has a level that is too low. The same is true for a restore from
  the trash.

## Edit a document

1. Click **Edit** (pencil) in the toolbar.
2. Change the title or the text.
3. Optionally, type a comment about the change in **Comment**.
4. Click **Save** at the left side of the toolbar.

The edit page has the same tree, path and toolbar as the page of the
document. The **Edit** button has a line below it. To stop without a
save, click **Edit** again. The other buttons of the toolbar stay
available. The toolbar stays at the top when you scroll. Thus the
**Save** button is always visible.

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

The item and the items in it go into the trash of the space. After 30
days in the trash, the wiki deletes them permanently.

## The trash

To open the trash, click **Trash** (box) in the toolbar of the space
page. If you do not have permission to delete items, the button is gray.
The trash shows the items that you can delete. Each row shows the old
place of the item, who deleted it and when.

- To restore an item, click **Restore** (arrow in a circle). The item
  goes back to its old place, with the items in it and all its history.
- If the old folder of an item is also in the trash, restore the folder
  first. Thus an item cannot go to a place with different permissions.
- An administrator of the space can click **Delete permanently** (waste
  bin). You cannot undo this. This also deletes the items that were in
  the item, also if they are separate items in the trash.

While an item is in the trash, nobody can open it. The search, the tree
and the wiki links do not show it.

## Compare versions

1. Click **History** (clock) in the toolbar.
2. Click **Changes** (two circles with arrows) in the row of a version.
   The page shows the changes from the version before it.

To compare two other versions, select them in **From version** and
**to version** above the list, then click **Compare**.

The page shows the changes in the title and in the Markdown source of
the text:

- A removed line has a red background and the sign `−`.
- An added line has a green background and the sign `+`.
- In a changed line, the changed words have a stronger color.
- The page shows three unchanged lines before and after each change.
  A gray row tells how many unchanged lines are not shown.

The Markdown source does not contain the alignment and the underline.
Thus the comparison does not show changes to these formats.

## Restore an old version

1. Click **History** (clock) in the toolbar.
2. Click **Restore** (arrow in a circle) in the row of the version. You
   can also open the version and click **Restore** above the text.
3. Click **Save**.

The wiki saves the old text as a new version. The history does not
change. For the current version, **Restore** is disabled, because the
text is the same.

## The editor

The editor shows the text with its formats, as the document page shows
it. The editor needs JavaScript. Without JavaScript, you can change only
the title of a document. The document then keeps its classification. All other pages work without JavaScript.

The toolbar of the editor is above the text. It stays visible when you
scroll. Put the mouse pointer on a button to see its name. A button for
a format that applies at the cursor has a gray background.

| Button | Action |
|--------|--------|
| Undo, Redo | Undo or redo a change. |
| Type of text | Normal text, or a heading of level 1 to 8. |
| Bold, Italic, Underline, Strikethrough, Code | Format the selected text. |
| Align left, Align center, Align right | Align the paragraph or the heading. |
| Bulleted list, Numbered list, Task list | Start or stop a list. Tab and Shift+Tab change the level of an item. |
| Quote, Code block, Horizontal line | Add a quotation, a block of code or a line. |
| Link | Add, change or remove a link. See [Links](#links). |
| Image | Add an image from your computer. See [Images](#images). |
| Table | Add a table with 3 rows and 3 columns. The first row is the header row. |
| Mermaid diagram | Add a diagram. See [Mermaid diagrams](#mermaid-diagrams). |
| Classification | Select the information classification of the document. See [Information classification](#information-classification). |

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
| `#` to `########` and a space at the start of a line | A heading of level 1 to 8. |
| `-` or `*` and a space | A bulleted list. |
| `1.` and a space | A numbered list. |
| `[ ]` and a space | A task list. |
| `>` and a space | A quotation. |
| ```` ``` ```` | A code block. |
| ```` ```mermaid ```` and a space | A Mermaid diagram. |
| `**text**`, `*text*`, `` `text` ``, `~~text~~` | Bold, italic, code, strikethrough. |
| `[[Title]]` | A wiki link. |

On a Mac, use Cmd in place of Ctrl.

### Headings

There are eight heading levels. Use level 1 for the main parts of a
document, and the next levels for the parts in them. Do not skip a
level. Screen readers use the levels to show the structure.

| Level | Style |
|-------|-------|
| 1 | Large |
| 2 | Medium, italic |
| 3 | Small, semibold |
| 4 | Text size, bold |
| 5 | Text size, bold and italic |
| 6 | Capitals, bold, with letter spacing |
| 7 | Capitals, normal weight, with more letter spacing |
| 8 | Text size, italic, gray |

The Markdown source has six heading levels. Levels 7 and 8 show as
level 6 in the source.

### Links

Click **Link** or press Ctrl+K. Type the address and click **Apply** or
press Enter. If you selected text, the text becomes the link. The
editor keeps the selected text marked while you type the address. If
the cursor is in a link, the editor selects all of the link and you can
change its address.

To link to a page in the same space, type a part of its title. The
link bar shows a list of the pages with that text in the title. Use the
arrow keys or the mouse to select a page, then press Enter again or
click **Apply**. If you did not select text, the title of the page
becomes the text of the link. The address of the link is
`doc:` and the ID of the page. This link stays correct when the page
gets a different title or moves to a different folder. Below the
address, the link bar shows the title of the page.

These addresses are permitted:

- `https://…` and `http://…`: a page on a different site.
- `mailto:…`: an e-mail address.
- `doc:ID` or `doc:ID#Heading`: a page in the same space. The list of
  pages makes this address.
- `wiki:Title` or `wiki:Title#Heading`: a document in the same space,
  found with its title.
- A path that starts with `/`, for example the short link of a
  document.

Type `[[Title]]` in the text to make a wiki link quickly. A wiki link
finds the page with its title. Thus a wiki link does not go to the page
after the page gets a different title. Use the list of pages for a link
that must stay correct. Wiki links are
case insensitive. `[[Install]]`, `[[install]]` and `[[INSTALL]]` go to
the same document. This is also true for the heading in
`[[Title#Heading]]`. If more than one document in the space has the
title of a wiki link, the wiki shows a list. Then select the correct
document.

### Images

Click **Image** and select a file. You can also paste an image, or drag
an image file into the text. The wiki keeps the image with the document.

- The file must be a PNG, JPEG, GIF or WebP image, or a draw.io
  diagram in SVG format (see below). Other SVG files are not permitted.
- The maximum size is 10 MiB.
- All files of a space together have a limit (1024 MiB if the
  administrator did not change it). When the space is full, the editor
  shows a message. Ask an administrator of the space to increase the
  limit.
- The name of the file becomes the description of the image (the
  alternative text).
- A user who can read the document can see its images.

If you add an image and do not save the document, the wiki deletes the
image after one day.

### draw.io diagrams

You can add a [draw.io](https://www.drawio.com/) diagram as an image.
The diagram must contain an image and a copy of the diagram. Thus the
page can show it, and draw.io can open it again.

1. In draw.io, click **File** > **Export as** > **SVG** (or **PNG**).
2. Select **Include a copy of my diagram**.
3. Click **Export** and save the file, for example `flow.drawio.svg`.
4. In the editor, click **Image** and select the file. You can also drag
   the file into the text.

To change the diagram later, open the image in the document, save it,
and open the file in draw.io. Then export it again and replace the
image in the document.

A `.drawio` file without an image is not permitted: the page cannot
show it. The wiki shows a message with the steps above. In dark mode,
images have a white background, because diagrams often have dark lines
on a transparent background.

### Mermaid diagrams

You can write a diagram as text in the
[Mermaid](https://mermaid.js.org/) language. The page shows the
diagram as an image.

1. Click **Mermaid diagram** in the toolbar. The editor adds a code
   block with a small example. You can also type ```` ```mermaid ````
   and a space at the start of a line.
2. Write the diagram in the code block. The label "Mermaid diagram"
   is above the block.
3. Save the document. The page shows the image.

For example:

```text
flowchart LR
  accTitle: Steps of a review
  A[Write] --> B[Review]
  B -->|Changes| A
  B --> C[Publish]
```

- Write `accTitle:` and `accDescr:` lines in the diagram to give the
  image a description for screen readers. Without them, the description
  is "Diagram".
- If the diagram has an error, the page shows the text of the diagram
  and the error message.
- The editor shows the text, not the image. Save the document to see
  the image. In a diagram, the buttons that do not apply are disabled,
  for example **Quote**, **Horizontal line** and **Table**.
- The diagram uses the font of the interface (Inter) and has a
  transparent background, thus it matches the page. You can change the
  colors and the font of one diagram with an `%%{init: ...}%%` line.
- Links in a diagram (`click` lines) do not work, because the diagram
  is an image.
- The server makes the image with the
  [mmdr](https://github.com/1jehuang/mermaid-rs-renderer) program. It
  supports the most usual types of diagrams, for example flowcharts,
  sequence, class, state and Gantt diagrams. Some features of Mermaid
  are not available, and the layout can be different from the Mermaid
  web site.
- If the server does not have the mmdr program, the page shows a
  diagram as a code block.

## Export a document

**Export** shows the file formats for an export. An export is a file
with the current version of the document. For each format, you can:

- Click **Download** (arrow down to a tray) to save the file.
- Click **Show as text** (`< >`) to see the file in the browser, if the
  format is text. You can then copy the text to a different program.

The file contains the title, the information classification and the
version of the document. Links to other documents refer to Skriva. A
reader must sign in to open them. A folder has no export.

| Format   | Extension | Contents |
|----------|-----------|----------|
| Markdown | `.md`     | [GitHub Flavored Markdown](https://github.github.com/gfm/). The images refer to Skriva. |
| HTML     | `.html`   | One web page with the text and the images. |
| PDF      | `.pdf`    | A document for print, with A4 pages and the images. |

### Markdown

The Markdown file starts with a YAML front matter block:

```markdown
---
title: "Install on Linux"
classification: "Restricted"
version: 4
updated: "2026-11-02T09:00:00Z"
source: "https://wiki.example.com/acme/handbook/d/k3x9p2qa7tm4zb/install-on-linux"
---

# Install on Linux

## Before you start
```

The classification is only in the front matter. The text does not
repeat it. The title is the first heading. Thus each heading of the text goes down
one level: a level 1 heading becomes `##`. Markdown has six levels, thus
the levels 5 to 8 become `######`. A wiki link becomes a normal link.
The formats of Markdown and of the editor are not the same. Thus the
file can look different from the page.

### HTML

The HTML file is one web page. It contains the images, thus a browser
can show the file without Skriva. The page has its own style, with the
fonts of your system. It has a dark mode and a print style.

The title, the classification, the version and the time of the change
are in the `<head>` of the page:

```html
<title>Install on Linux</title>
<meta name="classification" content="Restricted">
<meta name="version" content="4">
<meta name="dcterms.modified" content="2026-11-02T09:00:00Z">
<link rel="canonical" href="https://wiki.example.com/acme/handbook/d/k3x9p2qa7tm4zb/install-on-linux">
```

The title is the `<h1>` element. The headings of the text are `<h2>`
to `<h6>`, the same as on the page in Skriva. The file contains a
maximum of 32 MB of images. The images after this limit refer to
Skriva. Most browsers then do not show them, because Skriva does not
let other pages load its files. The page contains no scripts. A
security policy in the page does not let a browser run scripts.

### PDF

The PDF file has A4 pages. The top and the bottom of each page show the
classification, the same as a printed page. The bottom of each page
also shows the title and the page number. The PDF metadata contains the
title and the classification.

The file contains the uploaded images, with a maximum of 32 MB. An image
from a different site shows as a box with its description. Links
refer to Skriva.

The HTML file and the PDF file contain the images of the Mermaid
diagrams. The Markdown file contains the text of each diagram in a
```` ```mermaid ```` code block. Many Markdown programs show it as a
diagram.

The PDF file uses the fonts of the Typst program, not the fonts of the
page. A draw.io diagram with formatted labels (HTML labels) can show the
text "Text is not SVG - cannot display". To prevent this, clear
**Formatted Text** for the labels in draw.io before the export of the
diagram.

The PDF export needs the Typst program on the server. If the server
does not have it, the export page shows the PDF format with disabled
buttons.
