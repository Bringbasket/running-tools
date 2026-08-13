import { reactive } from 'vue'

const storageKey = 'running-api-key'

export const authState = reactive({
  apiKey: localStorage.getItem(storageKey) || '',
  loginOpen: !localStorage.getItem(storageKey),
})

export function setAPIKey(value: string) {
  authState.apiKey = value.trim()
  localStorage.setItem(storageKey, authState.apiKey)
  authState.loginOpen = false
}

export function logout() {
  localStorage.removeItem(storageKey)
  authState.apiKey = ''
  authState.loginOpen = true
}
