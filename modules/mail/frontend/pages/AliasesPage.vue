<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, CircleOff, Clock3, Copy, Download, LoaderCircle, MailCheck, MailOpen, MailPlus, Pencil, Play, Power, RefreshCw, Search, Trash2 } from '../../../../frontend/src/icons'
import { mailAccountState } from '../account'
import { errorMessage } from '../../../../frontend/src/api'
import AppDialog from '../../../../frontend/src/components/AppDialog.vue'
import AsyncState from '../../../../frontend/src/components/AsyncState.vue'
import { showToast } from '../../../../frontend/src/toast'
import { mailAPI } from '../api'
import type { BatchShareLinkItem, CreateScheduleStatus, MailAlias, ShareLink } from '../types'
import StatusBadge from '../components/StatusBadge.vue'

const aliases = ref<MailAlias[]>([])
const loading = ref(false)
const error = ref('')
const loadedAt = ref<number | null>(null)
const query = ref('')
const state = ref<'all' | 'active' | 'inactive'>('all')
const workspaceView = ref<'aliases' | 'schedule'>('aliases')
const pageSize = ref(10)
const currentPage = ref(1)
const pendingID = ref('')
const editOpen = ref(false)
const editing = ref<MailAlias | null>(null)
const editLabel = ref('')
const editNote = ref('')
const editSaving = ref(false)
const editError = ref('')
const shareOpen = ref(false)
const sharingAlias = ref<MailAlias | null>(null)
const shareLinks = ref<ShareLink[]>([])
const shareExpiry = ref<number | null>(86400)
const shareLoading = ref(false)
const shareNotice = ref('')
const shareError = ref('')
const shareCreatedURL = ref('')
const copiedShareID = ref('')
const batchShareOpen = ref(false)
const batchShareCount = ref(5)
const batchShareScope = ref<'all' | 'gpt_registered' | 'gpt_unregistered'>('all')
const batchShareExpiryPreset = ref<'3600' | '86400' | '604800' | '2592000' | 'never' | 'custom'>('86400')
const batchShareExpiryAt = ref('')
const batchShareResults = ref<BatchShareLinkItem[]>([])
const batchShareLoading = ref(false)
const batchShareError = ref('')
const clearSharesOpen = ref(false)
const clearSharesError = ref('')
const clearSharesLoading = ref(false)
const clearSharesReturnToShare = ref(false)
const deleteTarget = ref<MailAlias | null>(null)
const deleteConfirm = ref('')
const deleteError = ref('')
const schedule = ref<CreateScheduleStatus | null>(null)
const scheduleLoading = ref(false)
const scheduleSaving = ref(false)
const scheduleError = ref('')
const batchSize = ref(5)
const aliasIntervalSeconds = ref(3)
const intervalSeconds = ref(180)
const scheduleLabel = ref('shopping')
const scheduleNote = ref('')
let taskPollTimer: ReturnType<typeof window.setTimeout> | undefined
let taskPollGeneration = 0
let previousScheduleRunning = false

const visible = computed(() => aliases.value.filter((alias) => {
  if (state.value === 'active' && alias.isActive === false) return false
  if (state.value === 'inactive' && alias.isActive !== false) return false
  const applications = (alias.registeredApps || []).map((item) => `${item.key} ${item.label} ${item.status === 'confirmed' ? '已确认' : '已注册'}`).join(' ')
  const target = `${alias.hme} ${alias.label || ''} ${alias.note || ''} ${alias.forwardToEmail || ''} ${applications}`.toLowerCase()
  return target.includes(query.value.toLowerCase())
}))
const activeCount = computed(() => aliases.value.filter((alias) => alias.isActive !== false).length)
const gptRegisteredActiveCount = computed(() => aliases.value.filter((alias) => alias.isActive !== false && alias.registeredApps?.some((application) => application.key === 'gpt' && (application.status === 'observed' || application.status === 'confirmed'))).length)
const gptUnregisteredActiveCount = computed(() => activeCount.value - gptRegisteredActiveCount.value)
const batchShareAvailableCount = computed(() => batchShareScope.value === 'all' ? activeCount.value : batchShareScope.value === 'gpt_registered' ? gptRegisteredActiveCount.value : gptUnregisteredActiveCount.value)
const pageCount = computed(() => Math.max(1, Math.ceil(visible.value.length / pageSize.value)))
const pagedAliases = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return visible.value.slice(start, start + pageSize.value)
})
const rangeStart = computed(() => visible.value.length === 0 ? 0 : (currentPage.value - 1) * pageSize.value + 1)
const rangeEnd = computed(() => Math.min(currentPage.value * pageSize.value, visible.value.length))

watch([query, state, pageSize], () => { currentPage.value = 1 })
watch(batchShareScope, () => {
  batchShareError.value = ''
  batchShareResults.value = []
  batchShareCount.value = Math.min(5, Math.max(1, batchShareAvailableCount.value))
})
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
    if (generation === taskPollGeneration) scheduleTaskPolling()
  }, delay)
}

async function selectWorkspace(view: 'aliases' | 'schedule') {
  workspaceView.value = view
  clearTaskPolling()
  if (view === 'schedule') await loadSchedule()
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
	scheduleSaving.value = true; scheduleError.value = ''
	try {
		schedule.value = await mailAPI.updateCreateSchedule({ enabled, batchSize: batchSize.value, aliasIntervalSeconds: aliasIntervalSeconds.value, intervalSeconds: intervalSeconds.value, label: scheduleLabel.value, note: scheduleNote.value })
		showToast(enabled === true ? '创建计划已开启' : enabled === false ? '创建计划已暂停' : '创建计划已保存')
  } catch (reason) { scheduleError.value = errorMessage(reason) }
  finally { scheduleSaving.value = false; scheduleTaskPolling() }
}

async function runSchedule() {
	scheduleSaving.value = true; scheduleError.value = ''
  try {
    await mailAPI.updateCreateSchedule({ batchSize: batchSize.value, aliasIntervalSeconds: aliasIntervalSeconds.value, intervalSeconds: intervalSeconds.value, label: scheduleLabel.value, note: scheduleNote.value })
    schedule.value = await mailAPI.runCreateSchedule()
    previousScheduleRunning = true
		showToast('已开始执行一轮创建', 'info')
  }
  catch (reason) { scheduleError.value = errorMessage(reason) }
  finally { scheduleSaving.value = false; scheduleTaskPolling() }
}

async function stopSchedule() {
	scheduleSaving.value = true; scheduleError.value = ''
	try { schedule.value = await mailAPI.stopCreateSchedule(); showToast('创建计划已暂停') }
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

function createChannelLabel(value: string | null | undefined) {
  return value === 'apple_account' ? 'Apple Account' : value === 'icloud_web' ? 'iCloud Web' : '尚无记录'
}

async function runAction(alias: MailAlias, action: 'enable' | 'disable' | 'delete') {
	if (action === 'delete') {
		deleteTarget.value = alias
		deleteConfirm.value = ''
		deleteError.value = ''
		return
	}
	await performAliasAction(alias, action)
}

async function performAliasAction(alias: MailAlias, action: 'enable' | 'disable' | 'delete') {
	pendingID.value = alias.anonymousId; error.value = ''
	try {
		await mailAPI.aliasAction(alias.anonymousId, action)
		await load()
		if (action === 'delete') deleteTarget.value = null
		showToast(action === 'delete' ? `已删除 ${alias.hme}` : action === 'enable' ? `已启用 ${alias.hme}` : `已停用 ${alias.hme}`)
	} catch (reason) {
		const message = errorMessage(reason)
		if (action === 'delete') deleteError.value = message
		else error.value = message
	}
	finally { pendingID.value = '' }
}

function openEdit(alias: MailAlias) { editing.value = alias; editLabel.value = alias.label || ''; editNote.value = alias.note || ''; editError.value = ''; editOpen.value = true }
async function openShare(alias: MailAlias) { sharingAlias.value = alias; shareOpen.value = true; shareLoading.value = true; shareNotice.value = ''; shareCreatedURL.value = ''; shareError.value = ''; try { shareLinks.value = (await mailAPI.shareLinks(alias.anonymousId)).links } catch (reason) { shareError.value = errorMessage(reason) } finally { shareLoading.value = false } }
async function createShare() { if (!sharingAlias.value) return; shareLoading.value = true; shareNotice.value = ''; shareCreatedURL.value = ''; shareError.value = ''; try { const created = await mailAPI.createShareLink(sharingAlias.value.anonymousId, shareExpiry.value); shareLinks.value.unshift(created); shareCreatedURL.value = fullShareURL(created.shareUrl); shareNotice.value = '链接已生成，可复制后分享给收件人' } catch (reason) { shareError.value = errorMessage(reason) } finally { shareLoading.value = false } }
function fullShareURL(path?: string) { return path ? new URL(path, location.origin).toString() : '' }
async function copyShareURL(url: string, id: string) {
  if (!url) return
  try {
    await navigator.clipboard.writeText(url)
    copiedShareID.value = id
    showToast('取件地址已复制', 'success')
    window.setTimeout(() => { if (copiedShareID.value === id) copiedShareID.value = '' }, 1600)
  } catch {
    const message = '复制失败，请手动选择地址复制'
    shareError.value = message
    batchShareError.value = message
  }
}
function batchExpirySeconds() {
  if (batchShareExpiryPreset.value === 'never') return null
  if (batchShareExpiryPreset.value === 'custom') {
    const timestamp = new Date(batchShareExpiryAt.value).getTime()
    if (!Number.isFinite(timestamp)) throw new Error('请选择有效的失效时间')
    const seconds = Math.ceil((timestamp - Date.now()) / 1000)
    if (seconds < 300 || seconds > 365 * 24 * 60 * 60) throw new Error('自定义失效时间必须在 5 分钟至 365 天内')
    return seconds
  }
  return Number(batchShareExpiryPreset.value)
}
function openBatchShare() { batchShareOpen.value = true; batchShareError.value = ''; batchShareResults.value = []; batchShareScope.value = 'all'; batchShareCount.value = Math.min(5, Math.max(1, activeCount.value)); batchShareExpiryPreset.value = '86400'; batchShareExpiryAt.value = '' }
function closeBatchShare() { if (!batchShareLoading.value) batchShareOpen.value = false }
async function createBatchShare() {
  batchShareLoading.value = true; batchShareError.value = ''
  try {
    if (!Number.isInteger(batchShareCount.value) || batchShareCount.value < 1 || batchShareCount.value > 750) throw new Error('邮箱数量必须是 1 到 750 之间的整数')
    batchShareResults.value = []
    batchShareResults.value = (await mailAPI.createBatchShareLinks(batchShareCount.value, batchExpirySeconds(), batchShareScope.value)).items
  } catch (reason) { batchShareError.value = errorMessage(reason) }
  finally { batchShareLoading.value = false }
}
function downloadBatchShareTXT() {
  if (!batchShareResults.value.length) return
  const content = batchShareResults.value.map((item) => `${item.alias}----${fullShareURL(item.shareUrl)}`).join('\n')
  const url = URL.createObjectURL(new Blob([content], { type: 'text/plain;charset=utf-8' }))
  const anchor = document.createElement('a'); anchor.href = url; anchor.download = 'hme-retrieval-links.txt'; anchor.click(); URL.revokeObjectURL(url)
}
async function revokeShare(link: ShareLink) { shareLoading.value = true; shareError.value = ''; try { await mailAPI.revokeShareLink(link.id); link.active = false; link.revokedAt = Date.now() / 1000; showToast('分享链接已撤销') } catch (reason) { shareError.value = errorMessage(reason) } finally { shareLoading.value = false } }
function openClearShares(returnToShare: boolean) {
	clearSharesError.value = ''
	clearSharesReturnToShare.value = returnToShare && Boolean(sharingAlias.value)
	if (clearSharesReturnToShare.value) shareOpen.value = false
	clearSharesOpen.value = true
}
function closeClearShares() {
	if (clearSharesLoading.value) return
	clearSharesOpen.value = false
	shareOpen.value = clearSharesReturnToShare.value && Boolean(sharingAlias.value)
	clearSharesReturnToShare.value = false
}
async function clearInactiveShares() {
	if (clearSharesLoading.value) return
	clearSharesLoading.value = true; clearSharesError.value = ''; shareNotice.value = ''
	try {
		const result = await mailAPI.clearInactiveShareLinks()
		const returnToShare = clearSharesReturnToShare.value && Boolean(sharingAlias.value)
		if (returnToShare && sharingAlias.value) shareLinks.value = (await mailAPI.shareLinks(sharingAlias.value.anonymousId)).links
		clearSharesOpen.value = false
		shareOpen.value = returnToShare
		clearSharesReturnToShare.value = false
		showToast(`已从数据库清理 ${result.deleted} 条失效分享记录`)
	} catch (reason) { clearSharesError.value = errorMessage(reason) } finally { clearSharesLoading.value = false }
}
async function saveEdit() {
  if (!editing.value || !editLabel.value.trim()) return
  editSaving.value = true; error.value = ''
	try { const updated = await mailAPI.updateAlias(editing.value.anonymousId, editLabel.value, editNote.value); const index = aliases.value.findIndex((item) => item.anonymousId === editing.value?.anonymousId); if (index >= 0) aliases.value[index] = { ...aliases.value[index], ...updated, label: editLabel.value, note: editNote.value }; editOpen.value = false; showToast(`已更新 ${editing.value.hme}`) }
	catch (reason) { editError.value = errorMessage(reason) }
  finally { editSaving.value = false }
}

async function exportCSV() {
  error.value = ''
  try {
    const response = await fetch('/api/mail/v1/aliases/export.csv', { credentials: 'same-origin', headers: { 'X-Mail-Account-ID': mailAccountState.currentId } })
    if (!response.ok) throw new Error(`导出失败：HTTP ${response.status}`)
    const url = URL.createObjectURL(await response.blob())
    const anchor = document.createElement('a'); anchor.href = url; anchor.download = 'hide-my-email.csv'; anchor.click(); URL.revokeObjectURL(url)
  } catch (reason) { error.value = errorMessage(reason) }
}

function formatDate(value?: number) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value > 1e12 ? value : value * 1000))
}

async function handleAccountChange() {
  currentPage.value = 1
  await Promise.all([load(), loadSchedule(true)])
}

onMounted(async () => {
  await Promise.all([load(), loadSchedule(true)])
  document.addEventListener('visibilitychange', handleVisibilityChange)
  window.addEventListener('mail-account-change', handleAccountChange)
  scheduleTaskPolling()
})

onBeforeUnmount(() => {
  clearTaskPolling()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  window.removeEventListener('mail-account-change', handleAccountChange)
})
</script>

<template>
  <section class="page mail-aliases">
    <div class="page-heading">
      <div><h2>邮箱管理</h2><p>管理 iCloud+ 隐藏邮箱与服务器创建任务</p></div>
      <div class="page-actions"><button class="button ghost" :disabled="loading" @click="load"><RefreshCw :size="16" :class="{ spin: loading }" />刷新</button></div>
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
      </div>

      <div v-if="workspaceView === 'aliases'" class="aliases-pane">
        <p v-if="error" class="message error pane-message">{{ error }}</p>
        <div class="list-toolbar">
          <label class="search-field"><Search :size="16" /><input v-model="query" placeholder="搜索邮箱、标签、应用或转发地址" /></label>
          <div class="segmented" aria-label="状态筛选"><button v-for="option in ['all', 'active', 'inactive'] as const" :key="option" :class="{ active: state === option }" @click="state = option">{{ { all: '全部', active: '启用', inactive: '停用' }[option] }}</button></div>
          <button class="button ghost export-button" title="批量取件" @click="openBatchShare"><MailPlus :size="16" /><span>批量取件</span></button>
          <button class="button ghost danger-action export-button" title="批量清理失效取件链接" :disabled="clearSharesLoading" @click="openClearShares(false)"><Trash2 :size="16" /><span>清理失效</span></button>
          <button class="button ghost export-button" title="导出 CSV" @click="exportCSV"><Download :size="16" /><span>导出</span></button>
        </div>
        <div class="list-meta">
          <span>邮箱列表 <small>共 {{ visible.length }} 条</small></span>
          <label class="page-size">每页<select v-model.number="pageSize"><option :value="10">10 条</option><option :value="20">20 条</option><option :value="50">50 条</option><option :value="100">100 条</option></select></label>
        </div>
        <div class="data-frame">
          <table>
            <thead><tr><th>邮箱地址</th><th>标签 / 备注</th><th>转发到</th><th>状态</th><th>已注册应用</th><th>创建时间</th><th><span class="sr-only">操作</span></th></tr></thead>
            <tbody>
              <tr v-for="alias in pagedAliases" :key="alias.anonymousId">
                <td data-label="邮箱地址"><strong class="address">{{ alias.hme }}</strong><small>{{ alias.origin || 'iCloud+' }}</small></td>
                <td data-label="标签 / 备注"><span>{{ alias.label || '未命名' }}</span><small>{{ alias.note || '无备注' }}</small></td>
                <td data-label="转发到"><span class="forward-address">{{ alias.forwardToEmail || '—' }}</span></td>
                <td data-label="状态"><StatusBadge :state="alias.isActive === false ? 'inactive' : 'active'" :label="alias.isActive === false ? '已停用' : '转发中'" /></td>
                <td data-label="已注册应用">
                  <div v-if="alias.registeredApps?.length" class="app-badges">
                    <span v-for="app in alias.registeredApps" :key="app.key" class="app-badge" :class="app.status" :title="app.status === 'confirmed' ? `${app.label}：已收到后续登录或欢迎邮件` : `${app.label}：已收到注册验证码`">
                      <strong>{{ app.label }}</strong><small>{{ app.status === 'confirmed' ? '已确认' : '已注册' }}</small>
                    </span>
                  </div>
                  <span v-else class="muted">—</span>
                </td>
                <td data-label="创建时间">{{ formatDate(alias.createTimestamp) }}</td>
                <td class="row-actions"><LoaderCircle v-if="pendingID === alias.anonymousId" :size="18" class="spin" /><template v-else><a class="icon-button" :href="`/mail/mailbox?alias=${encodeURIComponent(alias.hme)}`" title="查看邮件" :aria-label="`查看 ${alias.hme} 的邮件`"><MailOpen :size="16" /></a><button class="icon-button" title="编辑标签和备注" @click="openEdit(alias)"><Pencil :size="16" /></button><button class="icon-button" title="生成分享链接" @click="openShare(alias)"><MailPlus :size="16" /></button><button class="icon-button" :title="alias.isActive === false ? '重新启用邮箱' : '停用邮箱'" @click="runAction(alias, alias.isActive === false ? 'enable' : 'disable')"><Power :size="16" /></button><button class="icon-button danger" title="删除邮箱" @click="runAction(alias, 'delete')"><Trash2 :size="16" /></button></template></td>
              </tr>
              <tr v-if="loading && aliases.length === 0"><td colspan="7" class="empty-state"><AsyncState state="loading" title="正在读取邮箱" /></td></tr>
              <tr v-else-if="error && aliases.length === 0"><td colspan="7" class="empty-state"><AsyncState state="error" title="邮箱列表加载失败" :detail="error" @retry="load" /></td></tr>
              <tr v-else-if="visible.length === 0"><td colspan="7" class="empty-state"><AsyncState state="empty" title="没有符合条件的邮箱" detail="调整关键词或状态筛选后重试"><template #icon><MailOpen :size="28" /></template></AsyncState></td></tr>
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
        <p v-if="scheduleError" class="message error">{{ scheduleError }}</p>
        <div class="schedule-form">
          <label class="field"><span>每轮数量 <small>1 - 20 个</small></span><input v-model.number="batchSize" type="number" min="1" max="20" /></label>
          <label class="field"><span>邮箱间隔 <small>秒</small></span><input v-model.number="aliasIntervalSeconds" type="number" min="1" max="3600" /></label>
          <label class="field"><span>执行周期 <small>至少 60 秒</small></span><input v-model.number="intervalSeconds" type="number" min="60" /></label>
          <label class="field"><span>统一标签</span><input v-model="scheduleLabel" maxlength="100" /></label>
          <label class="field schedule-note"><span>备注 <small>可留空</small></span><input v-model="scheduleNote" maxlength="500" /></label>
          <div class="schedule-save"><span v-if="schedule?.lastBatchStoppedReason">最近状态：{{ schedule.lastBatchStoppedReason }}<small v-if="schedule.lastRunAt"> · {{ formatScheduleTime(schedule.lastRunAt) }}</small></span><button class="button primary" :disabled="scheduleSaving || schedule?.running" @click="saveSchedule(schedule?.enabled)"><LoaderCircle v-if="scheduleSaving" :size="15" class="spin" />保存参数</button><button class="button secondary" :disabled="scheduleSaving || schedule?.enabled" @click="saveSchedule(true)"><Play :size="15" />保存并开启</button></div>
        </div>
        <div class="schedule-meta"><span>本轮成功 {{ schedule?.running ? schedule.currentSuccess : schedule?.lastBatchSuccess || 0 }} / {{ schedule?.running ? schedule.currentTotal : schedule?.lastBatchRequested || batchSize }} 个</span><span v-if="schedule?.lastUsedChannel">最近通道 {{ createChannelLabel(schedule.lastUsedChannel) }}{{ schedule.lastFallbackUsed ? ' · 已自动兜底' : '' }}</span><span v-if="schedule?.lastError" class="schedule-error">{{ schedule.lastError }}</span></div>
      </div>

    </section>
  </section>

  <AppDialog id="edit-alias" :open="editOpen" title="编辑邮箱信息" :subtitle="editing?.hme || ''" :busy="editSaving" @close="editOpen = false">
      <form id="edit-alias-form" @submit.prevent="saveEdit">
        <label class="field"><span>标签</span><input v-model="editLabel" maxlength="100" required /></label>
        <label class="field"><span>备注</span><textarea v-model="editNote" rows="3" maxlength="500" /></label>
        <p v-if="editError" class="message error">{{ editError }}</p>
      </form>
      <template #actions><button type="button" class="button ghost" :disabled="editSaving" @click="editOpen = false">取消</button><button form="edit-alias-form" class="button primary" :disabled="editSaving || !editLabel.trim()"><LoaderCircle v-if="editSaving" :size="16" class="spin" />保存</button></template>
  </AppDialog>

  <AppDialog id="share-alias" :open="shareOpen" title="分享收件地址" :subtitle="sharingAlias?.hme || ''" :busy="shareLoading" @close="shareOpen = false">
      <div class="share-dialog">
        <div class="share-tools"><button class="button ghost danger-action" :disabled="shareLoading || !shareLinks.some((item) => !item.active)" @click="openClearShares(true)"><Trash2 :size="15" />清理失效</button></div>
        <label class="field"><span>有效期</span><select v-model="shareExpiry"><option :value="3600">1 小时</option><option :value="86400">1 天</option><option :value="604800">7 天</option><option :value="2592000">30 天</option><option :value="null">永久</option></select></label>
        <p v-if="shareError" class="message error">{{ shareError }}</p>
        <p v-if="shareNotice" class="message success">{{ shareNotice }}</p>
        <div v-if="shareCreatedURL" class="share-created"><div><span class="muted">新取件地址</span><code>{{ shareCreatedURL }}</code></div><button class="icon-button" title="复制取件地址" aria-label="复制取件地址" @click="copyShareURL(shareCreatedURL, 'single')"><Copy :size="16" /></button></div>
        <div class="share-list"><div v-for="link in shareLinks" :key="link.id" class="share-item"><div><span>{{ link.active ? '有效' : '已撤销' }} · {{ link.expiresAt ? formatDate(link.expiresAt) : '永久' }}</span><code v-if="link.shareUrl">{{ fullShareURL(link.shareUrl) }}</code></div><div class="share-item-actions"><button v-if="link.shareUrl" class="icon-button" title="复制取件地址" aria-label="复制取件地址" @click="copyShareURL(fullShareURL(link.shareUrl), link.id)"><Copy :size="15" /></button><button v-if="link.active" class="button ghost" @click="revokeShare(link)">撤销</button></div></div><p v-if="!shareLoading && shareLinks.length === 0" class="muted">暂无分享链接</p></div>
      </div>
      <template #actions><button type="button" class="button ghost" :disabled="shareLoading" @click="shareOpen = false">关闭</button><button class="button primary" :disabled="shareLoading" @click="createShare"><LoaderCircle v-if="shareLoading" :size="16" class="spin" />生成分享链接</button></template>
  </AppDialog>

  <AppDialog id="batch-share" :open="batchShareOpen" title="批量取件" subtitle="按邮箱创建时间从早到晚生成取件地址" width="wide" :busy="batchShareLoading" @close="closeBatchShare">
    <div class="batch-share-dialog">
      <div class="batch-share-form">
        <label class="field"><span>邮箱范围</span><select v-model="batchShareScope" name="batch-share-scope"><option value="all">全部邮箱（{{ activeCount }}）</option><option value="gpt_registered">已注册 GPT（{{ gptRegisteredActiveCount }}）</option><option value="gpt_unregistered">未注册 GPT（{{ gptUnregisteredActiveCount }}）</option></select></label>
        <label class="field"><span>邮箱数量 <small>1 - 750 个</small></span><input v-model.number="batchShareCount" type="number" min="1" max="750" /></label>
        <label class="field"><span>链接有效期</span><select v-model="batchShareExpiryPreset"><option value="3600">1 小时</option><option value="86400">1 天</option><option value="604800">7 天</option><option value="2592000">30 天</option><option value="custom">自定义时间</option><option value="never">永久</option></select></label>
        <label v-if="batchShareExpiryPreset === 'custom'" class="field batch-share-custom-expiry"><span>失效时间</span><input v-model="batchShareExpiryAt" type="datetime-local" /></label>
      </div>
      <p v-if="batchShareScope === 'all'" class="muted batch-share-hint">包含所有启用邮箱；停用邮箱不会生成取件链接，数量不足时不会生成部分链接。</p>
      <p v-else-if="batchShareScope === 'gpt_registered'" class="muted batch-share-hint">包含黄色“GPT 已注册”和绿色“GPT 已确认”状态；只选择启用邮箱，数量不足时不会生成部分链接。</p>
      <p v-else class="muted batch-share-hint">只包含尚未检测到 GPT 注册状态的启用邮箱；数量不足时不会生成部分链接。</p>
      <p v-if="batchShareError" class="message error">{{ batchShareError }}</p>
      <div v-if="batchShareResults.length" class="batch-share-results">
        <div class="batch-share-result-heading"><strong>已生成 {{ batchShareResults.length }} 个取件地址</strong><button class="button ghost" @click="downloadBatchShareTXT"><Download :size="15" />下载 TXT</button></div>
        <div v-for="item in batchShareResults" :key="item.id" class="batch-share-item"><div><strong>{{ item.alias }}</strong><code>{{ fullShareURL(item.shareUrl) }}</code></div><button class="icon-button" title="复制取件地址" :aria-label="`复制 ${item.alias} 的取件地址`" @click="copyShareURL(fullShareURL(item.shareUrl), item.id)"><Copy :size="16" /></button></div>
      </div>
    </div>
    <template #actions><button type="button" class="button ghost" :disabled="batchShareLoading" @click="closeBatchShare">关闭</button><button v-if="batchShareResults.length" type="button" class="button secondary" :disabled="batchShareLoading" @click="downloadBatchShareTXT"><Download :size="15" />下载 TXT</button><button type="button" class="button primary" :disabled="batchShareLoading" @click="createBatchShare"><LoaderCircle v-if="batchShareLoading" :size="16" class="spin" /><MailPlus v-else :size="16" />生成取件地址</button></template>
  </AppDialog>

  <AppDialog id="delete-alias" :open="Boolean(deleteTarget)" title="删除隐藏邮箱" subtitle="此操作不可恢复" role="alertdialog" :busy="Boolean(pendingID)" @close="deleteTarget = null">
    <p v-if="deleteTarget">将永久删除 <strong>{{ deleteTarget.hme }}</strong>，请输入完整邮箱地址确认。</p>
    <label v-if="deleteTarget" class="field"><span>邮箱地址</span><input v-model="deleteConfirm" autocomplete="off" /></label>
    <p v-if="deleteError" class="message error">{{ deleteError }}</p>
    <template #actions><button type="button" class="button ghost" :disabled="Boolean(pendingID)" @click="deleteTarget = null">取消</button><button type="button" class="button danger-action danger-confirm" :disabled="Boolean(pendingID) || deleteConfirm !== deleteTarget?.hme" @click="deleteTarget && performAliasAction(deleteTarget, 'delete')"><LoaderCircle v-if="pendingID" :size="15" class="spin" /><Trash2 v-else :size="15" />永久删除</button></template>
  </AppDialog>

  <AppDialog id="clear-shares" :open="clearSharesOpen" title="清理失效分享" subtitle="此操作不可恢复" role="alertdialog" :busy="clearSharesLoading" @close="closeClearShares">
    <p>将从 PostgreSQL 永久删除当前账号所有已撤销或已过期的分享记录。</p>
    <p v-if="clearSharesError" class="message error">{{ clearSharesError }}</p>
    <template #actions><button type="button" class="button ghost" :disabled="clearSharesLoading" @click="closeClearShares">取消</button><button type="button" class="button danger-action danger-confirm" :disabled="clearSharesLoading" @click="clearInactiveShares"><LoaderCircle v-if="clearSharesLoading" :size="15" class="spin" /><Trash2 v-else :size="15" />永久清理</button></template>
  </AppDialog>
</template>

<style scoped>
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
.app-badges { display: flex; min-width: 112px; align-items: center; gap: 6px; flex-wrap: wrap; }
.app-badge { display: inline-flex; min-height: 27px; align-items: center; gap: 6px; padding: 0 8px; border: 1px solid; border-radius: 5px; white-space: nowrap; }
.app-badge strong { font-size: 11px; font-weight: 750; }
.app-badge small { font-size: 10px; font-weight: 600; }
.app-badge.observed { color: #92400e; background: #fffbeb; border-color: #fde68a; }
.app-badge.confirmed { color: #047857; background: #ecfdf5; border-color: #a7f3d0; }
:root[data-theme="dark"] .app-badge.observed { color: #fcd34d; background: rgba(245, 158, 11, .12); border-color: rgba(245, 158, 11, .3); }
:root[data-theme="dark"] .app-badge.confirmed { color: #6ee7b7; background: rgba(16, 185, 129, .12); border-color: rgba(16, 185, 129, .3); }
.task-pane { min-height: 390px; padding: 24px; }
.task-header { display: flex; align-items: center; justify-content: space-between; gap: 18px; padding-bottom: 20px; border-bottom: 1px solid var(--border-soft); }
.task-header h3 { margin: 0; color: var(--text); font-size: 16px; font-weight: 700; }
.task-header > div:first-child > span { display: block; margin-top: 5px; color: var(--muted); font-size: 11px; }
.share-dialog p { margin: 4px 0 0; color: var(--muted); font-size: 12px; }
.share-tools { display: flex; justify-content: flex-end; margin-bottom: 12px; }
.share-list { display: grid; gap: 8px; margin-top: 16px; max-height: 220px; overflow: auto; }
.share-item { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px; background: var(--surface-subtle); border: 1px solid var(--border-soft); border-radius: 6px; font-size: 12px; }
.share-item > div:first-child, .share-created > div, .batch-share-item > div { display: grid; min-width: 0; gap: 5px; }
.share-item code, .share-created code, .batch-share-item code { display: block; max-width: 100%; overflow: hidden; color: var(--muted); font: 11px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; text-overflow: ellipsis; white-space: nowrap; }
.share-item-actions { display: flex; align-items: center; gap: 6px; flex: 0 0 auto; }
.share-created { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 12px; padding: 10px; background: var(--primary-soft); border: 1px solid color-mix(in srgb, var(--primary) 25%, transparent); border-radius: 6px; }
.batch-share-form { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.batch-share-custom-expiry { grid-column: 1 / -1; }
.batch-share-hint { margin: 14px 0 0; }
.batch-share-results { display: grid; gap: 8px; margin-top: 18px; }
.batch-share-result-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; color: var(--text); font-size: 12px; }
.batch-share-item { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px; background: var(--surface-subtle); border: 1px solid var(--border-soft); border-radius: 6px; }
.muted { color: var(--muted); }
.danger-confirm { color: #fff; background: var(--danger); border-color: var(--danger); }
.schedule-actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; flex-wrap: wrap; }
.schedule-status { display: inline-flex; min-height: 32px; align-items: center; gap: 7px; padding: 0 10px; color: var(--muted); background: var(--surface-subtle); border: 1px solid var(--border); border-radius: 7px; font-size: 11px; white-space: nowrap; }
.schedule-status i { width: 7px; height: 7px; background: var(--muted); border-radius: 50%; }
.schedule-status.active { color: var(--primary-text); background: var(--primary-soft); border-color: color-mix(in srgb, var(--primary) 22%, transparent); }
.schedule-status.active i { background: #10b981; box-shadow: 0 0 0 3px rgba(16, 185, 129, .12); }
.schedule-form { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; margin-top: 22px; }
.schedule-form .field { margin: 0; }
.schedule-form .field small { float: right; color: var(--muted); font-size: 10px; font-weight: 500; }
.schedule-note { grid-column: span 2; }
.schedule-save { grid-column: 1 / -1; display: flex; align-items: center; justify-content: flex-end; gap: 8px; margin-top: 2px; }
.schedule-save > span { flex: 1; min-width: 0; color: var(--muted); font-size: 11px; }
.schedule-save > span small { color: var(--muted); }
.schedule-meta { display: flex; align-items: center; gap: 16px; margin-top: 14px; color: var(--muted); font-size: 11px; flex-wrap: wrap; }
.schedule-error { color: var(--danger); max-width: 100%; overflow-wrap: anywhere; }
@media (max-width: 900px) { .overview-update { display: none; } .alias-overview > div { flex: 1; min-width: 0; } .schedule-form { grid-template-columns: repeat(2, minmax(0, 1fr)); } .schedule-note { grid-column: auto; } }
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
  .batch-share-form { grid-template-columns: minmax(0, 1fr); }
  .batch-share-custom-expiry { grid-column: auto; }
  .batch-share-result-heading { align-items: stretch; flex-direction: column; }
  .task-header { align-items: flex-start; flex-direction: column; }
  .schedule-actions { justify-content: flex-start; width: 100%; }
  .schedule-form { grid-template-columns: minmax(0, 1fr); }
  .schedule-note { grid-column: auto; }
  .schedule-save { align-items: stretch; flex-direction: column; }
  .schedule-save > span { flex: initial; }
  .schedule-save .button { width: 100%; }
}
</style>
