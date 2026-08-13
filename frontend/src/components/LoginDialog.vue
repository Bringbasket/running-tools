<script setup lang="ts">
import { ref } from 'vue'
import { KeyRound, LoaderCircle } from '@lucide/vue'
import { apiRequest, errorMessage } from '../api'
import { authState, setAPIKey } from '../auth'

const input = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  if (!input.value.trim()) { error.value = '请输入 API Key'; return }
  setAPIKey(input.value)
  loading.value = true; error.value = ''
  try { await apiRequest('/api/system/version') }
  catch (reason) { authState.loginOpen = true; error.value = errorMessage(reason) }
  finally { loading.value = false }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="authState.loginOpen" class="dialog-backdrop auth-backdrop">
      <form class="dialog login-dialog" @submit.prevent="submit">
        <span class="login-mark"><KeyRound :size="22" /></span>
        <span class="eyebrow">RUNNING TOOLS</span>
        <h2>进入管理控制台</h2>
        <p>使用服务器环境变量中配置的 API Key。</p>
        <label class="field"><span>API Key</span><input v-model="input" type="password" autocomplete="current-password" autofocus placeholder="输入访问密钥" /></label>
        <p v-if="error" class="message error">{{ error }}</p>
        <button class="button primary full" :disabled="loading" type="submit"><LoaderCircle v-if="loading" :size="17" class="spin" />登录</button>
      </form>
    </div>
  </Teleport>
</template>
