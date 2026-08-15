<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ChevronDown, KeyRound, LoaderCircle, LogOut, Menu, Moon, Sun, UserRound, X } from '@lucide/vue'
import { authState, changePassword, logout } from '../auth'
import { modules } from '../modules'
import { showToast } from '../toast'

defineEmits<{ openNavigation: [] }>()
const route = useRoute()
const dark = ref(document.documentElement.dataset.theme === 'dark')
const accountOpen = ref(false)
const passwordOpen = ref(false)
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const passwordError = ref('')
const savingPassword = ref(false)
const title = computed(() => String(route.meta.title || 'Running Tools'))
const section = computed(() => modules.find((module) => route.path.startsWith(`/${module.id}/`))?.label || '控制台')

function toggleTheme() {
  dark.value = !dark.value
  const value = dark.value ? 'dark' : 'light'
  document.documentElement.dataset.theme = value
  localStorage.setItem('running-theme', value)
}

function openPasswordDialog() {
  accountOpen.value = false
  passwordError.value = ''
  currentPassword.value = ''
  newPassword.value = ''
  confirmPassword.value = ''
  passwordOpen.value = true
}

async function savePassword() {
  if (!currentPassword.value || !newPassword.value) {
    passwordError.value = '请填写当前密码和新密码'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    passwordError.value = '两次输入的新密码不一致'
    return
  }
  savingPassword.value = true
  passwordError.value = ''
  try {
    await changePassword(currentPassword.value, newPassword.value)
    passwordOpen.value = false
    showToast('密码已更新，其他浏览器会话已退出')
  } catch (reason) {
    passwordError.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    savingPassword.value = false
  }
}

function closeAccountMenu() { accountOpen.value = false }
onMounted(() => window.addEventListener('click', closeAccountMenu))
onBeforeUnmount(() => window.removeEventListener('click', closeAccountMenu))
</script>

<template>
  <header class="app-header">
    <div class="header-title">
      <button class="icon-button menu-button" title="打开菜单" @click="$emit('openNavigation')"><Menu :size="20" /></button>
      <div class="header-breadcrumb"><span class="header-context">{{ section }}</span><span class="breadcrumb-divider">/</span><h1>{{ title }}</h1></div>
    </div>
    <div class="header-actions">
      <button class="icon-button" :title="dark ? '切换浅色模式' : '切换深色模式'" @click="toggleTheme"><Sun v-if="dark" :size="18" /><Moon v-else :size="18" /></button>
      <div class="account-menu" @click.stop>
        <button class="account-trigger" :class="{ active: accountOpen }" @click="accountOpen = !accountOpen"><span><UserRound :size="16" /></span><strong>{{ authState.user?.username }}</strong><ChevronDown :size="15" /></button>
        <div v-if="accountOpen" class="account-popover">
          <div class="account-summary"><span><UserRound :size="18" /></span><div><strong>{{ authState.user?.username }}</strong><small>管理员账号</small></div></div>
          <button @click="openPasswordDialog"><KeyRound :size="16" />修改密码</button>
          <button class="danger" @click="logout"><LogOut :size="16" />退出登录</button>
        </div>
      </div>
    </div>
  </header>

  <Teleport to="body">
    <div v-if="passwordOpen" class="dialog-backdrop" @click.self="passwordOpen = false">
      <form class="dialog security-dialog" @submit.prevent="savePassword">
        <div class="dialog-header"><div><span class="eyebrow">ACCOUNT SECURITY</span><h3>修改登录密码</h3></div><button class="icon-button" type="button" title="关闭" @click="passwordOpen = false"><X :size="18" /></button></div>
        <label class="field"><span>当前密码</span><input v-model="currentPassword" type="password" autocomplete="current-password" autofocus /></label>
        <label class="field"><span>新密码</span><input v-model="newPassword" type="password" autocomplete="new-password" placeholder="至少 10 个字符，包含两类字符" /></label>
        <label class="field"><span>确认新密码</span><input v-model="confirmPassword" type="password" autocomplete="new-password" /></label>
        <p v-if="passwordError" class="message error">{{ passwordError }}</p>
        <div class="dialog-actions"><button class="button ghost" type="button" @click="passwordOpen = false">取消</button><button class="button primary" :disabled="savingPassword" type="submit"><LoaderCircle v-if="savingPassword" :size="16" class="spin" /><KeyRound v-else :size="16" />保存密码</button></div>
      </form>
    </div>
  </Teleport>
</template>

<style scoped>
.account-menu { position: relative; }
.account-trigger { display: flex; height: 38px; align-items: center; gap: 8px; padding: 0 10px 0 7px; color: var(--muted); background: var(--surface); border: 1px solid var(--border); border-radius: 7px; }
.account-trigger:hover, .account-trigger.active { color: var(--text); background: var(--surface-hover); }
.account-trigger > span { display: grid; width: 25px; height: 25px; place-items: center; color: var(--primary-text); background: var(--primary-soft); border-radius: 6px; }
.account-trigger strong { max-width: 120px; overflow: hidden; font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.account-popover { position: absolute; top: calc(100% + 8px); right: 0; z-index: 55; width: 210px; padding: 7px; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; box-shadow: 0 14px 36px rgba(15, 23, 42, .14); }
.account-summary { display: flex; align-items: center; gap: 10px; margin-bottom: 6px; padding: 9px; border-bottom: 1px solid var(--border-soft); }
.account-summary > span { display: grid; width: 32px; height: 32px; place-items: center; color: var(--primary-text); background: var(--primary-soft); border-radius: 7px; }
.account-summary div { display: flex; min-width: 0; flex-direction: column; }
.account-summary strong { overflow: hidden; font-size: 12px; text-overflow: ellipsis; }
.account-summary small { margin-top: 3px; color: var(--muted); font-size: 10px; }
.account-popover > button { display: flex; width: 100%; height: 35px; align-items: center; gap: 9px; padding: 0 9px; color: var(--text); background: transparent; border: 0; border-radius: 6px; font-size: 12px; text-align: left; }
.account-popover > button:hover { background: var(--surface-hover); }
.account-popover > button.danger { color: var(--danger); }
.security-dialog { width: min(440px, calc(100vw - 28px)); }
@media (max-width: 560px) { .account-trigger strong, .account-trigger > svg { display: none; } .account-trigger { width: 38px; padding: 0; justify-content: center; } .account-trigger > span { background: transparent; } }
</style>
