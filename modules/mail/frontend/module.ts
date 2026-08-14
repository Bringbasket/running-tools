import { Braces, Mail, Mailbox, MailOpen, ScrollText, ShieldCheck, UsersRound } from '../../../frontend/src/icons'
import type { ModuleManifest } from '../../../frontend/src/types'

export const mailModule: ModuleManifest = {
  id: 'mail',
  label: '邮件系统',
  description: 'iCloud 隐藏邮件地址管理',
  icon: Mail,
  navigation: [
    { label: '账号管理', to: '/mail/accounts', icon: UsersRound },
    { label: '邮箱管理', to: '/mail/aliases', icon: Mailbox },
    { label: '收件箱', to: '/mail/mailbox', icon: MailOpen },
    { label: 'API 调试', to: '/mail/api-builder', icon: Braces },
    { label: 'Session 管理', to: '/mail/session', icon: ShieldCheck },
    { label: '使用日志', to: '/mail/logs', icon: ScrollText },
  ],
  routes: [
    { path: '/mail/accounts', component: () => import('./pages/AccountsPage.vue'), meta: { title: '账号管理' } },
    { path: '/mail/aliases', component: () => import('./pages/AliasesPage.vue'), meta: { title: '邮箱管理' } },
    { path: '/mail/mailbox', component: () => import('./pages/MailboxPage.vue'), meta: { title: '收件箱' } },
    { path: '/mail/api-builder', component: () => import('./pages/APIBuilderPage.vue'), meta: { title: 'API 调试' } },
    { path: '/mail/session', component: () => import('./pages/SessionPage.vue'), meta: { title: 'Session 管理' } },
    { path: '/mail/logs', component: () => import('./pages/ActivityLogsPage.vue'), meta: { title: '使用日志' } },
  ],
}
