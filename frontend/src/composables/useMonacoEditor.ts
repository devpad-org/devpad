import { ref, shallowRef, reactive, computed, onBeforeUnmount, watch, type Ref } from 'vue'
import * as monaco from 'monaco-editor'

// Register Devpad dark theme once
let themeRegistered = false

function registerDevpadTheme() {
  if (themeRegistered) return
  themeRegistered = true

  monaco.editor.defineTheme('devpad-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'comment', foreground: '6a737d', fontStyle: 'italic' },
      { token: 'keyword', foreground: '7c3aed' },
      { token: 'string', foreground: '10b981' },
      { token: 'number', foreground: 'f59e0b' },
      { token: 'type', foreground: '00d4ff' },
      { token: 'type.identifier', foreground: '00d4ff' },
      { token: 'function', foreground: '60a5fa' },
      { token: 'variable', foreground: 'e4e4e7' },
      { token: 'constant', foreground: 'f59e0b' },
      { token: 'tag', foreground: 'f43f5e' },
      { token: 'attribute.name', foreground: '00d4ff' },
      { token: 'attribute.value', foreground: '10b981' },
      { token: 'delimiter', foreground: 'a1a1aa' },
      { token: 'operator', foreground: 'a1a1aa' },
    ],
    colors: {
      'editor.background': '#0e1117',
      'editor.foreground': '#e4e4e7',
      'editor.lineHighlightBackground': '#1a1f2e',
      'editor.selectionBackground': '#7c3aed40',
      'editor.inactiveSelectionBackground': '#7c3aed20',
      'editorLineNumber.foreground': '#71717a',
      'editorLineNumber.activeForeground': '#a1a1aa',
      'editorCursor.foreground': '#00d4ff',
      'editorIndentGuide.background': '#ffffff10',
      'editorIndentGuide.activeBackground': '#ffffff20',
      'editor.selectionHighlightBackground': '#00d4ff15',
      'editorBracketMatch.background': '#00d4ff20',
      'editorBracketMatch.border': '#00d4ff40',
      'editorGutter.background': '#0e1117',
      'minimap.background': '#0e1117',
      'minimapSlider.background': '#ffffff10',
      'minimapSlider.hoverBackground': '#ffffff18',
      'minimapSlider.activeBackground': '#ffffff25',
      'scrollbar.shadow': '#00000000',
      'scrollbarSlider.background': '#ffffff16',
      'scrollbarSlider.hoverBackground': '#ffffff25',
      'scrollbarSlider.activeBackground': '#ffffff30',
    },
  })
}

/** Map file extensions to Monaco language IDs */
function getLanguageFromPath(filePath: string): string {
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
  const editorInstance = shallowRef<monaco.editor.IStandaloneCodeEditor | null>(null)
  const isDirty = ref(false)

  // Per-file models and view states
  const models = new Map<string, monaco.editor.ITextModel>()
  const viewStates = new Map<string, monaco.editor.ICodeEditorViewState | null>()
  const dirtyFiles = reactive(new Set<string>())
  let activeFilePath: string | null = null

  registerDevpadTheme()

  function createEditor() {
    if (!container.value || editorInstance.value) return

    editorInstance.value = monaco.editor.create(container.value, {
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
      fontSize: 13,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace",
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
  }

  /** Open or update a file's model and switch the editor to it */
  function setContent(content: string, filePath: string) {
    if (!editorInstance.value) return

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
