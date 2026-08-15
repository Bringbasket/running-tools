import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiRequest } from './api'
import { authState, initializeAuth, login } from './auth'

const fetchMock = vi.fn()
vi.stubGlobal('fetch', fetchMock)

function envelope(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    headers: new Headers({ 'content-type': 'application/json' }),
    json: async () => ({ ok, data: ok ? data : null, error: ok ? null : { code: 'UNAUTHORIZED', message: '登录状态无效或已过期' }, meta: {} }),
  }
}

const user = { id: 1, username: 'admin', mustChangePassword: false, createdAt: '2026-08-16T00:00:00Z', lastLoginAt: null }

describe('browser authentication', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    localStorage.clear()
    authState.ready = false
    authState.authenticated = false
    authState.user = null
    authState.startupError = ''
  })

  it('restores an existing HttpOnly session without reading a local key', async () => {
    fetchMock.mockResolvedValue(envelope({ authenticated: true, user }))
    await initializeAuth()

    expect(authState.ready).toBe(true)
    expect(authState.authenticated).toBe(true)
    expect(authState.user?.username).toBe('admin')
    expect(fetchMock).toHaveBeenCalledWith('/api/auth/status', expect.objectContaining({ credentials: 'same-origin' }))
    expect(localStorage.getItem('running-api-key')).toBeNull()
  })

  it('sends credentials only in the login body and never persists the password', async () => {
    fetchMock.mockResolvedValue(envelope({ user }))
    await login('admin', 'private-password')

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.body).toBe(JSON.stringify({ username: 'admin', password: 'private-password' }))
    expect(localStorage.length).toBe(0)
    expect(authState.authenticated).toBe(true)
  })

  it('does not attach the retired X-API-Key header to API requests', async () => {
    fetchMock.mockResolvedValue(envelope({ revision: 'test' }))
    await apiRequest('/api/system/version')

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    const headers = init.headers as Headers
    expect(headers.has('X-API-Key')).toBe(false)
    expect(init.credentials).toBe('same-origin')
  })
})
