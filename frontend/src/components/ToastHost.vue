<script setup lang="ts">
import { CheckCircle2, CircleAlert, X } from '../icons'
import { dismissToast, toastState } from '../toast'
</script>

<template>
  <Teleport to="body">
    <div class="toast-host" aria-live="polite" aria-atomic="false">
      <TransitionGroup name="toast">
        <div v-for="item in toastState.items" :key="item.id" class="app-toast" :class="item.tone" :role="item.tone === 'error' ? 'alert' : 'status'">
          <CheckCircle2 v-if="item.tone === 'success'" :size="18" />
          <CircleAlert v-else :size="18" />
          <span>{{ item.message }}</span>
          <button type="button" title="关闭提示" aria-label="关闭提示" @click="dismissToast(item.id)"><X :size="15" /></button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-host { position: fixed; z-index: 100; top: 16px; right: 16px; display: grid; width: min(360px, calc(100vw - 28px)); gap: 8px; pointer-events: none; }
.app-toast { display: grid; min-height: 46px; grid-template-columns: 20px minmax(0, 1fr) 28px; align-items: center; gap: 9px; padding: 8px 8px 8px 12px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-left: 3px solid var(--primary); border-radius: 7px; box-shadow: 0 10px 28px rgba(15, 23, 42, .14); pointer-events: auto; }
.app-toast.success { border-left-color: #059669; }
.app-toast.error { border-left-color: var(--danger); }
.app-toast.warning { border-left-color: var(--warning); }
.app-toast > span { font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
.app-toast > button { display: grid; width: 28px; height: 28px; place-items: center; color: var(--muted); background: transparent; border: 0; border-radius: 5px; cursor: pointer; }
.app-toast > button:hover { color: var(--text); background: var(--surface-hover); }
.toast-enter-active, .toast-leave-active { transition: opacity 160ms ease, transform 160ms ease; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translateY(-6px); }
@media (prefers-reduced-motion: reduce) { .toast-enter-active, .toast-leave-active { transition: none; } }
</style>
