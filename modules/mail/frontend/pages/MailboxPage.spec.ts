import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import MailboxPage from './MailboxPage.vue'

const mocks = vi.hoisted(() => ({
  mailboxRecent: vi.fn(),
  mailboxWait: vi.fn(),
  mailboxMessage: vi.fn(),
}))

vi.mock('../api', () => ({
  mailAPI: {
    mailboxRecent: mocks.mailboxRecent,
    mailboxWait: mocks.mailboxWait,
    mailboxMessage: mocks.mailboxMessage,
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
  afterEach(() => vi.restoreAllMocks())

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

    expect(wrapper.findAll('tbody tr')).toHaveLength(20)
    expect(wrapper.get('.pagination-actions strong').text()).toBe('第 1 / 3 页')
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
})
