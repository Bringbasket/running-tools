<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, CircleAlert, Clock3, Copy, LoaderCircle, RefreshCw, ScrollText, Search, Trash2 } from '../../../../frontend/src/icons'
import { errorMessage } from '../../../../frontend/src/api'
import AppDialog from '../../../../frontend/src/components/AppDialog.vue'
import AsyncState from '../../../../frontend/src/components/AsyncState.vue'
import { showToast } from '../../../../frontend/src/toast'
import { mailAPI } from '../api'
import type { ActivityLogEntry, ActivityLogPage } from '../types'

const data = ref<ActivityLogPage>({ items: [], total: 0, page: 1, pageSize: 10, stats: { today: 0, failures24h: 0, background24h: 0 } })
const loading = ref(false)
const error = ref('')
const search = ref('')
const level = ref('')
const category = ref('')
const source = ref('')
const startDate = ref('')
const endDate = ref('')
const page = ref(1)
const pageSize = ref(10)
const selected = ref<ActivityLogEntry | null>(null)
const clearOpen = ref(false)
const clearing = ref(false)
const clearError = ref('')
const copied = ref('')
let searchTimer: ReturnType<typeof window.setTimeout> | undefined
let requestGeneration = 0

const pageCount = computed(() => Math.max(1, Math.ceil(data.value.total / pageSize.value)))
const rangeStart = computed(() => data.value.total === 0 ? 0 : (page.value - 1) * pageSize.value + 1)
const rangeEnd = computed(() => Math.min(page.value * pageSize.value, data.value.total))
const hasFilters = computed(() => Boolean(search.value || level.value || category.value || source.value || startDate.value || endDate.value))

const categoryLabels: Record<string, string> = { account: '账号管理', alias: '邮箱管理', session: 'Session', mailbox: '收件箱', automation: '自动任务' }
const sourceLabels: Record<string, string> = { user: '用户操作', background: '后台任务', system: '系统' }

function dateBoundary(value: string, end = false) {
  if (!value) return undefined
  const time = new Date(`${value}T${end ? '23:59:59.999' : '00:00:00'}`)
  return Number.isNaN(time.getTime()) ? undefined : time.toISOString()
}

async function load() {
  const generation = ++requestGeneration
  loading.value = true
  error.value = ''
  try {
    const result = await mailAPI.activityLogs({ page: page.value, pageSize: pageSize.value, search: search.value.trim(), level: level.value, category: category.value, source: source.value, start: dateBoundary(startDate.value), end: dateBoundary(endDate.value, true) })
    if (generation === requestGeneration) data.value = result
  } catch (reason) {
    if (generation === requestGeneration) error.value = errorMessage(reason)
  } finally {
    if (generation === requestGeneration) loading.value = false
  }
}

function applyFilters() {
  page.value = 1
  void load()
}

function scheduleSearch() {
  if (searchTimer !== undefined) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(applyFilters, 350)
}

function resetFilters() {
  search.value = level.value = category.value = source.value = startDate.value = endDate.value = ''
  applyFilters()
}

async function clearLogs() {
	if (!data.value.total || clearing.value) return
	clearing.value = true
	clearError.value = ''
	try {
		await mailAPI.clearActivityLogs()
		page.value = 1
		await load()
		clearOpen.value = false
		showToast('邮件系统使用日志已永久清理')
	} catch (reason) {
		clearError.value = errorMessage(reason)
	} finally {
		clearing.value = false
	}
}

function formatTime(value: string) {
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}

function formatDuration(value: number) {
  if (value < 1000) return `${value} ms`
  return `${(value / 1000).toFixed(value < 10_000 ? 1 : 0)} s`
}

async function copyRequestID(value: string) {
  await navigator.clipboard.writeText(value)
  copied.value = value
  window.setTimeout(() => { if (copied.value === value) copied.value = '' }, 1200)
}

watch(pageSize, applyFilters)
watch(page, () => { void load() })
function handleAccountChange() { page.value = 1; void load() }
onMounted(() => { window.addEventListener('mail-account-change', handleAccountChange); void load() })
onBeforeUnmount(() => window.removeEventListener('mail-account-change', handleAccountChange))
</script>

<template>
  <section class="page activity-logs">
    <div class="page-heading">
      <div><h2>使用日志</h2><p>查看邮件系统的用户操作与后台任务结果</p></div>
      <div class="page-actions"><button class="button ghost danger-action" :disabled="loading || clearing || !data.total" @click="clearError = ''; clearOpen = true"><Trash2 :size="16" />清理</button><button class="button ghost" :disabled="loading" @click="load"><LoaderCircle v-if="loading" :size="16" class="spin" /><RefreshCw v-else :size="16" />刷新</button></div>
    </div>

    <div class="log-overview" aria-label="日志概览">
      <div><ScrollText :size="17" /><span>今日记录</span><strong>{{ data.stats.today }}</strong></div>
      <div><CircleAlert :size="17" /><span>24 小时失败</span><strong :class="{ danger: data.stats.failures24h > 0 }">{{ data.stats.failures24h }}</strong></div>
      <div><Clock3 :size="17" /><span>24 小时后台任务</span><strong>{{ data.stats.background24h }}</strong></div>
    </div>

    <section class="log-workspace">
      <div class="log-toolbar">
        <label class="search-field"><Search :size="16" /><input v-model="search" placeholder="搜索摘要、动作或请求 ID" @input="scheduleSearch" /></label>
        <label><span>级别</span><select v-model="level" @change="applyFilters"><option value="">全部</option><option value="info">正常</option><option value="warning">警告</option><option value="error">错误</option></select></label>
        <label><span>分类</span><select v-model="category" @change="applyFilters"><option value="">全部</option><option value="account">账号管理</option><option value="alias">邮箱管理</option><option value="session">Session</option><option value="mailbox">收件箱</option><option value="automation">自动任务</option></select></label>
        <label><span>来源</span><select v-model="source" @change="applyFilters"><option value="">全部</option><option value="user">用户操作</option><option value="background">后台任务</option><option value="system">系统</option></select></label>
        <label class="date-filter"><span>开始日期</span><input v-model="startDate" type="date" @change="applyFilters" /></label>
        <label class="date-filter"><span>结束日期</span><input v-model="endDate" type="date" @change="applyFilters" /></label>
        <button v-if="hasFilters" class="button ghost reset-button" @click="resetFilters">重置</button>
      </div>

      <p v-if="error" class="message error log-message">{{ error }}</p>
      <div class="list-meta"><span>日志列表 <small>共 {{ data.total }} 条</small></span><label class="page-size">每页<select v-model.number="pageSize"><option :value="10">10 条</option><option :value="20">20 条</option><option :value="50">50 条</option><option :value="100">100 条</option></select></label></div>
      <div class="data-frame log-table">
        <table>
          <thead><tr><th>时间</th><th>分类 / 动作</th><th>结果</th><th>来源</th><th>耗时</th><th>请求 ID</th><th><span class="sr-only">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="entry in data.items" :key="entry.id">
              <td data-label="时间"><span class="time-value">{{ formatTime(entry.createdAt) }}</span></td>
              <td data-label="分类 / 动作"><strong>{{ categoryLabels[entry.category] || entry.category }}</strong><small>{{ entry.summary }}</small></td>
              <td data-label="结果"><span class="log-status" :class="entry.level"><i />{{ entry.outcome === 'success' ? '成功' : entry.level === 'warning' ? '警告' : '失败' }}</span></td>
              <td data-label="来源"><span>{{ sourceLabels[entry.source] || entry.source }}</span><small v-if="entry.method">{{ entry.method }} {{ entry.path }}</small></td>
              <td data-label="耗时"><span class="duration">{{ formatDuration(entry.durationMs) }}</span></td>
              <td data-label="请求 ID"><button v-if="entry.requestId" class="request-id" :title="copied === entry.requestId ? '已复制' : '复制请求 ID'" @click="copyRequestID(entry.requestId)"><code>{{ entry.requestId.slice(0, 10) }}</code><Copy :size="13" /></button><span v-else>—</span></td>
              <td class="row-actions"><button class="button ghost detail-button" @click="selected = entry">详情</button></td>
            </tr>
            <tr v-if="loading && data.items.length === 0"><td colspan="7" class="empty-state"><AsyncState state="loading" title="正在读取使用日志" /></td></tr>
            <tr v-else-if="error && data.items.length === 0"><td colspan="7" class="empty-state"><AsyncState state="error" title="使用日志加载失败" :detail="error" @retry="load" /></td></tr>
            <tr v-else-if="data.items.length === 0"><td colspan="7" class="empty-state"><AsyncState state="empty" :title="hasFilters ? '没有符合条件的日志' : '暂无使用日志'" :detail="hasFilters ? '调整筛选条件后重试' : '邮件系统执行操作后会在这里留下记录'"><template #icon><ScrollText :size="28" /></template></AsyncState></td></tr>
          </tbody>
        </table>
      </div>
      <div class="pagination-bar"><span>显示 {{ rangeStart }}–{{ rangeEnd }}，共 {{ data.total }} 条</span><div><button class="icon-button" title="上一页" :disabled="page <= 1 || loading" @click="page--"><ChevronLeft :size="17" /></button><strong>第 {{ page }} / {{ pageCount }} 页</strong><button class="icon-button" title="下一页" :disabled="page >= pageCount || loading" @click="page++"><ChevronRight :size="17" /></button></div></div>
    </section>
  </section>

  <AppDialog id="log-detail" :open="Boolean(selected)" title="日志详情" :subtitle="selected ? formatTime(selected.createdAt) : ''" width="wide" @close="selected = null">
      <div v-if="selected" class="log-detail">
        <dl>
          <div><dt>结果</dt><dd><span class="log-status" :class="selected.level"><i />{{ selected.outcome === 'success' ? '成功' : selected.level === 'warning' ? '警告' : '失败' }}</span></dd></div>
          <div><dt>摘要</dt><dd>{{ selected.summary }}</dd></div>
          <div><dt>分类</dt><dd>{{ categoryLabels[selected.category] || selected.category }}</dd></div>
          <div><dt>动作</dt><dd><code>{{ selected.action }}</code></dd></div>
          <div><dt>来源</dt><dd>{{ sourceLabels[selected.source] || selected.source }}</dd></div>
          <div><dt>请求</dt><dd>{{ selected.method && selected.path ? `${selected.method} ${selected.path}` : '后台任务' }}</dd></div>
          <div><dt>HTTP 状态</dt><dd>{{ selected.httpStatus || '—' }}</dd></div>
          <div><dt>耗时</dt><dd>{{ formatDuration(selected.durationMs) }}</dd></div>
          <div><dt>请求 ID</dt><dd><code>{{ selected.requestId || '—' }}</code></dd></div>
          <div v-if="selected.detail"><dt>详情</dt><dd class="detail-text">{{ selected.detail }}</dd></div>
          <div v-if="selected.metadata && Object.keys(selected.metadata).length"><dt>附加信息</dt><dd><pre>{{ JSON.stringify(selected.metadata, null, 2) }}</pre></dd></div>
        </dl>
      </div>
      <template #actions><button type="button" class="button ghost" @click="selected = null">关闭</button></template>
  </AppDialog>

  <AppDialog id="clear-logs" :open="clearOpen" title="清理使用日志" subtitle="此操作不可恢复" role="alertdialog" :busy="clearing" @close="clearOpen = false">
    <p>将从 PostgreSQL 永久删除当前账号的 <strong>{{ data.total }}</strong> 条邮件系统使用日志。</p>
    <p v-if="clearError" class="message error">{{ clearError }}</p>
    <template #actions><button type="button" class="button ghost" :disabled="clearing" @click="clearOpen = false">取消</button><button type="button" class="button danger-action danger-confirm" :disabled="clearing" @click="clearLogs"><LoaderCircle v-if="clearing" :size="15" class="spin" /><Trash2 v-else :size="15" />永久清理</button></template>
  </AppDialog>
</template>

<style scoped>
.log-overview { display: flex; min-height: 54px; align-items: center; margin-bottom: 16px; padding: 0 16px; background: var(--surface); border: 1px solid var(--border); border-radius: 7px; }
.log-overview > div { display: flex; min-width: 210px; align-items: center; gap: 8px; padding-right: 24px; color: var(--muted); font-size: 12px; }
.log-overview > div + div { padding-left: 24px; border-left: 1px solid var(--border-soft); }
.log-overview svg { color: #64748b; }
.log-overview strong { margin-left: auto; color: var(--text); font-size: 18px; font-variant-numeric: tabular-nums; }
.log-overview strong.danger { color: var(--danger); }
.log-workspace { overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 7px; }
.log-toolbar { display: grid; grid-template-columns: minmax(260px, 1.5fr) repeat(3, minmax(120px, .55fr)) repeat(2, minmax(145px, .65fr)) auto; align-items: end; gap: 10px; padding: 14px 16px; border-bottom: 1px solid var(--border-soft); }
.log-toolbar > label:not(.search-field) { display: grid; gap: 5px; min-width: 0; color: var(--muted); font-size: 11px; }
.log-toolbar select, .log-toolbar input[type="date"] { width: 100%; min-width: 0; min-height: 36px; padding: 0 9px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; outline: none; }
.log-toolbar select:focus, .log-toolbar input:focus { border-color: var(--primary); }
.reset-button { min-width: 68px; }
.log-message { margin: 14px 16px 0; }
.list-meta { display: flex; min-height: 48px; align-items: center; justify-content: space-between; padding: 0 16px; }
.list-meta > span { color: var(--text); font-size: 13px; font-weight: 700; }
.list-meta small { margin-left: 7px; color: var(--muted); font-size: 11px; font-weight: 500; }
.page-size { display: inline-flex; align-items: center; gap: 8px; color: var(--muted); font-size: 11px; }
.page-size select { min-height: 31px; padding: 0 27px 0 9px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; }
.log-table th:nth-child(1) { width: 175px; }
.log-table { contain: paint; }
.log-table th:nth-child(3) { width: 92px; }
.log-table th:nth-child(4) { width: 180px; }
.log-table th:nth-child(5) { width: 82px; }
.log-table th:nth-child(6) { width: 145px; }
.log-table th:last-child { width: 74px; }
.log-table td strong { display: block; color: var(--text); font-size: 12px; }
.log-table td small { display: block; max-width: 420px; margin-top: 3px; overflow: hidden; color: var(--muted); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.time-value, .duration { color: var(--muted); font-size: 11px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.log-status { display: inline-flex; min-width: 58px; min-height: 25px; align-items: center; justify-content: center; gap: 6px; padding: 0 7px; color: #047857; background: #ecfdf5; border: 1px solid #d1fae5; border-radius: 5px; font-size: 11px; }
.log-status i { width: 6px; height: 6px; background: currentColor; border-radius: 50%; }
.log-status.warning { color: #b45309; background: #fffbeb; border-color: #fde68a; }
.log-status.error { color: #b91c1c; background: #fef2f2; border-color: #fecaca; }
.request-id { display: inline-flex; max-width: 130px; align-items: center; gap: 5px; padding: 0; color: var(--muted); background: none; border: 0; }
.request-id code { overflow: hidden; text-overflow: ellipsis; }
.detail-button { min-width: 54px; height: 30px; padding: 0 9px; font-size: 11px; }
.pagination-bar { display: flex; min-height: 54px; align-items: center; justify-content: space-between; padding: 0 16px; color: var(--muted); border-top: 1px solid var(--border); font-size: 11px; }
.pagination-bar > div { display: flex; align-items: center; gap: 9px; }
.pagination-bar strong { min-width: 82px; color: var(--text); font-size: 11px; text-align: center; }
.log-detail dl { display: grid; margin: 0; border-top: 1px solid var(--border-soft); }
.log-detail dl > div { display: grid; grid-template-columns: 130px minmax(0, 1fr); min-height: 44px; border-bottom: 1px solid var(--border-soft); }
.log-detail dt, .log-detail dd { margin: 0; padding: 11px 12px; }
.log-detail dt { color: var(--muted); background: var(--surface-subtle); font-size: 11px; }
.log-detail dd { min-width: 0; color: var(--text); font-size: 12px; overflow-wrap: anywhere; }
.log-detail pre { max-height: 180px; margin: 0; overflow: auto; white-space: pre-wrap; }
.detail-text { white-space: pre-wrap; }
.danger-confirm { color: #fff; background: var(--danger); border-color: var(--danger); }
@media (max-width: 1200px) { .log-toolbar { grid-template-columns: minmax(260px, 1.5fr) repeat(3, minmax(120px, 1fr)); } .date-filter, .reset-button { grid-row: 2; } }
@media (max-width: 760px) {
  .log-overview { padding: 0 8px; }
  .log-overview > div { min-width: 0; flex: 1; flex-direction: column; gap: 2px; padding: 8px; text-align: center; }
  .log-overview > div + div { padding-left: 8px; }
  .log-overview strong { margin-left: 0; font-size: 16px; }
  .log-toolbar { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .log-toolbar .search-field { grid-column: 1 / -1; max-width: none; }
  .date-filter, .reset-button { grid-row: auto; }
  .log-table table, .log-table tbody, .log-table tr, .log-table td { display: block; width: 100%; }
  .log-table thead { display: none; }
  .log-table tr { padding: 12px 14px; border-bottom: 1px solid var(--border-soft); }
  .log-table td { display: grid; grid-template-columns: 92px minmax(0, 1fr); min-height: 31px; align-items: center; padding: 4px 0; border: 0; }
  .log-table td::before { content: attr(data-label); color: var(--muted); font-size: 11px; }
  .log-table td.row-actions { display: flex; justify-content: flex-end; }
  .log-table td.row-actions::before { display: none; }
  .pagination-bar { align-items: flex-start; flex-direction: column; padding: 10px 14px; }
  .log-detail dl > div { grid-template-columns: 96px minmax(0, 1fr); }
}
@media (max-width: 420px) { .log-toolbar { grid-template-columns: minmax(0, 1fr); } .log-toolbar .search-field { grid-column: auto; } }
:root[data-theme="dark"] .log-status { color: #6ee7b7; background: rgba(16,185,129,.12); border-color: rgba(16,185,129,.2); }
:root[data-theme="dark"] .log-status.warning { color: #fbbf24; background: rgba(245,158,11,.12); border-color: rgba(245,158,11,.25); }
:root[data-theme="dark"] .log-status.error { color: #fca5a5; background: rgba(239,68,68,.12); border-color: rgba(239,68,68,.25); }
</style>
