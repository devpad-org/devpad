<script setup lang="ts">
import { ref } from 'vue'
import type { ConversionNotice } from '@/api/projects'
import { credentialKind, REVOKE_REMINDER } from './credentials'

// What converting a workspace into the project did, and what the user still
// has to do upstream. It stays until dismissed, so nothing is lost if the
// conversion's own response never reached the browser.
const props = defineProps<{
  conversion: ConversionNotice
}>()

const emit = defineEmits<{
  dismiss: []
}>()

const copied = ref(false)

async function copyKey() {
  try {
    await navigator.clipboard.writeText(props.conversion.publicKey)
    copied.value = true
  } catch {
    copied.value = false
  }
}
</script>

<template>
  <div class="side-section conversion">
    <div class="side-section-title">Converted from a workspace</div>

    <p v-if="!conversion.finished" class="notice notice-warning">Devpad is finishing the conversion...</p>

    <template v-if="conversion.keyRotated">
      <p class="hint">
        Your devpad SSH key was replaced, because the workspace held it where the IDE assistant could read it. Add
        the new public key upstream (GitHub or wherever you push), then remove the old one there.
      </p>
      <textarea class="key mono" rows="3" readonly :value="conversion.publicKey" />
      <button type="button" class="btn btn-secondary btn-sm" @click="copyKey">{{ copied ? 'Copied' : 'Copy key' }}</button>
    </template>
    <p v-else-if="conversion.rotationPending" class="hint">Replacing your devpad SSH key...</p>
    <p v-else-if="conversion.rotationError" class="notice notice-error">
      Devpad couldn't replace your SSH key ({{ conversion.rotationError }}). The workspace held it, so generate a new
      one in Settings, add it upstream, and remove the old one there.
    </p>

    <div v-if="conversion.stripped.length > 0" class="notice notice-warning">
      Stripped from the workspace:
      <ul class="list">
        <li v-for="item in conversion.stripped" :key="item.id">
          {{ credentialKind(item.kind) }}: <span class="mono">{{ item.location }}</span>
        </li>
      </ul>
      Revoke these where they were issued; stripping can't undo that the assistant could read them.
    </div>
    <p class="notice notice-warning">{{ REVOKE_REMINDER }}</p>

    <p v-if="conversion.restartError" class="notice notice-error">
      Devpad couldn't restart the workspace ({{ conversion.restartError }}). Stop and start it yourself, so no
      process from before the conversion keeps a credential.
    </p>
    <p v-if="conversion.sshKept.length > 0" class="hint">
      Left in place because they don't match what devpad wrote:
      <span class="mono">{{ conversion.sshKept.join(', ') }}</span>.
    </p>

    <button type="button" class="btn btn-secondary btn-sm" :disabled="!conversion.finished" @click="emit('dismiss')">
      Done, dismiss
    </button>
  </div>
</template>

<style scoped src="./shared.css"></style>
<style scoped src="./panel.css"></style>
<style scoped>
.conversion {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.hint {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.key {
  width: 100%;
  resize: vertical;
  padding: var(--space-2);
  font-size: 0.7rem;
  color: var(--text-primary);
  background: var(--bg-void);
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-sm);
}

.list {
  margin: var(--space-1) 0 var(--space-1) var(--space-4);
  list-style: disc;
  overflow-wrap: anywhere;
}
</style>
