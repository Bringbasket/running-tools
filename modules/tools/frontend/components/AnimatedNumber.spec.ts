import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AnimatedNumber from './AnimatedNumber.vue'

describe('AnimatedNumber', () => {
  beforeEach(() => {
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => window.setTimeout(() => callback(performance.now() + 500), 0))
    vi.stubGlobal('cancelAnimationFrame', (id: number) => window.clearTimeout(id))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('formats prefixes, suffixes and decimal places', () => {
    const wrapper = mount(AnimatedNumber, { props: { value: 12.3456, digits: 3, prefix: '¥', suffix: '/$' } })

    expect(wrapper.text()).toBe('¥12.346/$')
  })

  it('finishes at the latest changed value', async () => {
    const wrapper = mount(AnimatedNumber, { props: { value: 10, digits: 2 } })

    await wrapper.setProps({ value: 20 })
    await new Promise((resolve) => window.setTimeout(resolve, 10))

    expect(wrapper.text()).toBe('20.00')
    expect(wrapper.classes()).toContain('animated-number')
  })

  it('shows an em dash for invalid values', () => {
    const wrapper = mount(AnimatedNumber, { props: { value: Number.NaN } })

    expect(wrapper.text()).toBe('—')
  })
})
