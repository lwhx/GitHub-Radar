<script setup lang="ts">
import { RouterLink } from 'vue-router'

import { favorites, removeFavorite } from '../stores/favorites'
</script>


<template>
  <div class="flex flex-col gap-4">
    <section class="rounded-lg border border-line bg-card p-4">
      <h1 class="text-lg font-semibold">我的关注</h1>
      <p class="mt-1 text-sm text-muted">共 {{ favorites.length }} 个项目 · 收藏只保存在当前浏览器，不做账号同步。</p>
    </section>

    <section v-if="favorites.length === 0" class="rounded-lg border border-warning/50 bg-card p-6">
      <h2 class="font-medium text-warning">还没有收藏的项目</h2>
      <p class="mt-2 text-sm text-muted">在探索项目页或项目详情页点击收藏，即可在这里找到它们。收藏只保存在当前浏览器。</p>
      <RouterLink to="/repos" class="mt-3 inline-block text-sm underline hover:text-accent">去探索项目</RouterLink>
    </section>

    <ul v-else class="flex flex-col gap-3">
      <li v-for="item in favorites" :key="item.full_name" class="rounded-lg border border-line bg-card p-4">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <RouterLink :to="`/repos/${item.owner}/${item.name}`" class="font-medium hover:text-accent">
            {{ item.full_name }}
          </RouterLink>
          <span class="text-sm text-muted">⭐ {{ item.stars_count }}</span>
        </div>
        <p v-if="item.description" class="mt-1 text-sm text-muted">{{ item.description }}</p>
        <p class="mt-2 flex flex-wrap items-center gap-3 text-xs text-muted">
          <span v-if="item.language">{{ item.language }}</span>
          <span>收藏于 {{ item.favorited_at.slice(0, 10) }}</span>
          <button type="button" class="underline" @click="removeFavorite(item.full_name)">移除</button>
        </p>
      </li>
    </ul>
  </div>
</template>
