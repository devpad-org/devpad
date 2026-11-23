<script setup lang="ts">
import { computed } from 'vue'
import DocLink from '@/components/DocLink.vue'
import MrIcon from '@/components/project/MrIcon.vue'
import { LOOK_CLOSELY, LOOK_CLOSELY_HINT, lookCloselyGroups, plural } from './format'
import { useMergeRequest } from './useMergeRequest'

// The look-closely mark: the files of the change devpad marks because a
// passing pipeline can't vouch for them, deleted tests and the files jobs
// depend on, worked out by the server from the change. Each opens in
// Changes. It never keeps the merge request from merging.
const { lookClosely, section, selectFile } = useMergeRequest()

const groups = computed(() => (lookClosely.value ? lookCloselyGroups(lookClosely.value) : []))

function open(path: string) {
  section.value = 'changes'
  selectFile(path)
}
</script>

<template>
  <section v-if="lookClosely && groups.length" class="card look-card" aria-label="Look closely">
    <div class="card-header">
      <span class="card-header-text">
        <MrIcon name="eye" :size="13" class="look-icon" />
        <strong>Look closely</strong>
        <DocLink topic="lookClosely" label="The look-closely mark" />
      </span>
    </div>
    <div class="card-body">
      <p class="explain">{{ LOOK_CLOSELY_HINT }}</p>
      <dl class="groups">
        <div v-for="group in groups" :key="group.reason" class="group">
          <dt>
            {{ LOOK_CLOSELY[group.reason] }} <span class="count">{{ plural(group.count, 'file') }}</span>
          </dt>
          <dd>
            <ul class="files">
              <li v-for="f in group.files" :key="f.path">
                <button type="button" class="file mono" :title="`Open ${f.path} in Changes`" @click="open(f.path)">{{ f.path }}</button>
                <span v-if="f.oldPath && f.oldPath !== f.path" class="was">
                  was <span class="mono">{{ f.oldPath }}</span>
                </span>
              </li>
              <li v-if="group.count > group.files.length" class="more">and {{ group.count - group.files.length }} more</li>
            </ul>
          </dd>
        </div>
      </dl>
    </div>
  </section>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped src="./mergerequest.css"></style>
<style scoped>
.card-header-text {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}

.look-icon {
  color: var(--warning);
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.explain {
  margin: 0;
  color: var(--text-secondary);
  font-size: 0.8rem;
}

.count,
.more {
  color: var(--text-tertiary);
  font-weight: 400;
}

.groups {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
}

.group dt {
  color: var(--text-primary);
  font-size: 0.8rem;
  font-weight: 500;
}

.group dd {
  margin: 0;
}

.files {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: var(--space-1) 0 0;
  padding: 0;
  list-style: none;
}

.files li {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1);
  min-width: 0;
  font-size: 0.78rem;
}

.file {
  padding: 0;
  border: none;
  background: none;
  color: var(--accent);
  font-size: inherit;
  text-align: left;
  overflow-wrap: anywhere;
  cursor: pointer;
}

.file:hover {
  text-decoration: underline;
}

.file:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--accent-glow);
  border-radius: var(--radius-sm);
}

.was {
  color: var(--text-tertiary);
  overflow-wrap: anywhere;
}
</style>
