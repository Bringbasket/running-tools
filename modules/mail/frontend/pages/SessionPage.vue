<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CheckCircle2, Clock3, LoaderCircle, Play, RefreshCw, Save, ShieldAlert, Upload } from '../../../../frontend/src/icons'
import { errorMessage } from '../../../../frontend/src/api'
import { mailAPI } from '../api'
import type { AutoRefreshStatus, SessionStatus } from '../types'
import StatusBadge from '../components/StatusBadge.vue'

const session = ref<SessionStatus | null>(null)
const auto = ref<AutoRefreshStatus | null>(null)
const loading = ref(false)
const error = ref('')
const notice = ref('')
const region = ref<'international' | 'china'>('international')
const importText = ref('')
const enabled = ref(true)
const interval = ref(600)
const sessionState = computed(() => !session.value?.persistedSession ? { state: 'neutral' as const, label: '未导入' } : session.value.needsReauth ? { state: 'invalid' as const, label: '需要重新导入' } : session.value.sessionValid ? { state: 'valid' as const, label: 'Session 有效' } : { state: 'neutral' as const, label: '等待检查' })

function formatTime(value: number | null | undefined) { return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value * 1000)) : '—' }
async function load() {
  loading.value = true; error.value = ''
  try { [session.value, auto.value] = await Promise.all([mailAPI.session(), mailAPI.autoRefresh()]); enabled.value = auto.value.enabled; interval.value = auto.value.intervalSeconds }
  catch (reason) { error.value = errorMessage(reason) }
  finally { loading.value = false }
}
async function refresh() { loading.value = true; error.value = ''; try { session.value = await mailAPI.refreshSession(); notice.value = session.value.sessionValid ? 'Session 检查成功' : 'Session 检查完成，请查看状态' } catch (reason) { error.value = errorMessage(reason) } finally { loading.value = false } }
async function importSession() { loading.value = true; error.value = ''; notice.value = ''; try { const result = await mailAPI.importSession(importText.value, region.value); notice.value = `已导入 ${result.host}`; importText.value = ''; await load() } catch (reason) { error.value = errorMessage(reason) } finally { loading.value = false } }
async function saveAuto() { loading.value = true; error.value = ''; try { auto.value = await mailAPI.updateAutoRefresh({ enabled: enabled.value, intervalSeconds: interval.value }); notice.value = '自动刷新设置已保存' } catch (reason) { error.value = errorMessage(reason) } finally { loading.value = false } }
async function runAuto() { loading.value = true; error.value = ''; try { const result = await mailAPI.runAutoRefresh(); auto.value = result.autoRefresh; session.value = result.session; enabled.value = auto.value.enabled; notice.value = '已执行一次 Session 检查' } catch (reason) { error.value = errorMessage(reason) } finally { loading.value = false } }
onMounted(load)
</script>

<template>
  <section class="page session-page">
    <div class="page-heading"><div><span class="eyebrow">ICLOUD SESSION</span><h2>Session 管理</h2><p>导入浏览器会话，并由服务端定时检查其有效性。</p></div><button class="button ghost" :disabled="loading" @click="load"><RefreshCw :size="16" :class="{ spin: loading }" />刷新状态</button></div>
    <p v-if="error" class="message error">{{ error }}</p><p v-if="notice" class="message success">{{ notice }}</p>

    <div class="session-grid">
      <section class="settings-section status-section">
        <div class="section-heading"><div class="section-title-with-icon"><span class="section-icon"><CheckCircle2 :size="18" /></span><div><span class="eyebrow">CURRENT STATUS</span><h3>当前会话</h3></div></div><StatusBadge :state="sessionState.state" :label="sessionState.label" /></div>
        <div v-if="session?.needsReauth" class="alert-banner"><ShieldAlert :size="19" /><span>Apple 已拒绝当前会话，请重新从浏览器导入。</span></div>
        <dl class="detail-list">
          <div><dt>iCloud 主机</dt><dd>{{ session?.metadata?.host || '—' }}</dd></div>
          <div><dt>DSID</dt><dd>{{ session?.metadata?.dsid || '—' }}</dd></div>
          <div><dt>最近检查</dt><dd>{{ formatTime(session?.lastRefreshAt) }}</dd></div>
          <div><dt>最近有效</dt><dd>{{ formatTime(session?.lastValidAt) }}</dd></div>
          <div><dt>邮箱数量</dt><dd>{{ session?.hme?.aliasCount ?? '—' }}</dd></div>
          <div><dt>转发地址</dt><dd>{{ session?.hme?.selectedForwardTo || '—' }}</dd></div>
        </dl>
        <button class="button secondary" :disabled="loading || !session?.persistedSession" @click="refresh"><CheckCircle2 :size="16" />立即检查</button>
      </section>

      <section class="settings-section">
        <div class="section-heading"><div class="section-title-with-icon"><span class="section-icon"><RefreshCw :size="18" /></span><div><span class="eyebrow">KEEPALIVE</span><h3>自动刷新</h3></div></div><span class="worker-state"><span :class="{ active: auto?.workerRunning }" />{{ auto?.workerRunning ? '工作中' : '未运行' }}</span></div>
        <label class="toggle-row"><span><strong>定时检查 Session</strong><small>失效时自动停用，避免重复请求 Apple。</small></span><input v-model="enabled" type="checkbox" role="switch" /></label>
        <label class="field"><span>检查间隔（秒）</span><input v-model.number="interval" type="number" min="300" step="60" /></label>
        <div class="inline-details"><span><Clock3 :size="15" />下次检查</span><strong>{{ formatTime(auto?.nextRunAt) }}</strong></div>
        <div class="split-actions"><button class="button ghost" :disabled="loading" @click="runAuto"><Play :size="16" />执行一次</button><button class="button primary" :disabled="loading" @click="saveAuto"><Save :size="16" />保存设置</button></div>
      </section>
    </div>

    <section class="settings-section import-section">
      <div class="section-heading"><div class="section-title-with-icon"><span class="section-icon"><Upload :size="18" /></span><div><span class="eyebrow">SESSION IMPORT</span><h3>手动导入 Session</h3></div></div></div>
      <div class="region-row"><span>iCloud 区域</span><div class="segmented"><button :class="{ active: region === 'international' }" @click="region = 'international'">国际版</button><button :class="{ active: region === 'china' }" @click="region = 'china'">中国大陆版</button></div></div>
      <label class="field"><span>cURL 或 HAR JSON</span><textarea v-model="importText" class="code-input" rows="11" spellcheck="false" placeholder="在浏览器开发者工具 Network 中找到 /v2/hme/list，请使用 Copy as cURL (bash)，或粘贴包含 request cookies 的 HAR JSON。" /></label>
      <div class="import-footer"><span>导入内容会持久化到服务器的数据目录，接口不会返回 Cookie。</span><button class="button primary" :disabled="loading || !importText.trim()" @click="importSession"><LoaderCircle v-if="loading" :size="16" class="spin" /><Upload v-else :size="16" />导入并保存</button></div>
    </section>
  </section>
</template>
