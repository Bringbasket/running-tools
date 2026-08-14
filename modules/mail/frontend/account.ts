import { computed, reactive } from 'vue'
import { apiRequest } from '../../../frontend/src/api'

export interface MailAccount {
  id: string
  name: string
  appleId?: string
  dsid?: string
  enabled: boolean
	status: 'active' | 'warning' | 'pending' | 'error'
	statusMessage: string
	aliasCount: number
	hasProxy: boolean
	icloudWeb: { configured: boolean; healthy: boolean; lastCheckedAt?: number; expiresAt?: number; message?: string }
	appleAccount: { configured: boolean; healthy: boolean; lastCheckedAt?: number; expiresAt?: number; message?: string }
	mailbox: { configured: boolean; enabled: boolean; lastSyncAt?: number; lastError?: string }
	autoRefreshEnabled: boolean
	autoCreateEnabled: boolean
	autoCreateRunning: boolean
	aliasQueueStatus: string
  createdAt: string
  updatedAt: string
}

export interface MailAccountProxyTest {
	reachable: boolean
	statusCode: number
	latencyMs: number
	target: string
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

export async function deleteMailAccount(id: string) {
  await apiRequest<{ deleted: boolean; id: string }>(`/api/mail/v1/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' })
  mailAccountState.accounts = mailAccountState.accounts.filter((item) => item.id !== id)
  if (mailAccountState.currentId === id) {
    const nextID = mailAccountState.accounts.find((item) => item.id === 'default')?.id || mailAccountState.accounts[0]?.id || 'default'
    mailAccountState.currentId = nextID
    localStorage.setItem('running-mail-account-id', nextID)
    window.dispatchEvent(new CustomEvent('mail-account-change', { detail: nextID }))
  }
}

export async function updateMailAccountProxy(id: string, proxy: string) {
	const account = await apiRequest<MailAccount>(`/api/mail/v1/accounts/${encodeURIComponent(id)}/proxy`, {
		method: 'PUT', body: JSON.stringify({ proxy }),
	})
	const index = mailAccountState.accounts.findIndex((item) => item.id === id)
	if (index >= 0) mailAccountState.accounts[index] = account
	return account
}

export function testMailAccountProxy(id: string, proxy: string) {
	return apiRequest<MailAccountProxyTest>(`/api/mail/v1/accounts/${encodeURIComponent(id)}/proxy/test`, {
		method: 'POST', body: JSON.stringify({ proxy }),
	})
}

export function selectMailAccount(id: string) {
  if (!mailAccountState.accounts.some((item) => item.id === id)) return
  mailAccountState.currentId = id
  localStorage.setItem('running-mail-account-id', id)
  window.dispatchEvent(new CustomEvent('mail-account-change', { detail: id }))
}
