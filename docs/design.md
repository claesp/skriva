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
| Size | 18 px on small screens to 21 px on large screens (1.125rem to 1.3125rem). | Butterick recommends 15 to 25 px on the web. |
| Line height | 1.6 | WCAG 1.4.8 recommends at least 1.5. |
| Line length | 40rem. This is approximately 60 to 70 characters on a large screen, and 40 characters on a telephone. | The guidelines recommend 45 to 75 characters. WCAG 1.4.8 permits a maximum of 80. |
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
- A printed document shows only the text, without the header, the tree
  and the toolbar. The print shows the address of each external link.
- Each element with the keyboard focus has a visible outline.
