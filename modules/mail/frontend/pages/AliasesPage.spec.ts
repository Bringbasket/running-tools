import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AliasesPage from './AliasesPage.vue'
import type { MailAlias } from '../types'

const mocks = vi.hoisted(() => ({
  aliases: vi.fn(),
  createSchedule: vi.fn(),
  aliasAction: vi.fn(),
  shareLinks: vi.fn(),
  createBatchShareLinks: vi.fn(),
  clearInactiveShareLinks: vi.fn(),
}))

vi.mock('../api', () => ({
  mailAPI: {
    aliases: mocks.aliases,
    createSchedule: mocks.createSchedule,
    aliasAction: mocks.aliasAction,
    shareLinks: mocks.shareLinks,
    createBatchShareLinks: mocks.createBatchShareLinks,
    clearInactiveShareLinks: mocks.clearInactiveShareLinks,
  },
}))

function aliases(count: number): MailAlias[] {
  return Array.from({ length: count }, (_, index) => ({
    anonymousId: `id-${index + 1}`,
    hme: `alias-${index + 1}@icloud.com`,
    label: 'shopping',
    note: '',
    forwardToEmail: 'owner@example.com',
    isActive: true,
    createTimestamp: 1786500000000 + index,
  }))
}

describe('邮箱列表分页', () => {
  beforeEach(() => vi.clearAllMocks())

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('支持每页条数、末页和筛选后回到第一页', async () => {
    mocks.aliases.mockResolvedValue(aliases(55))
    mocks.createSchedule.mockResolvedValue({
      enabled: false,
      running: false,
      batchSize: 5,
      aliasIntervalSeconds: 3,
      intervalSeconds: 180,
      label: 'shopping',
      note: '',
    })
    const wrapper = mount(AliasesPage)
    await flushPromises()

    expect(wrapper.find('.page-actions .primary').exists()).toBe(false)
    expect(wrapper.findAll('.workspace-tabs button')).toHaveLength(2)
    expect(wrapper.find('.workspace-tabs').text()).not.toContain('批量队列')
    expect(wrapper.findAll('tbody tr')).toHaveLength(10)
    expect(wrapper.get('.pagination-bar strong').text()).toBe('第 1 / 6 页')

    await wrapper.get('.page-size select').setValue('50')
    expect(wrapper.findAll('tbody tr')).toHaveLength(50)
    expect(wrapper.get('.pagination-bar strong').text()).toBe('第 1 / 2 页')

    await wrapper.get('button[title="下一页"]').trigger('click')
    expect(wrapper.findAll('tbody tr')).toHaveLength(5)
    expect(wrapper.get('.pagination-bar strong').text()).toBe('第 2 / 2 页')

    await wrapper.get('.search-field input').setValue('alias-55@icloud.com')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.get('.pagination-bar strong').text()).toBe('第 1 / 1 页')

    wrapper.unmount()
  })

  it('区分注册验证码和后续确认邮件的 GPT 状态', async () => {
    const items = aliases(2)
    items[0].registeredApps = [{ key: 'gpt', label: 'GPT', status: 'observed', detectedAt: 1786500000 }]
    items[1].registeredApps = [{ key: 'gpt', label: 'GPT', status: 'confirmed', detectedAt: 1786500000, confirmedAt: 1786500600 }]
    mocks.aliases.mockResolvedValue(items)
    mocks.createSchedule.mockResolvedValue({
      enabled: false, running: false, batchSize: 5, aliasIntervalSeconds: 3,
      intervalSeconds: 180, label: 'shopping', note: '',
    })
    const wrapper = mount(AliasesPage)
    await flushPromises()

    const badges = wrapper.findAll('.app-badge')
    expect(badges).toHaveLength(2)
    expect(badges[0].classes()).toContain('observed')
    expect(badges[0].text()).toContain('GPT已注册')
    expect(badges[1].classes()).toContain('confirmed')
    expect(badges[1].text()).toContain('GPT已确认')

    await wrapper.get('.search-field input').setValue('已确认')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.get('tbody tr').text()).toContain('alias-2@icloud.com')
    wrapper.unmount()
  })

  it('批量取件可筛选黄色和绿色的 GPT 注册状态', async () => {
		const items = aliases(3)
		items[0].registeredApps = [{ key: 'gpt', label: 'GPT', status: 'observed', detectedAt: 1786500000 }]
		items[1].registeredApps = [{ key: 'gpt', label: 'GPT', status: 'confirmed', detectedAt: 1786500000, confirmedAt: 1786500600 }]
		mocks.aliases.mockResolvedValue(items)
		mocks.createSchedule.mockResolvedValue({
			enabled: false, running: false, batchSize: 5, aliasIntervalSeconds: 3,
			intervalSeconds: 180, label: 'shopping', note: '',
		})
		mocks.createBatchShareLinks.mockResolvedValue({ items: [], count: 0, scope: 'gpt_registered' })
		const wrapper = mount(AliasesPage, { global: { stubs: { Teleport: true } } })
		await flushPromises()

		await wrapper.get('button[title="批量取件"]').trigger('click')
		const scope = wrapper.get<HTMLSelectElement>('select[name="batch-share-scope"]')
		expect(scope.findAll('option')).toHaveLength(3)
		expect(scope.findAll('option')[0].text()).toContain('全部邮箱（3）')
		expect(scope.findAll('option')[1].text()).toContain('已注册 GPT（2）')
		expect(scope.findAll('option')[2].text()).toContain('未注册 GPT（1）')
		expect(scope.element.value).toBe('all')
		expect(wrapper.get('.batch-share-hint').text()).toContain('所有启用邮箱')
		expect(wrapper.get<HTMLInputElement>('#batch-share-dialog input[type="number"]').element.value).toBe('3')
		await wrapper.get('#batch-share-dialog .button.primary').trigger('click')
		await flushPromises()
		expect(mocks.createBatchShareLinks).toHaveBeenCalledWith(3, 86400, 'all')

		await scope.setValue('gpt_registered')
		expect(wrapper.get('.batch-share-hint').text()).toContain('黄色“GPT 已注册”和绿色“GPT 已确认”')
		expect(wrapper.get<HTMLInputElement>('#batch-share-dialog input[type="number"]').element.value).toBe('2')
		await wrapper.get('#batch-share-dialog .button.primary').trigger('click')
		await flushPromises()
		expect(mocks.createBatchShareLinks).toHaveBeenCalledWith(2, 86400, 'gpt_registered')

		await scope.setValue('gpt_unregistered')
		expect(wrapper.get('.batch-share-hint').text()).toContain('尚未检测到 GPT 注册状态')
		expect(wrapper.get<HTMLInputElement>('#batch-share-dialog input[type="number"]').element.value).toBe('1')
		await wrapper.get('#batch-share-dialog .button.primary').trigger('click')
		await flushPromises()
		expect(mocks.createBatchShareLinks).toHaveBeenLastCalledWith(1, 86400, 'gpt_unregistered')
		wrapper.unmount()
	})

  it('空闲时不轮询，运行中只轮询当前任务标签', async () => {
    vi.useFakeTimers()
    mocks.aliases.mockResolvedValue(aliases(3))
    mocks.createSchedule.mockResolvedValue({
      enabled: false,
      running: false,
      batchSize: 5,
      aliasIntervalSeconds: 3,
      intervalSeconds: 180,
      label: 'shopping',
      note: '',
    })
    const wrapper = mount(AliasesPage)
    await flushPromises()
    expect(mocks.createSchedule).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(10_000)
    expect(mocks.createSchedule).toHaveBeenCalledTimes(1)

    mocks.createSchedule.mockResolvedValue({
      enabled: true,
      running: true,
      batchSize: 5,
      aliasIntervalSeconds: 3,
      intervalSeconds: 180,
      label: 'shopping',
      note: '',
      currentIndex: 1,
      currentTotal: 5,
      currentSuccess: 0,
    })
    await wrapper.findAll('.workspace-tabs button')[1].trigger('click')
    await flushPromises()
    expect(mocks.createSchedule).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(2600)
    expect(mocks.createSchedule).toHaveBeenCalledTimes(3)

    await wrapper.findAll('.workspace-tabs button')[0].trigger('click')
    await vi.advanceTimersByTimeAsync(10_000)
    expect(mocks.createSchedule).toHaveBeenCalledTimes(3)

    wrapper.unmount()
  })

  it('删除邮箱必须输入完整地址确认', async () => {
    const items = aliases(1)
    mocks.aliases.mockResolvedValue(items)
    mocks.createSchedule.mockResolvedValue({
      enabled: false, running: false, batchSize: 5, aliasIntervalSeconds: 3,
      intervalSeconds: 180, label: 'shopping', note: '',
    })
    mocks.aliasAction.mockResolvedValue({})
    const wrapper = mount(AliasesPage, { global: { stubs: { Teleport: true } } })
    await flushPromises()

    await wrapper.get('button[title="删除邮箱"]').trigger('click')
    const confirm = wrapper.get('#delete-alias-dialog .danger-confirm')
    expect(confirm.attributes('disabled')).toBeDefined()
    await wrapper.get('#delete-alias-dialog input').setValue(items[0].hme)
    expect(wrapper.get('#delete-alias-dialog .danger-confirm').attributes('disabled')).toBeUndefined()
    await wrapper.get('#delete-alias-dialog .danger-confirm').trigger('click')
    await flushPromises()

    expect(mocks.aliasAction).toHaveBeenCalledWith(items[0].anonymousId, 'delete')
    expect(wrapper.find('#delete-alias-dialog').exists()).toBe(false)
    wrapper.unmount()
  })

  it('可以从邮箱列表批量清理当前账号的失效取件链接', async () => {
    mocks.aliases.mockResolvedValue(aliases(1))
    mocks.createSchedule.mockResolvedValue({
      enabled: false, running: false, batchSize: 5, aliasIntervalSeconds: 3,
      intervalSeconds: 180, label: 'shopping', note: '',
    })
    mocks.clearInactiveShareLinks.mockResolvedValue({ cleared: true, deleted: 3 })
    const wrapper = mount(AliasesPage, { global: { stubs: { Teleport: true } } })
    await flushPromises()

    await wrapper.get('button[title="批量清理失效取件链接"]').trigger('click')
    expect(wrapper.find('#clear-shares-dialog').exists()).toBe(true)
    await wrapper.get('#clear-shares-dialog .danger-confirm').trigger('click')
    await flushPromises()

    expect(mocks.clearInactiveShareLinks).toHaveBeenCalledTimes(1)
    expect(mocks.shareLinks).not.toHaveBeenCalled()
    expect(wrapper.find('#clear-shares-dialog').exists()).toBe(false)
    expect(wrapper.find('#share-alias-dialog').exists()).toBe(false)
    wrapper.unmount()
  })

  it('从单邮箱入口清理后会刷新并返回原分享弹窗', async () => {
    const items = aliases(1)
    mocks.aliases.mockResolvedValue(items)
    mocks.createSchedule.mockResolvedValue({
      enabled: false, running: false, batchSize: 5, aliasIntervalSeconds: 3,
      intervalSeconds: 180, label: 'shopping', note: '',
    })
    mocks.shareLinks.mockResolvedValue({
      links: [{ id: 'share-1', active: false, shareUrl: '/mail?email=alias-1%40icloud.com&token=test' }],
    })
    mocks.clearInactiveShareLinks.mockResolvedValue({ cleared: true, deleted: 1 })
    const wrapper = mount(AliasesPage, { global: { stubs: { Teleport: true } } })
    await flushPromises()

    await wrapper.get('button[title="生成分享链接"]').trigger('click')
    await flushPromises()
    await wrapper.get('#share-alias-dialog .share-tools button').trigger('click')
    expect(wrapper.find('#share-alias-dialog').exists()).toBe(false)
    expect(wrapper.find('#clear-shares-dialog').exists()).toBe(true)

    await wrapper.get('#clear-shares-dialog .danger-confirm').trigger('click')
    await flushPromises()

    expect(mocks.clearInactiveShareLinks).toHaveBeenCalledTimes(1)
    expect(mocks.shareLinks).toHaveBeenCalledTimes(2)
    expect(mocks.shareLinks).toHaveBeenLastCalledWith(items[0].anonymousId)
    expect(wrapper.find('#clear-shares-dialog').exists()).toBe(false)
    expect(wrapper.find('#share-alias-dialog').exists()).toBe(true)
    wrapper.unmount()
  })
})
