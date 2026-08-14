import { describe, expect, it } from 'vitest'
import { mailModule, modules, toolsModule } from './modules'

describe('mail module manifest', () => {
  it('keeps all mail tools under one navigation group', () => {
    expect(mailModule.id).toBe('mail')
    expect(mailModule.navigation.map((item) => item.to)).toEqual([
      '/mail/accounts',
      '/mail/aliases',
      '/mail/mailbox',
      '/mail/api-builder',
      '/mail/session',
      '/mail/logs',
    ])
    expect(mailModule.routes).toHaveLength(6)
  })
})

describe('tools module manifest', () => {
  it('registers the toolbox as an independent navigation group', () => {
    expect(modules.map((module) => module.id)).toEqual(['mail', 'tools'])
    expect(toolsModule.label).toBe('工具箱')
    expect(toolsModule.navigation.map((item) => item.to)).toEqual(['/tools/pricing'])
    expect(toolsModule.routes).toHaveLength(1)
  })
})
