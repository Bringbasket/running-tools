import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountsPage from './AccountsPage.vue'
import { mailAccountState } from '../account'

const mocks = vi.hoisted(() => ({ apiRequest: vi.fn() }))
vi.mock('../../../../frontend/src/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../../../frontend/src/api')>()
  return { ...original, apiRequest: mocks.apiRequest }
})

const accounts = Array.from({ length: 12 }, (_, index) => ({
  id: index === 0 ? 'default' : `mail-${index}`,
  name: index === 0 ? 'hubacall@163.com' : `母号 ${index}`,
  appleId: index === 0 ? 'hubacall@163.com' : undefined,
  dsid: index === 0 ? '123456' : undefined,
  enabled: true,
  status: index === 0 ? 'active' : 'pending',
  statusMessage: index === 0 ? '运行正常' : '等待配置登录',
  aliasCount: index === 0 ? 12 : 0,
  hasProxy: false,
  icloudWeb: { configured: index === 0, healthy: index === 0, lastCheckedAt: index === 0 ? 1786636800 : undefined },
  appleAccount: { configured: false, healthy: false },
  mailbox: { configured: false, enabled: false },
  autoRefreshEnabled: true,
  autoCreateEnabled: false,
  autoCreateRunning: false,
  aliasQueueStatus: 'idle',
  createdAt: '2026-08-14T00:00:00Z',
  updatedAt: '2026-08-14T00:00:00Z',
}))

describe('账号管理页面', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    localStorage.setItem('running-mail-account-id', 'default')
    mailAccountState.currentId = 'default'
    mailAccountState.accounts = []
    mailAccountState.loaded = false
    mocks.apiRequest.mockResolvedValue(accounts.map((account) => ({ ...account })))
  })

  it('默认每页 10 条，并可搜索和切换母号', async () => {
    const wrapper = mount(AccountsPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    expect(wrapper.findAll('tbody tr')).toHaveLength(10)
    expect(wrapper.get('.pagination-bar strong').text()).toBe('第 1 / 2 页')
    expect(wrapper.get('.current-account').text()).toContain('当前使用')

    await wrapper.get('.search-field input').setValue('母号 11')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    await wrapper.get('tbody .button').trigger('click')
    expect(mailAccountState.currentId).toBe('mail-11')
    expect(localStorage.getItem('running-mail-account-id')).toBe('mail-11')
  })

  it('通过弹窗新增母号并自动切换', async () => {
    const created = { ...accounts[1], id: 'mail-new', name: '新母号' }
    mocks.apiRequest.mockImplementation((path: string, init?: RequestInit) => init?.method === 'POST' ? Promise.resolve(created) : Promise.resolve(accounts))
    const wrapper = mount(AccountsPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, Teleport: true } } })
    await flushPromises()

    await wrapper.get('.page-actions .button').trigger('click')
    await wrapper.get('#create-account-dialog input').setValue('新母号')
    await wrapper.get('#create-account-form').trigger('submit')
    await flushPromises()

    expect(mocks.apiRequest).toHaveBeenCalledWith('/api/mail/v1/accounts', { method: 'POST', body: JSON.stringify({ name: '新母号' }) })
    expect(mailAccountState.currentId).toBe('mail-new')
    expect(wrapper.find('#create-account-dialog').exists()).toBe(false)
  })

  it('默认账号受保护，删除当前母号后自动切换到默认账号', async () => {
    localStorage.setItem('running-mail-account-id', 'mail-1')
    mailAccountState.currentId = 'mail-1'
    mocks.apiRequest.mockImplementation((path: string, init?: RequestInit) => {
      if (init?.method === 'DELETE') return Promise.resolve({ deleted: true, id: 'mail-1' })
      return Promise.resolve(accounts.map((account) => ({ ...account })))
    })
    const accountChange = vi.fn()
    window.addEventListener('mail-account-change', accountChange)
    const wrapper = mount(AccountsPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, Teleport: true } } })
    await flushPromises()

    const rows = wrapper.findAll('tbody tr')
    expect(rows[0].get('.icon-button.danger').attributes('disabled')).toBeDefined()
    const targetRow = rows.find((row) => row.text().includes('母号 1'))
    expect(targetRow).toBeDefined()
    await targetRow!.get('.icon-button.danger').trigger('click')
    expect(wrapper.get('#delete-account-dialog').text()).toContain('母号 1')
    await wrapper.get('#delete-account-dialog .delete-confirm-field input').setValue('母号 1')
    await wrapper.get('.danger-confirm').trigger('click')
    await flushPromises()

    expect(mocks.apiRequest).toHaveBeenCalledWith('/api/mail/v1/accounts/mail-1', { method: 'DELETE' })
    expect(mailAccountState.accounts.some((account) => account.id === 'mail-1')).toBe(false)
    expect(mailAccountState.currentId).toBe('default')
    expect(localStorage.getItem('running-mail-account-id')).toBe('default')
    expect(accountChange).toHaveBeenCalledOnce()
    expect(wrapper.find('#delete-account-dialog').exists()).toBe(false)
    window.removeEventListener('mail-account-change', accountChange)
  })

	it('代理测试成功且输入未变化时才允许保存', async () => {
		mocks.apiRequest.mockImplementation((path: string, init?: RequestInit) => {
			if (path.endsWith('/proxy/test')) return Promise.resolve({ reachable: true, statusCode: 204, latencyMs: 36, target: 'www.icloud.com' })
			if (init?.method === 'PUT') return Promise.resolve({ ...accounts[1], hasProxy: true })
			return Promise.resolve(accounts.map((account) => ({ ...account })))
		})
		const wrapper = mount(AccountsPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, Teleport: true } } })
		await flushPromises()

		await wrapper.get('button[aria-label="配置 母号 1 的代理"]').trigger('click')
		expect(wrapper.get('#account-proxy-dialog input[type="password"]').element.getAttribute('value')).toBe(null)
		expect(wrapper.find('#account-proxy-dialog input[type="checkbox"]').exists()).toBe(false)
		await wrapper.get('#account-proxy-dialog input[type="password"]').setValue('socks5://user:pass@127.0.0.1:1080')
		expect(wrapper.get('.proxy-save-button').attributes('disabled')).toBeDefined()

		await wrapper.get('.proxy-test-button').trigger('click')
		await flushPromises()
		expect(mocks.apiRequest).toHaveBeenCalledWith('/api/mail/v1/accounts/mail-1/proxy/test', {
			method: 'POST', body: JSON.stringify({ proxy: 'socks5://user:pass@127.0.0.1:1080' }),
		})
		expect(wrapper.get('.proxy-test-message').text()).toContain('连接成功 · HTTP 204 · 36 ms')
		expect(wrapper.get('.proxy-save-button').attributes('disabled')).toBeUndefined()

		await wrapper.get('#account-proxy-dialog input[type="password"]').setValue('socks5://user:pass@127.0.0.1:1081')
		expect(wrapper.find('.proxy-test-message').exists()).toBe(false)
		expect(wrapper.get('.proxy-save-button').attributes('disabled')).toBeDefined()
		await wrapper.get('.proxy-test-button').trigger('click')
		await flushPromises()
		await wrapper.get('#account-proxy-form').trigger('submit')
		await flushPromises()

		expect(mocks.apiRequest).toHaveBeenCalledWith('/api/mail/v1/accounts/mail-1/proxy', {
			method: 'PUT', body: JSON.stringify({ proxy: 'socks5://user:pass@127.0.0.1:1081' }),
		})
		expect(wrapper.find('#account-proxy-dialog').exists()).toBe(false)
		expect(mailAccountState.accounts.find((account) => account.id === 'mail-1')?.hasProxy).toBe(true)
	})

	it('代理测试失败时保留错误且不允许保存', async () => {
		mocks.apiRequest.mockImplementation((path: string) => {
			if (path.endsWith('/proxy/test')) return Promise.reject(new Error('无法通过该代理访问 Apple，请检查地址、凭据和网络'))
			return Promise.resolve(accounts.map((account) => ({ ...account })))
		})
		const wrapper = mount(AccountsPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, Teleport: true } } })
		await flushPromises()

		await wrapper.get('button[aria-label="配置 母号 1 的代理"]').trigger('click')
		await wrapper.get('#account-proxy-dialog input[type="password"]').setValue('http://127.0.0.1:8080')
		await wrapper.get('.proxy-test-button').trigger('click')
		await flushPromises()

		expect(wrapper.get('.proxy-test-message').text()).toContain('无法通过该代理访问 Apple')
		expect(wrapper.get('.proxy-save-button').attributes('disabled')).toBeDefined()
		expect(wrapper.find('#account-proxy-dialog').exists()).toBe(true)
		expect(mocks.apiRequest).not.toHaveBeenCalledWith(expect.stringMatching(/\/proxy$/), expect.objectContaining({ method: 'PUT' }))
	})
})
