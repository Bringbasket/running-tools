<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, CircleOff, Clock3, Download, LoaderCircle, MailCheck, MailOpen, MailPlus, Pencil, Play, Power, RefreshCw, Search, Trash2, X } from '../../../../frontend/src/icons'
import { authState } from '../../../../frontend/src/auth'
import { errorMessage } from '../../../../frontend/src/api'
import { mailAPI } from '../api'
import type { AliasQueueStatus, CreateScheduleStatus, MailAlias, ShareLink } from '../types'
import StatusBadge from '../components/StatusBadge.vue'

const aliases = ref<MailAlias[]>([])
const loading = ref(false)
const error = ref('')
const loadedAt = ref<number | null>(null)
const query = ref('')
const state = ref<'all' | 'active' | 'inactive'>('all')
const workspaceView = ref<'aliases' | 'schedule' | 'queue'>('aliases')
const pageSize = ref(20)
const currentPage = ref(1)
const createOpen = ref(false)
const creating = ref(false)
const label = ref('shopping')
const note = ref('')
const pendingID = ref('')
const editOpen = ref(false)
const editing = ref<MailAlias | null>(null)
const editLabel = ref('')
const editNote = ref('')
const editSaving = ref(false)
const shareOpen = ref(false)
const sharingAlias = ref<MailAlias | null>(null)
const shareLinks = ref<ShareLink[]>([])
const shareExpiry = ref<number | null>(86400)
const shareLoading = ref(false)
const shareNotice = ref('')
const queue = ref<AliasQueueStatus | null>(null)
const queueCount = ref(5)
const queueBaseLabel = ref('shopping')
const queueNote = ref('')
const queueLoading = ref(false)
const schedule = ref<CreateScheduleStatus | null>(null)
const scheduleLoading = ref(false)
const scheduleSaving = ref(false)
const scheduleError = ref('')
const scheduleNotice = ref('')
const batchSize = ref(5)
const aliasIntervalSeconds = ref(3)
const intervalSeconds = ref(180)
const scheduleLabel = ref('shopping')
const scheduleNote = ref('')
let taskPollTimer: ReturnType<typeof window.setTimeout> | undefined
let taskPollGeneration = 0
let previousScheduleRunning = false

const queueFastPollStates = new Set(['queued', 'running'])
const queueWaitingStates = new Set(['waiting_rate_limit', 'waiting_retry'])

const visible = computed(() => aliases.value.filter((alias) => {
  if (state.value === 'active' && alias.isActive === false) return false
  if (state.value === 'inactive' && alias.isActive !== false) return false
  const target = `${alias.hme} ${alias.label || ''} ${alias.note || ''} ${alias.forwardToEmail || ''}`.toLowerCase()
  return target.includes(query.value.toLowerCase())
}))
const activeCount = computed(() => aliases.value.filter((alias) => alias.isActive !== false).length)
const pageCount = computed(() => Math.max(1, Math.ceil(visible.value.length / pageSize.value)))
const pagedAliases = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return visible.value.slice(start, start + pageSize.value)
})
const rangeStart = computed(() => visible.value.length === 0 ? 0 : (currentPage.value - 1) * pageSize.value + 1)
const rangeEnd = computed(() => Math.min(currentPage.value * pageSize.value, visible.value.length))

watch([query, state, pageSize], () => { currentPage.value = 1 })
watch(pageCount, (value) => {
  if (currentPage.value > value) currentPage.value = value
})

async function load() {
  loading.value = true; error.value = ''
  try { aliases.value = await mailAPI.aliases(); loadedAt.value = Date.now() }
  catch (reason) { error.value = errorMessage(reason) }
  finally { loading.value = false }
}

async function loadSchedule(hydrateForm = false) {
  try {
    const value = await mailAPI.createSchedule()
    schedule.value = value
    if (hydrateForm) {
      batchSize.value = value.batchSize
      aliasIntervalSeconds.value = value.aliasIntervalSeconds
      intervalSeconds.value = value.intervalSeconds
      scheduleLabel.value = value.label
      scheduleNote.value = value.note
    }
    if (previousScheduleRunning && !value.running) await load()
    previousScheduleRunning = value.running
  } catch (reason) { scheduleError.value = errorMessage(reason) }
}

function clearTaskPolling() {
  taskPollGeneration++
  if (taskPollTimer !== undefined) {
    window.clearTimeout(taskPollTimer)
    taskPollTimer = undefined
  }
}

function nextTaskPollDelay() {
  if (document.hidden) return null
  if (workspaceView.value === 'schedule') {
    if (schedule.value?.running) return 2500
    if (schedule.value?.enabled) {
      const untilNextRun = (schedule.value.nextRunAt || 0) * 1000 - Date.now()
      return Math.max(2500, Math.min(60000, untilNextRun + 500))
    }
  }
  if (workspaceView.value === 'queue') {
    if (queueFastPollStates.has(queue.value?.status || '')) return 2500
    if (queueWaitingStates.has(queue.value?.status || '')) {
      const untilNextAttempt = (queue.value?.nextAttemptAt || 0) * 1000 - Date.now()
      return Math.max(2500, Math.min(60000, untilNextAttempt + 500))
    }
  }
  return null
}

function scheduleTaskPolling() {
  clearTaskPolling()
  const delay = nextTaskPollDelay()
  if (delay == null) return
  const generation = taskPollGeneration
  taskPollTimer = window.setTimeout(async () => {
    if (generation !== taskPollGeneration) return
    taskPollTimer = undefined
    if (workspaceView.value === 'schedule') await loadSchedule()
    else if (workspaceView.value === 'queue') await loadQueue()
    if (generation === taskPollGeneration) scheduleTaskPolling()
  }, delay)
}

async function selectWorkspace(view: 'aliases' | 'schedule' | 'queue') {
  workspaceView.value = view
  clearTaskPolling()
  if (view === 'schedule') await loadSchedule()
  else if (view === 'queue') await loadQueue()
  scheduleTaskPolling()
}

function handleVisibilityChange() {
  if (document.hidden) {
    clearTaskPolling()
    return
  }
  void selectWorkspace(workspaceView.value)
}

async function saveSchedule(enabled?: boolean) {
  scheduleSaving.value = true; scheduleError.value = ''; scheduleNotice.value = ''
  try {
    schedule.value = await mailAPI.updateCreateSchedule({ enabled, batchSize: batchSize.value, aliasIntervalSeconds: aliasIntervalSeconds.value, intervalSeconds: intervalSeconds.value, label: scheduleLabel.value, note: scheduleNote.value })
    scheduleNotice.value = enabled === true ? '创建计划已开启' : enabled === false ? '创建计划已暂停' : '创建计划已保存'
  } catch (reason) { scheduleError.value = errorMessage(reason) }
  finally { scheduleSaving.value = false; scheduleTaskPolling() }
}

async function runSchedule() {
  scheduleSaving.value = true; scheduleError.value = ''; scheduleNotice.value = ''
  try {
    await mailAPI.updateCreateSchedule({ batchSize: batchSize.value, aliasIntervalSeconds: aliasIntervalSeconds.value, intervalSeconds: intervalSeconds.value, label: scheduleLabel.value, note: scheduleNote.value })
    schedule.value = await mailAPI.runCreateSchedule()
    previousScheduleRunning = true
    scheduleNotice.value = '已开始执行一轮创建'
  }
  catch (reason) { scheduleError.value = errorMessage(reason) }
  finally { scheduleSaving.value = false; scheduleTaskPolling() }
}

async function stopSchedule() {
  scheduleSaving.value = true; scheduleError.value = ''; scheduleNotice.value = ''
  try { schedule.value = await mailAPI.stopCreateSchedule(); scheduleNotice.value = '创建计划已暂停' }
  catch (reason) { scheduleError.value = errorMessage(reason) }
  finally { scheduleSaving.value = false; scheduleTaskPolling() }
}

function formatScheduleTime(value: number | null | undefined) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value * 1000))
}

function formatRemaining(value: number | null | undefined) {
  if (value == null) return '执行中'
  if (value < 60) return `${value} 秒`
  return `${Math.floor(value / 60)} 分 ${value % 60} 秒`
}

async function create() {
  if (!label.value.trim()) return
  creating.value = true; error.value = ''
  try { aliases.value.unshift(await mailAPI.createAlias(label.value, note.value)); createOpen.value = false; note.value = '' }
  catch (reason) { error.value = errorMessage(reason) }
  finally { creating.value = false }
}

async function runAction(alias: MailAlias, action: 'enable' | 'disable' | 'delete') {
  if (action === 'delete' && !confirm(`确定删除 ${alias.hme}？`)) return
  pendingID.value = alias.anonymousId; error.value = ''
  try { await mailAPI.aliasAction(alias.anonymousId, action); await load() }
  catch (reason) { error.value = errorMessage(reason) }
  finally { pendingID.value = '' }
}

function openEdit(alias: MailAlias) { editing.value = alias; editLabel.value = alias.label || ''; editNote.value = alias.note || ''; editOpen.value = true }
async function openShare(alias: MailAlias) { sharingAlias.value = alias; shareOpen.value = true; shareLoading.value = true; shareNotice.value = ''; try { shareLinks.value = (await mailAPI.shareLinks(alias.anonymousId)).links } catch (reason) { error.value = errorMessage(reason) } finally { shareLoading.value = false } }
async function createShare() { if (!sharingAlias.value) return; shareLoading.value = true; shareNotice.value = ''; try { const created = await mailAPI.createShareLink(sharingAlias.value.anonymousId, shareExpiry.value); shareLinks.value.unshift(created); shareNotice.value = `链接已生成：${location.origin}${created.shareUrl}` } catch (reason) { error.value = errorMessage(reason) } finally { shareLoading.value = false } }
async function revokeShare(link: ShareLink) { shareLoading.value = true; try { await mailAPI.revokeShareLink(link.id); link.active = false; link.revokedAt = Date.now() / 1000 } catch (reason) { error.value = errorMessage(reason) } finally { shareLoading.value = false } }
async function loadQueue() { try { queue.value = await mailAPI.aliasQueue() } catch (reason) { error.value = errorMessage(reason) } }
async function enqueueQueue() { queueLoading.value = true; try { queue.value = await mailAPI.enqueueAliases(queueBaseLabel.value, queueCount.value, queueNote.value, crypto.randomUUID()) } catch (reason) { error.value = errorMessage(reason) } finally { queueLoading.value = false; scheduleTaskPolling() } }
async function controlQueue(action: 'pause' | 'resume' | 'cancel') { if (!queue.value?.jobId) return; queueLoading.value = true; try { const confirmUncertain = action === 'resume' && queue.value.status === 'needs_attention' && confirm('上次保留结果可能不明确。系统会先检查邮箱列表；若未找到候选地址，是否确认继续创建？'); queue.value = await mailAPI.aliasQueueControl(action, queue.value.jobId, confirmUncertain) } catch (reason) { error.value = errorMessage(reason) } finally { queueLoading.value = false; scheduleTaskPolling() } }
async function saveEdit() {
  if (!editing.value || !editLabel.value.trim()) return
  editSaving.value = true; error.value = ''
  try { const updated = await mailAPI.updateAlias(editing.value.anonymousId, editLabel.value, editNote.value); const index = aliases.value.findIndex((item) => item.anonymousId === editing.value?.anonymousId); if (index >= 0) aliases.value[index] = { ...aliases.value[index], ...updated, label: editLabel.value, note: editNote.value }; editOpen.value = false }
  catch (reason) { error.value = errorMessage(reason) }
  finally { editSaving.value = false }
}

async function exportCSV() {
  error.value = ''
  try {
    const response = await fetch('/api/mail/v1/aliases/export.csv', { headers: { 'X-API-Key': authState.apiKey } })
    if (!response.ok) throw new Error(`导出失败：HTTP ${response.status}`)
    const url = URL.createObjectURL(await response.blob())
    const anchor = document.createElement('a'); anchor.href = url; anchor.download = 'hide-my-email.csv'; anchor.click(); URL.revokeObjectURL(url)
  } catch (reason) { error.value = errorMessage(reason) }
}

function formatDate(value?: number) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value > 1e12 ? value : value * 1000))
}

onMounted(async () => {
  await Promise.all([load(), loadSchedule(true), loadQueue()])
  document.addEventListener('visibilitychange', handleVisibilityChange)
  scheduleTaskPolling()
})

onBeforeUnmount(() => {
  clearTaskPolling()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>

<template>
  <section class="page mail-aliases">
    <div class="page-heading">
      <div><h2>邮箱管理</h2><p>管理 iCloud+ 隐藏邮箱与服务器创建任务</p></div>
      <div class="page-actions"><button class="button ghost" :disabled="loading" @click="load"><RefreshCw :size="16" :class="{ spin: loading }" />刷新</button><button class="button primary" @click="createOpen = true"><MailPlus :size="17" />创建邮箱</button></div>
    </div>

    <div class="alias-overview" aria-label="邮箱概览">
      <div><MailOpen :size="17" /><span>全部邮箱</span><strong>{{ aliases.length }}</strong></div>
      <div><MailCheck :size="17" /><span>正常转发</span><strong>{{ activeCount }}</strong></div>
      <div><CircleOff :size="17" /><span>已停用</span><strong>{{ aliases.length - activeCount }}</strong></div>
      <span class="overview-update">{{ loadedAt ? `更新于 ${formatDate(loadedAt)}` : '尚未加载' }}</span>
    </div>

    <section class="mail-workspace">
      <div class="workspace-tabs" role="tablist" aria-label="邮箱管理视图">
        <button :class="{ active: workspaceView === 'aliases' }" role="tab" :aria-selected="workspaceView === 'aliases'" @click="selectWorkspace('aliases')"><MailOpen :size="16" />邮箱列表<span>{{ aliases.length }}</span></button>
        <button :class="{ active: workspaceView === 'schedule' }" role="tab" :aria-selected="workspaceView === 'schedule'" @click="selectWorkspace('schedule')"><Clock3 :size="16" />自动创建<span class="tab-state" :class="{ running: schedule?.enabled || schedule?.running }">{{ schedule?.running ? '执行中' : schedule?.enabled ? '已开启' : '已暂停' }}</span></button>
        <button :class="{ active: workspaceView === 'queue' }" role="tab" :aria-selected="workspaceView === 'queue'" @click="selectWorkspace('queue')"><MailPlus :size="16" />批量队列<span class="tab-state" :class="{ running: queue?.workerRunning }">{{ queue?.workerRunning ? `${queue.success}/${queue.requested}` : '空闲' }}</span></button>
      </div>

      <div v-if="workspaceView === 'aliases'" class="aliases-pane">
        <p v-if="error" class="message error pane-message">{{ error }}</p>
        <div class="list-toolbar">
          <label class="search-field"><Search :size="16" /><input v-model="query" placeholder="搜索邮箱、标签、备注或转发地址" /></label>
          <div class="segmented" aria-label="状态筛选"><button v-for="option in ['all', 'active', 'inactive'] as const" :key="option" :class="{ active: state === option }" @click="state = option">{{ { all: '全部', active: '启用', inactive: '停用' }[option] }}</button></div>
          <button class="button ghost export-button" title="导出 CSV" @click="exportCSV"><Download :size="16" /><span>导出</span></button>
        </div>
        <div class="list-meta">
          <span>邮箱列表 <small>共 {{ visible.length }} 条</small></span>
          <label class="page-size">每页<select v-model.number="pageSize"><option :value="10">10 条</option><option :value="20">20 条</option><option :value="50">50 条</option><option :value="100">100 条</option></select></label>
        </div>
        <div class="data-frame">
          <table>
            <thead><tr><th>邮箱地址</th><th>标签 / 备注</th><th>转发到</th><th>状态</th><th>创建时间</th><th><span class="sr-only">操作</span></th></tr></thead>
            <tbody>
              <tr v-for="alias in pagedAliases" :key="alias.anonymousId">
                <td data-label="邮箱地址"><strong class="address">{{ alias.hme }}</strong><small>{{ alias.origin || 'iCloud+' }}</small></td>
                <td data-label="标签 / 备注"><span>{{ alias.label || '未命名' }}</span><small>{{ alias.note || '无备注' }}</small></td>
                <td data-label="转发到"><span class="forward-address">{{ alias.forwardToEmail || '—' }}</span></td>
                <td data-label="状态"><StatusBadge :state="alias.isActive === false ? 'inactive' : 'active'" :label="alias.isActive === false ? '已停用' : '转发中'" /></td>
                <td data-label="创建时间">{{ formatDate(alias.createTimestamp) }}</td>
                <td class="row-actions"><LoaderCircle v-if="pendingID === alias.anonymousId" :size="18" class="spin" /><template v-else><button class="icon-button" title="编辑标签和备注" @click="openEdit(alias)"><Pencil :size="16" /></button><button class="icon-button" title="生成分享链接" @click="openShare(alias)"><MailPlus :size="16" /></button><button class="icon-button" :title="alias.isActive === false ? '重新启用邮箱' : '停用邮箱'" @click="runAction(alias, alias.isActive === false ? 'enable' : 'disable')"><Power :size="16" /></button><button class="icon-button danger" title="删除邮箱" @click="runAction(alias, 'delete')"><Trash2 :size="16" /></button></template></td>
              </tr>
              <tr v-if="!loading && visible.length === 0"><td colspan="6" class="empty-state"><MailOpen :size="28" /><strong>没有符合条件的邮箱</strong><span>调整关键词或状态筛选后重试</span></td></tr>
              <tr v-if="loading && aliases.length === 0"><td colspan="6" class="empty-state"><LoaderCircle :size="22" class="spin" /><strong>正在读取邮箱</strong></td></tr>
            </tbody>
          </table>
        </div>
        <div class="pagination-bar">
          <span>显示 {{ rangeStart }}–{{ rangeEnd }}，共 {{ visible.length }} 条</span>
          <div><button class="icon-button" title="上一页" :disabled="currentPage <= 1" @click="currentPage--"><ChevronLeft :size="17" /></button><strong>第 {{ currentPage }} / {{ pageCount }} 页</strong><button class="icon-button" title="下一页" :disabled="currentPage >= pageCount" @click="currentPage++"><ChevronRight :size="17" /></button></div>
        </div>
      </div>

      <div v-else-if="workspaceView === 'schedule'" class="task-pane">
        <div class="task-header"><div><h3>自动创建计划</h3><span v-if="schedule?.nextRunAt">下次执行 {{ formatScheduleTime(schedule.nextRunAt) }}</span></div><div class="schedule-actions"><span class="schedule-status" :class="{ active: schedule?.running || schedule?.enabled }"><i />{{ schedule?.running ? `创建中 ${schedule.currentIndex}/${schedule.currentTotal}` : schedule?.enabled ? `已开启 · ${formatRemaining(schedule.remainingSeconds)}` : '已暂停' }}</span><button class="button ghost" :disabled="scheduleSaving || schedule?.running" @click="runSchedule"><Play :size="15" />立即执行</button><button v-if="schedule?.enabled || schedule?.running" class="button ghost" :disabled="scheduleSaving" @click="stopSchedule"><Power :size="15" />暂停</button></div></div>
        <p v-if="scheduleError" class="message error">{{ scheduleError }}</p><p v-if="scheduleNotice" class="message success">{{ scheduleNotice }}</p>
        <div class="schedule-form">
          <label class="field"><span>每轮数量 <small>1 - 20 个</small></span><input v-model.number="batchSize" type="number" min="1" max="20" /></label>
          <label class="field"><span>邮箱间隔 <small>秒</small></span><input v-model.number="aliasIntervalSeconds" type="number" min="1" max="3600" /></label>
          <label class="field"><span>执行周期 <small>至少 60 秒</small></span><input v-model.number="intervalSeconds" type="number" min="60" /></label>
          <label class="field"><span>统一标签</span><input v-model="scheduleLabel" maxlength="100" /></label>
          <label class="field schedule-note"><span>备注 <small>可留空</small></span><input v-model="scheduleNote" maxlength="500" /></label>
          <div class="schedule-save"><span v-if="schedule?.lastBatchStoppedReason">最近状态：{{ schedule.lastBatchStoppedReason }}<small v-if="schedule.lastRunAt"> · {{ formatScheduleTime(schedule.lastRunAt) }}</small></span><button class="button primary" :disabled="scheduleSaving || schedule?.running" @click="saveSchedule(schedule?.enabled)"><LoaderCircle v-if="scheduleSaving" :size="15" class="spin" />保存参数</button><button class="button secondary" :disabled="scheduleSaving || schedule?.enabled" @click="saveSchedule(true)"><Play :size="15" />保存并开启</button></div>
        </div>
        <div class="schedule-meta"><span>本轮成功 {{ schedule?.running ? schedule.currentSuccess : schedule?.lastBatchSuccess || 0 }} / {{ schedule?.running ? schedule.currentTotal : schedule?.lastBatchRequested || batchSize }} 个</span><span v-if="schedule?.lastError" class="schedule-error">{{ schedule.lastError }}</span></div>
      </div>

      <div v-else class="task-pane">
        <div class="task-header"><div><h3>批量创建队列</h3><span>进度 {{ queue?.success || 0 }} / {{ queue?.requested || queueCount }}</span></div><div class="schedule-actions"><span class="schedule-status" :class="{ active: queue?.status === 'running' || queue?.status === 'queued' }"><i />{{ ({ idle: '空闲', queued: '排队中', running: '创建中', paused: '已暂停', waiting_rate_limit: '限流等待', waiting_retry: '等待重试', needs_attention: '需要确认', completed: '已完成', cancelled: '已取消' } as Record<string, string>)[queue?.status || 'idle'] }}</span><button v-if="queue?.status === 'running' || queue?.status === 'queued' || queue?.status === 'waiting_rate_limit' || queue?.status === 'waiting_retry'" class="button ghost" :disabled="queueLoading" @click="controlQueue('pause')">暂停</button><button v-if="queue?.status === 'paused' || queue?.status === 'needs_attention'" class="button ghost" :disabled="queueLoading" @click="controlQueue('resume')">继续</button><button v-if="queue?.status && !['idle','completed','cancelled'].includes(queue.status)" class="button ghost" :disabled="queueLoading" @click="controlQueue('cancel')">取消</button></div></div>
        <p v-if="error" class="message error">{{ error }}</p>
        <div class="schedule-form queue-form">
          <label class="field"><span>创建数量 <small>1 - 99 个</small></span><input v-model.number="queueCount" type="number" min="1" max="99" /></label>
          <label class="field"><span>基础标签</span><input v-model="queueBaseLabel" maxlength="100" /></label>
          <label class="field schedule-note"><span>备注 <small>可留空</small></span><input v-model="queueNote" maxlength="500" /></label>
          <div class="schedule-save"><span><small v-if="queue?.lastError">{{ queue.lastError }}</small></span><button class="button primary" :disabled="queueLoading || (queue?.status && !['idle','completed','cancelled'].includes(queue.status))" @click="enqueueQueue"><LoaderCircle v-if="queueLoading" :size="15" class="spin" />加入队列</button></div>
        </div>
      </div>
    </section>
  </section>

  <Teleport to="body">
    <div v-if="createOpen" class="dialog-backdrop" @click.self="createOpen = false">
      <form class="dialog" @submit.prevent="create">
        <div class="dialog-heading"><div><h2>创建隐藏邮箱</h2></div><button type="button" class="icon-button" title="关闭" @click="createOpen = false"><X :size="18" /></button></div>
        <label class="field"><span>标签</span><input v-model="label" maxlength="100" required /></label>
        <label class="field"><span>备注</span><textarea v-model="note" rows="3" maxlength="500" placeholder="可留空" /></label>
        <div class="dialog-actions"><button type="button" class="button ghost" @click="createOpen = false">取消</button><button class="button primary" :disabled="creating"><LoaderCircle v-if="creating" :size="16" class="spin" />创建</button></div>
      </form>
    </div>
  </Teleport>
  <Teleport to="body">
    <div v-if="editOpen" class="dialog-backdrop" @click.self="editOpen = false">
      <form class="dialog" @submit.prevent="saveEdit">
        <div class="dialog-heading"><div><h2>编辑邮箱信息</h2></div><button type="button" class="icon-button" title="关闭" @click="editOpen = false"><X :size="18" /></button></div>
        <label class="field"><span>标签</span><input v-model="editLabel" maxlength="100" required /></label>
        <label class="field"><span>备注</span><textarea v-model="editNote" rows="3" maxlength="500" /></label>
        <div class="dialog-actions"><button type="button" class="button ghost" @click="editOpen = false">取消</button><button class="button primary" :disabled="editSaving"><LoaderCircle v-if="editSaving" :size="16" class="spin" />保存</button></div>
      </form>
    </div>
  </Teleport>
  <Teleport to="body">
    <div v-if="shareOpen" class="dialog-backdrop" @click.self="shareOpen = false">
      <div class="dialog share-dialog">
        <div class="dialog-heading"><div><h2>分享收件地址</h2><p>{{ sharingAlias?.hme }}</p></div><button class="icon-button" title="关闭" @click="shareOpen = false"><X :size="18" /></button></div>
        <label class="field"><span>有效期</span><select v-model="shareExpiry"><option :value="3600">1 小时</option><option :value="86400">1 天</option><option :value="604800">7 天</option><option :value="2592000">30 天</option><option :value="null">永久</option></select></label>
        <button class="button primary" :disabled="shareLoading" @click="createShare"><LoaderCircle v-if="shareLoading" :size="16" class="spin" />生成分享链接</button>
        <p v-if="shareNotice" class="message success">{{ shareNotice }}</p>
        <div class="share-list"><div v-for="link in shareLinks" :key="link.id" class="share-item"><span>{{ link.active ? '有效' : '已撤销' }} · {{ link.expiresAt ? formatDate(link.expiresAt) : '永久' }}</span><button v-if="link.active" class="button ghost" @click="revokeShare(link)">撤销</button></div><p v-if="!shareLoading && shareLinks.length === 0" class="muted">暂无分享链接</p></div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.mail-aliases { max-width: 1480px; }
.alias-overview { display: flex; min-height: 54px; align-items: center; gap: 0; margin-bottom: 16px; padding: 0 16px; color: var(--muted); background: var(--surface); border: 1px solid var(--border); border-radius: 7px; }
.alias-overview > div { display: flex; min-width: 155px; align-items: center; gap: 8px; padding-right: 24px; font-size: 12px; }
.alias-overview > div + div { padding-left: 24px; border-left: 1px solid var(--border-soft); }
.alias-overview svg { color: #64748b; }
.alias-overview > div:nth-child(2) svg { color: #059669; }
.alias-overview > div:nth-child(3) svg { color: #dc2626; }
.alias-overview strong { margin-left: auto; color: var(--text); font-size: 18px; font-variant-numeric: tabular-nums; }
.overview-update { margin-left: auto; color: var(--muted); font-size: 11px; }
.mail-workspace { overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 7px; }
.workspace-tabs { display: flex; min-height: 53px; align-items: stretch; gap: 4px; padding: 0 16px; background: var(--surface); border-bottom: 1px solid var(--border); }
.workspace-tabs button { position: relative; display: inline-flex; min-width: 150px; align-items: center; justify-content: center; gap: 8px; padding: 0 15px; color: var(--muted); background: transparent; border: 0; font-size: 12px; font-weight: 650; }
.workspace-tabs button::after { position: absolute; right: 10px; bottom: -1px; left: 10px; height: 2px; content: ''; background: transparent; }
.workspace-tabs button:hover { color: var(--text); }
.workspace-tabs button.active { color: var(--text); }
.workspace-tabs button.active::after { background: var(--primary); }
.workspace-tabs button > span { display: inline-flex; min-width: 22px; height: 20px; align-items: center; justify-content: center; padding: 0 6px; color: var(--muted); background: var(--surface-subtle); border: 1px solid var(--border-soft); border-radius: 4px; font-size: 10px; font-weight: 600; }
.workspace-tabs .tab-state { min-width: auto; }
.workspace-tabs .tab-state.running { color: #047857; background: #ecfdf5; border-color: #d1fae5; }
:root[data-theme="dark"] .workspace-tabs .tab-state.running { color: #6ee7b7; background: rgba(16,185,129,.12); border-color: rgba(16,185,129,.2); }
.aliases-pane { min-width: 0; }
.pane-message { margin: 14px 16px 0; }
.list-toolbar { display: flex; min-height: 65px; align-items: center; gap: 12px; padding: 12px 16px; border-bottom: 1px solid var(--border-soft); }
.list-toolbar .search-field { max-width: 520px; }
.list-toolbar .segmented { margin-left: auto; }
.list-meta { display: flex; min-height: 48px; align-items: center; justify-content: space-between; gap: 16px; padding: 0 16px; }
.list-meta > span { color: var(--text); font-size: 13px; font-weight: 700; }
.list-meta > span small { margin-left: 7px; color: var(--muted); font-size: 11px; font-weight: 500; }
.page-size { display: inline-flex; align-items: center; gap: 8px; color: var(--muted); font-size: 11px; }
.page-size select { min-height: 31px; padding: 0 27px 0 9px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; outline: none; font-size: 11px; }
.pagination-bar { display: flex; min-height: 54px; align-items: center; justify-content: space-between; gap: 14px; padding: 0 16px; color: var(--muted); border-top: 1px solid var(--border); font-size: 11px; }
.pagination-bar > div { display: flex; align-items: center; gap: 9px; }
.pagination-bar strong { min-width: 82px; color: var(--text); font-size: 11px; font-weight: 600; text-align: center; }
.pagination-bar .icon-button { width: 31px; height: 31px; }
.task-pane { min-height: 390px; padding: 24px; }
.task-header { display: flex; align-items: center; justify-content: space-between; gap: 18px; padding-bottom: 20px; border-bottom: 1px solid var(--border-soft); }
.task-header h3 { margin: 0; color: var(--text); font-size: 16px; font-weight: 700; }
.task-header > div:first-child > span { display: block; margin-top: 5px; color: var(--muted); font-size: 11px; }
.share-dialog { max-width: 520px; }
.share-dialog p { margin: 4px 0 0; color: var(--muted); font-size: 12px; }
.share-list { display: grid; gap: 8px; margin-top: 16px; max-height: 220px; overflow: auto; }
.share-item { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px; background: var(--surface-subtle); border: 1px solid var(--border-soft); border-radius: 6px; font-size: 12px; }
.muted { color: var(--muted); }
.schedule-actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; flex-wrap: wrap; }
.schedule-status { display: inline-flex; min-height: 32px; align-items: center; gap: 7px; padding: 0 10px; color: var(--muted); background: var(--surface-subtle); border: 1px solid var(--border); border-radius: 7px; font-size: 11px; white-space: nowrap; }
.schedule-status i { width: 7px; height: 7px; background: var(--muted); border-radius: 50%; }
.schedule-status.active { color: var(--primary-text); background: var(--primary-soft); border-color: color-mix(in srgb, var(--primary) 22%, transparent); }
.schedule-status.active i { background: #10b981; box-shadow: 0 0 0 3px rgba(16, 185, 129, .12); }
.schedule-form { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; margin-top: 22px; }
.schedule-form .field { margin: 0; }
.schedule-form .field small { float: right; color: var(--muted); font-size: 10px; font-weight: 500; }
.schedule-note { grid-column: span 2; }
.queue-form { grid-template-columns: minmax(160px, .6fr) minmax(220px, 1fr) minmax(240px, 1.4fr); }
.schedule-save { grid-column: 1 / -1; display: flex; align-items: center; justify-content: flex-end; gap: 8px; margin-top: 2px; }
.schedule-save > span { flex: 1; min-width: 0; color: var(--muted); font-size: 11px; }
.schedule-save > span small { color: var(--muted); }
.schedule-meta { display: flex; align-items: center; gap: 16px; margin-top: 14px; color: var(--muted); font-size: 11px; flex-wrap: wrap; }
.schedule-error { color: var(--danger); max-width: 100%; overflow-wrap: anywhere; }
@media (max-width: 900px) { .overview-update { display: none; } .alias-overview > div { flex: 1; min-width: 0; } .schedule-form, .queue-form { grid-template-columns: repeat(2, minmax(0, 1fr)); } .schedule-note { grid-column: auto; } }
@media (max-width: 620px) {
  .alias-overview { padding: 0 10px; }
  .alias-overview > div { min-width: 0; flex-direction: column; gap: 2px; padding: 8px; text-align: center; }
  .alias-overview > div + div { padding-left: 8px; }
  .alias-overview svg { display: none; }
  .alias-overview strong { margin: 0; font-size: 17px; }
  .workspace-tabs { gap: 0; padding: 0 6px; overflow-x: auto; }
  .workspace-tabs button { min-width: max-content; flex: 1; padding: 0 11px; }
  .workspace-tabs button > span { display: none; }
  .list-toolbar { align-items: stretch; flex-wrap: wrap; padding: 11px; }
  .list-toolbar .search-field { max-width: none; flex-basis: 100%; }
  .list-toolbar .segmented { flex: 1; margin-left: 0; }
  .list-toolbar .segmented button { flex: 1; }
  .list-meta { padding: 0 12px; }
  .pagination-bar { align-items: flex-start; flex-direction: column; padding: 11px 12px; }
  .pagination-bar > div { width: 100%; justify-content: space-between; }
  .task-pane { min-height: 0; padding: 16px; }
  .task-header { align-items: flex-start; flex-direction: column; }
  .schedule-actions { justify-content: flex-start; width: 100%; }
  .schedule-form, .queue-form { grid-template-columns: minmax(0, 1fr); }
  .schedule-note { grid-column: auto; }
  .schedule-save { align-items: stretch; flex-direction: column; }
  .schedule-save > span { flex: initial; }
  .schedule-save .button { width: 100%; }
}
</style>
