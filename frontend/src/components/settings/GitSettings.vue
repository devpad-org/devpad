<script setup lang="ts">
import GitIdentityCard from './GitIdentityCard.vue'
import SSHKeyCard from './SSHKeyCard.vue'
</script>

<template>
  <div>
    <header class="page-header">
      <h2 class="page-title">Git</h2>
      <p class="page-desc">Who your commits say they're from, and how devpad reaches your Git host.</p>
    </header>

    <div class="card-list">
      <GitIdentityCard />
      <SSHKeyCard />
    </div>
  </div>
</template>

<style scoped src="./settings.css"></style>
