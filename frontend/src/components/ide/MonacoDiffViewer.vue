<script setup lang="ts">
import { onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import type * as MonacoType from 'monaco-editor'
import {
  DEVPAD_MONACO_THEME,
  getLanguageFromPath,
  loadMonaco,
  registerDevpadTheme,
} from '@/composables/useMonacoEditor'

const props = defineProps<{
  oldContent: string
  newContent: string
  oldFileName: string
  newFileName: string
  sideBySide?: boolean
  // Lines of the new version to mark as carrying comments.
  markedLines?: number[]
  // Scrolls the new version to a line; pass a new object to reveal again.
  reveal?: { line: number } | null
  // Shows a pointer over the new version's line numbers, which emit lineClick.
  commentable?: boolean
}>()

const emit = defineEmits<{ lineClick: [line: number] }>()

const container = ref<HTMLElement | null>(null)
const editor = shallowRef<MonacoType.editor.IStandaloneDiffEditor | null>(null)
let monacoApi: typeof MonacoType | null = null
let marks: MonacoType.editor.IEditorDecorationsCollection | null = null
let originalModel: MonacoType.editor.ITextModel | null = null
let modifiedModel: MonacoType.editor.ITextModel | null = null
let renderVersion = 0
let pendingReveal: number | null = null

async function renderDiff() {
  const el = container.value
  if (!el) return

  const version = ++renderVersion
  const monaco = await loadMonaco()
  if (version !== renderVersion || !container.value) return
  monacoApi = monaco

  registerDevpadTheme(monaco)

  if (!editor.value) {
    editor.value = monaco.editor.createDiffEditor(el, {
      theme: DEVPAD_MONACO_THEME,
      readOnly: true,
      automaticLayout: true,
      renderSideBySide: props.sideBySide ?? true,
      useInlineViewWhenSpaceIsLimited: false,
      minimap: { enabled: false },
      lineNumbers: 'on',
      fontSize: 14,
      fontFamily: "'Geist Mono', 'JetBrains Mono', Menlo, monospace",
      fontLigatures: true,
      scrollBeyondLastLine: false,
      smoothScrolling: true,
      renderWhitespace: 'selection',
      overviewRulerLanes: 0,
      hideCursorInOverviewRuler: true,
      scrollbar: {
        verticalScrollbarSize: 8,
        horizontalScrollbarSize: 8,
        useShadows: false,
      },
    })
    const modified = editor.value.getModifiedEditor()
    marks = modified.createDecorationsCollection()
    modified.onMouseDown((e) => {
      const gutter =
        e.target.type === monaco.editor.MouseTargetType.GUTTER_LINE_NUMBERS ||
        e.target.type === monaco.editor.MouseTargetType.GUTTER_LINE_DECORATIONS
      if (gutter && e.target.position) emit('lineClick', e.target.position.lineNumber)
    })
    // The diff is computed after the models are set; reveal once it lands.
    editor.value.onDidUpdateDiff(() => {
      if (pendingReveal !== null) revealLine(pendingReveal)
      pendingReveal = null
    })
  }

  // The editor must let go of the old models before they are disposed.
  const previousOriginal = originalModel
  const previousModified = modifiedModel

  const language = getLanguageFromPath(props.newFileName || props.oldFileName)
  originalModel = monaco.editor.createModel(props.oldContent, language)
  modifiedModel = monaco.editor.createModel(props.newContent, language)
  editor.value.updateOptions({
    renderSideBySide: props.sideBySide ?? true,
    useInlineViewWhenSpaceIsLimited: false,
  })
  editor.value.setModel({
    original: originalModel,
    modified: modifiedModel,
  })
  previousOriginal?.dispose()
  previousModified?.dispose()
  applyMarks()
  pendingReveal = props.reveal?.line ?? null
}

function applyMarks() {
  const monaco = monacoApi
  if (!monaco || !marks || !modifiedModel) return
  const lineCount = modifiedModel.getLineCount()
  marks.set(
    (props.markedLines ?? [])
      .filter((line) => line >= 1 && line <= lineCount)
      .map((line) => ({
        range: new monaco.Range(line, 1, line, 1),
        options: {
          isWholeLine: true,
          className: 'devpad-diff-marked-line',
          linesDecorationsClassName: 'devpad-diff-marked-gutter',
        },
      })),
  )
}

function revealLine(line: number) {
  const modified = editor.value?.getModifiedEditor()
  if (!modified || !modifiedModel || line > modifiedModel.getLineCount()) return
  modified.revealLineInCenter(line)
  modified.setPosition({ lineNumber: line, column: 1 })
}

watch(
  () => [
    container.value,
    props.oldContent,
    props.newContent,
    props.oldFileName,
    props.newFileName,
    props.sideBySide,
  ] as const,
  () => {
    void renderDiff()
  },
  { immediate: true }
)

watch(() => props.markedLines, applyMarks)

watch(
  () => props.reveal,
  (reveal) => {
    if (reveal) revealLine(reveal.line)
  },
)

onBeforeUnmount(() => {
  renderVersion += 1
  editor.value?.dispose()
  originalModel?.dispose()
  modifiedModel?.dispose()
})
</script>

<template>
  <div ref="container" class="monaco-diff-viewer" :class="{ commentable }" />
</template>

<style scoped>
.monaco-diff-viewer {
  width: 100%;
  height: 100%;
  min-height: 0;
  background: var(--bg-void);
}

.commentable :deep(.editor.modified .line-numbers) {
  cursor: pointer;
}

.commentable :deep(.editor.modified .line-numbers:hover) {
  color: var(--accent-blue);
}
</style>

<style>
/* Monaco renders decorations outside the component's scope. */
.devpad-diff-marked-line {
  background: color-mix(in srgb, var(--accent-blue) 9%, transparent);
}

.devpad-diff-marked-gutter {
  background: var(--accent-blue);
  width: 3px !important;
  margin-left: 3px;
  border-radius: 2px;
}
</style>
