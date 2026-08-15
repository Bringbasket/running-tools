import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SessionPage from './SessionPage.vue'
import type { SessionStatus } from '../types'

const mocks = vi.hoisted(() => ({
  session: vi.fn(),
  autoRefresh: vi.fn(),
  startAppleLogin: vi.fn(),
  verifyAppleLogin: vi.fn(),
}))

vi.mock('../api', () => ({
  mailAPI: {
    session: mocks.session,
    autoRefresh: mocks.autoRefresh,
    startAppleLogin: mocks.startAppleLogin,
    verifyAppleLogin: mocks.verifyAppleLogin,
  },
}))

function status(): SessionStatus {
  return {
    metadataDetected: false,
    metadata: null,
    persistedSession: false,
    configPath: 'data/mail/hme-config.json',
    configUpdatedAt: null,
    lastSavedAt: null,
    sessionValid: false,
    lastRefreshAt: null,
    lastValidAt: null,
    expiresHint: 'apple-controlled',
    lastError: null,
    needsReauth: false,
    appleLogin: {
      icloudWeb: { configured: false, healthy: false },
      appleAccount: { configured: false, healthy: false },
      createChannel: 'icloud_web',
    },
  }
}

function statusWithAppleAccount(state: 'healthy' | 'degraded' | 'reauth_required' | undefined, healthy: boolean, requiresReauth = false) {
  const value = status()
  value.persistedSession = true
  value.appleLogin.icloudWeb = { configured: true, healthy: true, appleId: 'owner@example.com' }
  value.appleLogin.appleAccount = {
    configured: true,
    healthy,
    state,
    requiresReauth,
    appleId: 'owner@example.com',
  }
  value.appleLogin.createChannel = 'apple_account'
  return value
}

describe('Apple 登录向导', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.session.mockResolvedValue(status())
    mocks.autoRefresh.mockResolvedValue({ enabled: true, intervalSeconds: 600, workerRunning: true })
  })

  it('只显示一个主登录按钮，并在返回 2FA 后原地进入验证码步骤', async () => {
    mocks.startAppleLogin.mockResolvedValue({ channel: 'apple_account', needs2FA: true, pendingId: 'pending-1', expiresAt: 1786600000, message: '已发送验证码' })
    const wrapper = mount(SessionPage)
    await flushPromises()

    expect(wrapper.findAll('.login-surface .button.primary')).toHaveLength(1)
    await wrapper.get('input[type="email"]').setValue('owner@example.com')
    await wrapper.get('input[type="password"]').setValue('password')
    await wrapper.get('.apple-login-form').trigger('submit')
    await flushPromises()

    expect(mocks.startAppleLogin).toHaveBeenCalledWith({
      channel: 'apple_account',
      appleId: 'owner@example.com',
      password: 'password',
      twoFactorMethod: 'trusted_device',
    })
    expect(wrapper.find('.apple-login-form').exists()).toBe(false)
    expect(wrapper.get('.verification-form').text()).toContain('完成登录')
    expect(wrapper.findAll('.login-surface .button.primary')).toHaveLength(1)
  })

  it('Apple Account 作为同一向导的分段选项，兼容导入默认折叠', async () => {
    const wrapper = mount(SessionPage)
    await flushPromises()

    expect(wrapper.find('.compatibility-body').exists()).toBe(false)
    expect(wrapper.findAll('.channel-switch button')[0].text()).toBe('Apple Account')
    expect(wrapper.findAll('.channel-switch button')[0].classes()).toContain('active')
    await wrapper.findAll('.channel-switch button')[1].trigger('click')
    expect(wrapper.text()).toContain('用于邮箱同步、列表和管理')
    expect(wrapper.find('select option[value="china"]').exists()).toBe(false)
    await wrapper.get('.compatibility-trigger').trigger('click')
    expect(wrapper.find('.compatibility-body').exists()).toBe(true)
    expect(wrapper.get('.compatibility-body').text()).not.toContain('iCloud 区域')
  })

  it.each([
    { name: 'healthy', state: 'healthy' as const, healthy: true, requiresReauth: false, label: '有效', route: 'Apple Account' },
    { name: 'degraded', state: 'degraded' as const, healthy: true, requiresReauth: false, label: '临时异常，自动重试', route: 'iCloud Web' },
    { name: 'reauth required', state: 'reauth_required' as const, healthy: false, requiresReauth: true, label: '需要重新登录', route: 'iCloud Web' },
    { name: 'legacy healthy', state: undefined, healthy: true, requiresReauth: false, label: '有效', route: 'Apple Account' },
    { name: 'legacy expired', state: 'healthy' as const, healthy: false, requiresReauth: false, label: '临时异常，自动重试', route: 'iCloud Web' },
  ])('按 Apple Account $name 状态展示健康标签并选择创建通道', async ({ state: healthState, healthy, requiresReauth, label, route }) => {
    mocks.session.mockResolvedValue(statusWithAppleAccount(healthState, healthy, requiresReauth))
    const wrapper = mount(SessionPage)
    await flushPromises()

    const accountRow = wrapper.findAll('.channel-status-row')[0]
    expect(accountRow.text()).toContain(label)
    expect(accountRow.find('.status-badge').text()).toContain(label)
    expect(wrapper.get('.route-indicator').text()).toContain(route)
    if (route === 'iCloud Web') expect(accountRow.text()).not.toContain('优先创建通道')
  })

  it('优先显示 Apple 创建冷却状态', async () => {
    const value = statusWithAppleAccount('healthy', true)
    value.appleLogin.appleAccount.cooldownRemainingSeconds = 120
    mocks.session.mockResolvedValue(value)
    const wrapper = mount(SessionPage)
    await flushPromises()

    expect(wrapper.findAll('.channel-status-row')[0].find('.status-badge').text()).toContain('冷却中')
  })
})
