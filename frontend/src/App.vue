<script setup lang="ts">
import { ref } from 'vue'
import AppHeader from './components/AppHeader.vue'
import AppSidebar from './components/AppSidebar.vue'
import LoginDialog from './components/LoginDialog.vue'
import ToastHost from './components/ToastHost.vue'

const collapsed = ref(false)
const mobileOpen = ref(false)
</script>

<template>
  <div class="app-shell" :class="{ 'sidebar-collapsed': collapsed }">
    <AppSidebar v-model:collapsed="collapsed" v-model:mobile-open="mobileOpen" />
    <div class="workspace">
      <AppHeader @open-navigation="mobileOpen = true" />
      <main class="page-area">
        <RouterView />
      </main>
    </div>
    <LoginDialog />
    <ToastHost />
  </div>
</template>
