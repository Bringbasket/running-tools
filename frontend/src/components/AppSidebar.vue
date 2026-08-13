<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { CheckCircle2, ChevronDown, ChevronsLeft, ChevronsRight, CircleAlert, Download, Layers3, LoaderCircle, RefreshCw, X } from '@lucide/vue'
import { APIError, apiRequest, errorMessage } from '../api'
import { APP_VERSION } from '../version'
import { modules } from '../modules'

const collapsed = defineModel<boolean>('collapsed', { required: true })
const mobileOpen = defineModel<boolean>('mobileOpen', { required: true })
const expanded = ref<Record<string, boolean>>(Object.fromEntries(modules.map((module) => [module.id, true])))
const route = useRoute()
const updateOpen = ref(false)
const checking = ref(false)
const updating = ref(false)
const error = ref('')
const version = ref<SystemVersion | null>(null)
let pollTimer: ReturnType<typeof window.setInterval> | undefined

interface SystemVersion {
  state: string
  action?: 'check' | 'update'
  message: string
  currentRevision: string
  latestRevision: string | null
  updateAvailable: boolean | null
  canRequestUpdate: boolean
  repositoryUrl: string
  error?: string | null
}

const checkActive = () => Boolean(
  version.value
  && !version.value.canRequestUpdate
  && version.value.action === 'check'
  && ['check_queued', 'checking'].includes(version.value.state),
)
const updateActive = () => Boolean(
  version.value
  && !version.value.canRequestUpdate
  && version.value.action === 'update'
  && ['update_queued', 'updating', 'restarting'].includes(version.value.state),
)
const busy = () => checking.value || updating.value || checkActive() || updateActive()
const versionSummary = computed(() => {
  if (error.value) return { kind: 'error', title: '操作失败', detail: error.value }
  if (checking.value || checkActive()) return { kind: 'busy', title: '正在检查更新', detail: '正在查询最新可用构建' }
  if (updating.value || updateActive()) {
    const queued = version.value?.state === 'update_queued'
    return { kind: 'busy', title: '更新进行中', detail: queued ? '更新请求已提交' : '正在拉取并部署新版本' }
  }
  if (version.value?.state === 'error') return { kind: 'error', title: '版本任务失败', detail: version.value.error || '请重新检查更新' }
  if (version.value?.updateAvailable) return { kind: 'available', title: '发现新版本', detail: `最新构建 ${version.value.latestRevision?.slice(0, 8) || '可用'}` }
  if (version.value?.state === 'success') return { kind: 'success', title: '更新完成', detail: '服务已经切换到最新构建' }
  if (version.value?.state === 'up_to_date') return { kind: 'success', title: '已是最新版本', detail: '当前没有可用更新' }
  return { kind: 'neutral', title: '尚未检查更新', detail: '点击右上角按钮手动检查' }
})

async function loadVersion() {
  error.value = ''
  try {
    version.value = await apiRequest<SystemVersion>('/api/system/version')
    if (checkActive() || updateActive()) startPolling()
  } catch (reason) {
    error.value = errorMessage(reason)
  }
}

function stopPolling() {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(async () => {
    try {
      version.value = await apiRequest<SystemVersion>('/api/system/version')
      if (!checkActive() && !updateActive()) stopPolling()
    } catch (reason) {
      error.value = errorMessage(reason)
      if (reason instanceof APIError && reason.status === 401) stopPolling()
    }
  }, 2000)
}

async function checkForUpdates() {
  if (busy()) return
  checking.value = true
  error.value = ''
  try {
    version.value = await apiRequest<SystemVersion>('/api/system/version/check', { method: 'POST', body: '{}' })
    startPolling()
  } catch (reason) {
    error.value = errorMessage(reason)
  } finally {
    checking.value = false
  }
}

async function requestUpdate() {
  if (busy() || !version.value?.updateAvailable) return
  updating.value = true
  error.value = ''
  try {
    version.value = await apiRequest<SystemVersion>('/api/system/update', { method: 'POST', body: '{}' })
    startPolling()
  } catch (reason) {
    error.value = errorMessage(reason)
  } finally {
    updating.value = false
  }
}

function toggleUpdate() {
  if (collapsed.value) {
    collapsed.value = false
    window.requestAnimationFrame(() => { updateOpen.value = true })
    return
  }
  updateOpen.value = !updateOpen.value
  // Version state is fetched once in the background; opening the panel should be instant.
}

function closeUpdate() {
  updateOpen.value = false
}

function handleWindowClick() {
  if (updateOpen.value) closeUpdate()
}

onMounted(() => {
  window.addEventListener('click', handleWindowClick)
  if (localStorage.getItem('running-api-key')) void loadVersion()
})

onBeforeUnmount(() => {
  window.removeEventListener('click', handleWindowClick)
  stopPolling()
})
</script>

<template>
  <div v-if="mobileOpen" class="sidebar-overlay" @click="mobileOpen = false" />
  <aside class="sidebar" :class="{ mobile: mobileOpen }">
    <div class="brand-row">
      <span class="brand-mark"><Layers3 :size="20" /></span>
      <div v-if="!collapsed" class="brand-copy">
        <strong>Running Tools</strong>
        <small>服务管理平台</small>
        <span class="version-anchor" @click.stop>
          <button class="brand-version" :class="{ available: version?.updateAvailable }" title="版本与更新" @click="toggleUpdate">
            <span>v{{ APP_VERSION }}</span>
            <span v-if="version?.updateAvailable" class="update-indicator" />
          </button>
          <div v-if="updateOpen" class="version-popover" @click.stop>
            <div class="version-popover-header">
              <div><span>当前版本</span><strong>v{{ APP_VERSION }}</strong></div>
              <button class="icon-button" title="检查更新" :disabled="busy()" @click="checkForUpdates"><RefreshCw :size="15" :class="{ spin: checking || checkActive() }" /></button>
            </div>
            <div class="version-summary" :class="versionSummary.kind">
              <span class="version-summary-icon">
                <LoaderCircle v-if="versionSummary.kind === 'busy'" :size="17" class="spin" />
                <Download v-else-if="versionSummary.kind === 'available'" :size="17" />
                <CircleAlert v-else-if="versionSummary.kind === 'error'" :size="17" />
                <CheckCircle2 v-else-if="versionSummary.kind === 'success'" :size="17" />
                <RefreshCw v-else :size="17" />
              </span>
              <div>
                <strong>{{ versionSummary.title }}</strong>
                <small>{{ versionSummary.detail }}</small>
              </div>
            </div>
            <div class="version-details">
              <div><span>应用版本</span><strong>v{{ APP_VERSION }}</strong></div>
              <div v-if="version?.currentRevision"><span>构建标识</span><strong>{{ version.currentRevision.slice(0, 8) }}</strong></div>
              <div v-if="version?.updateAvailable && version.latestRevision"><span>最新构建</span><strong>{{ version.latestRevision.slice(0, 8) }}</strong></div>
            </div>
            <button v-if="version?.updateAvailable" class="button primary full" :disabled="busy() || !version.canRequestUpdate" @click="requestUpdate"><Download :size="15" />立即更新</button>
            <a v-if="version?.repositoryUrl" class="version-repository" :href="version.repositoryUrl" target="_blank" rel="noopener noreferrer">查看项目仓库</a>
          </div>
        </span>
      </div>
      <button v-else class="collapsed-version" :class="{ available: version?.updateAvailable }" title="版本与更新" @click="toggleUpdate"><span>v</span></button>
      <button class="icon-button mobile-close" title="关闭菜单" @click="mobileOpen = false"><X :size="18" /></button>
    </div>

    <nav class="navigation" aria-label="主导航">
      <p v-if="!collapsed" class="nav-section-label">业务模块</p>
      <section v-for="module in modules" :key="module.id" class="nav-module">
        <button class="module-trigger" :class="{ active: route.path.startsWith(`/${module.id}/`) }" :title="collapsed ? module.label : undefined" @click="expanded[module.id] = !expanded[module.id]">
          <component :is="module.icon" :size="19" />
          <span v-if="!collapsed">{{ module.label }}</span>
          <ChevronDown v-if="!collapsed" :size="16" class="chevron" :class="{ open: expanded[module.id] }" />
        </button>
        <div v-show="expanded[module.id] || collapsed" class="nav-children">
          <RouterLink v-for="item in module.navigation" :key="item.to" :to="item.to" class="nav-link" :title="collapsed ? item.label : undefined" @click="mobileOpen = false">
            <component :is="item.icon" :size="18" />
            <span v-if="!collapsed">{{ item.label }}</span>
          </RouterLink>
        </div>
      </section>
    </nav>

    <div class="sidebar-footer">
      <div v-if="!collapsed" class="service-state"><span /><div><strong>服务已连接</strong><small>Running Tools API</small></div></div>
      <button class="collapse-button" :title="collapsed ? '展开侧栏' : '收起侧栏'" @click="collapsed = !collapsed">
        <ChevronsRight v-if="collapsed" :size="18" />
        <ChevronsLeft v-else :size="18" /><span v-if="!collapsed">收起侧栏</span>
      </button>
    </div>
  </aside>
</template>
