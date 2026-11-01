# Page design

The document text is the main item of each page. The interface (header,
tree, menus and toolbars) uses a smaller sans-serif font (Inter) and
muted colors. Thus it stays in the background. Code uses JetBrains Mono.
See [Fonts](fonts.md).

The design of the text follows [Tufte CSS](https://edwardtufte.github.io/tufte-css/)
and these guidelines for long-form text:

- [WCAG 2 success criterion 1.4.8, Visual Presentation](https://www.w3.org/WAI/WCAG21/Understanding/visual-presentation).
- [Butterick's Practical Typography, summary of key rules](https://practicaltypography.com/summary-of-key-rules.html).
- [Optimal Line Length in Reading, a literature review](https://journals.uc.edu/index.php/vl/article/view/5765).

## Rules for the document text

| Item | Value | Reason |
|------|-------|--------|
| Font | Source Serif 4. The fallback fonts are Charter, Sitka Text, Cambria, Palatino and Georgia. | Tufte CSS uses a serif font for the text. The server supplies the font, thus all systems show the same font. See [Fonts](fonts.md). |
| Size | 18 px on small screens to 22 px on large screens (1.125rem to 1.375rem). | Butterick recommends 15 to 25 px on the web. |
| Line height | 1.6 | WCAG 1.4.8 recommends at least 1.5. |
| Line length | 75% of the window, from 40rem to 90rem. On a small window, the text uses all of the column. This is approximately 40 characters on a telephone and 125 characters on a 1440 px screen. | By decision of the project: the text uses most of the width on large screens. The guidelines recommend 45 to 75 characters, and WCAG 1.4.8 permits a maximum of 80. |
| Paragraph space | 1.25em below each paragraph. | The space between paragraphs must be larger than the space between lines. |
| Alignment | Left. No justification. | WCAG 1.4.8 does not permit justified text. |
| Headings | Eight levels. Levels 1 to 3 are larger than the text: normal weight (1), italic (2), semibold (3). Levels 4 to 8 are not smaller than the text, or they use capitals: bold (4), bold italic (5), bold capitals with letter spacing (6), normal capitals with more letter spacing (7), gray italic (8). The editor shows the headings with the same styles. | Size, weight, italic, capitals and spacing make each level different. Small headings are difficult to see, thus levels 4 to 8 do not use a smaller size. The fonts do not contain small capitals. |
| Links | The color of the text, with a thin underline. | The underline identifies the link. The color does not interrupt the reading. |
| Tables | Sans-serif font, smaller size. | As in Tufte CSS. |
| Colors | Text `#111111` on `#fffff8`. Dark mode: `#dddddd` on `#151515`. | High contrast, as in Tufte CSS. |

## Rules for the interface

- The interface text is one step smaller than the browser size: 15 px
  for the normal text and 13 px for menus, the tree, toolbars and notes
  (at the default browser size of 16 px). The document text and the
  title of a document do not change.
- The page header has the name of the tenant (or "System
  administration" on the pages of the system administration) at the
  left side. The user name and the **Sign out** button (or **Sign in**)
  are always at the right side, also on small windows.
- The **New** button in the page header has the accent color. It opens
  a list of the items that the user can make. The list is a `details`
  element, thus it does not need JavaScript. The left side of the list
  is below the left side of the button. On small windows, the list
  opens to the left. A click out of the list closes it: when the list
  is open, a transparent layer of the button covers the window.

## Other rules

- All pages have the same layout: the bar with the path below the page
  header, the column at the left side and the text in the center. This
  also applies to pages without a tree or a menu: the error pages (for
  example "Forbidden" and "Not Found"), the page after the sign-out and
  the page of a wiki link without one target. These pages use the
  template `plainhead`. On a small window, an empty column is not
  shown. A new page must use one of the shared head templates.

- All sizes use `rem` or `em`. Thus the text size that the user sets in
  the browser applies.
- A link "Skip to the content" is the first item of each page. It is
  visible when it has the keyboard focus.
- A heading that a link opens stays visible below the sticky toolbar.
- The toolbar of a document covers the full width of the page. The tree
  starts below it.
- The data below a document (version, date, short link) is small
  (0.75rem) and muted. It gives information, but it does not compete with
  the text.
- A printed document shows only the text, without the header, the tree
  and the toolbar. The print shows the address of each external link.
- Each element with the keyboard focus has a visible outline.
- A date shows as a relative time: "Just now", "2 seconds ago",
  "24 minutes ago", "3 hours ago", "Yesterday" or "2 days ago". A date
  that is more than 10 days ago shows as an ISO 8601 date, for example
  "2026-10-31". The server calculates the days with the calendar days in
  UTC. The pages do not use scripts, thus the server cannot know the
  time zone of the reader.
- When the mouse pointer is on a date, the page shows the full time in
  ISO 8601 format with the time zone, for example
  "2026-10-31T14:06:07+00:00". The date is a `time` element, thus
  programs can read the time from the `datetime` attribute.
- In a table, a date column is the last data column. The text in it is
  right-aligned.
- All toolbars and all columns of action buttons show all their
  buttons. A button for an action that the user cannot do (no
  permission, or not possible for this item) is disabled: gray, without
  a link, with "(not available)" in its name. It is not hidden. This
  applies to the toolbars of the space, of the administration pages and
  of the tables (for example the built-in roles, the OIDC groups and the
  old versions). The template function `when` disables a button. The
  menus of the administration also show all items. An item for a page
  that the user does not have permission for is gray and has no link.
- The toolbar of a folder or a document has the same buttons at the
  same places in all modes (the page, the forms and the form of a new
  item) and for all users. The user does not have to look for a button.
  A button is disabled (gray, no link) if the user does not have the
  permission, or, on the form of a new item, if it needs a saved item.
  On the form of a new item, the pencil button is the current button.
  It goes back without a save. For a folder, History and Source are
  disabled, because a folder has no text. Thus folders and documents
  have the buttons at the same places.
- The **Save** button of a form is at the left side of the toolbar, in
  its own group. The toolbar is right-aligned, thus the other buttons
  do not move. The toolbar is sticky, thus the button is always
  visible. The button is outside the form and uses the `form`
  attribute. This needs no script.
- The toolbar of the editor uses the same icon buttons as the toolbar
  of a page. It stays below the bar of the page when the user scrolls.
  The buttons for tables are visible only when the cursor is in a table.
- The editor shows the text with the same font, size and line length as
  the document page.
- The comparison of two versions uses the monospaced font, as a
  program for code. A removed line has a red background and the sign
  `−`, an added line has a green background and the sign `+`. The
  colors are not the only sign: screen readers read "Removed" and
  "Added", and the changed words are `mark` elements.
- Alignment uses CSS classes (`align-center`, `align-right`), not inline
  styles.
