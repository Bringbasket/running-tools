<script setup lang="ts">
import { CircleAlert, LoaderCircle } from '../icons'

withDefaults(defineProps<{ state: 'loading' | 'error' | 'empty'; title: string; detail?: string }>(), { detail: '' })
defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="async-state" :class="state">
    <LoaderCircle v-if="state === 'loading'" :size="22" class="spin" />
    <CircleAlert v-else-if="state === 'error'" :size="23" />
    <slot v-else name="icon" />
    <strong>{{ title }}</strong><span v-if="detail">{{ detail }}</span>
    <button v-if="state === 'error'" type="button" class="button ghost" @click="$emit('retry')">重试</button>
  </div>
</template>

<style scoped>
.async-state { display: flex; min-height: 136px; align-items: center; justify-content: center; flex-direction: column; gap: 7px; color: var(--muted); text-align: center; }
.async-state strong { color: var(--text); font-size: 13px; }
.async-state span { max-width: 460px; font-size: 11px; line-height: 1.5; overflow-wrap: anywhere; }
.async-state.error > svg { color: var(--danger); }
.async-state .button { margin-top: 3px; }
</style>
