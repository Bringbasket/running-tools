import { markUnauthenticated } from './auth'
import type { APIEnvelope } from './types'

const mailAccountStorageKey = 'running-mail-account-id'

export class APIError extends Error {
  constructor(public code: string, message: string, public status: number) {
    super(message)
  }
}

export async function apiRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (path.startsWith('/api/mail/')) {
    headers.set('X-Mail-Account-ID', localStorage.getItem(mailAccountStorageKey) || 'default')
  }
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' })
  const type = response.headers.get('content-type') || ''
  if (!type.includes('application/json')) throw new APIError('BAD_RESPONSE', `服务器返回了 HTTP ${response.status}`, response.status)
  const envelope = await response.json() as APIEnvelope<T>
  if (!response.ok || !envelope.ok) {
    if (response.status === 401) markUnauthenticated()
    throw new APIError(envelope.error?.code || 'REQUEST_FAILED', envelope.error?.message || '请求失败', response.status)
  }
  return envelope.data
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
