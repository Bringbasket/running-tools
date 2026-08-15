<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Braces, Copy, KeyRound, LoaderCircle, Play, Plus, Trash2 } from '../../../../frontend/src/icons'
import { apiRequest, errorMessage } from '../../../../frontend/src/api'

type Endpoint = { id: string; method: 'GET' | 'POST'; path: string; label: string; needsID?: boolean; body?: string }
type APIToken = { id: string; name: string; prefix: string; createdAt: string; lastUsedAt: string | null; expiresAt: string | null }
type CreatedAPIToken = APIToken & { token: string }

const endpoints: Endpoint[] = [
  { id: 'list', method: 'GET', path: '/api/mail/v1/aliases', label: '获取邮箱列表' },
  { id: 'status', method: 'GET', path: '/api/mail/v1/session/status', label: 'Session 状态' },
  { id: 'refresh', method: 'POST', path: '/api/mail/v1/session/refresh', label: '检查 Session', body: '{}' },
  { id: 'disable', method: 'POST', path: '/api/mail/v1/aliases/{id}/disable', label: '停用邮箱', needsID: true, body: '{}' },
  { id: 'enable', method: 'POST', path: '/api/mail/v1/aliases/{id}/enable', label: '启用邮箱', needsID: true, body: '{}' },
  { id: 'delete', method: 'POST', path: '/api/mail/v1/aliases/{id}/delete', label: '删除邮箱', needsID: true, body: '{}' },
]
const selected = ref(endpoints[0])
const anonymousID = ref('')
const body = ref('')
const responseText = ref('尚未发送请求')
const status = ref('')
const loading = ref(false)
const tokens = ref<APIToken[]>([])
const tokenName = ref('API 调试')
const tokenDays = ref(90)
const tokenLoading = ref(false)
const tokenError = ref('')
const createdToken = ref('')

const resolvedPath = computed(() => selected.value.path.replace('{id}', encodeURIComponent(anonymousID.value.trim() || '{id}')))
const curl = computed(() => {
  const bearer = createdToken.value || 'YOUR_ACCESS_TOKEN'
  const lines = [`curl --request ${selected.value.method} '${location.origin}${resolvedPath.value}'`, `  --header 'Authorization: Bearer ${bearer}'`]
  if (selected.value.method === 'POST') lines.push("  --header 'Content-Type: application/json'", `  --data '${body.value.replaceAll("'", "'\\''")}'`)
  return lines.join(' \\\n')
})

function choose(endpoint: Endpoint) { selected.value = endpoint; body.value = endpoint.body || ''; status.value = ''; responseText.value = '尚未发送请求' }

async function send() {
  if (selected.value.needsID && !anonymousID.value.trim()) { status.value = '请输入 anonymousId'; return }
  loading.value = true
  status.value = ''
  try {
    if (selected.value.method === 'POST') JSON.parse(body.value || '{}')
    const result = await fetch(resolvedPath.value, { method: selected.value.method, credentials: 'same-origin', headers: selected.value.method === 'POST' ? { 'Content-Type': 'application/json' } : {}, body: selected.value.method === 'POST' ? (body.value || '{}') : undefined })
    status.value = `HTTP ${result.status} ${result.statusText}`
    const text = await result.text()
    try { responseText.value = JSON.stringify(JSON.parse(text), null, 2) } catch { responseText.value = text }
  } catch (error) { status.value = error instanceof Error ? error.message : String(error) }
  finally { loading.value = false }
}

async function loadTokens() {
  try { tokens.value = (await apiRequest<{ items: APIToken[] }>('/api/auth/tokens')).items }
  catch (reason) { tokenError.value = errorMessage(reason) }
}

async function createToken() {
  tokenLoading.value = true
  tokenError.value = ''
  createdToken.value = ''
  try {
    const result = await apiRequest<CreatedAPIToken>('/api/auth/tokens', { method: 'POST', body: JSON.stringify({ name: tokenName.value, expiresInDays: tokenDays.value }) })
    createdToken.value = result.token
    await loadTokens()
  } catch (reason) { tokenError.value = errorMessage(reason) }
  finally { tokenLoading.value = false }
}

async function revokeToken(id: string) {
  if (!window.confirm('确定撤销这个访问令牌吗？使用它的脚本会立即失效。')) return
  try {
    await apiRequest(`/api/auth/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' })
    await loadTokens()
  } catch (reason) { tokenError.value = errorMessage(reason) }
}

async function copyCurl() { await navigator.clipboard.writeText(curl.value); status.value = 'cURL 已复制' }
async function copyToken() { if (createdToken.value) await navigator.clipboard.writeText(createdToken.value) }
function formatTime(value: string | null) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '从未使用' }

onMounted(loadTokens)
</script>

<template>
  <section class="page api-builder">
    <div class="page-heading"><div><span class="eyebrow">MAIL API</span><h2>API 调试</h2><p>在浏览器会话中调试接口，或创建独立令牌供脚本调用。</p></div></div>

    <section class="settings-section token-manager">
      <div class="section-heading"><div class="section-title-with-icon"><span class="section-icon"><KeyRound :size="18" /></span><div><span class="eyebrow">ACCESS TOKENS</span><h3>访问令牌</h3></div></div></div>
      <div class="token-create-row">
        <label class="field"><span>令牌名称</span><input v-model="tokenName" maxlength="100" /></label>
        <label class="field token-days"><span>有效期</span><select v-model.number="tokenDays"><option :value="30">30 天</option><option :value="90">90 天</option><option :value="180">180 天</option><option :value="365">365 天</option></select></label>
        <button class="button primary" :disabled="tokenLoading" @click="createToken"><LoaderCircle v-if="tokenLoading" :size="16" class="spin" /><Plus v-else :size="16" />创建令牌</button>
      </div>
      <div v-if="createdToken" class="token-secret"><div><strong>新令牌仅显示一次</strong><code>{{ createdToken }}</code></div><button class="button ghost" @click="copyToken"><Copy :size="15" />复制</button></div>
      <p v-if="tokenError" class="message error">{{ tokenError }}</p>
      <div v-if="tokens.length" class="token-list">
        <div v-for="token in tokens" :key="token.id"><span><strong>{{ token.name }}</strong><small>{{ token.prefix }}... · 最近使用：{{ formatTime(token.lastUsedAt) }}</small></span><button class="icon-button danger" title="撤销令牌" @click="revokeToken(token.id)"><Trash2 :size="16" /></button></div>
      </div>
      <p v-else-if="!tokenLoading" class="token-empty">暂无有效访问令牌</p>
    </section>

    <div class="builder-layout">
      <aside class="endpoint-panel"><div class="panel-title"><span><Braces :size="17" />接口列表</span><small>{{ endpoints.length }}</small></div><button v-for="endpoint in endpoints" :key="endpoint.id" :class="{ active: selected.id === endpoint.id }" @click="choose(endpoint)"><span :class="['method', endpoint.method.toLowerCase()]">{{ endpoint.method }}</span><span>{{ endpoint.label }}</span></button></aside>
      <div class="request-workspace">
        <section class="work-section">
          <div class="section-heading"><div><span class="eyebrow">REQUEST</span><h3>{{ selected.label }}</h3></div><button class="button primary" :disabled="loading" @click="send"><LoaderCircle v-if="loading" :size="16" class="spin" /><Play v-else :size="16" />发送请求</button></div>
          <div class="request-line"><span :class="['method', selected.method.toLowerCase()]">{{ selected.method }}</span><code>{{ resolvedPath }}</code></div>
          <label v-if="selected.needsID" class="field"><span>anonymousId</span><input v-model="anonymousID" placeholder="邮箱记录的 anonymousId" /></label>
          <label v-if="selected.method === 'POST'" class="field"><span>JSON 请求体</span><textarea v-model="body" class="code-input" rows="8" spellcheck="false" /></label>
          <div class="code-block"><button class="icon-button copy-button" title="复制 cURL" @click="copyCurl"><Copy :size="16" /></button><pre>{{ curl }}</pre></div>
        </section>
        <section class="work-section response-section"><div class="section-heading"><div><span class="eyebrow">RESPONSE</span><h3>响应结果</h3></div><span class="response-status" :class="{ filled: status }">{{ status || '等待请求' }}</span></div><pre class="response-output">{{ responseText }}</pre></section>
      </div>
    </div>
  </section>
</template>

<style scoped>
.token-manager { margin-bottom: 16px; }
.token-create-row { display: grid; grid-template-columns: minmax(200px, 1fr) 130px auto; align-items: end; gap: 12px; }
.token-create-row .button { height: 42px; margin: 0; }
.token-days { min-width: 0; }
.token-secret { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-top: 14px; padding: 12px; color: var(--primary-text); background: var(--primary-soft); border: 1px solid color-mix(in srgb, var(--primary) 22%, transparent); border-radius: 7px; }
.token-secret > div { display: grid; min-width: 0; gap: 6px; }
.token-secret strong { font-size: 12px; }
.token-secret code { overflow: hidden; color: var(--text); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.token-list { display: grid; margin-top: 14px; border-top: 1px solid var(--border-soft); }
.token-list > div { display: flex; min-height: 54px; align-items: center; justify-content: space-between; gap: 12px; border-bottom: 1px solid var(--border-soft); }
.token-list span { display: grid; gap: 4px; }
.token-list strong { font-size: 12px; }
.token-list small, .token-empty { color: var(--muted); font-size: 11px; }
.token-empty { margin: 14px 0 0; }
@media (max-width: 700px) { .token-create-row { grid-template-columns: minmax(0, 1fr) 110px; } .token-create-row .button { grid-column: 1 / -1; } .token-secret { align-items: stretch; flex-direction: column; } }
@media (max-width: 420px) { .token-create-row { grid-template-columns: minmax(0, 1fr); } .token-create-row .button { grid-column: auto; } }
</style>
