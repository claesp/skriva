# WYSIWYG editors: research

This document compares free editor components for WYSIWYG editing of
documents in Skriva. It gives a recommendation and a plan for the
integration. The data is from 2026-10-31.

## Requirements

1. Performance: a small script, and fast typing in long documents.
2. Ease of use: the user writes formatted text, not Markdown syntax.
3. Reliability: an active project with many users and a stable API.
4. Security: the editor must work with a strict Content Security
   Policy. The server must not trust the editor output.
5. Control: Skriva makes the toolbar buttons. The component must not
   force its own user interface.
6. Storage: if possible, keep Markdown (GFM, footnotes and wiki links)
   as the storage format.
7. License: an open-source license that permits use in all types of
   software (MIT, BSD, Apache or MPL). GPL-only components are not
   suitable.

## Components

| Component | Version | License | Last release | Model | Own toolbar | Markdown |
|-----------|---------|---------|--------------|-------|-------------|----------|
| Tiptap 3 | 3.32.2 | MIT | 2026-10-30 | ProseMirror | Yes, headless | Official `@tiptap/markdown` (MIT, uses `marked`). The vendor calls it an early release. |
| Milkdown 7 | 7.23.0 | MIT | 2026-10-27 | ProseMirror + remark | Yes (`@milkdown/kit`). `@milkdown/crepe` has a fixed user interface. | Native. The document model comes from the Markdown syntax tree. GFM preset. |
| ProseMirror | 1.42.9 (view) | MIT | 2026-10-31 | ProseMirror | Yes, all parts are your own | `prosemirror-markdown` (CommonMark only, other syntax is your own work) |
| Lexical | 0.53.0 | MIT | 2026-10-28 | Own model | Yes, headless | `@lexical/markdown` with transformers. Version 0.x: breaking changes are possible. |
| Toast UI Editor | 3.2.2 | MIT | 2023-02-17 | ProseMirror | Partly | Native. No releases since 2023. |
| Quill 2 | 2.0.3 | BSD-3 | 2024-11-30 | Delta | Yes | No. Delta or HTML only. |
| BlockNote | 0.57.0 | MPL-2.0 core, GPL-3.0 for the `xl-*` packages | 2026 | Tiptap | Partly, block user interface | Export with data loss. |
| CKEditor 5 | 49.0.1 | GPL-2.0+ or commercial | 2026 | Own model | Yes | Plugin |
| TinyMCE 8 | 8.10.1 | GPL-2.0+ or commercial | 2026 | HTML | Yes | No |

The versions, licenses and dates come from the npm registry.

### Components that are not suitable

- **CKEditor 5 and TinyMCE.** The free license is GPL. The license key
  must be `GPL` and CKEditor shows its logo. This is not compatible with
  requirement 7.
- **Toast UI Editor.** No release since February 2023. Not reliable.
- **Quill.** It does not support Markdown. A change to HTML or Delta as
  the storage format is necessary.
- **BlockNote.** The block model does not agree with Markdown, and the
  export loses data. The useful parts have the GPL license.
- **ProseMirror alone.** It is the base of Tiptap and Milkdown and it is
  very reliable. But the schema, the commands, the Markdown support and
  the keyboard shortcuts are all your own work. Tiptap and Milkdown give
  these parts.
- **Lexical.** It is good and fast. But it is still at version 0.x, and
  tables and footnotes in Markdown need your own transformers.

## WebAssembly

There is no maintained WYSIWYG editor in WebAssembly. A text editor in
the browser must use the DOM (`contenteditable`). Only the DOM gives
input methods (IME), spelling checks, screen readers and the selection
of the operating system. An editor that draws on a canvas does not give
these functions. Thus the editor must use JavaScript.

WebAssembly can give one small advantage: the Go Markdown renderer
(goldmark) can run in the browser. Then the preview is the same as the
server result. But the Go runtime adds approximately 2 to 5 MB. The
server preview is sufficient. Do not use WebAssembly.

## Storage format

Keep Markdown as the storage format. The reasons are:

- The history shows the differences between versions as text.
- The "Source" page, the search and the text export use the text.
- A user without JavaScript can edit the text in the text field.
- The server renders the text with goldmark and bluemonday. Thus the
  editor is not a security control.

The editor must show only the formats that Markdown can keep. Do not
add buttons for formats that Markdown does not have, for example text
color, text alignment, underline or merged table cells. If a format is
necessary in the future, do the change of the storage format then.

The editor needs these formats, because Skriva supports them now:

| Format | Tiptap | Milkdown |
|--------|--------|----------|
| Headings, bold, italic, code, links, lists, quotes, code blocks, rules | Yes | Yes |
| Strikethrough, tables, task lists (GFM) | Yes | Yes (GFM preset) |
| Footnotes | Own extension | Yes (GFM preset) |
| Wiki links `[[Title]]` | Own extension | Own plugin |
| Images (links to images) | Yes | Yes |

## Recommendation

Use **Tiptap 3** with `@tiptap/markdown`. The reasons are:

- **Control.** Tiptap has no user interface. Skriva makes the toolbar
  with its own HTML and icons. Each button calls a command, for example
  `editor.chain().focus().toggleBold().run()`. The function
  `editor.isActive("bold")` gives the state of each button.
- **Reliability.** Tiptap uses ProseMirror. It has the most users of the
  components in this document, a company maintains it, and it has new
  releases each week. All parts that Skriva needs have the MIT license.
  Skriva does not use the paid parts (Tiptap Cloud, collaboration, AI).
- **Security.** The ProseMirror schema removes all HTML that it does not
  know, also in pasted text. The editor does not need `eval`.
- **Performance.** ProseMirror changes only the parts of the DOM that
  change. The size of the script is approximately 150 to 250 KB
  minified (estimate, measure it in the prototype).

The risk: `@tiptap/markdown` is new. The vendor calls it an early
release. Do a test of the conversion from Markdown to the editor and
back with a set of test documents before the decision. Each document
must stay the same, or change only in the format of the text (for
example `*` in place of `_`).

The alternative is **Milkdown 7** (`@milkdown/kit`, not Crepe). Its
Markdown support is better, because it uses remark, and its GFM preset
includes footnotes. But one person does most of the work on Milkdown.
Use Milkdown if the Tiptap test fails.

## Integration plan

1. **Progressive enhancement.** The edit page keeps the text field. The
   script replaces the text field with the editor. Before the form is
   sent, the script writes the Markdown into the text field. Without
   JavaScript, the page works as it does now.
2. **Content Security Policy.** Add `script-src 'self'` only on the
   edit pages. Do not use inline scripts. The page gives settings to
   the script with `data-` attributes. Keep `style-src 'self'` without
   `'unsafe-inline'`. Some ProseMirror menus set `style` attributes.
   Skriva does not use these menus. Do a test of each function with the
   strict policy.
3. **Toolbar.** The Go templates make the toolbar with the same
   `button.tool` icons as other pages. Each button has a
   `data-command` attribute. The script connects the buttons to the
   commands and sets `aria-pressed`. The buttons are not visible
   without JavaScript.
4. **Mode switch.** A button changes between the editor and the
   Markdown text. The preview button stays as it is.
5. **Build.** Keep `package.json` and `package-lock.json` in
   `web/editor/`. Build the script in a Podman container with
   `npm ci --ignore-scripts` and esbuild. Put the result in
   `internal/web/static/editor.js`. The Go program embeds it. The
   program does not load scripts from other sites.
6. **Dependencies.** Use exact versions. Do a check with `npm audit` at
   each update, as with `govulncheck` for Go.
7. **Server checks.** The server keeps all checks: the size limit, the
   version check and the sanitizer. The editor gives only Markdown.
8. **Tests.** Add a test of the conversion with the test documents. Add
   an end-to-end test that edits a document with the editor.

## Sources

- npm registry data for each package (version, license, date), read on
  2026-10-31.
- [Tiptap: bidirectional Markdown support](https://tiptap.dev/blog/release-notes/introducing-bidirectional-markdown-support-in-tiptap)
- [Tiptap Markdown documentation](https://tiptap.dev/docs/editor/markdown)
- [Lexical Markdown package](https://lexical.dev/docs/packages/lexical-markdown)
- [Toast UI Editor releases](https://github.com/nhnent/tui.editor/releases)
- [BlockNote repository (license)](https://github.com/TypeCellOS/BlockNote)
- [CKEditor 5 license key and activation](https://ckeditor.com/docs/ckeditor5/latest/support/licensing/license-key-and-activation.html)
- [TinyMCE 7 license key](https://www.tiny.cloud/docs/tinymce/7/license-key/)
- [ProseMirror forum: CSP and inline styles](https://discuss.prosemirror.net/t/way-to-modify-replace-dom-render-function-for-csp-compliance/3780)
- [MDN: CSP style-src](https://developer.mozilla.org/docs/Web/HTTP/Headers/Content-Security-Policy/style-src)
