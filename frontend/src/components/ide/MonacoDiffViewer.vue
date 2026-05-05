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
}>()

const container = ref<HTMLElement | null>(null)
const editor = shallowRef<MonacoType.editor.IStandaloneDiffEditor | null>(null)
let originalModel: MonacoType.editor.ITextModel | null = null
let modifiedModel: MonacoType.editor.ITextModel | null = null
let renderVersion = 0

async function renderDiff() {
  const el = container.value
  if (!el) return

  const version = ++renderVersion
  const monaco = await loadMonaco()
  if (version !== renderVersion || !container.value) return

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
  }

  originalModel?.dispose()
  modifiedModel?.dispose()

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

onBeforeUnmount(() => {
  renderVersion += 1
  originalModel?.dispose()
  modifiedModel?.dispose()
  editor.value?.dispose()
})
</script>

<template>
  <div ref="container" class="monaco-diff-viewer" />
</template>

<style scoped>
.monaco-diff-viewer {
  width: 100%;
  height: 100%;
  min-height: 0;
  background: var(--bg-void);
}
</style>
