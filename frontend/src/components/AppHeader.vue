<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { LogOut, Menu, Moon, Sun } from '@lucide/vue'
import { logout } from '../auth'
import { modules } from '../modules'

defineEmits<{ openNavigation: [] }>()
const route = useRoute()
const dark = ref(document.documentElement.dataset.theme === 'dark')
const title = computed(() => String(route.meta.title || 'Running Tools'))
const section = computed(() => modules.find((module) => route.path.startsWith(`/${module.id}/`))?.label || '控制台')

function toggleTheme() {
  dark.value = !dark.value
  const value = dark.value ? 'dark' : 'light'
  document.documentElement.dataset.theme = value
  localStorage.setItem('running-theme', value)
}

</script>

<template>
  <header class="app-header">
    <div class="header-title">
      <button class="icon-button menu-button" title="打开菜单" @click="$emit('openNavigation')"><Menu :size="20" /></button>
      <div class="header-breadcrumb"><span class="header-context">{{ section }}</span><span class="breadcrumb-divider">/</span><h1>{{ title }}</h1></div>
    </div>
    <div class="header-actions">
      <button class="icon-button" :title="dark ? '切换浅色模式' : '切换深色模式'" @click="toggleTheme"><Sun v-if="dark" :size="18" /><Moon v-else :size="18" /></button>
      <button class="icon-button" title="退出" @click="logout"><LogOut :size="18" /></button>
    </div>
  </header>
</template>
