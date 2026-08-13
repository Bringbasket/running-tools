import { Calculator, CirclePercent } from '../../../frontend/src/icons'
import type { ModuleManifest } from '../../../frontend/src/types'

export const toolsModule: ModuleManifest = {
  id: 'tools',
  label: '工具箱',
  description: '本地计算和运营辅助工具',
  icon: Calculator,
  navigation: [
    { label: '保本测算', to: '/tools/pricing', icon: CirclePercent },
  ],
  routes: [
    { path: '/tools/pricing', component: () => import('./pages/PricingDeskPage.vue'), meta: { title: '保本测算' } },
  ],
}
