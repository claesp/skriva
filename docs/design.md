# Page design

The document text is the main item of each page. The interface (header,
tree, menus and toolbars) uses a smaller sans-serif font and muted
colors. Thus it stays in the background.

The design of the text follows [Tufte CSS](https://edwardtufte.github.io/tufte-css/)
and these guidelines for long-form text:

- [WCAG 2 success criterion 1.4.8, Visual Presentation](https://www.w3.org/WAI/WCAG21/Understanding/visual-presentation).
- [Butterick's Practical Typography, summary of key rules](https://practicaltypography.com/summary-of-key-rules.html).
- [Optimal Line Length in Reading, a literature review](https://journals.uc.edu/index.php/vl/article/view/5765).

## Rules for the document text

| Item | Value | Reason |
|------|-------|--------|
| Font | A serif font that the system has: Charter, Sitka Text, Cambria, Palatino or Georgia. | Tufte CSS uses a serif font for the text. The page does not load fonts from other sites. |
| Size | 18 px on small screens to 22 px on large screens (1.125rem to 1.375rem). | Butterick recommends 15 to 25 px on the web. |
| Line height | 1.6 | WCAG 1.4.8 recommends at least 1.5. |
| Line length | 40rem to 64rem. The width increases with the width of the window. This is approximately 40 characters on a telephone, 75 characters on a 1440 px screen and up to 90 characters on very large screens. | The guidelines recommend 45 to 75 characters, and WCAG 1.4.8 permits a maximum of 80. Large screens use more of the width, by decision of the project. |
| Paragraph space | 1.25em below each paragraph. | The space between paragraphs must be larger than the space between lines. |
| Alignment | Left. No justification. | WCAG 1.4.8 does not permit justified text. |
| Headings | Normal weight. The second and third levels are italic. | As in Tufte CSS. |
| Links | The color of the text, with a thin underline. | The underline identifies the link. The color does not interrupt the reading. |
| Tables | Sans-serif font, smaller size. | As in Tufte CSS. |
| Colors | Text `#111111` on `#fffff8`. Dark mode: `#dddddd` on `#151515`. | High contrast, as in Tufte CSS. |

## Other rules

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
  UTC. The page does not use scripts, thus the server cannot know the
  time zone of the reader.
- When the mouse pointer is on a date, the page shows the full time in
  ISO 8601 format with the time zone, for example
  "2026-10-31T14:06:07+00:00". The date is a `time` element, thus
  programs can read the time from the `datetime` attribute.
- In a table, a date column is the last data column. The text in it is
  right-aligned.
