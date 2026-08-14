import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import AppDialog from './AppDialog.vue'

describe('AppDialog', () => {
  afterEach(() => { document.body.innerHTML = '' })

  it('打开后聚焦首个字段，Esc 关闭并恢复触发按钮焦点', async () => {
    const opener = document.createElement('button')
    opener.textContent = '打开'
    document.body.appendChild(opener)
    opener.focus()
    const wrapper = mount(AppDialog, {
      attachTo: document.body,
      props: { id: 'test', open: false, title: '测试弹窗' },
      slots: { default: '<input autofocus aria-label="首个字段" />', actions: '<button>确认</button>' },
    })

    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(document.activeElement?.getAttribute('aria-label')).toBe('首个字段')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toHaveLength(1)

    await wrapper.setProps({ open: false })
    await flushPromises()
    expect(document.activeElement).toBe(opener)
    wrapper.unmount()
  })
})
