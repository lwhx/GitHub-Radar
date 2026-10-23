<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <div class="rounded-lg border border-line bg-card p-10 text-center">
    <h1 class="text-2xl font-semibold">404</h1>
    <p class="mt-2 text-sm text-muted">页面不存在或已被移动。</p>
    <RouterLink to="/" class="mt-4 inline-block rounded-md bg-accent px-4 py-2 text-sm font-medium text-accent-fg hover:opacity-90">
      返回发现首页
    </RouterLink>
  </div>
</template>
