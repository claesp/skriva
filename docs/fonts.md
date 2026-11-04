# Fonts: research

This document compares free fonts for the pages of Skriva. It gives the
decision and the reasons. The data is from 2026-11-01.

Skriva uses **Inter** for the interface, **Source Serif 4** for the
document text and **JetBrains Mono** for code. The server keeps the font
files. See [Decision](#decision).

## Requirements

1. The same fonts on all operating systems and browsers. Thus the server
   must supply the fonts. Before this change, each system used a
   different font from a list (for example Charter, Cambria or Georgia).
2. License: the license must permit redistribution with the software,
   also in commercial software. The SIL Open Font License 1.1 (OFL)
   permits this.
3. Quality: a good design for screens, a large character set, and an
   active project.
4. Popularity: many users. A popular font is well tested in all
   browsers.
5. Performance: small files. Variable fonts are best. One file contains
   all weights.
6. Security: the security policy of the pages does not permit content
   from other sites. Thus Skriva cannot use a font service such as Google
   Fonts. The fonts come from the Skriva server.
7. The current fonts of the system stay as fallback fonts.

## Rules for user interfaces

These rules come from typography guidelines for screens (WCAG 2
success criterion 1.4.8 and Butterick's Practical Typography) and from
the design notes of the fonts:

- Interface text is small. A sans-serif font with a large x-height and
  open shapes is easy to read at 13 to 15 px.
- Long text is easier to read in a serif font with a moderate contrast,
  as in Tufte CSS. See [Page design](design.md).
- Code needs a monospaced font in which the similar characters are
  different: `I`, `l`, `1` and `O`, `0`.
- Fonts that go together have similar proportions and x-heights.
- Use few fonts: one for each function.
- Show the text immediately. The browser shows a fallback font until
  the font is available (`font-display: swap`).

## Sans-serif fonts (interface)

| Font | License | Variable | Monthly downloads (Fontsource) | Notes |
|------|---------|----------|-------------------------------:|-------|
| **Inter** | OFL 1.1 | Yes (weight, optical size) | 24.0 million | Made for screen interfaces. Large x-height, tabular figures, many languages. The most used font on Fontsource of this list. |
| IBM Plex Sans | OFL 1.1 | Yes | 5.5 million | Good, but it has a strong character. |
| Source Sans 3 | OFL 1.1 | Yes | 1.3 million | Good, but smaller x-height than Inter. |
| Roboto | OFL 1.1 | Yes | 1.3 million | The Android font. It looks like the system font on Android only. |
| Public Sans | OFL 1.1 | Yes | 1.0 million | A font of the US government, based on Libre Franklin. |

Decision: **Inter**, as requested.

## Serif fonts (document text)

| Font | License | Variable | Monthly downloads (Fontsource) | Notes |
|------|---------|----------|-------------------------------:|-------|
| **Source Serif 4** | OFL 1.1, Reserved Font Name "Source" | Yes (weight, optical size 8 to 60) | 2.1 million | By Adobe (Frank Grießhammer). The optical size axis gives a text design for the body and a display design for the titles. Proportions agree with Inter. |
| Newsreader | OFL 1.1 | Yes (weight, optical size) | 1.5 million | Good for news text. No change to the repository since 2021. |
| IBM Plex Serif | OFL 1.1 | No (static files) | 0.5 million | Goes with IBM Plex Sans better than with Inter. |
| Literata | OFL 1.1 | Yes (weight, optical size) | 0.4 million | Made for e-books (Google Play Books). Very good for long text. The best alternative. |
| Merriweather | OFL 1.1 | Yes | 0.2 million | Large x-height, but heavy at text sizes. |
| Charis SIL | OFL 1.1 | No | 0.02 million | Based on Charter, the first font of the old list. Not popular, large files. |

Decision: **Source Serif 4**. It is popular, well made and maintained by
Adobe. Its optical sizes give good titles and good body text from one
file. Its design is similar to Charter, the first font of the old list.

The license reserves the name "Source". A changed version of the font
must not use this name. Skriva uses the files without changes, thus
this rule does not apply. Do not subset or change the files. Use new
files from the package.

## Monospaced fonts (code)

| Font | License | Variable | Monthly downloads (Fontsource) | Notes |
|------|---------|----------|-------------------------------:|-------|
| IBM Plex Mono | OFL 1.1 | No (static files) | 10.7 million | Good. It goes with IBM Plex Sans. |
| **JetBrains Mono** | OFL 1.1 | Yes (weight) | 8.4 million | Made for code. Large x-height, the same as Inter. Clear `Il1` and `O0`. Small files. |
| Roboto Mono | OFL 1.1 | Yes | 0.5 million | Narrow. |
| Fira Code | OFL 1.1 | Yes | 0.3 million | Known for its ligatures. |
| Source Code Pro | OFL 1.1 | Yes | 0.3 million | Good, small x-height. |
| Cascadia Code | OFL 1.1 | Yes | 0.02 million | The font of Windows Terminal. |

Decision: **JetBrains Mono**. It is made for code, its x-height agrees
with Inter, and the variable file is small. IBM Plex Mono has more
downloads, but it has no variable font.

JetBrains Mono has ligatures. For example, it shows `!=` as one sign.
Ligatures make code difficult to read and copy. The style sheet turns
them off for code (`font-variant-ligatures: none`).

## Decision

| Function | Font | Version | Fallback fonts |
|----------|------|---------|----------------|
| Interface | Inter | 4.001 | `system-ui`, Segoe UI, Roboto, Helvetica Neue, Arial |
| Document text and titles | Source Serif 4 | 4.004 | Charter, Sitka Text, Cambria, Iowan Old Style, Palatino, Georgia |
| Code | JetBrains Mono | 2.211 | `ui-monospace`, SF Mono, Menlo, Consolas, Liberation Mono |

## Implementation

- The files come from the [Fontsource](https://fontsource.org/) npm
  packages `@fontsource-variable/inter`,
  `@fontsource-variable/source-serif-4` and
  `@fontsource-variable/jetbrains-mono`. These are the files of Google
  Fonts in the WOFF2 format, already divided into subsets. `make fonts`
  gets them. See [Development: fonts](development.md#fonts).
- Skriva uses two subsets of each font: Latin and Latin Extended. They
  contain the characters of most European languages. Other characters
  (for example Greek, Cyrillic or Chinese) use the fallback fonts.
- Each subset has a `unicode-range`. The browser gets a file only if the
  page contains its characters. The browser gets an italic file only if
  the page contains italic text.
- Inter and JetBrains Mono use the weight axis only. The interface text
  is small, thus the optical size of Inter is not necessary. Source
  Serif 4 uses the weight and optical size axes. The browser selects the
  optical size from the font size (`font-optical-sizing: auto`).
- The pages do not preload the fonts (`<link rel="preload">`). The
  browser keeps the files, thus a preload helps only on the first visit.
  On the next pages, Firefox uses the font that it loaded before and not
  the preloaded file. Then it shows a warning in the console.
- The files are in `internal/web/static/fonts`. The name of each folder
  contains the package version. The server tells the browser to keep the
  files for one year.
- Each folder contains the license of the font (`LICENSE.txt`).
- The security policy permits fonts from the same site only
  (`font-src 'self'`).

| Font | Files | Size |
|------|-------|-----:|
| Inter | Latin and Latin Extended, normal and italic | 277 KB |
| Source Serif 4 | Latin and Latin Extended, normal and italic | 464 KB |
| JetBrains Mono | Latin and Latin Extended, normal and italic | 115 KB |

A normal page in English or Swedish needs Inter Latin (48 KB) and
Source Serif 4 Latin (122 KB). Italic text adds 130 KB and code adds
40 KB. The browser gets each file one time only.

## Sources

- npm registry: versions, licenses and monthly downloads of the
  Fontsource packages, read on 2026-11-01.
- [Inter](https://github.com/rsms/inter) and its
  [license](https://github.com/rsms/inter/blob/master/LICENSE.txt).
- [Source Serif](https://github.com/adobe-fonts/source-serif) and its
  [license](https://github.com/adobe-fonts/source-serif/blob/release/LICENSE.md).
- [JetBrains Mono](https://github.com/JetBrains/JetBrainsMono) and its
  [license](https://github.com/JetBrains/JetBrainsMono/blob/master/OFL.txt).
- [Literata](https://github.com/googlefonts/literata),
  [IBM Plex](https://github.com/IBM/plex),
  [Charis SIL](https://github.com/silnrsi/font-charis),
  [Newsreader](https://github.com/productiontype/Newsreader),
  [Cascadia Code](https://github.com/microsoft/cascadia-code).
- [SIL Open Font License 1.1 and FAQ](https://openfontlicense.org/).
- [WCAG 2, success criterion 1.4.8](https://www.w3.org/WAI/WCAG21/Understanding/visual-presentation).
- [Butterick's Practical Typography](https://practicaltypography.com/summary-of-key-rules.html).
- [MDN: font-display](https://developer.mozilla.org/docs/Web/CSS/@font-face/font-display),
  [unicode-range](https://developer.mozilla.org/docs/Web/CSS/@font-face/unicode-range)
  and [font-optical-sizing](https://developer.mozilla.org/docs/Web/CSS/font-optical-sizing).
