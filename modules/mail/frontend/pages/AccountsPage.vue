<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Check, ChevronLeft, ChevronRight, LoaderCircle, Network, Plus, Search, ShieldCheck, Trash2, UsersRound } from '../../../../frontend/src/icons'
import { errorMessage } from '../../../../frontend/src/api'
import AppDialog from '../../../../frontend/src/components/AppDialog.vue'
import AsyncState from '../../../../frontend/src/components/AsyncState.vue'
import { showToast } from '../../../../frontend/src/toast'
import { createMailAccount, deleteMailAccount, loadMailAccounts, mailAccountState, selectMailAccount, testMailAccountProxy, updateMailAccountProxy, type MailAccount, type MailAccountProxyTest } from '../account'

const loading = ref(false)
const creating = ref(false)
const createOpen = ref(false)
const deleteTarget = ref<MailAccount | null>(null)
const proxyTarget = ref<MailAccount | null>(null)
const deletingID = ref('')
const savingProxy = ref(false)
const testingProxy = ref(false)
const name = ref('')
const deleteConfirm = ref('')
const proxyValue = ref('')
const error = ref('')
const createError = ref('')
const deleteError = ref('')
const proxyError = ref('')
const proxyTestError = ref('')
const testedProxy = ref('')
const proxyTestResult = ref<MailAccountProxyTest | null>(null)
const search = ref('')
const page = ref(1)
const pageSize = ref(10)

const filteredAccounts = computed(() => {
  const query = search.value.trim().toLowerCase()
  if (!query) return mailAccountState.accounts
  return mailAccountState.accounts.filter((account) => [account.name, account.appleId, account.dsid]
    .some((value) => value?.toLowerCase().includes(query)))
})
const pageCount = computed(() => Math.max(1, Math.ceil(filteredAccounts.value.length / pageSize.value)))
const pagedAccounts = computed(() => filteredAccounts.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
const rangeStart = computed(() => filteredAccounts.value.length === 0 ? 0 : (page.value - 1) * pageSize.value + 1)
const rangeEnd = computed(() => Math.min(page.value * pageSize.value, filteredAccounts.value.length))
const canSaveProxy = computed(() => Boolean(proxyTarget.value && proxyValue.value.trim() && testedProxy.value === proxyValue.value.trim() && proxyTestResult.value?.reachable))

async function load() {
  loading.value = true
  error.value = ''
  try { await loadMailAccounts() }
  catch (reason) { error.value = errorMessage(reason) }
  finally { loading.value = false }
}

function openCreate() {
  name.value = ''
  createError.value = ''
  createOpen.value = true
}

function closeCreate() {
  if (creating.value) return
  createOpen.value = false
}

async function create() {
  const value = name.value.trim()
  if (!value || creating.value) return
  creating.value = true
  createError.value = ''
  try {
    await createMailAccount(value)
    search.value = ''
    page.value = Math.max(1, Math.ceil(mailAccountState.accounts.length / pageSize.value))
    createOpen.value = false
    showToast(`已添加母号 ${value}`)
  } catch (reason) {
    createError.value = errorMessage(reason)
  } finally {
    creating.value = false
  }
}

function useAccount(account: MailAccount) {
  if (!account.enabled || account.id === mailAccountState.currentId) return
  selectMailAccount(account.id)
  showToast(`已切换到 ${account.name}`, 'info')
}

function openProxy(account: MailAccount) {
  proxyTarget.value = account
  proxyValue.value = ''
  proxyError.value = ''
  proxyTestError.value = ''
  testedProxy.value = ''
  proxyTestResult.value = null
}

function closeProxy() {
  if (savingProxy.value || testingProxy.value) return
  proxyTarget.value = null
}

async function testProxy() {
  const account = proxyTarget.value
  const proxy = proxyValue.value.trim()
  if (!account || testingProxy.value || savingProxy.value || !proxy) return
  testingProxy.value = true
  testedProxy.value = ''
  proxyTestResult.value = null
  proxyTestError.value = ''
  proxyError.value = ''
  try {
    const result = await testMailAccountProxy(account.id, proxy)
    if (proxyTarget.value?.id !== account.id || proxyValue.value.trim() !== proxy) return
    testedProxy.value = proxy
    proxyTestResult.value = result
  } catch (reason) {
    if (proxyTarget.value?.id === account.id && proxyValue.value.trim() === proxy) {
      proxyTestError.value = errorMessage(reason)
    }
  } finally {
    testingProxy.value = false
  }
}

async function saveProxy() {
  const account = proxyTarget.value
  const proxy = proxyValue.value.trim()
  if (!account || savingProxy.value || testingProxy.value || !proxy) return
  if (!canSaveProxy.value) {
    proxyError.value = '请先测试当前代理，连接成功后再保存'
    return
  }
  savingProxy.value = true
  proxyError.value = ''
  try {
    await updateMailAccountProxy(account.id, proxy)
    proxyTarget.value = null
    showToast(`已更新 ${account.name} 的代理`)
  } catch (reason) {
    proxyError.value = errorMessage(reason)
  } finally {
    savingProxy.value = false
  }
}

function openDelete(account: MailAccount) {
  if (account.id === 'default') return
  deleteError.value = ''
  deleteConfirm.value = ''
  deleteTarget.value = account
}

function closeDelete() {
  if (deletingID.value) return
  deleteTarget.value = null
  deleteError.value = ''
}

async function confirmDelete() {
  const account = deleteTarget.value
  if (!account || deletingID.value || deleteConfirm.value !== account.name) return
  deletingID.value = account.id
  deleteError.value = ''
  try {
    await deleteMailAccount(account.id)
    deleteTarget.value = null
    showToast(`已永久删除 ${account.name}`)
  } catch (reason) {
    deleteError.value = errorMessage(reason)
  } finally {
    deletingID.value = ''
  }
}

function healthText(channel: MailAccount['icloudWeb'], label: string) {
  if (!channel.configured) return `${label} 未配置`
  if (!channel.lastCheckedAt) return `${label} 待检测`
  return `${label} ${channel.healthy ? '正常' : '异常'}`
}

function statusClass(account: MailAccount) {
  return account.status === 'active' ? 'active' : account.status === 'warning' ? 'warning' : account.status === 'error' ? 'invalid' : 'neutral'
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}

watch([search, pageSize], () => { page.value = 1 })
watch(pageCount, (count) => { if (page.value > count) page.value = count })
watch(proxyValue, () => {
  testedProxy.value = ''
  proxyTestResult.value = null
  proxyTestError.value = ''
  proxyError.value = ''
})
onMounted(load)
</script>

<template>
  <section class="page mail-accounts">
    <div class="page-heading">
      <div><h2>账号管理</h2><p>为每个 iCloud 母号建立独立运行空间，并选择当前操作账号</p></div>
      <div class="page-actions"><button class="button primary" @click="openCreate"><Plus :size="16" />添加母号</button></div>
    </div>

    <p v-if="error" class="message error account-message">{{ error }}</p>
    <section class="accounts-workspace">
      <div class="accounts-toolbar">
        <label class="search-field"><Search :size="16" /><input v-model="search" placeholder="搜索账号名称、Apple ID 或 DSID" /></label>
        <button class="button ghost" :disabled="loading" @click="load"><LoaderCircle v-if="loading" :size="16" class="spin" /><UsersRound v-else :size="16" />刷新账号</button>
      </div>
      <div class="account-list-meta">
        <span>母号列表 <small>共 {{ filteredAccounts.length }} 个</small></span>
        <label class="page-size">每页<select v-model.number="pageSize"><option :value="10">10 条</option><option :value="20">20 条</option><option :value="50">50 条</option><option :value="100">100 条</option></select></label>
      </div>
      <div class="data-frame">
        <table>
          <thead><tr><th>账号名称</th><th>Apple ID / DSID</th><th>运行状态</th><th>更新时间</th><th><span class="sr-only">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="account in pagedAccounts" :key="account.id" :class="{ 'current-account': account.id === mailAccountState.currentId }">
              <td data-label="账号名称"><div class="account-identity"><strong class="account-name" :title="account.name">{{ account.name }}</strong><small>{{ account.id === 'default' ? '默认运行空间' : account.id }}</small></div></td>
              <td data-label="Apple ID / DSID"><div class="account-identity"><span class="identity-value" :title="account.appleId || undefined">{{ account.appleId || '尚未登录' }}</span><small>{{ account.dsid ? `DSID ${account.dsid}` : '进入 Session 管理完成登录' }}</small></div></td>
              <td data-label="运行状态"><div class="account-health"><span class="status-badge" :class="statusClass(account)"><span />{{ account.statusMessage }}<em v-if="account.id === mailAccountState.currentId"> · 当前使用</em></span><small class="health-detail">{{ healthText(account.icloudWeb, 'Web') }} · {{ healthText(account.appleAccount, 'Account') }} · {{ account.mailbox.configured ? (account.mailbox.lastError ? 'IMAP 异常' : 'IMAP 已配置') : 'IMAP 未配置' }}</small></div></td>
              <td data-label="更新时间"><span>{{ formatTime(account.updatedAt) }}</span></td>
              <td class="row-actions"><div class="account-row-actions">
                <RouterLink v-if="account.id === mailAccountState.currentId" to="/mail/session" class="button ghost account-action" :title="account.appleId ? '查看 Session' : '配置 Session'" :aria-label="account.appleId ? '查看 Session' : '配置 Session'"><ShieldCheck :size="15" /><span>{{ account.appleId ? '查看 Session' : '配置 Session' }}</span></RouterLink>
                <button v-else class="button ghost account-action" :disabled="!account.enabled" title="切换使用" aria-label="切换使用" @click="useAccount(account)"><Check :size="15" /><span>切换使用</span></button>
                <button class="icon-button" :class="{ 'proxy-configured': account.hasProxy }" :title="account.hasProxy ? `修改 ${account.name} 的代理` : `配置 ${account.name} 的代理`" :aria-label="account.hasProxy ? `修改 ${account.name} 的代理` : `配置 ${account.name} 的代理`" @click="openProxy(account)"><Network :size="16" /></button>
                <button class="icon-button danger" :disabled="account.id === 'default' || deletingID === account.id" :title="account.id === 'default' ? '默认账号不能删除' : `删除 ${account.name}`" :aria-label="account.id === 'default' ? '默认账号不能删除' : `删除 ${account.name}`" @click="openDelete(account)"><LoaderCircle v-if="deletingID === account.id" :size="16" class="spin" /><Trash2 v-else :size="16" /></button>
              </div>
              </td>
            </tr>
            <tr v-if="loading && mailAccountState.accounts.length === 0"><td colspan="5" class="empty-state"><AsyncState state="loading" title="正在读取母号" /></td></tr>
            <tr v-else-if="error && mailAccountState.accounts.length === 0"><td colspan="5" class="empty-state"><AsyncState state="error" title="母号加载失败" :detail="error" @retry="load" /></td></tr>
            <tr v-else-if="filteredAccounts.length === 0"><td colspan="5" class="empty-state"><AsyncState state="empty" :title="search ? '没有匹配的母号' : '尚未添加母号'" :detail="search ? '调整搜索关键词后重试' : '添加母号后再配置独立 Session'"><template #icon><UsersRound :size="28" /></template></AsyncState></td></tr>
          </tbody>
        </table>
      </div>
      <div class="pagination-bar"><span>显示 {{ rangeStart }}–{{ rangeEnd }}，共 {{ filteredAccounts.length }} 个</span><div><button class="icon-button" title="上一页" :disabled="page <= 1 || loading" @click="page--"><ChevronLeft :size="17" /></button><strong>第 {{ page }} / {{ pageCount }} 页</strong><button class="icon-button" title="下一页" :disabled="page >= pageCount || loading" @click="page++"><ChevronRight :size="17" /></button></div></div>
    </section>
  </section>

  <AppDialog id="create-account" :open="createOpen" title="添加母号" subtitle="创建独立运行空间后，再为该母号配置 Session" :busy="creating" @close="closeCreate">
    <form id="create-account-form" @submit.prevent="create"><label class="field"><span>账号名称</span><input v-model="name" maxlength="120" autocomplete="off" placeholder="例如：hubacall@163.com" autofocus /></label><p v-if="createError" class="message error">{{ createError }}</p></form>
    <template #actions><button type="button" class="button ghost" :disabled="creating" @click="closeCreate">取消</button><button form="create-account-form" class="button primary" :disabled="creating || !name.trim()"><LoaderCircle v-if="creating" :size="15" class="spin" /><Plus v-else :size="15" />创建并使用</button></template>
  </AppDialog>

  <AppDialog id="account-proxy" :open="Boolean(proxyTarget)" title="账号代理" :subtitle="proxyTarget?.hasProxy ? '已配置代理；测试新地址成功后保存将直接覆盖' : '该代理将用于此母号的 Apple 登录和 iCloud 请求'" :busy="savingProxy || testingProxy" @close="closeProxy">
    <form id="account-proxy-form" @submit.prevent="saveProxy">
      <label class="field"><span>代理地址</span><input v-model="proxyValue" type="password" autocomplete="new-password" placeholder="http://user:pass@host:port" autofocus /><small>支持 http、https、socks5；测试不会保存地址，连接成功后才能保存</small></label>
      <p v-if="proxyTestResult" class="message success proxy-test-message">连接成功 · HTTP {{ proxyTestResult.statusCode }} · {{ proxyTestResult.latencyMs }} ms</p>
      <p v-if="proxyTestError" class="message error proxy-test-message">{{ proxyTestError }}</p>
      <p v-if="proxyError" class="message error">{{ proxyError }}</p>
    </form>
    <template #actions><button type="button" class="button ghost proxy-cancel-button" :disabled="savingProxy || testingProxy" @click="closeProxy">取消</button><button type="button" class="button ghost proxy-test-button" :disabled="testingProxy || savingProxy || !proxyValue.trim()" @click="testProxy"><LoaderCircle v-if="testingProxy" :size="15" class="spin" /><Network v-else :size="15" />测试连接</button><button type="submit" form="account-proxy-form" class="button primary proxy-save-button" :disabled="savingProxy || testingProxy || !canSaveProxy"><LoaderCircle v-if="savingProxy" :size="15" class="spin" /><Network v-else :size="15" />保存代理</button></template>
  </AppDialog>

  <AppDialog id="delete-account" :open="Boolean(deleteTarget)" title="删除母号" subtitle="此操作不可恢复" role="alertdialog" :busy="Boolean(deletingID)" @close="closeDelete">
    <div v-if="deleteTarget" class="delete-account-copy"><p>将永久删除账号 <strong>{{ deleteTarget.name }}</strong>。</p><p>该账号的 Session、自动任务、邮件缓存、分享链接和使用日志都会被清理，其他母号不受影响。</p></div>
    <label v-if="deleteTarget" class="field delete-confirm-field"><span>输入“{{ deleteTarget.name }}”确认</span><input v-model="deleteConfirm" autocomplete="off" /></label>
    <p v-if="deleteError" class="message error">{{ deleteError }}</p>
    <template #actions><button type="button" class="button ghost" :disabled="Boolean(deletingID)" @click="closeDelete">取消</button><button type="button" class="button danger-action danger-confirm" :disabled="Boolean(deletingID) || deleteConfirm !== deleteTarget?.name" @click="confirmDelete"><LoaderCircle v-if="deletingID" :size="15" class="spin" /><Trash2 v-else :size="15" />永久删除</button></template>
  </AppDialog>
</template>

<style scoped>
.accounts-workspace { overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 7px; }
.accounts-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 14px 16px; border-bottom: 1px solid var(--border-soft); }
.account-message { margin-bottom: 16px; }
.account-list-meta { display: flex; min-height: 48px; align-items: center; justify-content: space-between; gap: 12px; padding: 0 16px; }
.account-list-meta > span { color: var(--text); font-size: 13px; font-weight: 700; }
.account-list-meta small { margin-left: 7px; color: var(--muted); font-size: 11px; font-weight: 500; }
.page-size { display: inline-flex; align-items: center; gap: 8px; color: var(--muted); font-size: 11px; }
.page-size select { min-height: 31px; padding: 0 27px 0 9px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; outline: none; }
.account-name, .identity-value { display: block; max-width: 310px; overflow: hidden; color: var(--text); font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.identity-value { font-weight: 500; }
.account-identity { min-width: 0; }
.account-identity small { display: block; margin-top: 3px; }
.mail-accounts td { padding-top: 9px; padding-bottom: 9px; }
.current-account { background: color-mix(in srgb, var(--primary-soft) 48%, var(--surface)); }
.account-action { min-width: 124px; }
.account-row-actions { display: flex; align-items: center; justify-content: flex-end; gap: 6px; }
.account-row-actions .icon-button:disabled { color: var(--border); cursor: not-allowed; }
.account-row-actions .proxy-configured { color: var(--primary-text); background: var(--primary-soft); border-color: color-mix(in srgb, var(--primary) 24%, var(--border)); }
.account-health { display: grid; justify-items: start; gap: 6px; }
.account-health em { font-style: normal; }
.health-detail { display: block; max-width: 330px; overflow: hidden; color: var(--muted); font-size: 10px; font-weight: 500; text-overflow: ellipsis; white-space: nowrap; }
.mail-accounts td:nth-child(4) > span { white-space: nowrap; }
.pagination-bar { display: flex; min-height: 54px; align-items: center; justify-content: space-between; gap: 14px; padding: 0 16px; color: var(--muted); border-top: 1px solid var(--border); font-size: 11px; }
.pagination-bar > div { display: flex; align-items: center; gap: 9px; }
.pagination-bar strong { min-width: 82px; color: var(--text); font-size: 11px; font-weight: 600; text-align: center; }
.pagination-bar .icon-button { width: 31px; height: 31px; }
.delete-account-copy { padding: 14px 16px; background: var(--danger-soft); border: 1px solid color-mix(in srgb, var(--danger) 18%, transparent); border-radius: 7px; }
.delete-account-copy p { margin: 0; color: var(--text); font-size: 13px; line-height: 1.65; overflow-wrap: anywhere; }
.delete-account-copy p + p { margin-top: 6px; color: var(--muted); font-size: 12px; }
.danger-confirm { color: #fff; background: var(--danger); border-color: var(--danger); }
.danger-confirm:hover:not(:disabled) { color: #fff; background: color-mix(in srgb, var(--danger) 88%, #000); border-color: transparent; }
.delete-confirm-field { margin-top: 16px; }
.proxy-test-message { margin-top: 12px; }
@media (max-width: 1150px) and (min-width: 621px) {
  .account-action { width: 36px; min-width: 36px; padding: 0; }
  .account-action span { display: none; }
}
@media (max-width: 760px) {
  .accounts-toolbar { align-items: stretch; flex-direction: column; padding: 12px; }
  .accounts-toolbar .search-field { max-width: none; }
  .accounts-toolbar .button { width: 100%; }
}
@media (max-width: 620px) {
  .account-name, .identity-value { max-width: 100%; white-space: normal; overflow-wrap: anywhere; }
  .account-action { width: 100%; min-width: 0; }
  .account-row-actions { display: grid; width: 100%; grid-template-columns: minmax(0, 1fr) 36px 36px; }
  .pagination-bar { align-items: flex-start; flex-direction: column; padding: 11px 12px; }
  .pagination-bar > div { width: 100%; justify-content: space-between; }
}
@media (max-width: 420px) {
  .proxy-cancel-button, .proxy-test-button, .proxy-save-button { min-width: 0; flex: 1 1 0; padding-right: 7px; padding-left: 7px; }
}
</style>
