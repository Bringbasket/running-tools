<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { X } from '../icons'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  open: boolean
  title: string
  subtitle?: string
  busy?: boolean
  role?: 'dialog' | 'alertdialog'
  width?: 'normal' | 'wide'
}>(), { subtitle: '', busy: false, role: 'dialog', width: 'normal' })
const emit = defineEmits<{ close: [] }>()
const panel = ref<HTMLElement | null>(null)
let restoreFocus: HTMLElement | null = null

function focusable() {
  return Array.from(panel.value?.querySelectorAll<HTMLElement>(
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
  ) || []).filter((element) => element.offsetParent !== null || element === document.activeElement)
}

function requestClose() {
  if (!props.busy) emit('close')
}

function onKeydown(event: KeyboardEvent) {
  if (!props.open) return
  if (event.key === 'Escape') {
    event.preventDefault()
    requestClose()
    return
  }
  if (event.key !== 'Tab') return
  const items = focusable()
  if (!items.length) {
    event.preventDefault()
    panel.value?.focus()
    return
  }
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(() => props.open, async (open) => {
  if (open) {
    restoreFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    document.addEventListener('keydown', onKeydown)
    await nextTick()
    const preferred = panel.value?.querySelector<HTMLElement>('[autofocus]')
    ;(preferred || focusable()[0] || panel.value)?.focus()
  } else {
    document.removeEventListener('keydown', onKeydown)
    restoreFocus?.focus()
    restoreFocus = null
  }
})

onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="dialog-backdrop" @mousedown.self="requestClose">
      <section :id="`${$attrs.id || 'app'}-dialog`" ref="panel" class="dialog app-dialog" :class="{ wide: width === 'wide' }" :role="role" aria-modal="true" tabindex="-1" :aria-labelledby="`${$attrs.id || 'app'}-dialog-title`">
        <header class="dialog-heading">
          <div><h2 :id="`${$attrs.id || 'app'}-dialog-title`">{{ title }}</h2><p v-if="subtitle">{{ subtitle }}</p></div>
          <button type="button" class="icon-button" title="关闭" aria-label="关闭" :disabled="busy" @click="requestClose"><X :size="18" /></button>
        </header>
        <div class="app-dialog-body"><slot /></div>
        <footer class="dialog-actions"><slot name="actions" /></footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.app-dialog { display: grid; max-height: min(680px, calc(100vh - 40px)); grid-template-rows: auto minmax(0, 1fr) auto; padding: 0; overflow: hidden; }
.app-dialog.wide { width: min(760px, calc(100vw - 28px)); }
.dialog-heading { padding: 19px 20px 15px; border-bottom: 1px solid var(--border-soft); }
.dialog-heading p { margin: 5px 0 0; color: var(--muted); font-size: 12px; line-height: 1.5; }
.app-dialog-body { padding: 18px 20px; overflow: auto; }
.dialog-actions { min-height: 66px; padding: 14px 20px; border-top: 1px solid var(--border-soft); }
@media (max-width: 620px) {
  .app-dialog { width: calc(100vw - 24px); max-height: calc(100vh - 24px); }
  .dialog-heading, .app-dialog-body { padding-right: 16px; padding-left: 16px; }
  .dialog-actions { padding: 12px 16px; }
}
</style>
