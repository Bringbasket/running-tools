<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, LoaderCircle, MailOpen, RefreshCw, Save, Settings2, ShieldCheck, Trash2, X } from '../../../../frontend/src/icons'
import { errorMessage } from '../../../../frontend/src/api'
import { mailAPI } from '../api'
import type { MailboxSettings, MailboxSettingsInput, MailboxStatus, MailMessage } from '../types'

const status = ref<MailboxStatus | null>(null)
const loading = ref(false)
const error = ref('')
const notice = ref('')
const initialAlias = new URLSearchParams(window.location.search).get('alias')?.trim().toLowerCase() || ''
const alias = ref(initialAlias)
const scopedAlias = ref(initialAlias)
const messages = ref<MailMessage[]>([])
const selected = ref(new Set<string>())
const detail = ref<MailMessage | null>(null)
const detailMode = ref<'text' | 'html'>('text')
const page = ref(1)
const pageSize = ref(10)
const settingsOpen = ref(false)
const settingsLoading = ref(false)
const settingsSaving = ref(false)
const settingsTesting = ref(false)
const settingsError = ref('')
const settingsNotice = ref('')
const passwordConfigured = ref(false)
const settingsSource = ref<MailboxSettings['source']>('environment')
const settingsForm = ref<MailboxSettingsInput>({
  username: '', password: '', host: 'imap.mail.me.com', port: 993, mailbox: 'INBOX',
  enabled: false, pollSeconds: 120, lookbackDays: 90, cacheMax: 5000,
})
let stopped = false

const providerHosts = new Set(['imap.mail.me.com', 'imap.icloud.com', 'imap.gmail.com', 'outlook.office365.com'])

function recommendedIMAPHost(username: string) {
  const address = username.trim().toLowerCase()
  if (address.endsWith('@icloud.com') || address.endsWith('@me.com') || address.endsWith('@mac.com')) return 'imap.mail.me.com'
  if (address.endsWith('@gmail.com') || address.endsWith('@googlemail.com')) return 'imap.gmail.com'
  if (address.endsWith('@outlook.com') || address.endsWith('@hotmail.com') || address.endsWith('@live.com')) return 'outlook.office365.com'
  return ''
}

const filtered = computed(() => {
  const query = alias.value.trim().toLowerCase()
  return query ? messages.value.filter((item) => item.aliases.some((address) => address.includes(query))) : messages.value
})
const pageCount = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize.value)))
const paged = computed(() => filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
const rangeStart = computed(() => filtered.value.length ? (page.value - 1) * pageSize.value + 1 : 0)
const rangeEnd = computed(() => Math.min(page.value * pageSize.value, filtered.value.length))
const keyOf = (message: MailMessage, address = message.aliases[0] || '') => `${address}:${message.uid}`
const syncMode = computed(() => ({ idle: '实时监听', sync: '正在同步', poll: '定时轮询', disabled: '未启用', stopped: '已停止' }[status.value?.syncMode || ''] || '等待启动'))

watch([alias, pageSize], () => { page.value = 1 })
watch(pageCount, (count) => { if (page.value > count) page.value = count })
watch(() => settingsForm.value.username, (username) => {
  const recommended = recommendedIMAPHost(username)
  const current = settingsForm.value.host.trim().toLowerCase()
  if (recommended && (!current || providerHosts.has(current))) settingsForm.value.host = recommended
})

async function load(showLoading = true) {
  if (showLoading) loading.value = true
  error.value = ''
  try {
    const result = scopedAlias.value
      ? await mailAPI.mailboxMessages(scopedAlias.value, 100)
      : await mailAPI.mailboxRecent(500)
    messages.value = result.messages
    status.value = result.sync
    selected.value = new Set()
  } catch (reason) {
    error.value = errorMessage(reason)
  } finally {
    if (showLoading) loading.value = false
  }
}
async function run() {
  loading.value = true
  error.value = ''
  try {
    status.value = await mailAPI.mailboxRun()
    await load(false)
  } catch (reason) {
    error.value = errorMessage(reason)
  } finally {
    loading.value = false
  }
}
function applySettings(settings: MailboxSettings) {
  settingsForm.value = {
    username: settings.username,
    password: '',
    host: settings.host,
    port: settings.port,
    mailbox: settings.mailbox,
    enabled: settings.enabled,
    pollSeconds: settings.pollSeconds,
    lookbackDays: settings.lookbackDays,
    cacheMax: settings.cacheMax,
  }
  passwordConfigured.value = settings.passwordConfigured
  settingsSource.value = settings.source
}
async function openSettings() {
  settingsOpen.value = true
  settingsLoading.value = true
  settingsError.value = ''
  settingsNotice.value = ''
  try {
    applySettings(await mailAPI.mailboxSettings())
  } catch (reason) {
    settingsError.value = errorMessage(reason)
  } finally {
    settingsLoading.value = false
  }
}
async function testSettings() {
  settingsTesting.value = true
  settingsError.value = ''
  settingsNotice.value = ''
  try {
    await mailAPI.testMailboxSettings(settingsForm.value)
    settingsNotice.value = '连接成功，账号和邮箱目录可用'
  } catch (reason) {
    settingsError.value = errorMessage(reason)
  } finally {
    settingsTesting.value = false
  }
}
async function saveSettings() {
  settingsSaving.value = true
  settingsError.value = ''
  settingsNotice.value = ''
  try {
    const saved = await mailAPI.updateMailboxSettings(settingsForm.value)
    applySettings(saved)
    settingsOpen.value = false
    notice.value = saved.enabled ? 'IMAP 设置已保存，后台同步已启动' : 'IMAP 设置已保存，后台同步未启用'
    await load(false)
  } catch (reason) {
    settingsError.value = errorMessage(reason)
  } finally {
    settingsSaving.value = false
  }
}
async function watchMailbox() {
  while (!stopped) {
    const revision = status.value?.revision
    if (revision == null) {
      await new Promise((resolve) => setTimeout(resolve, 1000))
      continue
    }
    try {
      const next = await mailAPI.mailboxWait(revision)
      if (next.revision !== revision) await load(false)
      else status.value = next
    } catch {
      if (!stopped) await new Promise((resolve) => setTimeout(resolve, 3000))
    }
  }
}
async function openMessage(message: MailMessage) {
  const address = alias.value.trim().toLowerCase() || message.aliases[0]
  if (!address) return
  try {
    detail.value = await mailAPI.mailboxMessage(address, message.uid)
    detailMode.value = 'text'
  } catch (reason) {
    error.value = errorMessage(reason)
  }
}
function toggle(message: MailMessage) {
  const key = keyOf(message)
  const next = new Set(selected.value)
  next.has(key) ? next.delete(key) : next.add(key)
  selected.value = next
}
async function hideOne(message: MailMessage) {
  if (!status.value) return
  const address = alias.value.trim().toLowerCase() || message.aliases[0]
  if (!address) return
  try {
    await mailAPI.hideMailboxMessage(address, message.uid, status.value)
    messages.value = messages.value.filter((item) => item.uid !== message.uid)
    detail.value = null
  } catch (reason) {
    error.value = errorMessage(reason)
  }
}
async function hideSelected() {
  if (!status.value || selected.value.size === 0) return
  const items = messages.value.filter((item) => selected.value.has(keyOf(item))).map((item) => ({ alias: item.aliases[0], uid: item.uid }))
  try {
    await mailAPI.hideMailboxMessages(items, status.value)
    const keys = selected.value
    messages.value = messages.value.filter((item) => !keys.has(keyOf(item)))
    selected.value = new Set()
  } catch (reason) {
    error.value = errorMessage(reason)
  }
}
async function copyCode(code: string) {
  try {
    await window.navigator.clipboard.writeText(code)
  } catch (reason) {
    error.value = errorMessage(reason)
  }
}
async function clearMessages() {
  if (!messages.value.length || !window.confirm(`确认永久清理当前账号的 ${messages.value.length} 封本地收件箱缓存？`)) return
  loading.value = true; error.value = ''
  try { await mailAPI.clearMailboxMessages(); messages.value = []; page.value = 1 }
  catch (reason) { error.value = errorMessage(reason) }
  finally { loading.value = false }
}
function setPage(next: number) { page.value = Math.min(pageCount.value, Math.max(1, next)) }
function clearAliasFilter() {
  scopedAlias.value = ''
  alias.value = ''
  void load()
}

async function handleAccountChange() { stopped = true; await load(); stopped = false; void watchMailbox() }
onMounted(async () => { window.addEventListener('mail-account-change', handleAccountChange); await load(); void watchMailbox() })
onBeforeUnmount(() => { stopped = true; window.removeEventListener('mail-account-change', handleAccountChange) })
</script>

<template>
  <section class="page mailbox-page">
    <div class="page-heading">
      <div><h2>收件箱</h2><p>查看隐藏邮箱转发的最近邮件和验证码。</p></div>
      <div class="page-actions">
        <button class="button ghost danger-action" :disabled="loading || !messages.length" @click="clearMessages"><Trash2 :size="16" />清理缓存</button>
        <button class="button ghost" :disabled="settingsLoading" @click="openSettings"><Settings2 :size="16" />IMAP 设置</button>
        <button class="button ghost" :disabled="loading" @click="load()"><RefreshCw :size="16" :class="{ spin: loading }" />刷新列表</button>
        <button class="button primary" :disabled="loading || !status?.configured" @click="run"><LoaderCircle v-if="loading" :size="16" class="spin" /><MailOpen v-else :size="16" />立即同步</button>
      </div>
    </div>

    <p v-if="error" class="message error">{{ error }}</p>
    <p v-if="notice" class="message success">{{ notice }}</p>
    <div v-if="!status?.configured" class="message warning mailbox-warning"><span>尚未配置 IMAP 收件账号和应用专用密码。</span><button class="button ghost compact" @click="openSettings"><Settings2 :size="15" />现在配置</button></div>

    <div class="mailbox-summary" aria-label="收件箱同步状态">
      <span class="state-dot" :class="{ active: status?.workerRunning }"></span>
      <strong>{{ status?.configured ? syncMode : 'IMAP 未配置' }}</strong>
      <span>{{ status?.host || '服务器环境变量' }}</span>
      <span class="summary-separator"></span>
      <span>最近 72 小时 <strong>{{ filtered.length }}</strong> 封</span>
      <span>版本 {{ status?.revision || 0 }}</span>
      <span class="summary-time">{{ status?.lastSyncAt ? `上次同步 ${new Date(status.lastSyncAt * 1000).toLocaleString('zh-CN')}` : '尚未同步' }}</span>
    </div>

    <div class="table-panel">
      <div class="toolbar mailbox-toolbar">
        <label class="search-field"><MailOpen :size="17" /><input v-model="alias" placeholder="搜索隐藏邮箱地址" /></label>
        <span v-if="alias" class="mailbox-filter">当前地址：{{ alias }} <button class="icon-button" title="清除地址筛选" aria-label="清除地址筛选" @click="clearAliasFilter"><X :size="14" /></button></span>
        <div class="toolbar-tail">
          <button v-if="selected.size" class="button ghost" @click="hideSelected"><Trash2 :size="16" />隐藏所选 {{ selected.size }} 封</button>
          <label class="page-size">每页<select v-model.number="pageSize"><option :value="10">10</option><option :value="20">20</option><option :value="50">50</option><option :value="100">100</option></select>条</label>
        </div>
      </div>
      <div class="data-frame">
        <table>
          <thead><tr><th class="select-cell"></th><th>邮件</th><th>隐藏邮箱</th><th>验证码</th><th>时间</th><th class="action-cell"></th></tr></thead>
          <tbody>
            <tr v-for="message in paged" :key="message.uid">
              <td><input type="checkbox" :checked="selected.has(keyOf(message))" aria-label="选择邮件" @change="toggle(message)" /></td>
              <td class="message-summary" @click="openMessage(message)"><strong>{{ message.subject || '无主题' }}</strong><small>{{ message.from }}</small><span>{{ message.text }}</span></td>
              <td class="alias-cell">{{ message.aliases.join('、') }}</td>
              <td class="code-cell"><button v-for="code in [...message.partnerCodes, ...message.codes]" :key="code" class="code-chip" title="复制验证码" @click="copyCode(code)">{{ code }}</button><span v-if="!message.codes.length && !message.partnerCodes.length" class="muted">—</span></td>
              <td class="time-cell">{{ new Date(message.date * 1000).toLocaleString('zh-CN') }}</td>
              <td><button class="icon-button" title="从本地列表隐藏" @click="hideOne(message)"><Trash2 :size="16" /></button></td>
            </tr>
            <tr v-if="!loading && paged.length === 0" class="empty-row"><td colspan="6" class="empty-state"><MailOpen :size="28" /><strong>{{ alias ? '没有匹配的邮件' : '最近 72 小时暂无邮件' }}</strong></td></tr>
          </tbody>
        </table>
      </div>
      <div class="pagination-bar">
        <span>显示 {{ rangeStart }}–{{ rangeEnd }}，共 {{ filtered.length }} 封</span>
        <div class="pagination-actions"><button class="icon-button" title="上一页" :disabled="page === 1" @click="setPage(page - 1)"><ChevronLeft :size="16" /></button><strong>第 {{ page }} / {{ pageCount }} 页</strong><button class="icon-button" title="下一页" :disabled="page === pageCount" @click="setPage(page + 1)"><ChevronRight :size="16" /></button></div>
      </div>
    </div>
  </section>

  <Teleport to="body">
    <div v-if="settingsOpen" class="dialog-backdrop" @click.self="settingsOpen = false">
      <article class="dialog mailbox-settings-dialog">
        <div class="dialog-heading"><div><h2>IMAP 收件设置</h2><p>通过 TLS 只读同步转发到主邮箱的邮件。</p></div><button class="icon-button" title="关闭" :disabled="settingsSaving || settingsTesting" @click="settingsOpen = false"><X :size="18" /></button></div>
        <p v-if="settingsError" class="message error">{{ settingsError }}</p>
        <p v-if="settingsNotice" class="message success">{{ settingsNotice }}</p>
        <div v-if="settingsLoading" class="settings-loading"><LoaderCircle :size="20" class="spin" />正在读取设置</div>
        <form v-else class="imap-form" @submit.prevent="saveSettings">
          <label class="toggle-row"><span><strong>启用后台同步</strong><small>优先实时监听，不支持 IMAP IDLE 时按间隔轮询。</small></span><input v-model="settingsForm.enabled" type="checkbox" role="switch" /></label>
          <div class="imap-form-grid">
            <label class="field full"><span>IMAP 账号</span><input v-model.trim="settingsForm.username" type="text" autocomplete="username" placeholder="mail@example.com" maxlength="320" /></label>
            <label class="field full"><span>应用专用密码</span><input v-model="settingsForm.password" type="password" autocomplete="new-password" :placeholder="passwordConfigured ? '已保存；留空表示不修改' : '请输入应用专用密码'" maxlength="4096" /><small>iCloud 必须使用 Apple Account 生成的 App 专用密码，不能使用 Apple ID 登录密码。密码不会通过页面或接口读回。</small></label>
            <label class="field host-field"><span>IMAP 服务器</span><input v-model.trim="settingsForm.host" type="text" placeholder="imap.mail.me.com" /></label>
            <label class="field port-field"><span>端口</span><input v-model.number="settingsForm.port" type="number" min="1" max="65535" /></label>
            <label class="field full"><span>邮箱目录</span><input v-model.trim="settingsForm.mailbox" type="text" placeholder="INBOX" maxlength="255" /></label>
          </div>
          <div class="imap-advanced">
            <label class="field"><span>轮询间隔（秒）</span><input v-model.number="settingsForm.pollSeconds" type="number" min="30" max="86400" step="10" /></label>
            <label class="field"><span>首次回看（天）</span><input v-model.number="settingsForm.lookbackDays" type="number" min="1" max="3650" /></label>
            <label class="field"><span>缓存上限（封）</span><input v-model.number="settingsForm.cacheMax" type="number" min="100" max="50000" step="100" /></label>
          </div>
          <div class="settings-origin"><ShieldCheck :size="15" /><span>{{ settingsSource === 'saved' ? '当前使用网页保存的配置' : '当前读取服务器环境变量默认值' }}</span></div>
          <div class="dialog-actions imap-actions"><button type="button" class="button ghost" :disabled="settingsSaving || settingsTesting" @click="testSettings"><LoaderCircle v-if="settingsTesting" :size="16" class="spin" /><ShieldCheck v-else :size="16" />测试连接</button><div><button type="button" class="button ghost" :disabled="settingsSaving || settingsTesting" @click="settingsOpen = false">取消</button><button type="submit" class="button primary" :disabled="settingsSaving || settingsTesting"><LoaderCircle v-if="settingsSaving" :size="16" class="spin" /><Save v-else :size="16" />保存设置</button></div></div>
        </form>
      </article>
    </div>
    <div v-if="detail" class="dialog-backdrop" @click.self="detail = null">
      <article class="dialog mail-detail">
        <div class="dialog-heading"><div><h2>{{ detail.subject || '无主题' }}</h2><p>{{ detail.from }} · {{ new Date(detail.date * 1000).toLocaleString('zh-CN') }}</p></div><button class="icon-button" title="关闭" @click="detail = null"><X :size="18" /></button></div>
        <div class="detail-toolbar">
          <div class="detail-codes"><button v-for="code in [...detail.partnerCodes, ...detail.codes]" :key="code" class="code-chip" title="复制验证码" @click="copyCode(code)">{{ code }}</button></div>
          <div v-if="detail.safeHtml" class="mode-switch"><button :class="{ active: detailMode === 'text' }" @click="detailMode = 'text'">纯文本</button><button :class="{ active: detailMode === 'html' }" @click="detailMode = 'html'">原邮件</button></div>
        </div>
        <pre v-if="detailMode === 'text'">{{ detail.text }}</pre>
        <iframe v-else class="mail-html" sandbox="allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer" :srcdoc="detail.safeHtml" title="清理后的原邮件正文"></iframe>
      </article>
    </div>
  </Teleport>
</template>

<style scoped>
.mailbox-page { gap: 18px; }
.mailbox-filter { display: inline-flex; align-items: center; gap: 4px; color: var(--text-secondary); font-size: 13px; white-space: nowrap; }
.mailbox-filter .icon-button { width: 24px; height: 24px; }
.mailbox-warning { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.button.compact { min-height: 30px; padding: 0 10px; white-space: nowrap; }
.mailbox-summary { display: flex; min-height: 42px; align-items: center; gap: 10px; padding: 0 14px; color: var(--muted); background: var(--surface); border: 1px solid var(--border); border-radius: 7px; font-size: 12px; }
.mailbox-summary strong { color: var(--text); }
.state-dot { width: 7px; height: 7px; flex: 0 0 7px; background: var(--muted); border-radius: 50%; }
.state-dot.active { background: #16a34a; box-shadow: 0 0 0 3px color-mix(in srgb, #16a34a 15%, transparent); }
.summary-separator { width: 1px; height: 18px; background: var(--border); }
.summary-time { margin-left: auto; }
.select-cell { width: 36px; }
.action-cell { width: 48px; }
.message-summary { max-width: 420px; cursor: pointer; }
.message-summary strong, .message-summary span, .message-summary small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.message-summary span, .message-summary small { max-width: 420px; color: var(--muted); font-size: 11px; }
.message-summary strong { margin-bottom: 2px; }
.alias-cell { max-width: 260px; overflow-wrap: anywhere; }
.time-cell { width: 150px; color: var(--muted); font-size: 11px; white-space: nowrap; }
.code-chip { display: inline-flex; margin: 2px; padding: 3px 7px; color: var(--primary-text); background: var(--primary-soft); border: 0; border-radius: 5px; font-weight: 700; }
.toolbar-tail, .pagination-actions, .detail-toolbar, .detail-codes { display: flex; align-items: center; gap: 8px; }
.toolbar-tail { margin-left: auto; }
.page-size { display: inline-flex; align-items: center; gap: 7px; color: var(--muted); font-size: 11px; }
.page-size select { min-height: 31px; padding: 0 25px 0 8px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; }
.pagination-bar { display: flex; min-height: 48px; align-items: center; justify-content: space-between; gap: 16px; padding: 8px 14px; color: var(--muted); border-top: 1px solid var(--border); font-size: 11px; }
.pagination-actions strong { min-width: 80px; color: var(--text); text-align: center; }
.mail-detail { width: min(880px, calc(100vw - 32px)); max-width: 880px; }
.dialog-heading p { margin: 4px 0 0; color: var(--muted); font-size: 12px; }
.detail-toolbar { min-height: 42px; justify-content: space-between; margin-top: 10px; padding: 6px 0; border-bottom: 1px solid var(--border); }
.mode-switch { display: inline-flex; padding: 2px; background: var(--surface-soft); border: 1px solid var(--border); border-radius: 6px; }
.mode-switch button { min-height: 28px; padding: 0 10px; color: var(--muted); background: transparent; border: 0; border-radius: 4px; font-size: 11px; }
.mode-switch button.active { color: var(--text); background: var(--surface); box-shadow: 0 1px 2px #00000010; }
.mail-detail pre, .mail-html { width: 100%; height: min(56vh, 620px); margin: 0; padding: 18px 0 0; overflow: auto; border: 0; background: var(--surface); }
.mail-detail pre { white-space: pre-wrap; overflow-wrap: anywhere; font: 13px/1.65 inherit; }
.mail-html { background: #fff; }
.muted { color: var(--muted); }
.mailbox-settings-dialog { width: min(650px, calc(100vw - 32px)); max-width: 650px; }
.settings-loading { display: flex; min-height: 220px; align-items: center; justify-content: center; gap: 8px; color: var(--muted); }
.imap-form-grid { display: grid; grid-template-columns: minmax(0, 1fr) 110px; gap: 14px; }
.imap-form-grid .full { grid-column: 1 / -1; }
.field small { color: var(--muted); font-size: 11px; line-height: 1.45; }
.imap-advanced { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; margin-top: 14px; padding-top: 16px; border-top: 1px solid var(--border-soft); }
.settings-origin { display: flex; align-items: center; gap: 7px; margin-top: 14px; color: var(--muted); font-size: 11px; }
.imap-actions { align-items: center; justify-content: space-between; gap: 12px; }
.imap-actions > div { display: flex; gap: 8px; }
@media (max-width: 760px) {
  .mailbox-summary { flex-wrap: wrap; padding: 10px 12px; }
  .summary-separator { display: none; }
  .summary-time { width: 100%; margin-left: 17px; }
  .mailbox-toolbar, .toolbar-tail, .pagination-bar { align-items: stretch; flex-direction: column; }
  .toolbar-tail { width: 100%; margin-left: 0; }
  .page-size { margin-left: auto; }
  .pagination-actions { justify-content: space-between; }
  .data-frame { overflow: visible; }
  table, tbody, tbody tr, tbody td { display: block; width: 100%; }
  thead { display: none; }
  tbody tr { position: relative; min-height: 0; padding: 14px 44px 14px 14px; border-bottom: 1px solid var(--border); }
  tbody td { padding: 0; border: 0; }
  tbody td:first-child { position: absolute; top: 16px; right: 45px; width: 22px; }
  tbody td:last-child { position: absolute; top: 10px; right: 8px; width: 34px; }
  .message-summary { max-width: none; padding-right: 42px; }
  .message-summary span, .message-summary small { max-width: none; }
  .alias-cell, .code-cell, .time-cell { display: flex; max-width: none; align-items: flex-start; gap: 8px; margin-top: 10px; color: var(--text); white-space: normal; }
  .alias-cell::before, .code-cell::before, .time-cell::before { width: 52px; flex: 0 0 52px; color: var(--muted); font-size: 11px; }
  .alias-cell::before { content: '隐藏邮箱'; }
  .code-cell::before { content: '验证码'; }
  .time-cell::before { content: '时间'; }
  tbody tr.empty-row { min-height: 180px; padding: 0; }
  tbody tr.empty-row td.empty-state { position: static; display: flex; width: 100%; min-height: 180px; align-items: center; justify-content: center; padding: 24px; }
  .detail-toolbar { align-items: flex-start; flex-direction: column; }
  .mailbox-warning, .imap-actions { align-items: stretch; flex-direction: column; }
  .mailbox-warning .button { width: 100%; }
  .imap-advanced { grid-template-columns: 1fr; }
  .imap-actions > div { display: grid; grid-template-columns: 1fr 1fr; }
}
@media (max-width: 420px) {
  .imap-form-grid { grid-template-columns: 1fr; }
  .imap-form-grid .full { grid-column: auto; }
  .imap-actions > div { grid-template-columns: 1fr; }
}
</style>
