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
  hideMailboxMessage: vi.fn(),
  hideMailboxMessages: vi.fn(),
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
    hideMailboxMessage: mocks.hideMailboxMessage,
    hideMailboxMessages: mocks.hideMailboxMessages,
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
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); toastState.items = [] })

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

    await wrapper.get('.search-field input').setValue('alias-45')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.get('.pagination-actions strong').text()).toBe('第 1 / 1 页')

    await wrapper.get('.message-summary').trigger('click')
    await flushPromises()
    expect(mocks.mailboxMessage).toHaveBeenCalledWith('alias-45@icloud.com', 1)
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

  it('完整邮箱筛选优先操作精确别名且保留同封邮件的其他别名', async () => {
    vi.clearAllMocks()
    const previousURL = window.location.href
    window.history.pushState({}, '', '/mail/mailbox?alias=target%40icloud.com')
    mocks.mailboxMessages.mockResolvedValue({
      configured: true,
      alias: 'target@icloud.com',
      messages: [{ ...messages(1)[0], aliases: ['xtarget@icloud.com', 'target@icloud.com'] }],
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 8, lastSyncAt: 1786500000 },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))
    mocks.hideMailboxMessage.mockResolvedValue({})

    const wrapper = mount(MailboxPage)
    await flushPromises()
    await wrapper.get('button[title="从本地列表隐藏"]').trigger('click')
    await flushPromises()

    expect(mocks.hideMailboxMessage).toHaveBeenCalledWith('target@icloud.com', 1, expect.objectContaining({ revision: 8 }))
    expect(wrapper.text()).toContain('没有匹配的邮件')

    wrapper.unmount()
    window.history.replaceState({}, '', previousURL)
  })

  it('完整邮箱地址防抖查询服务端，清空后恢复最近邮件', async () => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    const previousURL = window.location.href
    window.history.pushState({}, '', '/mail/mailbox')
    mocks.mailboxRecent.mockResolvedValue({
      days: 3,
      messages: messages(3),
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 9, lastSyncAt: 1786500000 },
    })
    mocks.mailboxMessages.mockResolvedValue({
      configured: true,
      alias: 'target@icloud.com',
      messages: [{ ...messages(1)[0], aliases: ['target@icloud.com'] }],
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 9, lastSyncAt: 1786500000 },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))

    const wrapper = mount(MailboxPage)
    await flushPromises()
    expect(mocks.mailboxRecent).toHaveBeenCalledWith(500)

    const search = wrapper.get('.search-field input')
    await search.setValue('target@icloud')
    await vi.advanceTimersByTimeAsync(400)
    expect(mocks.mailboxMessages).not.toHaveBeenCalled()

    await search.setValue('target@icloud.com')
    await vi.advanceTimersByTimeAsync(349)
    expect(mocks.mailboxMessages).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(mocks.mailboxMessages).toHaveBeenCalledTimes(1)
    expect(mocks.mailboxMessages).toHaveBeenCalledWith('target@icloud.com', 100)
    expect(wrapper.text()).toContain('target@icloud.com')

    await search.setValue('target@icloud')
    await flushPromises()
    expect(mocks.mailboxRecent).toHaveBeenCalledTimes(2)
    expect(mocks.mailboxRecent).toHaveBeenLastCalledWith(500)

    await search.setValue('')

    wrapper.unmount()
    window.history.replaceState({}, '', previousURL)
  })

  it('卸载页面时取消尚未执行的邮箱查询', async () => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    const previousURL = window.location.href
    window.history.pushState({}, '', '/mail/mailbox')
    mocks.mailboxRecent.mockResolvedValue({
      days: 3,
      messages: messages(1),
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 10, lastSyncAt: 1786500000 },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))

    const wrapper = mount(MailboxPage)
    await flushPromises()
    await wrapper.get('.search-field input').setValue('later@icloud.com')
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(400)

    expect(mocks.mailboxMessages).not.toHaveBeenCalled()
    window.history.replaceState({}, '', previousURL)
  })

  it('清空邮箱搜索后忽略尚未完成的旧查询结果', async () => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    const previousURL = window.location.href
    window.history.pushState({}, '', '/mail/mailbox')
    let resolveAlias!: (value: {
      configured: boolean
      alias: string
      messages: ReturnType<typeof messages>
      sync: { configured: boolean; enabled: boolean; workerRunning: boolean; syncMode: string; revision: number; lastSyncAt: number }
    }) => void
    mocks.mailboxRecent.mockResolvedValue({
      days: 3,
      messages: [{ ...messages(1)[0], aliases: ['recent@icloud.com'] }],
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 11, lastSyncAt: 1786500000 },
    })
    mocks.mailboxMessages.mockImplementation(() => new Promise((resolve) => { resolveAlias = resolve }))
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))

    const wrapper = mount(MailboxPage)
    await flushPromises()
    const search = wrapper.get('.search-field input')
    await search.setValue('late@icloud.com')
    await vi.advanceTimersByTimeAsync(350)
    expect(mocks.mailboxMessages).toHaveBeenCalledTimes(1)

    await search.setValue('')
    await flushPromises()
    expect(wrapper.text()).toContain('recent@icloud.com')
    resolveAlias({
      configured: true,
      alias: 'late@icloud.com',
      messages: [{ ...messages(1)[0], aliases: ['late@icloud.com'] }],
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 11, lastSyncAt: 1786500000 },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('recent@icloud.com')
    expect(wrapper.text()).not.toContain('late@icloud.com')

    wrapper.unmount()
    window.history.replaceState({}, '', previousURL)
  })

  it('切换母号后旧长轮询返回时不会启动重复监听', async () => {
    vi.clearAllMocks()
    const waitResolvers: Array<(value: { revision: number }) => void> = []
    mocks.mailboxRecent.mockResolvedValue({
      days: 3,
      messages: messages(1),
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 12, lastSyncAt: 1786500000 },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise((resolve) => { waitResolvers.push(resolve) }))

    const wrapper = mount(MailboxPage)
    await flushPromises()
    await vi.waitFor(() => expect(mocks.mailboxWait).toHaveBeenCalledTimes(1))

    window.dispatchEvent(new Event('mail-account-change'))
    await flushPromises()
    expect(mocks.mailboxWait).toHaveBeenCalledTimes(2)

    waitResolvers[0]({ revision: 13 })
    await flushPromises()
    expect(mocks.mailboxWait).toHaveBeenCalledTimes(2)

    wrapper.unmount()
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

  it('hiding one alias removes the complete multi-alias message row', async () => {
    vi.clearAllMocks()
    const previousURL = window.location.href
    window.history.pushState({}, '', '/mail/mailbox')
    mocks.mailboxRecent.mockResolvedValue({
      days: 3,
      messages: [{ ...messages(1)[0], aliases: ['one@icloud.com', 'two@icloud.com'] }],
      sync: { configured: true, enabled: true, workerRunning: true, syncMode: 'idle', revision: 8, lastSyncAt: 1786500000 },
    })
    mocks.mailboxWait.mockImplementation(() => new Promise(() => {}))
    mocks.hideMailboxMessage.mockResolvedValue({})

    const wrapper = mount(MailboxPage)
    await flushPromises()
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)

    await wrapper.get('tbody .icon-button').trigger('click')
    await flushPromises()
    expect(mocks.hideMailboxMessage).toHaveBeenCalledWith('one@icloud.com', 1, expect.objectContaining({ revision: 8 }))
    expect(wrapper.find('tbody tr.empty-row').exists()).toBe(true)

    wrapper.unmount()
    window.history.replaceState({}, '', previousURL)
  })
})
