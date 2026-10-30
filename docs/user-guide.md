# User guide

## Sign in

1. Open the URL of your tenant, for example `https://wiki.example.com/acme/`.
2. Sign in at the identity provider.

The wiki then shows the page that you opened. Each tenant has its own
sign-in. If you use more than one tenant, sign in to each tenant.

## Spaces, folders and documents

- The start page of a tenant shows its **spaces**.
- A space contains **folders** and **documents** in a tree.
- A folder contains documents and other folders.
- A document contains text. It can also contain other documents, for
  example a main document with sub-documents.

The page of a document shows the tree of the space at the left side. The
path to the document is above the title.

## Links to a document

Each document has two links:

- The full link, for example `/acme/handbook/d/Hq7mZp2Kx4aB/install`.
- The short link, for example `/acme/handbook/d/Hq7mZp2Kx4aB`. The
  bottom of the page shows it.

The two links stay valid when the title changes or when the document
moves. Only users with access to the tenant can open them.

## Make a document or a folder

1. Open the space, a folder or a document.
2. Click **New document** or **New folder**.
3. Type the title.
4. For a document, type the text.
5. Click **Save**.

The new item is in the folder or the document that you opened. A folder
cannot be in a document.

You can also make a document from a wiki link. Write `[[Title of new
document]]` in a document. Click the link. Then click **Make this
document**.

## Edit a document

1. Click **Edit**.
2. Change the text.
3. Click **Preview** to see the result. The preview does not save.
4. Type a short comment about the change.
5. Click **Save**.

If a different user saved the document after you started, the wiki does
not save your text. Copy your text. Then reload the page and add your
changes again.

To change the name of a folder, click **Rename**.

## Move a document or a folder

1. Click **Move**.
2. Select the new place. "(Top of the space)" is the top level.
3. Click **Move**.

The list does not show the item itself and the items in it. For a
folder, the list shows only folders.

## Delete a document or a folder

1. Click **Delete**.
2. Read the number of items in it. The wiki also deletes these items.
3. Click **Delete**.

You cannot undo this.

## Restore an old version

1. Click **History**.
2. Click **Restore** in the row of the version.
3. Click **Save**.

The wiki saves the old text as a new version. The history does not
change.

## Markdown

| Text                          | Result |
|-------------------------------|--------|
| `# Heading`                   | A heading. Use `##` and `###` for lower levels. |
| `**bold**`, `*italic*`        | **bold**, *italic* |
| `` `code` ``                  | `code` |
| `~~text~~`                    | ~~text~~ |
| `[text](https://example.com)` | A link to a different site. |
| `[[Title]]`                   | A link to a document in the same space. |
| `[[Title#Heading]]`           | A link to a heading in a document. |
| `- item`                      | A list. |
| `1. item`                     | A numbered list. |
| `- [ ] task`                  | A task list. |
| `> text`                      | A quotation. |
| `![text](https://...)`        | An image. |

If more than one document in the space has the title of a wiki link, the
wiki shows a list. Then select the correct document.

Use three back-ticks (```` ``` ````) before and after a code block. To
make a table, use `|` between the columns:

```
| Name | Value |
|------|-------|
| a    | 1     |
```

The wiki does not permit HTML in documents.

## Search

Type words in the search field and push Enter. In a space, the search
finds documents in that space. Click **Search in all spaces** to search
in all spaces of the tenant. These forms are possible:

| Search          | Finds |
|-----------------|-------|
| `alpha beta`    | Documents with the two words. |
| `"alpha beta"`  | Documents with the two words together. |
| `alpha or beta` | Documents with one of the words. |
| `alpha -beta`   | Documents with `alpha`, but not `beta`. |
