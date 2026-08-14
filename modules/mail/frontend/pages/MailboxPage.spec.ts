import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import MailboxPage from './MailboxPage.vue'
import { toastState } from '../../../../frontend/src/toast'

const mocks = vi.hoisted(() => ({
  mailboxRecent: vi.fn(),
  mailboxMessages: vi.fn(),
  mailboxWait: vi.fn(),
  mailboxMessage: vi.fn(),
  mailboxSettings: vi.fn(),
  updateMailboxSettings: vi.fn(),
  testMailboxSettings: vi.fn(),
}))

vi.mock('../api', () => ({
  mailAPI: {
    mailboxRecent: mocks.mailboxRecent,
    mailboxMessages: mocks.mailboxMessages,
    mailboxWait: mocks.mailboxWait,
    mailboxMessage: mocks.mailboxMessage,
    mailboxSettings: mocks.mailboxSettings,
    updateMailboxSettings: mocks.updateMailboxSettings,
    testMailboxSettings: mocks.testMailboxSettings,
  },
}))

function messages(count: number) {
  return Array.from({ length: count }, (_, index) => ({
    uid: count - index,
    aliases: [`alias-${index + 1}@icloud.com`],
    from: 'Service <service@example.com>',
    subject: `邮件 ${index + 1}`,
    date: 1786500000 - index,
    text: `验证码邮件 ${index + 1}`,
    codes: index === 0 ? ['123456'] : [],
    partnerCodes: [],
  }))
}

describe('收件箱页面', () => {
  afterEach(() => { vi.restoreAllMocks(); toastState.items = [] })

	it('支持分页、筛选和按需加载安全 HTML 详情', async () => {
    mocks.mailboxRecent.mockResolvedValue({
      days: 3,
      messages: messages(45),
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 7, lastSyncAt: 1786500000 },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))
    mocks.mailboxMessage.mockResolvedValue({
      ...messages(1)[0],
      safeHtml: '<strong>安全正文</strong>',
    })

    const wrapper = mount(MailboxPage, { attachTo: document.body })
    await flushPromises()

    expect(wrapper.findAll('tbody tr')).toHaveLength(10)
    expect(wrapper.get('.pagination-actions strong').text()).toBe('第 1 / 5 页')
    expect(mocks.mailboxWait).toHaveBeenCalledWith(7)

    await wrapper.get('.page-size select').setValue('50')
    expect(wrapper.findAll('tbody tr')).toHaveLength(45)
    expect(wrapper.get('.pagination-actions strong').text()).toBe('第 1 / 1 页')

    await wrapper.get('.search-field input').setValue('alias-45@icloud.com')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.get('.pagination-actions strong').text()).toBe('第 1 / 1 页')

    await wrapper.get('.search-field input').setValue('')
    await wrapper.get('.message-summary').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('原邮件')
    const htmlMode = Array.from(document.body.querySelectorAll<HTMLButtonElement>('.mode-switch button')).find((button) => button.textContent === '原邮件')
    expect(htmlMode).toBeTruthy()
    htmlMode?.click()
    await flushPromises()
    expect(document.body.querySelector('iframe.mail-html')?.getAttribute('sandbox')).toContain('allow-popups')

		wrapper.unmount()
	})

	it('兼容 IMAP 返回 null 验证码列表', async () => {
		mocks.mailboxRecent.mockResolvedValue({
			days: 3,
			messages: [{ ...messages(1)[0], codes: null, partnerCodes: null }],
			sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 1, lastSyncAt: 1786500000 },
		})
		mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))

		const wrapper = mount(MailboxPage)
		await flushPromises()

		expect(wrapper.text()).toContain('邮件 1')
		expect(wrapper.text()).toContain('—')
		wrapper.unmount()
	})

	it('从邮箱列表进入时只读取指定隐藏邮箱的邮件', async () => {
    vi.clearAllMocks()
    const previousURL = window.location.href
    window.history.pushState({}, '', '/mail/mailbox?alias=target%40icloud.com')
    mocks.mailboxMessages.mockResolvedValue({
      configured: true,
      alias: 'target@icloud.com',
      messages: messages(1),
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 8, lastSyncAt: 1786500000 },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))

    const wrapper = mount(MailboxPage, { attachTo: document.body })
    await flushPromises()

    expect(mocks.mailboxMessages).toHaveBeenCalledWith('target@icloud.com', 100)
    expect(mocks.mailboxRecent).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('当前地址：target@icloud.com')

    wrapper.unmount()
    window.history.replaceState({}, '', previousURL)
  })

  it('从前端保存 IMAP 设置且不回填已有密码', async () => {
    mocks.mailboxRecent.mockResolvedValue({
      days: 3,
      messages: [],
      sync: { configured: false, enabled: false, workerRunning: false, syncMode: 'disabled', revision: 0, lastSyncAt: null },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))
    mocks.mailboxSettings.mockResolvedValue({
      username: 'owner@example.com', host: 'imap.example.com', port: 993, mailbox: 'INBOX',
      enabled: true, pollSeconds: 120, lookbackDays: 90, cacheMax: 5000,
      passwordConfigured: true, source: 'saved',
    })
    mocks.updateMailboxSettings.mockResolvedValue({
      username: 'owner@example.com', host: 'imap.example.com', port: 993, mailbox: 'INBOX',
      enabled: true, pollSeconds: 120, lookbackDays: 90, cacheMax: 5000,
      passwordConfigured: true, source: 'saved',
    })

    const wrapper = mount(MailboxPage, { attachTo: document.body })
    await flushPromises()
    const settingsButton = wrapper.findAll('button').find((button) => button.text().includes('IMAP 设置'))
    expect(settingsButton).toBeTruthy()
    await settingsButton!.trigger('click')
    await flushPromises()

    const dialog = document.body.querySelector<HTMLElement>('.mailbox-settings-dialog')
    expect(dialog).toBeTruthy()
    const password = dialog!.querySelector<HTMLInputElement>('input[type="password"]')!
    expect(password.value).toBe('')
    expect(password.placeholder).toContain('留空表示不修改')

    dialog!.querySelector<HTMLFormElement>('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()
    expect(mocks.updateMailboxSettings).toHaveBeenCalledWith(expect.objectContaining({
      username: 'owner@example.com',
      password: '',
      enabled: true,
    }))
    expect(document.body.querySelector('.mailbox-settings-dialog')).toBeNull()
	expect(toastState.items.some((item) => item.message.includes('IMAP 设置已保存'))).toBe(true)

    wrapper.unmount()
  })

  it('为 iCloud 账号纠正错误的 Gmail 服务器', async () => {
    mocks.mailboxRecent.mockResolvedValue({
      days: 3, messages: [],
      sync: { configured: false, enabled: false, workerRunning: false, syncMode: 'disabled', revision: 0, lastSyncAt: null },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))
    mocks.mailboxSettings.mockResolvedValue({
      username: 'owner@icloud.com', host: 'imap.icloud.com', port: 993, mailbox: 'INBOX',
      enabled: false, pollSeconds: 120, lookbackDays: 90, cacheMax: 5000,
      passwordConfigured: false, source: 'saved',
    })

    const wrapper = mount(MailboxPage, { attachTo: document.body })
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text().includes('IMAP 设置'))!.trigger('click')
    await flushPromises()

    const dialog = document.body.querySelector<HTMLElement>('.mailbox-settings-dialog')!
    const host = Array.from(dialog.querySelectorAll<HTMLInputElement>('input')).find((input) => input.placeholder === 'imap.mail.me.com')
    expect(host?.value).toBe('imap.mail.me.com')
    expect(dialog.textContent).toContain('不能使用 Apple ID 登录密码')
    wrapper.unmount()
  })
})
