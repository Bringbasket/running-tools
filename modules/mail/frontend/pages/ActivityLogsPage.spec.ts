import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ActivityLogsPage from './ActivityLogsPage.vue'

const mocks = vi.hoisted(() => ({ activityLogs: vi.fn(), clearActivityLogs: vi.fn() }))

vi.mock('../api', () => ({ mailAPI: { activityLogs: mocks.activityLogs, clearActivityLogs: mocks.clearActivityLogs } }))

function page(total = 25) {
  return {
    items: [{
      id: 'log-1', module: 'mail', category: 'session', action: 'session.check.manual', level: 'info', outcome: 'success',
      summary: '手动检查 Session 成功', source: 'user', method: 'POST', path: '/api/mail/v1/session/refresh', httpStatus: 200,
      durationMs: 42, requestId: 'request-1234567890', createdAt: '2026-08-13T10:00:00Z',
    }],
    total,
    page: 1,
    pageSize: 10,
    stats: { today: 8, failures24h: 1, background24h: 3 },
  }
}

describe('邮件系统使用日志', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.activityLogs.mockResolvedValue(page())
    mocks.clearActivityLogs.mockResolvedValue({ cleared: true })
  })

  it('加载模块日志并提供筛选、每页条数和分页状态', async () => {
    const wrapper = mount(ActivityLogsPage)
    await flushPromises()

    expect(mocks.activityLogs).toHaveBeenCalledWith(expect.objectContaining({ page: 1, pageSize: 10 }))
    expect(wrapper.text()).toContain('手动检查 Session 成功')
    expect(wrapper.text()).toContain('显示 1–10，共 25 条')
    expect(wrapper.text()).toContain('第 1 / 3 页')

    await wrapper.get('.page-size select').setValue('50')
    await flushPromises()
    expect(mocks.activityLogs).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, pageSize: 50 }))

    await wrapper.findAll('.log-toolbar select')[0].setValue('error')
    await flushPromises()
    expect(mocks.activityLogs).toHaveBeenLastCalledWith(expect.objectContaining({ level: 'error', page: 1 }))
  })

  it('打开日志详情时展示追踪字段', async () => {
    const wrapper = mount(ActivityLogsPage, { attachTo: document.body })
    await flushPromises()
    await wrapper.get('.detail-button').trigger('click')
    await flushPromises()

    const dialog = document.body.querySelector('.log-detail')
    expect(dialog?.textContent).toContain('session.check.manual')
    expect(dialog?.textContent).toContain('request-1234567890')
    wrapper.unmount()
  })

  it('清理按钮调用服务端清理接口', async () => {
    const wrapper = mount(ActivityLogsPage)
    await flushPromises()
    vi.stubGlobal('confirm', vi.fn(() => true))
    await wrapper.get('.danger-action').trigger('click')
    await flushPromises()
    expect(mocks.clearActivityLogs).toHaveBeenCalledTimes(1)
    vi.unstubAllGlobals()
  })
})
