<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ChevronRight, Clock3, KeyRound, LoaderCircle, RefreshCw, Save, ShieldAlert, ShieldCheck, Upload } from '../../../../frontend/src/icons'
import { errorMessage } from '../../../../frontend/src/api'
import { mailAPI } from '../api'
import type { AutoRefreshStatus, SessionStatus } from '../types'
import StatusBadge from '../components/StatusBadge.vue'

type Channel = 'icloud_web' | 'apple_account'
type TwoFactorMethod = 'trusted_device' | 'phone'

const session = ref<SessionStatus | null>(null)
const auto = ref<AutoRefreshStatus | null>(null)
const pageLoading = ref(false)
const checkLoading = ref(false)
const loginLoading = ref(false)
const autoLoading = ref(false)
const importLoading = ref(false)
const error = ref('')
const notice = ref('')
const channel = ref<Channel>('apple_account')
const method = ref<TwoFactorMethod>('trusted_device')
const appleId = ref('')
const password = ref('')
const pendingId = ref('')
const verificationCode = ref('')
const pendingExpiresAt = ref<number | null>(null)
const importText = ref('')
const enabled = ref(true)
const interval = ref(600)
const compatibilityOpen = ref(false)

const sessionState = computed(() => !session.value?.persistedSession ? { state: 'neutral' as const, label: '未登录' } : session.value.needsReauth ? { state: 'invalid' as const, label: '需要重新登录' } : session.value.sessionValid ? { state: 'valid' as const, label: '有效' } : { state: 'neutral' as const, label: '等待检查' })
const appleAccount = computed(() => session.value?.appleLogin?.appleAccount)
const iCloudWeb = computed(() => session.value?.appleLogin?.icloudWeb)
const currentCreateChannel = computed(() => session.value?.appleLogin?.createChannel === 'apple_account' ? 'Apple Account' : 'iCloud Web')
const loginTitle = computed(() => pendingId.value ? '输入验证码' : channel.value === 'icloud_web' ? '登录 iCloud Web' : '登录 Apple Account')

function formatTime(value: number | null | undefined) {
  return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value * 1000)) : '—'
}

function clearFeedback() { error.value = ''; notice.value = '' }

async function load() {
  pageLoading.value = true; error.value = ''
  try {
    [session.value, auto.value] = await Promise.all([mailAPI.session(), mailAPI.autoRefresh()])
    enabled.value = auto.value.enabled; interval.value = auto.value.intervalSeconds
    if (!appleId.value) appleId.value = appleAccount.value?.appleId || iCloudWeb.value?.appleId || ''
  } catch (reason) { error.value = errorMessage(reason) }
  finally { pageLoading.value = false }
}

async function startLogin() {
  clearFeedback(); loginLoading.value = true
  try {
    const result = await mailAPI.startAppleLogin({ appleId: appleId.value.trim(), password: password.value, channel: channel.value, twoFactorMethod: method.value })
    password.value = ''
    if (result.needs2FA && result.pendingId) {
      pendingId.value = result.pendingId; pendingExpiresAt.value = result.expiresAt || null; notice.value = result.message
    } else {
      if (result.channel === 'icloud_web') session.value = await mailAPI.refreshSession()
      else session.value = await mailAPI.session()
      notice.value = result.message
    }
  } catch (reason) { password.value = ''; error.value = errorMessage(reason) }
  finally { loginLoading.value = false }
}

async function verifyLogin() {
  clearFeedback(); loginLoading.value = true
  try {
    const result = await mailAPI.verifyAppleLogin(pendingId.value, verificationCode.value)
    if (result.channel === 'icloud_web') session.value = await mailAPI.refreshSession()
    else session.value = await mailAPI.session()
    notice.value = result.message; pendingId.value = ''; verificationCode.value = ''; pendingExpiresAt.value = null
  } catch (reason) { error.value = errorMessage(reason) }
  finally { loginLoading.value = false }
}

function cancelVerification() { pendingId.value = ''; verificationCode.value = ''; pendingExpiresAt.value = null; clearFeedback() }

function channelStatusText(value: typeof appleAccount.value, channelName: 'Apple Account' | 'iCloud Web') {
  if ((value?.cooldownRemainingSeconds || 0) > 0) return `创建冷却中 · ${formatDuration(value?.cooldownRemainingSeconds || 0)}`
  if (value?.lastCreateAt) return `最近创建 ${formatTime(value.lastCreateAt)}`
  return channelName === 'Apple Account' ? '优先创建通道' : '邮箱管理与备用创建通道'
}

function formatDuration(seconds: number) {
  if (seconds < 60) return `${seconds} 秒`
  return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
}

async function refresh() {
  checkLoading.value = true; clearFeedback()
  try { session.value = await mailAPI.refreshSession(); notice.value = session.value.sessionValid ? '会话检查成功' : '检查完成，请查看状态' }
  catch (reason) { error.value = errorMessage(reason) }
  finally { checkLoading.value = false }
}

async function importSession() {
  importLoading.value = true; clearFeedback()
  try { const result = await mailAPI.importSession(importText.value); notice.value = `已导入 ${result.host}`; importText.value = ''; await load() }
  catch (reason) { error.value = errorMessage(reason) }
  finally { importLoading.value = false }
}

async function saveAuto() {
  autoLoading.value = true; clearFeedback()
  try { auto.value = await mailAPI.updateAutoRefresh({ enabled: enabled.value, intervalSeconds: interval.value }); notice.value = '自动检查设置已保存' }
  catch (reason) { error.value = errorMessage(reason) }
  finally { autoLoading.value = false }
}

function handleAccountChange() { session.value = null; auto.value = null; appleId.value = ''; void load() }
onMounted(() => { window.addEventListener('mail-account-change', handleAccountChange); void load() })
onBeforeUnmount(() => window.removeEventListener('mail-account-change', handleAccountChange))
</script>

<template>
  <section class="page session-page">
    <div class="page-heading">
      <div><h2>Session 管理</h2><p>直接使用 Apple ID 建立服务端会话，手动导入仅作为兼容方式保留。</p></div>
      <button class="button ghost" :disabled="pageLoading" @click="load"><RefreshCw :size="16" :class="{ spin: pageLoading }" />刷新状态</button>
    </div>
    <p v-if="error" class="message error">{{ error }}</p><p v-if="notice" class="message success">{{ notice }}</p>

    <div class="session-workspace">
    <section class="session-surface login-surface">
      <div class="surface-heading">
        <div><h3>{{ loginTitle }}</h3><p>{{ pendingId ? `验证码将在 ${formatTime(pendingExpiresAt)} 前有效` : '密码仅用于本次 SRP 登录，不会保存到服务器。' }}</p></div>
        <div v-if="!pendingId" class="segmented channel-switch" aria-label="登录接口">
          <button :class="{ active: channel === 'apple_account' }" @click="channel = 'apple_account'">Apple Account</button>
          <button :class="{ active: channel === 'icloud_web' }" @click="channel = 'icloud_web'">iCloud Web</button>
        </div>
      </div>

      <form v-if="!pendingId" class="apple-login-form" @submit.prevent="startLogin">
        <label class="field"><span>Apple ID</span><input v-model="appleId" type="email" autocomplete="username" placeholder="name@example.com" required /></label>
        <label class="field"><span>密码</span><input v-model="password" type="password" autocomplete="current-password" placeholder="Apple ID 密码" required /></label>
        <label class="field compact-field"><span>验证码方式</span><select v-model="method"><option value="trusted_device">受信任设备</option><option value="phone">短信</option></select></label>
        <button class="button primary login-submit" :disabled="loginLoading || !appleId.trim() || !password"><LoaderCircle v-if="loginLoading" :size="16" class="spin" /><KeyRound v-else :size="16" />开始登录</button>
      </form>

      <form v-else class="verification-form" @submit.prevent="verifyLogin">
        <label class="field"><span>6 位验证码</span><input v-model="verificationCode" class="verification-input" inputmode="numeric" maxlength="6" pattern="[0-9]{6}" autocomplete="one-time-code" placeholder="000000" required /></label>
        <div class="verification-actions"><button type="button" class="button ghost" :disabled="loginLoading" @click="cancelVerification">取消</button><button class="button primary" :disabled="loginLoading || verificationCode.length !== 6"><LoaderCircle v-if="loginLoading" :size="16" class="spin" /><ShieldCheck v-else :size="16" />完成登录</button></div>
      </form>

      <div class="channel-note"><ShieldAlert :size="16" /><span v-if="channel === 'apple_account'">默认优先创建通道；这是 Apple 网页内部接口，可能随时调整。</span><span v-else>用于邮箱同步、列表和管理，并在 Apple Account 不可用时接管创建。</span></div>
    </section>

    <section class="session-surface status-surface">
      <div class="surface-heading"><div><h3>登录状态</h3><p>创建时优先使用健康的 Apple Account 通道，其他邮箱操作使用 iCloud Web。</p></div><span class="route-indicator">当前创建：{{ currentCreateChannel }}</span></div>
      <div class="channel-status-list">
        <div class="channel-status-row">
          <div class="channel-identity"><span class="channel-icon account"><KeyRound :size="17" /></span><div><strong>Apple Account</strong><small>{{ appleAccount?.appleId || '尚未登录' }}</small></div></div>
          <div class="channel-meta"><span>{{ channelStatusText(appleAccount, 'Apple Account') }}</span><StatusBadge :state="appleAccount?.cooldownRemainingSeconds ? 'neutral' : appleAccount?.healthy ? 'valid' : appleAccount?.configured ? 'invalid' : 'neutral'" :label="appleAccount?.cooldownRemainingSeconds ? '冷却中' : appleAccount?.healthy ? '有效' : appleAccount?.configured ? '待自动恢复' : '未登录'" /></div>
        </div>
        <div class="channel-status-row">
          <div class="channel-identity"><span class="channel-icon"><ShieldCheck :size="17" /></span><div><strong>iCloud Web</strong><small>{{ iCloudWeb?.appleId || session?.metadata?.host || '尚未登录' }}</small></div></div>
          <div class="channel-meta"><span>{{ channelStatusText(iCloudWeb, 'iCloud Web') }}</span><StatusBadge :state="iCloudWeb?.cooldownRemainingSeconds ? 'neutral' : sessionState.state" :label="iCloudWeb?.cooldownRemainingSeconds ? '冷却中' : sessionState.label" /></div>
        </div>
      </div>
      <div class="status-details"><span>邮箱数量 <strong>{{ session?.hme?.aliasCount ?? '—' }}</strong></span><span>转发地址 <strong>{{ session?.hme?.selectedForwardTo || '—' }}</strong></span><button class="button ghost" :disabled="checkLoading || (!session?.persistedSession && !appleAccount?.configured)" @click="refresh"><RefreshCw :size="16" :class="{ spin: checkLoading }" />检查会话</button></div>
    </section>

    <section class="session-surface auto-surface">
      <div class="surface-heading"><div><h3>自动检查</h3><p>后台按计划验证登录态，失效后停止向 Apple 重复请求。</p></div><span class="worker-state"><span :class="{ active: auto?.workerRunning }" />{{ auto?.workerRunning ? '运行中' : '未运行' }}</span></div>
      <div class="auto-settings">
        <label class="toggle-row"><span><strong>定时检查</strong><small>同时验证 iCloud Web 与 Apple Account 状态。</small></span><input v-model="enabled" type="checkbox" role="switch" /></label>
        <label class="field interval-field"><span>检查间隔（秒）</span><input v-model.number="interval" type="number" min="300" step="60" /></label>
        <div class="next-check"><Clock3 :size="15" /><span>下次检查</span><strong>{{ formatTime(auto?.nextRunAt) }}</strong></div>
        <button class="button primary" :disabled="autoLoading" @click="saveAuto"><LoaderCircle v-if="autoLoading" :size="16" class="spin" /><Save v-else :size="16" />保存设置</button>
      </div>
    </section>

    <section class="session-surface compatibility-surface">
      <button class="compatibility-trigger" :aria-expanded="compatibilityOpen" @click="compatibilityOpen = !compatibilityOpen"><span><Upload :size="17" /><span><strong>兼容导入</strong><small>使用 cURL 或 HAR 恢复 iCloud Web 会话</small></span></span><ChevronRight :size="17" :class="{ expanded: compatibilityOpen }" /></button>
      <div v-if="compatibilityOpen" class="compatibility-body">
        <label class="field"><span>cURL 或 HAR JSON</span><textarea v-model="importText" class="code-input" rows="8" spellcheck="false" placeholder="粘贴 /v2/hme/list 请求的 Copy as cURL (bash)，或包含 request cookies 的 HAR JSON。" /></label>
        <div class="import-footer"><span>Cookie 以仅所有者可读权限保存在服务器数据目录，接口不会返回原文。</span><button class="button primary" :disabled="importLoading || !importText.trim()" @click="importSession"><LoaderCircle v-if="importLoading" :size="16" class="spin" /><Upload v-else :size="16" />导入并保存</button></div>
      </div>
    </section>
    </div>
  </section>
</template>

<style scoped>
.session-workspace { overflow: hidden; background: var(--surface); border: 1px solid var(--border-soft); border-radius: 8px; }
.session-surface { overflow: hidden; border-bottom: 1px solid var(--border-soft); }
.session-surface:last-child { border-bottom: 0; }
.login-surface, .status-surface, .auto-surface { padding: 20px; }
.surface-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 18px; }
.surface-heading h3 { margin: 0 0 4px; color: var(--text); font-size: 16px; }
.surface-heading p { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.5; }
.channel-switch { flex: 0 0 auto; }
.apple-login-form { display: grid; grid-template-columns: minmax(220px, 1fr) minmax(220px, 1fr) minmax(160px, .65fr) auto; align-items: end; gap: 12px; }
.apple-login-form .field, .auto-settings .field { margin: 0; }
.field { min-width: 0; }
.field select { width: 100%; min-height: 38px; padding: 0 32px 0 11px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 7px; outline: none; }
.login-submit { min-width: 118px; min-height: 40px; }
.channel-note { display: flex; align-items: center; gap: 8px; margin-top: 14px; color: var(--muted); font-size: 11px; }
.verification-form { display: flex; max-width: 520px; align-items: end; gap: 12px; }
.verification-form .field { flex: 1; margin: 0; }
.verification-input { font-family: ui-monospace, monospace; font-size: 19px !important; letter-spacing: 7px !important; }
.verification-actions { display: flex; min-height: 40px; align-items: stretch; gap: 8px; }
.verification-actions .button { min-height: 40px; }
.channel-status-list { border-top: 1px solid var(--border-soft); }
.channel-status-row { display: flex; min-height: 70px; align-items: center; justify-content: space-between; gap: 16px; border-bottom: 1px solid var(--border-soft); }
.channel-identity, .channel-meta { display: flex; align-items: center; gap: 12px; }
.channel-identity > div { display: flex; min-width: 0; flex-direction: column; gap: 3px; }
.channel-identity strong { color: var(--text); font-size: 13px; }
.channel-identity small, .channel-meta > span { color: var(--muted); font-size: 11px; }
.channel-icon { display: grid; width: 34px; height: 34px; flex: 0 0 34px; color: #0f766e; background: #f0fdfa; border-radius: 7px; place-items: center; }
.channel-icon.account { color: #2563eb; background: #eff6ff; }
:root[data-theme="dark"] .channel-icon { color: #5eead4; background: rgba(20,184,166,.13); }
:root[data-theme="dark"] .channel-icon.account { color: #93c5fd; background: rgba(37,99,235,.14); }
.channel-meta > span { min-width: 180px; text-align: right; }
.route-indicator { padding: 5px 8px; color: var(--primary-text); background: var(--primary-soft); border-radius: 5px; font-size: 11px; white-space: nowrap; }
.status-details { display: flex; min-height: 54px; align-items: center; gap: 24px; padding-top: 14px; }
.status-details > span { display: flex; flex-direction: column; gap: 3px; color: var(--muted); font-size: 10px; }
.status-details strong { max-width: 320px; overflow: hidden; color: var(--text); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.status-details .button { margin-left: auto; }
.auto-settings { display: grid; grid-template-columns: minmax(240px, 1fr) 170px minmax(200px, .8fr) auto; align-items: end; gap: 20px; }
.auto-settings .toggle-row { margin: 0; padding: 0; border: 0; }
.next-check { display: grid; grid-template-columns: auto 1fr; align-items: center; gap: 2px 7px; padding-bottom: 3px; color: var(--muted); font-size: 10px; }
.next-check strong { grid-column: 2; color: var(--text); font-size: 11px; }
.compatibility-trigger { display: flex; width: 100%; min-height: 62px; align-items: center; justify-content: space-between; padding: 0 20px; color: var(--text); background: transparent; border: 0; text-align: left; }
.compatibility-trigger > span { display: flex; align-items: center; gap: 10px; }
.compatibility-trigger > span > span { display: flex; flex-direction: column; gap: 3px; }
.compatibility-trigger strong { font-size: 13px; }
.compatibility-trigger small { color: var(--muted); font-size: 11px; }
.compatibility-trigger > svg { transition: transform 150ms ease; }
.compatibility-trigger > svg.expanded { transform: rotate(90deg); }
.compatibility-body { padding: 18px 20px 20px; border-top: 1px solid var(--border-soft); }

@media (max-width: 1024px) {
  .apple-login-form { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .login-submit { width: 100%; }
  .auto-settings { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 620px) {
  .surface-heading, .channel-status-row, .channel-meta, .status-details, .verification-form { align-items: stretch; flex-direction: column; }
  .channel-switch { width: 100%; }
  .apple-login-form, .auto-settings { grid-template-columns: 1fr; }
  .channel-meta { gap: 7px; }
  .channel-meta > span { min-width: 0; text-align: left; }
  .status-details .button { width: 100%; margin-left: 0; }
  .verification-actions .button { flex: 1; }
}
</style>
