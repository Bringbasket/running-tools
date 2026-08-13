import { computed, reactive } from 'vue'
import { apiRequest } from '../../../frontend/src/api'

export interface MailAccount {
  id: string
  name: string
  appleId?: string
  dsid?: string
  enabled: boolean
  createdAt: string
  updatedAt: string
}

const stored = localStorage.getItem('running-mail-account-id') || 'default'
export const mailAccountState = reactive({ currentId: stored, accounts: [] as MailAccount[], loaded: false })
export const currentMailAccount = computed(() => mailAccountState.accounts.find((item) => item.id === mailAccountState.currentId) || mailAccountState.accounts[0])

export async function loadMailAccounts() {
  const accounts = await apiRequest<MailAccount[]>('/api/mail/v1/accounts')
  mailAccountState.accounts = accounts
  if (!accounts.some((item) => item.id === mailAccountState.currentId)) {
    mailAccountState.currentId = accounts[0]?.id || 'default'
  }
  localStorage.setItem('running-mail-account-id', mailAccountState.currentId)
  mailAccountState.loaded = true
  return accounts
}

export async function createMailAccount(name: string) {
  const account = await apiRequest<MailAccount>('/api/mail/v1/accounts', { method: 'POST', body: JSON.stringify({ name }) })
  mailAccountState.accounts.push(account)
  selectMailAccount(account.id)
  return account
}

export function selectMailAccount(id: string) {
  if (!mailAccountState.accounts.some((item) => item.id === id)) return
  mailAccountState.currentId = id
  localStorage.setItem('running-mail-account-id', id)
  window.dispatchEvent(new CustomEvent('mail-account-change', { detail: id }))
}
