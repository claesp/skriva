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
// URLs, wiki links, paths on the same site and fragments.
function isAllowedHref(href) {
  if (!href || /[\u0000-\u001f\u007f\\]/.test(href)) return false
  if (href.startsWith('wiki:')) return href.slice(5).split('#')[0].trim() !== ''
  if (href.startsWith('#') || (href.startsWith('/') && !href.startsWith('//'))) return true
  try {
    const u = new URL(href)
    return ['http:', 'https:'].includes(u.protocol) ? u.host !== '' : u.protocol === 'mailto:'
  } catch {
    return false
  }
}

function start(root) {
  const form = root.closest('form')
  const input = form.querySelector('[data-editor-body]')
  const toolbar = root.querySelector('[data-editor-toolbar]')
  const linkbar = root.querySelector('[data-editor-linkbar]')
  const hrefInput = root.querySelector('[data-editor-href]')
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
        heading: { levels: [1, 2, 3] },
        link: {
          openOnClick: false,
          autolink: true,
          defaultProtocol: 'https',
          protocols: ['wiki'],
          isAllowedUri: (url) => isAllowedHref(url),
          HTMLAttributes: { rel: 'noopener noreferrer nofollow', target: null },
        },
      }),
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
      attributes: { 'aria-label': 'Text', 'aria-multiline': 'true', role: 'textbox', spellcheck: 'true' },
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
      throw new Error((await resp.text()).trim() || 'The upload failed.')
    }
    return (await resp.json()).url
  }

  // altText makes a short description from the name of a file.
  const altText = (file) => file.name.replace(/\.[a-z0-9]+$/i, '').replace(/[-_]+/g, ' ').trim()

  async function insertImages(files, pos) {
    for (const file of files) {
      say('Uploading ' + file.name + '…')
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
  function openLinkbar() {
    // In a link, with no selection, the link bar changes all of the link.
    if (editor.state.selection.empty && editor.isActive('link')) {
      editor.commands.extendMarkRange('link')
    }
    const { from, to, empty } = editor.state.selection
    keepSelection(empty ? null : { from, to })
    linkbar.hidden = false
    hrefInput.value = editor.getAttributes('link').href || ''
    hrefInput.focus()
    hrefInput.select()
  }
  function closeLinkbar() {
    linkbar.hidden = true
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
      say('The link address is not valid. Use https://…, mailto:…, wiki:Title or a path that starts with /.', true)
      return
    }
    say('')
    const chain = editor.chain().focus().extendMarkRange('link')
    if (editor.state.selection.empty && !editor.isActive('link')) {
      chain.insertContent({ type: 'text', text: href.replace(/^wiki:/, ''), marks: [{ type: 'link', attrs: { href } }] })
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
    link: () => editor.isActive('link'),
  }

  function update() {
    for (const btn of toolbar.querySelectorAll('button[data-command]')) {
      const name = btn.dataset.command
      if (pressed[name]) btn.setAttribute('aria-pressed', String(pressed[name]()))
      if (commands[name]) btn.disabled = !commands[name](editor.can().chain().focus()).run()
    }
    tableGroup.hidden = !editor.isActive('table')
    const level = [1, 2, 3].find((l) => editor.isActive('heading', { level: l }))
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

  // Enter in the link bar applies the link. It does not send the form.
  hrefInput.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      applyLink()
    } else if (event.key === 'Escape') {
      event.preventDefault()
      closeLinkbar()
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

  form.addEventListener('submit', () => {
    saving = true
    input.value = JSON.stringify(editor.getJSON())
  })

  toolbar.hidden = false
  update()
}

for (const root of document.querySelectorAll('.wysiwyg')) start(root)
