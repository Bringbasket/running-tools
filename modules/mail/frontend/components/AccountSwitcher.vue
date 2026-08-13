<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { LoaderCircle, Plus, UsersRound } from '../../../../frontend/src/icons'
import { errorMessage } from '../../../../frontend/src/api'
import { createMailAccount, loadMailAccounts, mailAccountState, selectMailAccount } from '../account'

const creating = ref(false)
const loading = ref(false)
const name = ref('')
const error = ref('')

async function load() {
  loading.value = true
  try { await loadMailAccounts() } catch (reason) { error.value = errorMessage(reason) } finally { loading.value = false }
}

async function create() {
  const value = name.value.trim()
  if (!value) return
  loading.value = true; error.value = ''
  try { await createMailAccount(value); name.value = ''; creating.value = false } catch (reason) { error.value = errorMessage(reason) } finally { loading.value = false }
}

onMounted(load)
</script>

<template>
  <div class="account-switcher">
    <UsersRound :size="16" />
    <select :value="mailAccountState.currentId" aria-label="当前邮件账号" :disabled="loading" @change="selectMailAccount(($event.target as HTMLSelectElement).value)">
      <option v-for="account in mailAccountState.accounts" :key="account.id" :value="account.id">{{ account.name }}</option>
    </select>
    <button class="icon-button" title="新增邮件账号" @click="creating = !creating"><Plus :size="16" /></button>
    <form v-if="creating" class="account-create" @submit.prevent="create">
      <input v-model="name" maxlength="120" placeholder="账号名称" autofocus />
      <button class="button primary" :disabled="loading || !name.trim()"><LoaderCircle v-if="loading" :size="15" class="spin" />创建</button>
      <small v-if="error">{{ error }}</small>
    </form>
  </div>
</template>

<style scoped>
.account-switcher { position: relative; display: flex; height: 34px; align-items: center; gap: 6px; padding-left: 10px; color: var(--muted); border: 1px solid var(--border); border-radius: 6px; }
.account-switcher select { width: 150px; height: 32px; padding: 0 24px 0 4px; color: var(--text); background: transparent; border: 0; outline: 0; }
.account-switcher .icon-button { width: 32px; height: 32px; border-width: 0 0 0 1px; border-radius: 0; }
.account-create { position: absolute; z-index: 20; top: 40px; right: 0; display: grid; grid-template-columns: minmax(180px, 1fr) auto; gap: 8px; width: 300px; padding: 12px; background: var(--surface); border: 1px solid var(--border); border-radius: 7px; box-shadow: 0 12px 30px rgba(15,23,42,.15); }
.account-create input { min-width: 0; }
.account-create small { grid-column: 1 / -1; color: var(--danger); }
@media (max-width: 600px) { .account-switcher select { width: 100px; } .account-create { position: fixed; top: 58px; right: 12px; width: min(300px, calc(100vw - 24px)); } }
</style>
