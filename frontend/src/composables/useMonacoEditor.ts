import { ref, shallowRef, reactive, computed, onBeforeUnmount, watch, type Ref } from 'vue'
import type * as MonacoType from 'monaco-editor'

let monaco: typeof MonacoType | null = null
let monacoPromise: Promise<typeof MonacoType> | null = null

export async function loadMonaco(): Promise<typeof MonacoType> {
  if (monaco) return monaco
  if (!monacoPromise) {
    monacoPromise = import('monaco-editor').then((m) => {
      monaco = m
      return m
    })
  }
  return monacoPromise
}

// Register Devpad dark theme once
let themeRegistered = false

export function registerDevpadTheme(m: typeof MonacoType) {
  if (themeRegistered) return
  themeRegistered = true

  m.editor.defineTheme('devpad-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      // Devpad brand palette — electric blue, violet, emerald, amber, rose
      { token: 'comment',              foreground: '4a5568', fontStyle: 'italic' },
      { token: 'comment.doc',          foreground: '5a6a80', fontStyle: 'italic' },
      { token: 'keyword',              foreground: 'a78bfa' },          // violet
      { token: 'keyword.control',      foreground: 'c084fc' },          // brighter violet
      { token: 'keyword.operator',     foreground: '94a3b8' },          // slate
      { token: 'storage',              foreground: 'a78bfa' },          // violet
      { token: 'storage.type',         foreground: 'a78bfa' },
      { token: 'string',               foreground: '34d399' },          // emerald
      { token: 'string.escape',        foreground: '10b981', fontStyle: 'bold' },
      { token: 'string.template',      foreground: '34d399' },
      { token: 'number',               foreground: 'fbbf24' },          // amber
      { token: 'number.float',         foreground: 'fbbf24' },
      { token: 'number.hex',           foreground: 'f59e0b' },
      { token: 'type',                 foreground: 'c084fc' },          // soft purple
      { token: 'type.identifier',      foreground: 'c084fc' },
      { token: 'class',                foreground: '60cdff' },          // electric blue
      { token: 'class.name',           foreground: '60cdff' },
      { token: 'interface',            foreground: '60cdff' },
      { token: 'function',             foreground: '38bdf8' },          // sky blue
      { token: 'function.call',        foreground: '7dd3fc' },
      { token: 'method',               foreground: '38bdf8' },
      { token: 'method.call',          foreground: '7dd3fc' },
      { token: 'variable',             foreground: 'e2e8f0' },          // near-white
      { token: 'variable.name',        foreground: 'e2e8f0' },
      { token: 'variable.parameter',   foreground: 'f1a8b8' },          // rose-tinted
      { token: 'variable.language',    foreground: 'fb923c' },          // orange (this, self)
      { token: 'constant',             foreground: 'fb923c' },          // orange
      { token: 'constant.language',    foreground: 'f87171' },          // rose (true/false/null)
      { token: 'constant.numeric',     foreground: 'fbbf24' },
      { token: 'tag',                  foreground: '60cdff' },          // electric blue
      { token: 'tag.id',               foreground: '38bdf8' },
      { token: 'tag.class',            foreground: '7dd3fc' },
      { token: 'metatag',              foreground: 'a78bfa' },
      { token: 'attribute.name',       foreground: 'a78bfa' },          // violet
      { token: 'attribute.value',      foreground: '34d399' },          // emerald
      { token: 'delimiter',            foreground: '64748b' },          // visible slate
      { token: 'delimiter.bracket',    foreground: '7dd3fc' },          // blue brackets
      { token: 'delimiter.curly',      foreground: 'a78bfa' },          // purple braces
      { token: 'delimiter.parenthesis',foreground: '94a3b8' },
      { token: 'operator',             foreground: '94a3b8' },          // slate
      { token: 'operator.assignment',  foreground: '60cdff' },
      { token: 'identifier',           foreground: 'e2e8f0' },
      { token: 'namespace',            foreground: '60cdff' },
      { token: 'decorator',            foreground: 'fb923c' },          // orange
      { token: 'annotation',           foreground: 'fb923c' },
      { token: 'regexp',               foreground: 'f87171' },          // rose
      { token: 'invalid',              foreground: 'f43f5e', fontStyle: 'underline' },
    ],
    colors: {
      'editor.background': '#08090c',
      'editor.foreground': '#e8eaed',
      'editor.lineHighlightBackground': '#4dd0e108',
      'editor.selectionBackground': '#4dd0e120',
      'editor.inactiveSelectionBackground': '#4dd0e10c',
      'editorLineNumber.foreground': '#40454e',
      'editorLineNumber.activeForeground': '#5f646e',
      'editorCursor.foreground': '#4dd0e1',
      'editorIndentGuide.background': '#ffffff08',
      'editorIndentGuide.activeBackground': '#ffffff12',
      'editor.selectionHighlightBackground': '#4dd0e112',
      'editorBracketMatch.background': '#4dd0e115',
      'editorBracketMatch.border': '#4dd0e130',
      'editorGutter.background': '#08090c',
      'minimap.background': '#08090c',
      'minimapGutter.background': '#08090c',
      'minimapSlider.background': '#ffffff08',
      'minimapSlider.hoverBackground': '#ffffff12',
      'minimapSlider.activeBackground': '#ffffff1e',
      'scrollbar.shadow': '#00000000',
      'scrollbarSlider.background': '#ffffff10',
      'scrollbarSlider.hoverBackground': '#ffffff1a',
      'scrollbarSlider.activeBackground': '#ffffff25',
    },
  })
}

/** Map file extensions to Monaco language IDs */
export function getLanguageFromPath(filePath: string): string {
  const ext = filePath.split('.').pop()?.toLowerCase() ?? ''
  const map: Record<string, string> = {
    ts: 'typescript',
    tsx: 'typescript',
    js: 'javascript',
    jsx: 'javascript',
    json: 'json',
    html: 'html',
    htm: 'html',
    css: 'css',
    scss: 'scss',
    less: 'less',
    vue: 'html',
    md: 'markdown',
    yaml: 'yaml',
    yml: 'yaml',
    xml: 'xml',
    sql: 'sql',
    py: 'python',
    go: 'go',
    rs: 'rust',
    java: 'java',
    c: 'c',
    cpp: 'cpp',
    h: 'c',
    hpp: 'cpp',
    cs: 'csharp',
    rb: 'ruby',
    php: 'php',
    sh: 'shell',
    bash: 'shell',
    zsh: 'shell',
    dockerfile: 'dockerfile',
    toml: 'ini',
    ini: 'ini',
    env: 'ini',
    makefile: 'plaintext',
    txt: 'plaintext',
    log: 'plaintext',
    svg: 'xml',
  }

  // Handle Dockerfile, Makefile etc.
  const name = filePath.split('/').pop()?.toLowerCase() ?? ''
  if (name === 'dockerfile') return 'dockerfile'
  if (name === 'makefile') return 'plaintext'
  if (name === '.gitignore' || name === '.dockerignore') return 'plaintext'
  if (name === 'go.mod' || name === 'go.sum') return 'go'

  return map[ext] ?? 'plaintext'
}

export function useMonacoEditor(
  container: Ref<HTMLElement | null>,
  options?: {
    readOnly?: boolean
  },
) {
  const editorInstance = shallowRef<MonacoType.editor.IStandaloneCodeEditor | null>(null)
  const isDirty = ref(false)
  const isLoading = ref(true)

  // Per-file models and view states
  const models = new Map<string, MonacoType.editor.ITextModel>()
  const viewStates = new Map<string, MonacoType.editor.ICodeEditorViewState | null>()
  const dirtyFiles = reactive(new Set<string>())
  let activeFilePath: string | null = null

  let editorReady: Promise<void> | null = null
  let resolveEditorReady: (() => void) | null = null

  function initEditorReadyPromise() {
    editorReady = new Promise<void>((resolve) => {
      resolveEditorReady = resolve
    })
  }
  initEditorReadyPromise()

  async function createEditor() {
    if (!container.value || editorInstance.value) return

    const m = await loadMonaco()
    registerDevpadTheme(m)

    if (!container.value) return // container may have been removed during await
    isLoading.value = false

    editorInstance.value = m.editor.create(container.value, {
      theme: 'devpad-dark',
      readOnly: options?.readOnly ?? false,
      automaticLayout: true,
      minimap: {
        enabled: true,
        scale: 1,
        showSlider: 'mouseover',
        renderCharacters: false,
        maxColumn: 80,
      },
      lineNumbers: 'on',
      fontSize: 14,
      fontFamily: "'Geist Mono', 'JetBrains Mono', Menlo, monospace",
      fontLigatures: true,
      tabSize: 2,
      insertSpaces: true,
      renderWhitespace: 'selection',
      smoothScrolling: true,
      cursorBlinking: 'smooth',
      cursorSmoothCaretAnimation: 'on',
      padding: { top: 8, bottom: 8 },
      scrollBeyondLastLine: false,
      bracketPairColorization: { enabled: true },
      guides: {
        bracketPairs: true,
        indentation: true,
      },
      overviewRulerLanes: 0,
      hideCursorInOverviewRuler: true,
      renderLineHighlight: 'line',
      contextmenu: true,
      wordWrap: 'off',
      scrollbar: {
        verticalScrollbarSize: 8,
        horizontalScrollbarSize: 8,
        useShadows: false,
      },
    })

    editorInstance.value.onDidChangeModelContent(() => {
      if (activeFilePath) {
        dirtyFiles.add(activeFilePath)
      }
      isDirty.value = true
    })

    resolveEditorReady?.()
  }

  /** Open or update a file's model and switch the editor to it */
  async function setContent(content: string, filePath: string) {
    await editorReady
    if (!editorInstance.value || !monaco) return

    // Save view state of the previous file
    if (activeFilePath && activeFilePath !== filePath) {
      viewStates.set(activeFilePath, editorInstance.value.saveViewState())
    }

    const language = getLanguageFromPath(filePath)
    let model = models.get(filePath)

    if (!model) {
      const normalizedPath = filePath.startsWith('/') ? filePath : `/${filePath}`
      const uri = monaco.Uri.from({ scheme: 'devpad', path: normalizedPath })
      model = monaco.editor.createModel(content, language, uri)
      models.set(filePath, model)
    } else {
      // Update content only if it differs (avoids resetting undo stack on re-open)
      if (model.getValue() !== content) {
        model.setValue(content)
      }
      monaco.editor.setModelLanguage(model, language)
    }

    editorInstance.value.setModel(model)

    // Restore view state if we had one
    const savedState = viewStates.get(filePath)
    if (savedState) {
      editorInstance.value.restoreViewState(savedState)
    }

    activeFilePath = filePath
    isDirty.value = dirtyFiles.has(filePath)
  }

  /** Switch to an already-open file without reloading content */
  function switchToFile(filePath: string) {
    if (!editorInstance.value) return
    const model = models.get(filePath)
    if (!model) return

    // Save current view state
    if (activeFilePath && activeFilePath !== filePath) {
      viewStates.set(activeFilePath, editorInstance.value.saveViewState())
    }

    editorInstance.value.setModel(model)

    const savedState = viewStates.get(filePath)
    if (savedState) {
      editorInstance.value.restoreViewState(savedState)
    }

    activeFilePath = filePath
    isDirty.value = dirtyFiles.has(filePath)
  }

  /** Close a file's model and clean up */
  function closeFile(filePath: string) {
    const model = models.get(filePath)
    if (model) {
      model.dispose()
      models.delete(filePath)
    }
    viewStates.delete(filePath)
    dirtyFiles.delete(filePath)

    if (activeFilePath === filePath) {
      activeFilePath = null
      isDirty.value = false
    }
  }

  function isFileDirty(filePath: string): boolean {
    return dirtyFiles.has(filePath)
  }

  function markClean(filePath: string) {
    dirtyFiles.delete(filePath)
    if (activeFilePath === filePath) {
      isDirty.value = false
    }
  }

  function getContent(): string {
    return editorInstance.value?.getValue() ?? ''
  }

  function getFileContent(filePath: string): string {
    const model = models.get(filePath)
    return model?.getValue() ?? ''
  }

  function getDirtyFiles(): string[] {
    return [...dirtyFiles]
  }

  const hasDirtyFiles = computed(() => dirtyFiles.size > 0)

  function dispose() {
    models.forEach((m) => m.dispose())
    models.clear()
    viewStates.clear()
    dirtyFiles.clear()
    editorInstance.value?.dispose()
    editorInstance.value = null
  }

  // Create editor when container is available
  watch(container, (el) => {
    if (el && !editorInstance.value) {
      createEditor()
    }
  })

  onBeforeUnmount(() => {
    dispose()
  })

  return {
    editor: editorInstance,
    isDirty,
    isLoading,
    hasDirtyFiles,
    createEditor,
    setContent,
    switchToFile,
    closeFile,
    isFileDirty,
    markClean,
    getContent,
    getFileContent,
    getDirtyFiles,
    dispose,
  }
}
