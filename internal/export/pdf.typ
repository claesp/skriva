// The template of the PDF export. Skriva writes the document to
// "doc.json" and runs Typst on this file. The text of the document is
// data. Typst shows strings from JSON as text, thus the text cannot
// contain Typst code. The Go code in pdf.go prepares the data: the links
// are absolute URLs, and the source of an image is a file in the folder,
// or "" if the file does not contain the image. A code block with a
// Mermaid diagram has the attribute "src" with the file of the image, or
// "error" with an error message.

#let data = json("doc.json")
#let classification = upper(data.classification)

#set document(
  title: data.title,
  description: if classification != "" { "Classification: " + classification } else { none },
  keywords: if classification != "" { (classification,) } else { () },
)
#set text(font: "Libertinus Serif", size: 11pt, lang: "en", hyphenate: auto)
#set par(justify: false, leading: 0.65em, spacing: 1.1em)
#show raw: set text(font: "DejaVu Sans Mono", size: 0.85em)
#show link: underline

// Each page shows the classification at the top and at the bottom, the
// same as the printed page of Skriva.
#let marking = if classification != "" {
  align(center, text(size: 9pt, weight: "bold", tracking: 0.08em, classification))
}
#set page(
  paper: "a4",
  margin: (x: 2.2cm, top: 2.6cm, bottom: 2.6cm),
  header: marking,
  footer: context {
    set text(size: 8.5pt, fill: luma(90))
    grid(
      columns: (1fr, auto, 1fr),
      align: (left, center, right),
      data.title,
      marking,
      [#counter(page).display() / #counter(page).final().first()],
    )
  },
)

// The headings of the text. Level 1 of the text is below the title.
#set heading(numbering: none)
#show heading: set block(above: 1.4em, below: 0.6em)
#show heading.where(level: 1): set text(size: 16pt, weight: "regular")
#show heading.where(level: 2): set text(size: 13.5pt, weight: "regular", style: "italic")
#show heading.where(level: 3): set text(size: 12pt, weight: "semibold")
#show heading.where(level: 4): set text(size: 11pt, weight: "bold")
#show heading.where(level: 5): set text(size: 11pt, weight: "bold", style: "italic")
#show heading.where(level: 6): it => block(above: 1.4em, below: 0.6em,
  text(size: 9.5pt, weight: "bold", tracking: 0.08em, upper(it.body)))
#show heading.where(level: 7): it => block(above: 1.4em, below: 0.6em,
  text(size: 9.5pt, weight: "regular", tracking: 0.12em, upper(it.body)))
#show heading.where(level: 8): it => block(above: 1.4em, below: 0.6em,
  text(style: "italic", fill: luma(90), it.body))

#let attr(n, key, default) = {
  let a = n.at("attrs", default: (:))
  if a == none { default } else { a.at(key, default: default) }
}
#let children(n) = n.at("content", default: ())

// inline returns the content of the inline nodes of n.
#let inline(n) = {
  for c in children(n) {
    if c.type == "text" {
      let body = c.text
      let marks = c.at("marks", default: ())
      let href = none
      let code = false
      for m in marks { if m.type == "code" { code = true } }
      if code { body = raw(c.text) }
      for m in marks {
        if m.type == "bold" { body = strong(body) }
        else if m.type == "italic" { body = emph(body) }
        else if m.type == "underline" { body = underline(body) }
        else if m.type == "strike" { body = strike(body) }
        else if m.type == "link" { href = attr(m, "href", "") }
      }
      if href != none and href != "" { link(href, body) } else { body }
    } else if c.type == "hardBreak" {
      linebreak()
    } else if c.type == "image" {
      let src = attr(c, "src", "")
      if src != "" { box(image(src, alt: attr(c, "alt", ""), height: 1.2em)) }
    }
  }
}

#let align-of(n) = {
  let a = attr(n, "textAlign", "")
  if a == "center" { center } else if a == "right" { right } else { left }
}

// picture shows an image with its natural size, but not wider than the
// text. An image that the file does not contain shows its description.
#let picture(n) = {
  let src = attr(n, "src", "")
  let alt = attr(n, "alt", "")
  if src == "" {
    block(stroke: 0.5pt + luma(160), inset: 0.6em, text(fill: luma(90), "[Image" + if alt != "" { ": " + alt } + "]"))
  } else {
    layout(size => {
      let img = image(src, alt: alt)
      let w = measure(img).width
      block(if w > size.width { image(src, alt: alt, width: 100%) } else { img })
    })
  }
}

#let task-box(checked) = box(
  baseline: 0.1em,
  rect(width: 0.75em, height: 0.75em, stroke: 0.6pt, inset: 0pt,
    if checked { align(center + horizon, text(size: 0.7em, "✓")) }),
)

// blocks returns the content of a list of block nodes. A Typst function
// can call itself, but two functions cannot call each other. Thus one
// function handles all block nodes.
#let blocks(nodes) = for n in nodes {
  let t = n.type
  if t == "paragraph" {
    align(align-of(n), par(inline(n)))
  } else if t == "heading" {
    align(align-of(n), heading(level: attr(n, "level", 1), inline(n)))
  } else if t == "blockquote" {
    block(inset: (left: 1em), stroke: (left: 1.5pt + luma(180)), emph(blocks(children(n))))
  } else if t == "codeBlock" and attr(n, "src", "") != "" {
    // A Mermaid diagram. The Go code wrote the image.
    picture(n)
  } else if t == "codeBlock" {
    let code = children(n).map(c => c.at("text", default: "")).join()
    block(fill: luma(244), inset: 0.8em, radius: 3pt, width: 100%,
      raw(if code == none { "" } else { code }, block: true, lang: attr(n, "language", none)))
    let error = attr(n, "error", "")
    if error != "" {
      text(size: 0.85em, fill: luma(90), upper(error.first()) + error.slice(error.first().len()) + ".")
    }
  } else if t == "horizontalRule" {
    line(length: 100%, stroke: 0.5pt + luma(160))
  } else if t == "bulletList" {
    list(..children(n).map(i => blocks(children(i))))
  } else if t == "orderedList" {
    enum(start: attr(n, "start", 1), ..children(n).map(i => blocks(children(i))))
  } else if t == "taskList" {
    for i in children(n) {
      let done = attr(i, "checked", false)
      grid(columns: (1.4em, 1fr), task-box(done),
        if done { text(fill: luma(90), strike(blocks(children(i)))) } else { blocks(children(i)) })
    }
  } else if t == "image" {
    picture(n)
  } else if t == "table" {
    let rows = children(n)
    let width = 0
    for c in children(rows.at(0, default: (:))) { width += attr(c, "colspan", 1) }
    if width > 0 {
      set text(size: 0.9em)
      table(
        columns: width,
        stroke: 0.5pt + luma(170),
        inset: 0.45em,
        ..rows.map(r => children(r).map(c => {
          let body = blocks(children(c))
          table.cell(
            colspan: attr(c, "colspan", 1),
            rowspan: attr(c, "rowspan", 1),
            fill: if c.type == "tableHeader" { luma(240) } else { none },
            if c.type == "tableHeader" { strong(body) } else { body },
          )
        })).flatten()
      )
    }
  } else {
    blocks(children(n))
  }
}

#text(size: 22pt, data.title)
#v(0.6em)
#blocks(children(data.body))
