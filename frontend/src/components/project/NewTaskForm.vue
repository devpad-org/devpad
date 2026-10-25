<script setup lang="ts">
import { ref } from 'vue'
import { taskApi, type Task } from '@/api/tasks'
import AgentModelPicker from './AgentModelPicker.vue'

// Creates a task, optionally starting an agent on it right away.
const props = defineProps<{
  projectId: number
  compact?: boolean
}>()

const emit = defineEmits<{
  created: [task: Task]
  cancel: []
}>()

const title = ref('')
const body = ref('')
const model = ref('')
const effort = ref('')
const startNow = ref(true)
const creating = ref(false)
const error = ref('')

async function submit() {
  if (!title.value.trim()) {
    error.value = 'A task needs a title'
    return
  }
  creating.value = true
  error.value = ''
  try {
    const res = await taskApi.create(props.projectId, {
      title: title.value.trim(),
      body: body.value,
      start: startNow.value,
      model: model.value || undefined,
      effort: effort.value || undefined,
    })
    title.value = ''
    body.value = ''
    emit('created', res.task)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to create task'
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <form class="new-task" :class="{ compact, panel: !compact }" @submit.prevent="submit">
    <p v-if="error" class="form-error">{{ error }}</p>
    <div class="form-group">
      <label class="form-label" :for="`new-task-title-${compact ? 'side' : 'board'}`">Title</label>
      <input
        :id="`new-task-title-${compact ? 'side' : 'board'}`"
        v-model="title"
        class="form-input"
        type="text"
        autocomplete="off"
        placeholder="Add rate limiting to the login endpoint"
      />
    </div>
    <div class="form-group">
      <label class="form-label" :for="`new-task-body-${compact ? 'side' : 'board'}`">Description</label>
      <textarea
        :id="`new-task-body-${compact ? 'side' : 'board'}`"
        v-model="body"
        class="form-input form-textarea"
        :rows="compact ? 4 : 5"
        placeholder="What should change, and how will we know it works? Markdown is fine."
      />
    </div>
    <div class="new-task-row">
      <AgentModelPicker v-model:model="model" v-model:effort="effort" />
      <div class="new-task-actions">
        <label class="form-check">
          <input v-model="startNow" type="checkbox" />
          Start an agent now
        </label>
        <button v-if="compact" type="button" class="btn btn-secondary btn-sm" @click="emit('cancel')">Cancel</button>
        <button type="submit" class="btn btn-primary" :class="{ 'btn-sm': compact }" :disabled="creating || (startNow && !model)">
          {{ creating ? 'Creating...' : 'Create task' }}
        </button>
      </div>
    </div>
  </form>
</template>

<style scoped src="./shared.css"></style>
<style scoped>
.new-task-row,
.new-task-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.compact .form-group {
  margin-bottom: var(--space-3);
}

.compact .new-task-row,
.compact .new-task-actions {
  gap: var(--space-2);
}

.compact .new-task-actions {
  justify-content: flex-end;
  width: 100%;
}

.compact .form-check {
  margin-right: auto;
}
</style>
