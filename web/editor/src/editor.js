// The WYSIWYG editor of Skriva. It replaces the text of a document with
// a Tiptap editor. The server makes the toolbar. This script connects the
// buttons to the commands of the editor.
//
// The script does not use inline styles or a style element, because the
// security policy of the page does not permit them. Alignment uses CSS
// classes. The server checks all data from the editor again.

import { Editor, Extension, InputRule, mergeAttributes } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { TaskItem, TaskList } from '@tiptap/extension-list'
import { Table, TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import Image from '@tiptap/extension-image'
import Heading from '@tiptap/extension-heading'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'

const ALIGNMENTS = ['center', 'right']
const ALIGN_TYPES = ['paragraph', 'heading']

// Align adds the textAlign attribute to paragraphs and headings. The
// attribute becomes a class, for example "align-center".
const Align = Extension.create({
  name: 'align',
  addGlobalAttributes() {
    return [{
      types: ALIGN_TYPES,
      attributes: {
        textAlign: {
          default: null,
          parseHTML: (el) =>
            ALIGNMENTS.find((a) => el.classList.contains('align-' + a)) ||
            (ALIGNMENTS.includes(el.style.textAlign) ? el.style.textAlign : null),
          renderHTML: (attrs) =>
            ALIGNMENTS.includes(attrs.textAlign) ? { class: 'align-' + attrs.textAlign } : {},
        },
      },
    }]
  },
  addCommands() {
    return {
      setAlign: (align) => ({ commands }) => {
        const value = ALIGNMENTS.includes(align) ? align : null
        return ALIGN_TYPES.map((t) => commands.updateAttributes(t, { textAlign: value })).some(Boolean)
      },
    }
  },
})

// SkrivaHeading shows a heading with the element of the document page.
// The title of the page is the h1 element. Thus heading level 1 is an
// h2 element and level 5 is h6. HTML has no h7 to h9 elements, thus
// levels 6 to 8 are paragraphs with the role "heading". The class
// "level-n" gives the style. The editor then uses the same styles as the
// page. See internal/doc/html.go.
const LEVELS = [1, 2, 3, 4, 5, 6, 7, 8]
const HTML_LEVELS = 5
// Pasted headings h1 to h6 become levels 1 to 5.
const HEADING_TAGS = { h1: 1, h2: 1, h3: 2, h4: 3, h5: 4, h6: 5 }
const SkrivaHeading = Heading.extend({
  parseHTML() {
    return [
      ...Object.entries(HEADING_TAGS).map(([tag, level]) => ({ tag, attrs: { level } })),
      {
        tag: 'p[role="heading"]',
        priority: 60,
        getAttrs: (el) => {
          const level = Number(el.getAttribute('aria-level')) - 1
          return LEVELS.includes(level) && level > HTML_LEVELS ? { level } : false
        },
      },
    ]
  },
  renderHTML({ node, HTMLAttributes }) {
    const level = LEVELS.includes(node.attrs.level) ? node.attrs.level : 1
    const attrs = { class: 'level-' + level }
    let tag = 'h' + (level + 1)
    if (level > HTML_LEVELS) {
      tag = 'p'
      attrs.role = 'heading'
      attrs['aria-level'] = String(level + 1)
    }
    return [tag, mergeAttributes(this.options.HTMLAttributes, HTMLAttributes, attrs), 0]
  },
}).configure({ levels: LEVELS })

// KeptSelection shows the selected text while the focus is in a field
// out of the editor, for example the link bar. The browser does not show
// the selection of an element that does not have the focus. The plugin
// adds the class "kept-selection" to the text. The meta value of the key
// sets the range ({ from, to }) or removes it (null).
const keptSelection = new PluginKey('keptSelection')
const KeptSelection = Extension.create({
  name: 'keptSelection',
  addProseMirrorPlugins() {
    return [new Plugin({
      key: keptSelection,
      state: {
        init: () => DecorationSet.empty,
        apply(tr, set) {
          const range = tr.getMeta(keptSelection)
          if (range === null) return DecorationSet.empty
          if (range) {
            return DecorationSet.create(tr.doc, [
              Decoration.inline(range.from, range.to, { class: 'kept-selection' }),
            ])
          }
          return set.map(tr.mapping, tr.doc)
        },
      },
      props: {
        decorations: (state) => keptSelection.getState(state),
      },
    })]
  },
})

// WikiLink changes the text [[Title]] to a link with the destination
// "wiki:Title". The server makes the URL of the document from the title.
const WikiLink = Extension.create({
  name: 'wikiLink',
  addInputRules() {
    return [new InputRule({
      find: /\[\[([^[\]]+)\]\]$/,
      handler: ({ state, range, match }) => {
        const target = match[1].trim()
        const label = target.split('#')[0].trim()
        if (!label) return null
        const link = state.schema.marks.link.create({ href: 'wiki:' + target })
        state.tr.replaceWith(range.from, range.to, state.schema.text(label, [link]))
        state.tr.removeStoredMark(state.schema.marks.link)
      },
    })]
  },
})

// The table does not show the widths of the columns, because the server
// does not keep them. The cells do not have the align attribute, because
// it becomes an inline style. The paragraphs in a cell can have an
// alignment.
const noAttr = { default: null, parseHTML: () => null, renderHTML: () => ({}) }
const SkrivaTable = Table.extend({
  renderHTML({ HTMLAttributes }) {
    const attrs = { ...HTMLAttributes }
    delete attrs.style
    return ['table', mergeAttributes(this.options.HTMLAttributes, attrs), ['tbody', 0]]
  },
})
const SkrivaCell = TableCell.extend({
  addAttributes() { return { ...this.parent?.(), align: noAttr, colwidth: noAttr } },
})
const SkrivaHeader = TableHeader.extend({
  addAttributes() { return { ...this.parent?.(), align: noAttr, colwidth: noAttr } },
})

// isAllowedHref has the same rules as the server: http, https and mailto
// URLs, wiki links, document links, paths on the same site and fragments.
function isAllowedHref(href) {
  if (!href || /[\u0000-\u001f\u007f\\]/.test(href)) return false
  if (href.startsWith('wiki:')) return href.slice(5).split('#')[0].trim() !== ''
  if (href.startsWith('doc:')) return /^doc:[0-9a-z]{14}(#.*)?$/.test(href)
  if (href.startsWith('#') || (href.startsWith('/') && !href.startsWith('//'))) return true
  try {
    const u = new URL(href)
    return ['http:', 'https:'].includes(u.protocol) ? u.host !== '' : u.protocol === 'mailto:'
  } catch {
    return false
  }
}

function start(root) {
  // texts are the translations of the texts of the script. The server
  // gives them in data-i18n. A text without a translation stays in
  // English. tr replaces each %s with the next argument.
  let texts = {}
  try {
    texts = JSON.parse(root.dataset.i18n || '{}')
  } catch {
    // Use the English texts.
  }
  const tr = (msg, ...args) => {
    let i = 0
    return (texts[msg] || msg).replace(/%s/g, () => String(args[i++] ?? ''))
  }
  const form = root.closest('form')
  const input = form.querySelector('[data-editor-body]')
  const toolbar = root.querySelector('[data-editor-toolbar]')
  const linkbar = root.querySelector('[data-editor-linkbar]')
  const hrefInput = root.querySelector('[data-editor-href]')
  const linkList = root.querySelector('[data-editor-linklist]')
  const linkNote = root.querySelector('[data-editor-linknote]')
  const fileInput = root.querySelector('[data-editor-file]')
  const status = root.querySelector('[data-editor-status]')
  const tableGroup = root.querySelector('[data-editor-group="table"]')
  const block = root.querySelector('[data-command="block"]')

  const say = (text, isError) => {
    status.textContent = text
    status.classList.toggle('error', !!isError)
  }

  let content
  try {
    content = JSON.parse(input.value)
  } catch {
    content = { type: 'doc' }
  }
  // A document must contain at least one block. Without it, the editor
  // selects all and the commands for blocks do not work.
  if (!Array.isArray(content.content) || content.content.length === 0) {
    content = { type: 'doc', content: [{ type: 'paragraph' }] }
  }

  const editor = new Editor({
    element: root.querySelector('[data-editor-text]'),
    content,
    injectCSS: false,
    extensions: [
      StarterKit.configure({
        heading: false,
        link: {
          openOnClick: false,
          autolink: true,
          defaultProtocol: 'https',
          protocols: ['wiki', 'doc'],
          isAllowedUri: (url) => isAllowedHref(url),
          HTMLAttributes: { rel: 'noopener noreferrer nofollow', target: null },
        },
      }),
      SkrivaHeading,
      Align,
      WikiLink,
      KeptSelection,
      TaskList,
      TaskItem.configure({ nested: true }),
      SkrivaTable.configure({ resizable: false }),
      TableRow,
      SkrivaHeader,
      SkrivaCell,
      Image.configure({ inline: false, allowBase64: false }),
    ],
    editorProps: {
      attributes: { 'aria-label': tr('Text'), 'aria-multiline': 'true', role: 'textbox', spellcheck: 'true' },
      handlePaste: (_view, event) => uploadFiles(event.clipboardData?.files),
      handleDrop: (view, event) => {
        const pos = view.posAtCoords({ left: event.clientX, top: event.clientY })
        return uploadFiles(event.dataTransfer?.files, pos?.pos)
      },
    },
  })

  // Uploads: the server keeps the file and gives its URL.
  async function upload(file) {
    const data = new FormData()
    data.append('csrf_token', root.dataset.csrf)
    data.append('file', file)
    const resp = await fetch(root.dataset.upload, { method: 'POST', body: data, credentials: 'same-origin' })
    if (!resp.ok) {
      throw new Error((await resp.text()).trim() || tr('The upload failed.'))
    }
    return (await resp.json()).url
  }

  // altText makes a short description from the name of a file.
  // A draw.io diagram has two extensions, for example "flow.drawio.svg".
  const altText = (file) => file.name.replace(/(\.drawio)?\.[a-z0-9]+$/i, '').replace(/[-_]+/g, ' ').trim()

  async function insertImages(files, pos) {
    for (const file of files) {
      say(tr('Uploading %s…', file.name))
      try {
        const src = await upload(file)
        const node = { type: 'image', attrs: { src, alt: altText(file) } }
        const chain = editor.chain().focus()
        ;(pos === undefined ? chain.insertContent(node) : chain.insertContentAt(pos, node))
        // The new image is selected. Without a new paragraph, the next
        // text or table replaces the image.
        chain.createParagraphNear().run()
        say('')
      } catch (err) {
        say(err.message, true)
        return
      }
    }
  }

  // uploadFiles uploads the images in a paste or a drop. It returns true
  // if the event contains images.
  function uploadFiles(fileList, pos) {
    const files = Array.from(fileList || []).filter((f) => f.type.startsWith('image/'))
    if (files.length === 0) return false
    insertImages(files, pos)
    return true
  }

  // The link bar.
  const keepSelection = (range) => editor.view.dispatch(editor.state.tr.setMeta(keptSelection, range))

  // The list of pages in the link bar. Text that is not an address
  // finds the pages with that text in the title. A selected page gives a
  // "doc:" link. This link stays correct when the page gets a different
  // title or moves.
  let targets = []
  let active = -1
  let chosenTitle = ''
  let searchTimer = 0
  let searchSeq = 0
  const looksLikeAddress = (v) => /^[a-z][a-z0-9+.-]*:/i.test(v) || v.startsWith('/') || v.startsWith('#')

  async function fetchTargets(params) {
    const resp = await fetch(root.dataset.links + '?' + new URLSearchParams(params), { credentials: 'same-origin' })
    if (!resp.ok) throw new Error(tr('The search failed.'))
    return resp.json()
  }

  function showTargets(list) {
    targets = list
    active = list.length > 0 ? 0 : -1
    linkList.replaceChildren(...list.map((t, i) => {
      const li = document.createElement('li')
      li.id = 'editor-link-option-' + i
      li.setAttribute('role', 'option')
      li.dataset.index = String(i)
      li.textContent = t.title
      if (t.parent) {
        const small = document.createElement('small')
        small.textContent = tr('in %s', t.parent)
        li.append(' ', small)
      }
      return li
    }))
    const open = list.length > 0
    linkList.hidden = !open
    hrefInput.setAttribute('aria-expanded', String(open))
    markActive()
  }

  function markActive() {
    for (const li of linkList.children) {
      li.setAttribute('aria-selected', String(Number(li.dataset.index) === active))
    }
    if (active >= 0) {
      hrefInput.setAttribute('aria-activedescendant', 'editor-link-option-' + active)
      linkList.children[active].scrollIntoView({ block: 'nearest' })
    } else {
      hrefInput.removeAttribute('aria-activedescendant')
    }
  }

  function chooseTarget(i) {
    const t = targets[i]
    if (!t) return
    hrefInput.value = 'doc:' + t.id
    chosenTitle = t.title
    linkNote.textContent = tr('Links to the page "%s".', t.title)
    showTargets([])
    hrefInput.focus()
  }

  // describeHref shows the title of the page of a "doc:" link.
  async function describeHref(href) {
    const m = /^doc:([0-9a-z]{14})/.exec(href)
    linkNote.textContent = ''
    if (!m) return
    try {
      const [t] = await fetchTargets({ id: m[1] })
      if (hrefInput.value === href) {
        linkNote.textContent = t ? tr('Links to the page "%s".', t.title) : tr('The page does not exist, or you cannot read it.')
      }
    } catch {
      // The note is only information.
    }
  }

  hrefInput.addEventListener('input', () => {
    const v = hrefInput.value.trim()
    chosenTitle = ''
    linkNote.textContent = ''
    clearTimeout(searchTimer)
    if (v === '' || looksLikeAddress(v)) {
      showTargets([])
      describeHref(v)
      return
    }
    const seq = ++searchSeq
    searchTimer = setTimeout(async () => {
      try {
        const list = await fetchTargets({ q: v })
        if (seq === searchSeq) showTargets(list)
      } catch (err) {
        say(err.message, true)
      }
    }, 150)
  })

  // A mouse down on an option does not take the focus from the input.
  linkList.addEventListener('mousedown', (event) => event.preventDefault())
  linkList.addEventListener('click', (event) => {
    const li = event.target.closest('[role="option"]')
    if (li) chooseTarget(Number(li.dataset.index))
  })

  function openLinkbar() {
    // In a link, with no selection, the link bar changes all of the link.
    if (editor.state.selection.empty && editor.isActive('link')) {
      editor.commands.extendMarkRange('link')
    }
    const { from, to, empty } = editor.state.selection
    keepSelection(empty ? null : { from, to })
    linkbar.hidden = false
    hrefInput.value = editor.getAttributes('link').href || ''
    chosenTitle = ''
    showTargets([])
    describeHref(hrefInput.value)
    hrefInput.focus()
    hrefInput.select()
  }
  function closeLinkbar() {
    linkbar.hidden = true
    showTargets([])
    linkNote.textContent = ''
    keepSelection(null)
    editor.commands.focus()
  }
  function applyLink() {
    const href = hrefInput.value.trim()
    if (href === '') {
      editor.chain().focus().extendMarkRange('link').unsetLink().run()
      closeLinkbar()
      return
    }
    if (!isAllowedHref(href)) {
      say(tr('Select a page in the list, or type an address: https://…, mailto:…, wiki:Title or a path that starts with /.'), true)
      return
    }
    say('')
    const chain = editor.chain().focus().extendMarkRange('link')
    if (editor.state.selection.empty && !editor.isActive('link')) {
      const text = chosenTitle || href.replace(/^wiki:/, '')
      chain.insertContent({ type: 'text', text, marks: [{ type: 'link', attrs: { href } }] })
    } else {
      chain.setLink({ href })
    }
    chain.run()
    closeLinkbar()
  }

  const commands = {
    undo: (c) => c.undo(),
    redo: (c) => c.redo(),
    bold: (c) => c.toggleBold(),
    italic: (c) => c.toggleItalic(),
    underline: (c) => c.toggleUnderline(),
    strike: (c) => c.toggleStrike(),
    code: (c) => c.toggleCode(),
    alignLeft: (c) => c.setAlign(null),
    alignCenter: (c) => c.setAlign('center'),
    alignRight: (c) => c.setAlign('right'),
    bulletList: (c) => c.toggleBulletList(),
    orderedList: (c) => c.toggleOrderedList(),
    taskList: (c) => c.toggleTaskList(),
    blockquote: (c) => c.toggleBlockquote(),
    codeBlock: (c) => c.toggleCodeBlock(),
    horizontalRule: (c) => c.setHorizontalRule(),
    table: (c) => c.insertTable({ rows: 3, cols: 3, withHeaderRow: true }),
    // A Mermaid diagram is a code block with the language "mermaid". The
    // server makes the image when it shows the page.
    diagram: (c) => c.insertContent({
      type: 'codeBlock',
      attrs: { language: 'mermaid' },
      content: [{ type: 'text', text: 'flowchart LR\n  A[Start] --> B[End]' }],
    }),
    addRow: (c) => c.addRowAfter(),
    deleteRow: (c) => c.deleteRow(),
    addColumn: (c) => c.addColumnAfter(),
    deleteColumn: (c) => c.deleteColumn(),
    mergeCells: (c) => c.mergeOrSplit(),
    headerRow: (c) => c.toggleHeaderRow(),
    deleteTable: (c) => c.deleteTable(),
  }

  // The state of the buttons: pressed for the active formats, disabled
  // for commands that are not possible at the cursor.
  const pressed = {
    bold: () => editor.isActive('bold'),
    italic: () => editor.isActive('italic'),
    underline: () => editor.isActive('underline'),
    strike: () => editor.isActive('strike'),
    code: () => editor.isActive('code'),
    alignLeft: () => !editor.isActive({ textAlign: 'center' }) && !editor.isActive({ textAlign: 'right' }),
    alignCenter: () => editor.isActive({ textAlign: 'center' }),
    alignRight: () => editor.isActive({ textAlign: 'right' }),
    bulletList: () => editor.isActive('bulletList'),
    orderedList: () => editor.isActive('orderedList'),
    taskList: () => editor.isActive('taskList'),
    blockquote: () => editor.isActive('blockquote'),
    codeBlock: () => editor.isActive('codeBlock'),
    diagram: () => editor.isActive('codeBlock', { language: 'mermaid' }),
    link: () => editor.isActive('link'),
  }

  // The commands that do not apply in a Mermaid diagram. ProseMirror can
  // put a code block in a quote, or a line, an image or a table in a code
  // block, but the text of the diagram must stay in one code block. A
  // code block cannot contain links.
  const notInDiagram = new Set(['blockquote', 'horizontalRule', 'link', 'image', 'table'])

  function update() {
    const inDiagram = editor.isActive('codeBlock', { language: 'mermaid' })
    for (const btn of toolbar.querySelectorAll('button[data-command]')) {
      const name = btn.dataset.command
      if (pressed[name]) btn.setAttribute('aria-pressed', String(pressed[name]()))
      const blocked = inDiagram && notInDiagram.has(name)
      if (commands[name]) {
        btn.disabled = blocked || !commands[name](editor.can().chain().focus()).run()
      } else if (notInDiagram.has(name)) {
        // Link and Image open their own dialogs, thus they have no entry
        // in commands.
        btn.disabled = blocked
      }
    }
    tableGroup.hidden = !editor.isActive('table')
    const level = LEVELS.find((l) => editor.isActive('heading', { level: l }))
    block.value = level ? String(level) : 'paragraph'
  }

  root.addEventListener('click', (event) => {
    const btn = event.target.closest('button[data-command]')
    if (!btn) return
    const name = btn.dataset.command
    if (commands[name]) {
      commands[name](editor.chain().focus()).run()
    } else if (name === 'link') {
      openLinkbar()
    } else if (name === 'linkApply') {
      applyLink()
    } else if (name === 'linkRemove') {
      editor.chain().focus().extendMarkRange('link').unsetLink().run()
      closeLinkbar()
    } else if (name === 'linkCancel') {
      closeLinkbar()
    } else if (name === 'image') {
      fileInput.click()
    }
  })

  block.addEventListener('change', () => {
    const chain = editor.chain().focus()
    if (block.value === 'paragraph') chain.setParagraph()
    else chain.setHeading({ level: Number(block.value) })
    chain.run()
  })

  fileInput.addEventListener('change', () => {
    insertImages(Array.from(fileInput.files))
    fileInput.value = ''
  })

  // Enter in the link bar selects the marked page in the list, or applies
  // the link. It does not send the form. The arrow keys move in the list.
  // Escape closes the list, and then the link bar.
  hrefInput.addEventListener('keydown', (event) => {
    const open = !linkList.hidden
    if (event.key === 'ArrowDown' && open) {
      event.preventDefault()
      active = (active + 1) % targets.length
      markActive()
    } else if (event.key === 'ArrowUp' && open) {
      event.preventDefault()
      active = (active - 1 + targets.length) % targets.length
      markActive()
    } else if (event.key === 'Enter') {
      event.preventDefault()
      if (open && active >= 0) chooseTarget(active)
      else applyLink()
    } else if (event.key === 'Escape') {
      event.preventDefault()
      if (open) showTargets([])
      else closeLinkbar()
    }
  })

  editor.view.dom.addEventListener('keydown', (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
      event.preventDefault()
      openLinkbar()
    }
  })

  // The browser asks before it leaves a page with changes that are not
  // saved.
  let dirty = false
  let saving = false
  editor.on('update', () => { dirty = true })
  // When the user goes back to the text, the browser shows the selection
  // again.
  editor.on('focus', () => {
    if (keptSelection.getState(editor.state) !== DecorationSet.empty) {
      queueMicrotask(() => keepSelection(null))
    }
  })
  editor.on('transaction', update)
  window.addEventListener('beforeunload', (event) => {
    if (dirty && !saving) event.preventDefault()
  })

  // The bar below the toolbar shows the selected classification.
  const classSelect = form.querySelector('select[name="classification"]')
  const classLevel = document.querySelector('[data-classbar-level]')
  if (classSelect && classLevel) {
    classSelect.addEventListener('change', () => {
      classLevel.textContent = classSelect.selectedOptions[0].textContent.toUpperCase()
      dirty = true
    })
  }

  form.addEventListener('submit', () => {
    saving = true
    input.value = JSON.stringify(editor.getJSON())
  })

  toolbar.hidden = false
  update()
}

for (const root of document.querySelectorAll('.wysiwyg')) start(root)
