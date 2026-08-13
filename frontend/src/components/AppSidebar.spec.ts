import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AppSidebar from './AppSidebar.vue'

const apiRequest = vi.fn()

vi.mock('../api', () => ({
  APIError: class APIError extends Error {
    status = 500
  },
  apiRequest: (...args: unknown[]) => apiRequest(...args),
  errorMessage: (error: unknown) => error instanceof Error ? error.message : String(error),
}))

function version(overrides: Record<string, unknown> = {}) {
  return {
    state: 'idle',
    message: '尚未检查更新',
    currentRevision: 'revision-a',
    latestRevision: null,
    updateAvailable: null,
    canRequestUpdate: true,
    repositoryUrl: 'https://example.com/repository',
    ...overrides,
  }
}

async function renderSidebar() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/mail/aliases', component: { template: '<div />' } }],
  })
  await router.push('/mail/aliases')
  await router.isReady()
  return mount(AppSidebar, {
    props: { collapsed: false, mobileOpen: false },
    global: { plugins: [router] },
  })
}

describe('AppSidebar version updates', () => {
  beforeEach(() => {
    apiRequest.mockReset()
    localStorage.clear()
  })

  it('opens the panel without checking or updating', async () => {
    const wrapper = await renderSidebar()

    await wrapper.get('.brand-version').trigger('click')

    expect(wrapper.find('.version-popover').exists()).toBe(true)
    expect(apiRequest).not.toHaveBeenCalled()
    expect(wrapper.find('.version-popover .button.primary').exists()).toBe(false)
  })

  it('uses the refresh icon for a check-only request', async () => {
    apiRequest.mockResolvedValue(version({ state: 'check_queued', action: 'check', canRequestUpdate: false }))
    const wrapper = await renderSidebar()
    await wrapper.get('.brand-version').trigger('click')

    await wrapper.get('.version-popover-header .icon-button').trigger('click')
    await flushPromises()

    expect(apiRequest).toHaveBeenCalledOnce()
    expect(apiRequest).toHaveBeenCalledWith('/api/system/version/check', { method: 'POST', body: '{}' })
  })

  it('offers update only after a check found a new revision', async () => {
    apiRequest.mockResolvedValueOnce(version({
      state: 'update_available',
      action: 'check',
      latestRevision: 'revision-b',
      updateAvailable: true,
    })).mockResolvedValueOnce(version({
      state: 'update_queued',
      action: 'update',
      latestRevision: 'revision-b',
      updateAvailable: true,
      canRequestUpdate: false,
    }))
    const wrapper = await renderSidebar()
    await wrapper.get('.brand-version').trigger('click')
    await wrapper.get('.version-popover-header .icon-button').trigger('click')
    await flushPromises()

    const updateButton = wrapper.get('.version-popover .button.primary')
    expect(updateButton.text()).toContain('立即更新')
    expect(apiRequest).toHaveBeenCalledTimes(1)

    await updateButton.trigger('click')
    await flushPromises()

    expect(apiRequest).toHaveBeenCalledTimes(2)
    expect(apiRequest).toHaveBeenLastCalledWith('/api/system/update', { method: 'POST', body: '{}' })
  })
})
