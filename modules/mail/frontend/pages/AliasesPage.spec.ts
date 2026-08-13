import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AliasesPage from './AliasesPage.vue'

const mocks = vi.hoisted(() => ({
  aliases: vi.fn(),
  createSchedule: vi.fn(),
  aliasQueue: vi.fn(),
}))

vi.mock('../api', () => ({
  mailAPI: {
    aliases: mocks.aliases,
    createSchedule: mocks.createSchedule,
    aliasQueue: mocks.aliasQueue,
  },
}))

function aliases(count: number) {
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
    mocks.aliasQueue.mockResolvedValue({ status: 'idle', requested: 0, success: 0, workerRunning: false })

    const wrapper = mount(AliasesPage)
    await flushPromises()

    expect(wrapper.findAll('tbody tr')).toHaveLength(20)
    expect(wrapper.get('.pagination-bar strong').text()).toBe('第 1 / 3 页')

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
    mocks.aliasQueue.mockResolvedValue({ status: 'idle', requested: 0, success: 0, workerRunning: false })

    const wrapper = mount(AliasesPage)
    await flushPromises()
    expect(mocks.createSchedule).toHaveBeenCalledTimes(1)
    expect(mocks.aliasQueue).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(10_000)
    expect(mocks.createSchedule).toHaveBeenCalledTimes(1)
    expect(mocks.aliasQueue).toHaveBeenCalledTimes(1)

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
    expect(mocks.aliasQueue).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(2600)
    expect(mocks.createSchedule).toHaveBeenCalledTimes(3)
    expect(mocks.aliasQueue).toHaveBeenCalledTimes(1)

    await wrapper.findAll('.workspace-tabs button')[0].trigger('click')
    await vi.advanceTimersByTimeAsync(10_000)
    expect(mocks.createSchedule).toHaveBeenCalledTimes(3)
    expect(mocks.aliasQueue).toHaveBeenCalledTimes(1)

    wrapper.unmount()
  })
})
