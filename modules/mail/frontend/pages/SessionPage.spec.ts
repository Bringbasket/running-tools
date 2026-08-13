import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SessionPage from './SessionPage.vue'

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

function status() {
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
})
