# User guide

## Sign in

1. Open the wiki.
2. Sign in at the identity provider.

The wiki then shows the page that you opened.

## Read a page

Each page has these links:

- **Edit**: change the page. You must have the `page.edit` permission.
- **History**: show all versions of the page.
- **Source**: show the Markdown text of the page.
- **Delete**: delete the page. You must have the `page.delete` permission.

## Make a page

1. Click **New page**.
2. Type the title.
3. Click **Continue**.
4. Type the text.
5. Click **Save**.

You can also make a page from a link. Write `[[Title of new page]]` in a
page. Click the link. Then click **Make this page**.

## Edit a page

1. Click **Edit**.
2. Change the text.
3. Click **Preview** to see the result. The preview does not save.
4. Type a short comment about the change.
5. Click **Save**.

If a different user saved the page after you started, the wiki does not
save your text. Copy your text. Then reload the page and add your
changes again.

## Restore an old version

1. Click **History**.
2. Click **Restore** in the row of the version.
3. Click **Save**.

The wiki saves the old text as a new version. The history does not
change.

## Markdown

| Text                         | Result |
|------------------------------|--------|
| `# Heading`                  | A heading. Use `##` and `###` for lower levels. |
| `**bold**`, `*italic*`       | **bold**, *italic* |
| `` `code` ``                 | `code` |
| `~~text~~`                   | ~~text~~ |
| `[text](https://example.com)`| A link to a different site. |
| `[[Page title]]`             | A link to a wiki page. |
| `[[Page title#Heading]]`     | A link to a heading on a wiki page. |
| `- item`                     | A list. |
| `1. item`                    | A numbered list. |
| `- [ ] task`                 | A task list. |
| `> text`                     | A quotation. |
| `![text](https://...)`       | An image. |

Use three back-ticks (```` ``` ````) before and after a code block. To
make a table, use `|` between the columns:

```
| Name | Value |
|------|-------|
| a    | 1     |
```

The wiki does not permit HTML in pages.

## Search

Type words in the search field and push Enter. These forms are
possible:

| Search            | Finds |
|-------------------|-------|
| `alpha beta`      | Pages with the two words. |
| `"alpha beta"`    | Pages with the two words together. |
| `alpha or beta`   | Pages with one of the words. |
| `alpha -beta`     | Pages with `alpha`, but not `beta`. |
