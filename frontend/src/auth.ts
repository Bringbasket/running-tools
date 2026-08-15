import { reactive } from 'vue'
import type { APIEnvelope } from './types'

export interface AuthUser {
  id: number
  username: string
  mustChangePassword: boolean
  createdAt: string
  lastLoginAt: string | null
}

interface AuthStatus {
  authenticated: boolean
  user: AuthUser | null
}

export const authState = reactive({
  ready: false,
  authenticated: false,
  user: null as AuthUser | null,
  startupError: '',
})

async function authRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' })
  const envelope = await response.json() as APIEnvelope<T>
  if (!response.ok || !envelope.ok) {
    throw new Error(envelope.error?.message || `请求失败（HTTP ${response.status}）`)
  }
  return envelope.data
}

export async function initializeAuth() {
  authState.startupError = ''
  try {
    const status = await authRequest<AuthStatus>('/api/auth/status')
    authState.authenticated = status.authenticated
    authState.user = status.user
  } catch (error) {
    authState.authenticated = false
    authState.user = null
    authState.startupError = error instanceof Error ? error.message : String(error)
  } finally {
    authState.ready = true
  }
}

export async function login(username: string, password: string) {
  const result = await authRequest<{ user: AuthUser }>('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })
  authState.authenticated = true
  authState.user = result.user
  authState.startupError = ''
  return result.user
}

export async function changePassword(currentPassword: string, newPassword: string) {
  const user = await authRequest<AuthUser>('/api/auth/password', {
    method: 'PUT',
    body: JSON.stringify({ currentPassword, newPassword }),
  })
  authState.user = user
  return user
}

export function markUnauthenticated() {
  authState.authenticated = false
  authState.user = null
}

export async function logout() {
  try {
    await authRequest('/api/auth/logout', { method: 'POST', body: '{}' })
  } finally {
    markUnauthenticated()
  }
}
