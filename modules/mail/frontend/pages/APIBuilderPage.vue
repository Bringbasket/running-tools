<script setup lang="ts">
import { computed, ref } from 'vue'
import { Braces, Copy, LoaderCircle, Play } from '../../../../frontend/src/icons'
import { authState } from '../../../../frontend/src/auth'

type Endpoint = { id: string; method: 'GET' | 'POST'; path: string; label: string; needsID?: boolean; body?: string }
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

const resolvedPath = computed(() => selected.value.path.replace('{id}', encodeURIComponent(anonymousID.value.trim() || '{id}')))
const curl = computed(() => {
  const lines = [`curl --request ${selected.value.method} '${location.origin}${resolvedPath.value}'`, `  --header 'X-API-Key: YOUR_API_KEY'`]
  if (selected.value.method === 'POST') lines.push("  --header 'Content-Type: application/json'", `  --data '${body.value.replaceAll("'", "'\\''")}'`)
  return lines.join(' \\\n')
})

function choose(endpoint: Endpoint) { selected.value = endpoint; body.value = endpoint.body || ''; status.value = ''; responseText.value = '尚未发送请求' }
async function send() {
  if (selected.value.needsID && !anonymousID.value.trim()) { status.value = '请输入 anonymousId'; return }
  loading.value = true; status.value = ''
  try {
    if (selected.value.method === 'POST') JSON.parse(body.value || '{}')
    const result = await fetch(resolvedPath.value, { method: selected.value.method, headers: { 'X-API-Key': authState.apiKey, ...(selected.value.method === 'POST' ? { 'Content-Type': 'application/json' } : {}) }, body: selected.value.method === 'POST' ? (body.value || '{}') : undefined })
    status.value = `HTTP ${result.status} ${result.statusText}`
    const text = await result.text()
    try { responseText.value = JSON.stringify(JSON.parse(text), null, 2) } catch { responseText.value = text }
  } catch (error) { status.value = error instanceof Error ? error.message : String(error) }
  finally { loading.value = false }
}
async function copyCurl() { await navigator.clipboard.writeText(curl.value); status.value = 'cURL 已复制' }
</script>

<template>
  <section class="page api-builder">
    <div class="page-heading"><div><span class="eyebrow">MAIL API</span><h2>API 调试</h2><p>选择邮件接口，编辑请求参数并检查服务端响应。</p></div></div>
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
