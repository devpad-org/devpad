<script setup lang="ts">
import InboxList from '@/components/inbox/InboxList.vue'

// The inbox as a page of its own, for links to it. The header's inbox button
// opens the same list over any page.
</script>

<template>
  <div class="page inbox-page">
    <div class="page-header">
      <div>
        <h1 class="page-title">Inbox</h1>
        <p class="page-subtitle">Agents waiting for you to approve a command or answer a question</p>
      </div>
    </div>
    <InboxList />
  </div>
</template>

<style scoped src="@/components/project/shared.css"></style>
<style scoped>
.inbox-page {
  max-width: 760px;
}
</style>
