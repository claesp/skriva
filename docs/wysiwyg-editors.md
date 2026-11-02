# WYSIWYG editors: research

This document compares free editor components for WYSIWYG editing of
documents in Skriva. It gives the decision and the reasons. The data is
from 2026-10-31.

Skriva uses Tiptap 3, and keeps the documents in the JSON format of
Tiptap. See [Decision](#decision).

## Requirements

1. Performance: a small script, and fast typing in long documents.
2. Ease of use: the user writes formatted text, not Markdown syntax.
3. Reliability: an active project with many users and a stable API.
4. Security: the editor must work with a strict Content Security
   Policy. The server must not trust the editor output.
5. Control: Skriva makes the toolbar buttons. The component must not
   force its own user interface.
6. Formats: inline images, full table support (header rows, merged
   cells), text formats, alignment and headings. The storage format can
   change for these formats.
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

WebAssembly could run Go code of the server in the browser, for example
the HTML converter. But the Go runtime adds approximately 2 to 5 MB, and
a WYSIWYG editor shows the result directly. Do not use WebAssembly.

## Storage format

Markdown (GFM) does not have alignment, underline or merged table cells.
Raw HTML in Markdown can have them, but Skriva does not permit raw HTML
for security. Thus Markdown cannot be the storage format.

| Format | Advantages | Disadvantages |
|--------|------------|---------------|
| Tiptap JSON (ProseMirror) | The format of the editor, with no conversion and no loss. The server can check it against a list of nodes and attributes. The server makes all HTML. | Not readable as text. Diffs of versions are difficult. Needs a converter for the search and for export. |
| HTML | Easy to show. | The server must sanitize HTML from the browser. Inline styles conflict with the security policy. Diffs are difficult. |
| Markdown | Readable. Easy to diff. | Does not have the necessary formats. |

The decision is Tiptap JSON. The server keeps a plain-text version of
each document for the search, and converts a document to Markdown for
the export. The Markdown ignores the formats that Markdown does
not have.

## Changes without the Markdown requirement

- **Tiptap** stays the best choice. Its MIT extensions have all the
  necessary formats: tables with merged cells and header rows, text
  alignment, images and underline.
- **Milkdown** is not suitable now. Its main advantage is the Markdown
  support.
- **Lexical** is the best alternative. It has tables with merged cells
  and alignment, but it is still at version 0.x.
- **CKEditor 5** has the most formats, but its free license is GPL.

## Decision

Skriva uses **Tiptap 3** (MIT) with the JSON format. The reasons are:

- **Control.** Tiptap has no user interface. The Go templates make the
  toolbar with the same icons as the other pages. The script connects
  each button to a command.
- **Reliability.** Tiptap uses ProseMirror, which many products use. A
  company maintains Tiptap and makes new releases each week. Skriva uses
  only the MIT parts.
- **Security.** The ProseMirror schema removes unknown HTML, also in
  pasted text. The editor works with the strict security policy: no
  inline scripts, no `eval`, no inline styles. The server checks all
  data again.
- **Performance.** ProseMirror changes only the parts of the DOM that
  change. The script is 445 KB minified (140 KB with gzip). Only the
  form of a document loads it, and the browser keeps it in its cache.

WebAssembly is not an option: no maintained WYSIWYG editor uses it, and
a text editor must use the DOM for input methods, spelling checks and
screen readers.

## Implementation

- [Architecture: document format](architecture.md#document-format)
- [Architecture: uploaded files](architecture.md#uploaded-files)
- [Security: JavaScript](security.md#javascript)
- [Development: editor](development.md#editor)
- [User guide: the editor](user-guide.md#the-editor)

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
