<script setup lang="ts">
import { ref } from 'vue'
import { THEMES, currentTheme, type ThemeId } from '@/theme'
import { chooseTheme } from '@/theme/account'

const saving = ref(false)
const error = ref('')

async function choose(id: ThemeId) {
  if (saving.value || id === currentTheme.value) return
  saving.value = true
  error.value = ''
  try {
    await chooseTheme(id)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save the theme'
  } finally {
    saving.value = false
  }
}

// Arrow keys move the choice, as in any radio group.
function onKeydown(event: KeyboardEvent, index: number) {
  const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[event.key]
  if (!step) return
  event.preventDefault()
  const next = THEMES[(index + step + THEMES.length) % THEMES.length]
  void choose(next.id)
  const group = (event.currentTarget as HTMLElement).parentElement
  group?.querySelector<HTMLElement>(`[data-theme-option="${next.id}"]`)?.focus()
}
</script>

<template>
  <div>
    <header class="page-header">
      <h2 class="page-title">Appearance</h2>
      <p class="page-desc">The theme is saved to your account and follows you to other browsers.</p>
    </header>

    <div v-if="error" class="alert alert-error" role="alert">{{ error }}</div>

    <div class="theme-grid" role="radiogroup" aria-label="Theme">
      <button
        v-for="(theme, index) in THEMES"
        :key="theme.id"
        type="button"
        role="radio"
        class="theme-option"
        :class="{ selected: theme.id === currentTheme }"
        :aria-checked="theme.id === currentTheme"
        :tabindex="theme.id === currentTheme ? 0 : -1"
        :data-theme-option="theme.id"
        @click="choose(theme.id)"
        @keydown="onKeydown($event, index)"
      >
        <!-- A small IDE drawn in the theme's own colours. -->
        <span class="preview" :data-theme="theme.id" aria-hidden="true">
          <span class="preview-bar">
            <span class="preview-dot" />
            <span class="preview-title" />
            <span class="preview-button" />
          </span>
          <span class="preview-body">
            <span class="preview-side">
              <span class="preview-row" style="width: 70%" />
              <span class="preview-row active" style="width: 85%" />
              <span class="preview-row" style="width: 55%" />
              <span class="preview-row" style="width: 75%" />
            </span>
            <span class="preview-main">
              <span class="preview-code">
                <span class="code-line"><i class="kw" style="width: 18%" /><i class="fn" style="width: 26%" /><i class="dl" style="width: 8%" /></span>
                <span class="code-line indent"><i class="kw" style="width: 12%" /><i class="va" style="width: 20%" /><i class="st" style="width: 30%" /></span>
                <span class="code-line indent"><i class="cm" style="width: 52%" /></span>
                <span class="code-line indent"><i class="ty" style="width: 16%" /><i class="va" style="width: 14%" /><i class="nu" style="width: 8%" /></span>
                <span class="code-line"><i class="dl" style="width: 5%" /></span>
              </span>
              <span class="preview-term">
                <span class="code-line"><i class="ok" style="width: 6%" /><i class="tx" style="width: 38%" /></span>
                <span class="code-line"><i class="tx" style="width: 24%" /><i class="er" style="width: 14%" /></span>
              </span>
            </span>
          </span>
        </span>

        <span class="theme-meta">
          <span class="theme-radio" aria-hidden="true" />
          <span class="theme-text">
            <span class="theme-name">{{ theme.label }}</span>
            <span class="theme-desc">{{ theme.description }}</span>
          </span>
        </span>
      </button>
    </div>
  </div>
</template>

<style scoped src="./settings.css"></style>
<style scoped>
.theme-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}

.theme-option {
  display: flex;
  flex-direction: column;
  text-align: left;
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  overflow: hidden;
  transition:
    border-color var(--transition-fast),
    box-shadow var(--transition-fast);
}

.theme-option:hover {
  border-color: var(--border-strong);
}

.theme-option.selected {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.theme-option:focus-visible {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-glow), 0 0 0 1px var(--accent-border);
}

/* The preview carries data-theme, so every token below is that theme's. */
.preview {
  display: flex;
  flex-direction: column;
  height: 132px;
  background: var(--bg-base);
  border-bottom: 1px solid var(--border-default);
}

.preview-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 20px;
  padding: 0 8px;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-subtle);
}

.preview-dot {
  width: 7px;
  height: 7px;
  border-radius: 2px;
  background: var(--accent);
}

.preview-title {
  width: 56px;
  height: 4px;
  border-radius: 2px;
  background: var(--text-secondary);
}

.preview-button {
  width: 26px;
  height: 9px;
  margin-left: auto;
  border-radius: 2px;
  background: var(--accent-blue);
}

.preview-body {
  display: flex;
  flex: 1;
  min-height: 0;
}

.preview-side {
  display: flex;
  flex-direction: column;
  gap: 7px;
  width: 26%;
  padding: 9px 8px;
  border-right: 1px solid var(--border-subtle);
  overflow: hidden;
}

.preview-row {
  position: relative;
  height: 4px;
  border-radius: 2px;
  background: var(--text-dim);
}

.preview-row.active {
  background: var(--accent);
}

.preview-row.active::before {
  content: '';
  position: absolute;
  inset: -4px -200% -4px -8px;
  background: var(--bg-selected);
}

.preview-main {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.preview-code {
  display: flex;
  flex-direction: column;
  gap: 7px;
  flex: 1;
  padding: 9px 10px;
  background: var(--editor-bg);
}

.preview-term {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 7px 10px;
  background: var(--terminal-bg);
  border-top: 1px solid var(--border-subtle);
}

.code-line {
  display: flex;
  gap: 5px;
  height: 4px;
}

.code-line.indent {
  padding-left: 10%;
}

.code-line i {
  height: 100%;
  border-radius: 2px;
}

.kw { background: var(--syntax-keyword); }
.fn { background: var(--syntax-function); }
.st { background: var(--syntax-string); }
.ty { background: var(--syntax-type); }
.nu { background: var(--syntax-number); }
.va { background: var(--syntax-variable); }
.cm { background: var(--syntax-comment); }
.dl { background: var(--syntax-delimiter); }
.tx { background: var(--term-fg); opacity: 0.75; }
.ok { background: var(--success); }
.er { background: var(--error); }

.theme-meta {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
}

.theme-radio {
  flex-shrink: 0;
  width: 16px;
  height: 16px;
  margin-top: 2px;
  border: 1.5px solid var(--text-muted);
  border-radius: 50%;
  transition: all var(--transition-fast);
}

.theme-option.selected .theme-radio {
  border-color: var(--accent);
  background: radial-gradient(circle, var(--accent) 0 4px, transparent 4.5px);
}

.theme-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.theme-name {
  font-size: 0.9rem;
  font-weight: 500;
  color: var(--text-primary);
}

.theme-desc {
  font-size: 0.78rem;
  color: var(--text-muted);
  line-height: 1.45;
}

@media (max-width: 560px) {
  .theme-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
