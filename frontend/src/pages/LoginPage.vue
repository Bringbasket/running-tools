<script setup lang="ts">
import { computed, ref } from 'vue'
import { Eye, EyeOff, Layers3, LoaderCircle, LockKeyhole, ShieldCheck, UserRound } from '@lucide/vue'
import { authState, changePassword, login } from '../auth'
import loginMain from '../assets/auth/login-main.svg'
import loginBackground from '../assets/auth/login-bg.svg'

const username = ref(localStorage.getItem('running-login-username') || 'admin')
const password = ref('')
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const showPassword = ref(false)
const rememberUsername = ref(Boolean(localStorage.getItem('running-login-username')))
const loading = ref(false)
const error = ref('')
const changingPassword = computed(() => Boolean(authState.authenticated && authState.user?.mustChangePassword))

async function submitLogin() {
  if (!username.value.trim() || !password.value) {
    error.value = '请输入账号和密码'
    return
  }
  loading.value = true
  error.value = ''
  try {
    await login(username.value, password.value)
    currentPassword.value = password.value
    password.value = ''
    if (rememberUsername.value) localStorage.setItem('running-login-username', username.value.trim())
    else localStorage.removeItem('running-login-username')
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    loading.value = false
  }
}

async function submitPasswordChange() {
  if (!currentPassword.value || !newPassword.value) {
    error.value = '请填写当前密码和新密码'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    error.value = '两次输入的新密码不一致'
    return
  }
  loading.value = true
  error.value = ''
  try {
    await changePassword(currentPassword.value, newPassword.value)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <section class="login-visual" aria-label="Running Tools">
      <div class="login-brand">
        <span class="login-brand-mark"><Layers3 :size="27" /></span>
        <div><strong>Running Tools</strong><small>邮箱、任务与工具管理平台</small></div>
      </div>
      <img class="login-illustration" :src="loginMain" alt="" />
      <img class="login-waves" :src="loginBackground" alt="" />
      <div class="login-visual-copy">
        <span>SELF-HOSTED WORKSPACE</span>
        <strong>让日常管理保持清晰、有序。</strong>
      </div>
    </section>

    <section class="login-form-side">
      <div class="login-panel">
        <span class="animated-border border-horizontal" />
        <span class="animated-border border-vertical" />
        <div class="login-panel-content">
          <div class="mobile-brand"><span><Layers3 :size="22" /></span><strong>Running Tools</strong></div>

          <template v-if="changingPassword">
            <div class="login-heading">
              <span class="login-heading-icon"><ShieldCheck :size="22" /></span>
              <span class="eyebrow">ACCOUNT SECURITY</span>
              <h1>设置新密码</h1>
              <p>首次登录需要更换初始密码，完成后进入管理平台。</p>
            </div>
            <form class="login-form" @submit.prevent="submitPasswordChange">
              <label class="login-field"><span>当前密码</span><div><LockKeyhole :size="17" /><input v-model="currentPassword" type="password" autocomplete="current-password" autofocus placeholder="输入初始密码" /></div></label>
              <label class="login-field"><span>新密码</span><div><LockKeyhole :size="17" /><input v-model="newPassword" type="password" autocomplete="new-password" placeholder="至少 10 个字符" /></div></label>
              <label class="login-field"><span>确认新密码</span><div><ShieldCheck :size="17" /><input v-model="confirmPassword" type="password" autocomplete="new-password" placeholder="再次输入新密码" /></div></label>
              <p v-if="error" class="login-error" role="alert">{{ error }}</p>
              <button class="login-submit" type="submit" :disabled="loading"><LoaderCircle v-if="loading" :size="18" class="spin" /><ShieldCheck v-else :size="18" />保存并进入平台</button>
            </form>
          </template>

          <template v-else>
            <div class="login-heading">
              <span class="login-heading-icon"><LockKeyhole :size="22" /></span>
              <span class="eyebrow">WELCOME BACK</span>
              <h1>欢迎回来</h1>
              <p>登录 Running Tools 管理控制台。</p>
            </div>
            <form class="login-form" @submit.prevent="submitLogin">
              <label class="login-field"><span>账号</span><div><UserRound :size="17" /><input v-model="username" autocomplete="username" autofocus placeholder="请输入账号" /></div></label>
              <label class="login-field">
                <span>密码</span>
                <div><LockKeyhole :size="17" /><input v-model="password" :type="showPassword ? 'text' : 'password'" autocomplete="current-password" placeholder="请输入密码" /><button type="button" class="password-toggle" :title="showPassword ? '隐藏密码' : '显示密码'" @click="showPassword = !showPassword"><EyeOff v-if="showPassword" :size="17" /><Eye v-else :size="17" /></button></div>
              </label>
              <label class="remember-account"><input v-model="rememberUsername" type="checkbox" />记住账号</label>
              <p v-if="error || authState.startupError" class="login-error" role="alert">{{ error || authState.startupError }}</p>
              <button class="login-submit" type="submit" :disabled="loading"><LoaderCircle v-if="loading" :size="18" class="spin" /><LockKeyhole v-else :size="18" />登录</button>
            </form>
          </template>
        </div>
      </div>
      <p class="login-footer">Running Tools · Self-hosted management workspace</p>
    </section>
  </main>
</template>

<style scoped>
.login-page { display: flex; min-height: 100vh; overflow: hidden; color: #17202e; background: #fff; }
.login-page, .login-page * { box-sizing: border-box; }
.login-visual { position: relative; flex: 1 1 auto; min-width: 0; margin-right: 80px; background: #d3efff; }
.login-brand { position: absolute; top: 42px; left: 52px; z-index: 3; display: flex; align-items: center; gap: 12px; }
.login-brand-mark, .mobile-brand span { display: grid; width: 46px; height: 46px; place-items: center; color: #fff; background: #0f766e; border-radius: 8px; box-shadow: 0 8px 20px rgba(15, 118, 110, .18); }
.login-brand div { display: flex; flex-direction: column; }
.login-brand strong { font-size: 22px; line-height: 1.2; }
.login-brand small { margin-top: 4px; color: #4b716e; font-size: 12px; }
.login-illustration { position: absolute; top: 49%; left: 50%; z-index: 1; width: min(74%, 680px); max-height: 58vh; object-fit: contain; transform: translate(-50%, -50%); animation: illustration-in 520ms ease-out both; }
.login-waves { position: absolute; top: 0; right: -90px; z-index: 2; width: 100px; height: 100%; }
.login-visual-copy { position: absolute; right: 52px; bottom: 44px; left: 52px; z-index: 3; display: flex; flex-direction: column; }
.login-visual-copy span { color: #4b716e; font-size: 10px; font-weight: 800; letter-spacing: 1.4px; }
.login-visual-copy strong { margin-top: 7px; color: #173d3a; font-size: 18px; }
.login-form-side { position: relative; display: flex; width: min(44%, 680px); min-width: 520px; flex: 0 0 min(44%, 680px); align-items: center; justify-content: center; padding: 32px 42px; background: var(--surface, #fff); }
.login-panel { position: relative; width: min(100%, 460px); min-height: 510px; overflow: hidden; background: var(--surface, #fff); border: 1px solid #cfdad9; border-radius: 8px; box-shadow: 0 18px 50px rgba(31, 41, 55, .09); }
.animated-border { position: absolute; z-index: 2; pointer-events: none; }
.border-horizontal { top: 0; left: -45%; width: 45%; height: 2px; background: linear-gradient(90deg, transparent, #0f766e); animation: border-horizontal 3.2s linear infinite; }
.border-vertical { top: -42%; right: 0; width: 2px; height: 42%; background: linear-gradient(180deg, transparent, #2563eb); animation: border-vertical 3.2s linear 800ms infinite; }
.login-panel-content { position: relative; z-index: 1; padding: 48px 48px 42px; }
.mobile-brand { display: none; align-items: center; gap: 10px; margin-bottom: 34px; }
.mobile-brand span { width: 38px; height: 38px; }
.mobile-brand strong { font-size: 18px; }
.login-heading { margin-bottom: 30px; }
.login-heading-icon { display: grid; width: 42px; height: 42px; margin-bottom: 18px; place-items: center; color: #0f766e; background: #e8f5f3; border: 1px solid #cce8e4; border-radius: 8px; }
.login-heading .eyebrow { color: #64748b; font-size: 10px; font-weight: 800; letter-spacing: 1.3px; }
.login-heading h1 { margin: 7px 0 8px; color: var(--text, #17202e); font-size: 27px; letter-spacing: 0; }
.login-heading p { margin: 0; color: var(--muted, #64748b); font-size: 13px; line-height: 1.65; }
.login-form { display: grid; gap: 18px; }
.login-field { display: grid; gap: 7px; }
.login-field > span { color: var(--text, #263241); font-size: 12px; font-weight: 700; }
.login-field > div { display: flex; height: 44px; align-items: center; gap: 10px; padding: 0 12px; color: #81908f; background: var(--surface-subtle, #f8fafc); border: 1px solid var(--border, #d7dfdf); border-radius: 7px; transition: border-color 150ms ease, box-shadow 150ms ease, background 150ms ease; }
.login-field > div:focus-within { background: var(--surface, #fff); border-color: #0f766e; box-shadow: 0 0 0 3px rgba(15, 118, 110, .11); }
.login-field input { min-width: 0; flex: 1; height: 100%; color: var(--text, #17202e); background: transparent; border: 0; outline: 0; font: inherit; font-size: 13px; }
.password-toggle { display: grid; width: 30px; height: 30px; padding: 0; place-items: center; color: #64748b; background: transparent; border: 0; border-radius: 6px; cursor: pointer; }
.password-toggle:hover { background: rgba(100, 116, 139, .1); }
.remember-account { display: inline-flex; width: fit-content; align-items: center; gap: 8px; color: var(--muted, #64748b); font-size: 12px; cursor: pointer; }
.remember-account input { width: 15px; height: 15px; accent-color: #0f766e; }
.login-error { margin: -4px 0 0; padding: 9px 11px; color: #b42318; background: #fff1f0; border: 1px solid #ffd3cf; border-radius: 7px; font-size: 12px; line-height: 1.5; }
.login-submit { display: inline-flex; width: 100%; height: 44px; align-items: center; justify-content: center; gap: 8px; margin-top: 3px; color: #fff; background: #0f766e; border: 1px solid #0f766e; border-radius: 7px; box-shadow: 0 7px 16px rgba(15, 118, 110, .17); font-size: 13px; font-weight: 700; cursor: pointer; transition: background 150ms ease, transform 150ms ease; }
.login-submit:hover:not(:disabled) { background: #0b625c; transform: translateY(-1px); }
.login-submit:disabled { cursor: wait; opacity: .66; }
.login-footer { position: absolute; right: 24px; bottom: 18px; left: 24px; margin: 0; color: #94a3b8; font-size: 10px; text-align: center; }
:root[data-theme="dark"] .login-page, :root[data-theme="dark"] .login-form-side, :root[data-theme="dark"] .login-panel { background: #111827; }
:root[data-theme="dark"] .login-panel { border-color: #334155; box-shadow: 0 18px 50px rgba(0, 0, 0, .3); }
:root[data-theme="dark"] .login-visual { background: #d3efff; }
:root[data-theme="dark"] .login-brand strong, :root[data-theme="dark"] .login-visual-copy strong { color: #e7f6f4; }
:root[data-theme="dark"] .login-brand small, :root[data-theme="dark"] .login-visual-copy span { color: #9dc8c3; }
:root[data-theme="dark"] .login-error { color: #fda29b; background: rgba(180, 35, 24, .14); border-color: rgba(253, 162, 155, .25); }
@keyframes illustration-in { from { opacity: 0; transform: translate(-50%, calc(-50% + 14px)); } to { opacity: 1; transform: translate(-50%, -50%); } }
@keyframes border-horizontal { from { transform: translateX(0); } to { transform: translateX(325%); } }
@keyframes border-vertical { from { transform: translateY(0); } to { transform: translateY(340%); } }
@media (max-width: 1199px) { .login-visual { display: none; margin-right: 0; } .login-form-side { width: 100%; min-width: 0; flex-basis: 100%; } .mobile-brand { display: flex; } }
@media (max-width: 560px) { .login-form-side { align-items: stretch; padding: 0; } .login-panel { width: 100%; min-height: 100vh; border: 0; border-radius: 0; box-shadow: none; } .login-panel-content { width: min(100%, 430px); margin: 0 auto; padding: 34px 24px 80px; } .login-footer { bottom: 14px; } }
@media (max-width: 340px) { .login-panel-content { padding-right: 16px; padding-left: 16px; } .login-heading h1 { font-size: 24px; } }
@media (prefers-reduced-motion: reduce) { .login-illustration, .animated-border, .spin { animation: none !important; } .login-submit { transition: none; } }
</style>
