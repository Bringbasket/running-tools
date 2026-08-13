<script setup lang="ts">
defineProps<{ state: 'active' | 'inactive' | 'valid' | 'invalid' | 'neutral'; label: string }>()
</script>

<template><span class="status-badge" :class="state"><span />{{ label }}</span></template>
