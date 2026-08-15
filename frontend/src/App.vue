<script setup lang="ts">
import { ref } from 'vue'
import { LoaderCircle } from '@lucide/vue'
import AppHeader from './components/AppHeader.vue'
import AppSidebar from './components/AppSidebar.vue'
import LoginPage from './pages/LoginPage.vue'
import ToastHost from './components/ToastHost.vue'
import { authState } from './auth'

const collapsed = ref(false)
const mobileOpen = ref(false)
</script>

<template>
  <div v-if="!authState.ready" class="auth-loading" role="status" aria-live="polite">
    <span class="brand-mark"><LoaderCircle :size="22" class="spin" /></span>
    <strong>Running Tools</strong>
  </div>
  <LoginPage v-else-if="!authState.authenticated || authState.user?.mustChangePassword" />
  <div v-else class="app-shell" :class="{ 'sidebar-collapsed': collapsed }">
    <AppSidebar v-model:collapsed="collapsed" v-model:mobile-open="mobileOpen" />
    <div class="workspace">
      <AppHeader @open-navigation="mobileOpen = true" />
      <main class="page-area"><RouterView /></main>
    </div>
    <ToastHost />
  </div>
</template>
