<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

const props = withDefaults(defineProps<{
  value: number
  digits?: number
  prefix?: string
  suffix?: string
  duration?: number
}>(), {
  digits: 2,
  prefix: '',
  suffix: '',
  duration: 480,
})

const displayed = ref(props.value)
const bumping = ref(false)
let frame: number | undefined
let bumpTimer: ReturnType<typeof window.setTimeout> | undefined

const text = computed(() => {
  if (!Number.isFinite(displayed.value)) return '—'
  const number = new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: props.digits,
    maximumFractionDigits: props.digits,
  }).format(displayed.value)
  return `${props.prefix}${number}${props.suffix}`
})

function reducedMotion(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function stopAnimation() {
  if (frame !== undefined) window.cancelAnimationFrame(frame)
  frame = undefined
}

function triggerBump() {
  if (reducedMotion()) return
  bumping.value = false
  window.requestAnimationFrame(() => { bumping.value = true })
  if (bumpTimer !== undefined) window.clearTimeout(bumpTimer)
  bumpTimer = window.setTimeout(() => { bumping.value = false }, 380)
}

watch(() => props.value, (target, previous) => {
  stopAnimation()
  if (!Number.isFinite(target) || !Number.isFinite(previous) || reducedMotion() || props.duration <= 0) {
    displayed.value = target
    return
  }
  if (Math.abs(target - previous) < Number.EPSILON) return

  triggerBump()
  const source = Number.isFinite(displayed.value) ? displayed.value : previous
  const started = performance.now()
  const step = (timestamp: number) => {
    const progress = Math.min(1, (timestamp - started) / props.duration)
    const eased = 1 - Math.pow(1 - progress, 3)
    displayed.value = source + (target - source) * eased
    if (progress < 1) frame = window.requestAnimationFrame(step)
    else {
      displayed.value = target
      frame = undefined
    }
  }
  frame = window.requestAnimationFrame(step)
})

onBeforeUnmount(() => {
  stopAnimation()
  if (bumpTimer !== undefined) window.clearTimeout(bumpTimer)
})
</script>

<template>
  <strong class="animated-number" :class="{ bump: bumping }">{{ text }}</strong>
</template>

<style scoped>
.animated-number {
  display: inline-block;
  min-width: 0;
  font-variant-numeric: tabular-nums;
  transform-origin: left center;
  will-change: transform;
}

.animated-number.bump {
  animation: number-bump 360ms cubic-bezier(.22, 1, .36, 1);
}

@keyframes number-bump {
  0% { transform: scale(1); }
  42% { transform: scale(1.055); }
  100% { transform: scale(1); }
}

@media (prefers-reduced-motion: reduce) {
  .animated-number {
    will-change: auto;
  }

  .animated-number.bump {
    animation: none;
  }
}
</style>
