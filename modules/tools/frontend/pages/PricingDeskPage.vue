<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { BookOpenCheck, Calculator, Check, Clipboard, CirclePercent, Copy, RotateCcw, WalletCards } from '../../../../frontend/src/icons'
import { calculatePricing, calculateQuota, calculateSimulation } from '../pricing'
import AnimatedNumber from '../components/AnimatedNumber.vue'

type QuotaUnit = 'M' | '$' | '万' | '亿'

const cost = ref(158)
const quota = ref(100)
const rate = ref(1)
const loss = ref(3)
const targetProfit = ref(20)
const quotaPercent = ref(3)
const quotaAmount = ref(6.5)
const quotaUnit = ref<QuotaUnit>('M')
const simulationMultiplier = ref(0.3)
const copied = ref(false)
const tickerPaused = ref(false)
const now = ref(new Date())
let clockTimer: ReturnType<typeof window.setInterval> | undefined

const ratePresets = [1, 7.19, 7.2, 7.5]
const quotaPercentPresets = [3, 1, 5, 10, 25, 50]
const quotaUnits: QuotaUnit[] = ['M', '$', '万', '亿']

const format = (value: number, digits = 2) => Number.isFinite(value)
  ? new Intl.NumberFormat('zh-CN', { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(value)
  : '—'
const pricing = computed(() => calculatePricing({ cost: cost.value, quota: quota.value, rate: rate.value, lossPercent: loss.value, profitPercent: targetProfit.value }))
const quotaCalculation = computed(() => calculateQuota(quotaPercent.value, quotaAmount.value))
const simulation = computed(() => calculateSimulation(pricing.value, simulationMultiplier.value))

const rulerMax = computed(() => Math.max(0.5, pricing.value.breakEven || 0, pricing.value.recommended || 0) * 1.25)
const rulerBreakEven = computed(() => Number.isFinite(pricing.value.breakEven) ? Math.min(100, pricing.value.breakEven / rulerMax.value * 100) : 0)
const rulerRecommended = computed(() => Number.isFinite(pricing.value.recommended) ? Math.min(100, pricing.value.recommended / rulerMax.value * 100) : 0)
const simulationBreakEven = computed(() => {
  const minimum = 0.05
  const maximum = 1.2
  return Number.isFinite(pricing.value.breakEven) ? Math.min(100, Math.max(0, (pricing.value.breakEven - minimum) / (maximum - minimum) * 100)) : 0
})

const formattedTime = computed(() => now.value.toLocaleTimeString('zh-CN', { hour12: false }))
const tickerItems = computed(() => [
  { label: '官方参考 USD/CNY', value: '7.19' },
  { label: '站用常见汇率', value: '7.30 ~ 7.50' },
  { label: '当前账号成本', value: `¥${format(cost.value, 0)}` },
  { label: '当前账号额度', value: `$${format(quota.value, 0)}` },
  { label: '收款通道费率约', value: '2% ~ 4%' },
  { label: '口诀：有效单价', value: '倍率 × 汇率' },
  { label: '官方原价倍率', value: '1.00×' },
  { label: '当前保本线', value: `${format(pricing.value.breakEven, 3)}×` },
  { label: '当前时间', value: formattedTime.value },
])
const lossHint = computed(() => `实收 ${format(pricing.value.netRate * 100, 0)}%`)
const quotaSummary = computed(() => quotaCalculation.value.valid
  ? `${format(quotaCalculation.value.percent)}% = ${format(quotaCalculation.value.amount)} ${quotaUnit.value}`
  : '请输入占比与对应额度')

function resetPricing() {
  cost.value = 158
  quota.value = 100
  rate.value = 1
  loss.value = 3
  targetProfit.value = 20
  simulationMultiplier.value = 0.3
}

async function copyResult() {
  const result = `保本倍率 ${format(pricing.value.breakEven, 4)} | 推荐倍率 ${format(pricing.value.recommended, 4)} | 保本单价 ¥${format(pricing.value.breakEvenPrice, 2)}/$`
  try {
    await navigator.clipboard.writeText(result)
    copied.value = true
    window.setTimeout(() => { copied.value = false }, 1800)
  } catch {
    copied.value = false
  }
}

onMounted(() => {
  clockTimer = window.setInterval(() => { now.value = new Date() }, 1000)
})

onBeforeUnmount(() => {
  if (clockTimer !== undefined) window.clearInterval(clockTimer)
})
</script>

<template>
  <section class="page pricing-page">
    <div class="pricing-ticker" aria-label="定价台提示" @mouseenter="tickerPaused = true" @mouseleave="tickerPaused = false">
      <div class="ticker-track" :class="{ paused: tickerPaused }">
        <div v-for="copy in 2" :key="copy" class="ticker-copy" aria-hidden="true">
          <span v-for="(item, index) in tickerItems" :key="`${copy}-${item.label}`" class="ticker-item">
            <CirclePercent v-if="index === 0" :size="13" />
            <span>{{ item.label }}</span><strong>{{ item.value }}</strong>
          </span>
        </div>
      </div>
    </div>

    <div class="page-heading pricing-heading">
      <div>
        <span class="eyebrow">LOCAL TOOLBOX / PRICING DESK</span>
        <h2>保本测算</h2>
        <p>保本倍率、额度反推和利润试算，所有计算都在当前浏览器完成。</p>
      </div>
      <div class="pricing-actions">
        <span class="local-status"><span />纯本地计算</span>
        <button class="button ghost" title="恢复默认参数" @click="resetPricing"><RotateCcw :size="15" />重置</button>
      </div>
    </div>

    <div class="pricing-grid">
      <section class="tool-panel">
        <div class="tool-panel-heading">
          <div class="tool-title"><span class="tool-icon accent"><Calculator :size="19" /></span><div><span class="eyebrow">BREAK-EVEN MULTIPLIER</span><h3>账号保本测算</h3></div></div>
          <button class="icon-button" title="复制计算结果" @click="copyResult"><Check v-if="copied" :size="17" /><Copy v-else :size="17" /></button>
        </div>

        <div class="tool-form-grid">
          <label class="tool-field"><span>账号成本 <small>¥ 人民币</small></span><input v-model.number="cost" type="number" min="0" step="any" /></label>
          <label class="tool-field"><span>账号额度 <small>$ 美元面值</small></span><input v-model.number="quota" type="number" min="0" step="any" /></label>
          <label class="tool-field wide"><span>结算汇率 <small>¥ / $</small></span><input v-model.number="rate" type="number" min="0" step="any" /><span class="preset-row"><button v-for="preset in ratePresets" :key="preset" type="button" class="preset" :class="{ active: rate === preset }" @click="rate = preset">{{ preset === 7.19 ? '7.19 官价' : preset.toFixed(2) }}</button></span></label>
          <label class="tool-field"><span>综合损耗 <small>{{ lossHint }}</small></span><input v-model.number="loss" type="number" min="0" max="90" step="any" /></label>
          <label class="tool-field"><span>目标利润率 <small>%</small></span><input v-model.number="targetProfit" type="number" min="0" step="any" /></label>
        </div>

        <div class="result-grid">
          <div class="result-card primary-result"><span>保本倍率</span><AnimatedNumber :value="pricing.breakEven" :digits="3" suffix="×" /><small>低于此倍率出售即亏损</small></div>
          <div class="result-card"><span>推荐售价倍率</span><AnimatedNumber :value="pricing.recommended" :digits="3" suffix="×" /><small>含 {{ format(pricing.profitPercent, 0) }}% 目标利润</small></div>
          <div class="result-card"><span>保本单价</span><AnimatedNumber :value="pricing.breakEvenPrice" prefix="¥" /><small>含损耗 / 每 1 美元额度</small></div>
          <div class="result-card"><span>进货单价</span><AnimatedNumber :value="pricing.unitCost" prefix="¥" /><small>成本 ÷ 额度</small></div>
        </div>

        <div class="ruler-block">
          <div class="ruler-label"><span>定价标尺 · 倍率轴</span><span>红线内 = 亏损区</span></div>
          <div class="ruler"><span class="ruler-loss" :style="{ width: `${rulerBreakEven}%` }" /><span class="ruler-mark break-even" :style="{ left: `${rulerBreakEven}%` }"><i>保本 {{ format(pricing.breakEven, 3) }}</i></span><span class="ruler-mark recommended" :style="{ left: `${rulerRecommended}%` }"><i>推荐 {{ format(pricing.recommended, 3) }}</i></span></div>
          <div class="ruler-note">红线 = 保本倍率 · 金色标记 = 推荐倍率</div>
        </div>

        <div class="simulation-block">
          <div class="simulation-heading"><span>利润试算 · 拖动查看不同售价倍率</span><strong>{{ format(simulationMultiplier, 3) }}×</strong></div>
          <div class="range-wrap"><input v-model.number="simulationMultiplier" type="range" min="0.05" max="1.2" step="0.005" /><span class="range-break-even" :style="{ left: `${simulationBreakEven}%` }">保本</span></div>
          <div class="simulation-results"><div><span>售空营收</span><AnimatedNumber :value="simulation.revenue" prefix="¥" /></div><div><span>净利润</span><AnimatedNumber :value="Math.abs(simulation.netProfit)" :prefix="simulation.netProfit >= 0 ? '+¥' : '-¥'" :class="simulation.netProfit >= 0 ? 'positive' : 'negative'" /></div><div><span>投资回报</span><AnimatedNumber :value="simulation.roi" :digits="1" suffix="%" :class="simulation.roi >= 0 ? 'positive' : 'negative'" /></div></div>
        </div>
      </section>

      <section class="tool-panel">
        <div class="tool-panel-heading"><div class="tool-title"><span class="tool-icon blue"><WalletCards :size="19" /></span><div><span class="eyebrow">QUOTA BACK-CALC</span><h3>账号额度反推</h3></div></div></div>
        <div class="tool-form-grid quota-form">
          <label class="tool-field"><span>已知占比 <small>%</small></span><input v-model.number="quotaPercent" type="number" min="0.01" max="100" step="any" /><span class="preset-row"><button v-for="preset in quotaPercentPresets" :key="preset" type="button" class="preset" :class="{ active: quotaPercent === preset }" @click="quotaPercent = preset">{{ preset }}%</button></span></label>
          <label class="tool-field"><span>对应额度 <small>按所选单位</small></span><input v-model.number="quotaAmount" type="number" min="0" step="any" /></label>
          <div class="tool-field wide"><span>单位 <small>输入与结果同单位</small></span><span class="preset-row unit-row"><button v-for="unit in quotaUnits" :key="unit" type="button" class="preset" :class="{ active: quotaUnit === unit }" @click="quotaUnit = unit">{{ unit === 'M' ? 'M · 百万' : unit }}</button></span></div>
        </div>
        <div class="result-grid quota-results"><div class="result-card primary-result"><span>100% 总额度</span><AnimatedNumber :value="quotaCalculation.total" :suffix="` ${quotaUnit}`" /><small>{{ quotaSummary }}</small></div><div class="result-card"><span>1% 对应额度</span><AnimatedNumber :value="quotaCalculation.perOne" :suffix="` ${quotaUnit}`" /><small>总额 ÷ 100</small></div><div class="result-card"><span>剩余额度</span><AnimatedNumber :value="quotaCalculation.rest" :suffix="` ${quotaUnit}`" /><small>总额 − 已知部分</small></div><div class="result-card"><span>放大倍数</span><AnimatedNumber :value="quotaCalculation.multiplier" suffix="×" /><small>100% ÷ 已知占比</small></div></div>
        <p class="formula-note">换算：<strong>总额 = 对应额度 ÷ 占比</strong> · 例如 6.5M ÷ 3% = 216.67M</p>
      </section>
    </div>

    <section class="tool-panel audit-panel">
      <div class="tool-panel-heading"><div class="tool-title"><span class="tool-icon green"><BookOpenCheck :size="19" /></span><div><span class="eyebrow">FORMULA AUDIT</span><h3>定价逻辑 · 校验与修正</h3></div></div></div>
      <div class="audit-grid"><div class="formula-list"><div class="formula-item"><strong>保本倍率 = 成本 ÷ (额度 × 汇率 × (1 − 损耗率))</strong><span>汇率在分母，额度按美元计价，你收的是人民币。</span></div><div class="formula-item"><strong>有效单价 = 倍率 × 汇率 × (1 − 折扣率)</strong><span>比较不同站点价格时，统一换算成有效单价。</span></div><div class="formula-item"><strong>利润 = 售空营收 − 账号成本</strong><span>本工具按额度全部售空估算，实际成交率需要自行折算。</span></div><div class="formula-item warning"><strong>常见错误：成本 ÷ 额度 × 汇率</strong><span>汇率方向错误会把倍率放大，容易得出虚高的保本线。</span></div></div><div class="tips-list"><div><b>01</b><span><strong>只比倍率，不比汇率</strong><small>倍率低的站可能通过更高结算汇率赚回来。</small></span></div><div><b>02</b><span><strong>把通道费和坏账计入损耗</strong><small>收款抽成、退款和封号风险都应进入综合损耗。</small></span></div><div><b>03</b><span><strong>不要把面值当成实际可用额度</strong><small>订阅制额度常常跑不满，建议按实际成交率折算。</small></span></div><div><b>04</b><span><strong>保持折扣口径一致</strong><small>倍率折扣和余额折扣的计算顺序会影响最终价格。</small></span></div></div></div>
    </section>

    <footer class="tool-footer"><span>Running Tools 工具箱 · 保本测算</span><span><Clipboard :size="13" />刷新页面不会上传或保存计算数据</span></footer>
  </section>
</template>

<style scoped>
.pricing-page { max-width: 1440px; }
.pricing-heading { align-items: center; }
.pricing-actions { display: flex; align-items: center; gap: 10px; }
.local-status { display: inline-flex; align-items: center; gap: 7px; padding: 0 10px; min-height: 32px; border: 1px solid var(--border-soft); border-radius: 7px; color: var(--muted); font-size: 11px; white-space: nowrap; }
.local-status span { width: 7px; height: 7px; border-radius: 50%; background: #10b981; box-shadow: 0 0 0 3px rgba(16, 185, 129, .12); }
.pricing-ticker { position: relative; display: flex; align-items: center; min-height: 36px; margin: -28px -28px 24px; padding: 0; color: var(--muted); background: color-mix(in srgb, var(--surface) 90%, var(--app-bg)); border-bottom: 1px solid var(--border-soft); font: 500 11px/1 ui-monospace, SFMono-Regular, Consolas, monospace; overflow: hidden; white-space: nowrap; }
.pricing-ticker::before, .pricing-ticker::after { position: absolute; top: 0; bottom: 0; z-index: 1; width: 34px; content: ''; pointer-events: none; }
.pricing-ticker::before { left: 0; background: linear-gradient(90deg, color-mix(in srgb, var(--surface) 90%, var(--app-bg)), transparent); }
.pricing-ticker::after { right: 0; background: linear-gradient(270deg, color-mix(in srgb, var(--surface) 90%, var(--app-bg)), transparent); }
.ticker-track { display: flex; width: max-content; animation: pricing-ticker-scroll 36s linear infinite; will-change: transform; }
.ticker-track.paused { animation-play-state: paused; }
.ticker-copy { display: flex; flex: 0 0 auto; align-items: center; }
.ticker-item { display: inline-flex; align-items: center; gap: 7px; min-height: 36px; padding: 0 28px; }
.ticker-item::before { width: 4px; height: 4px; margin-right: 13px; content: ''; background: var(--border); border-radius: 50%; }
.ticker-item strong { color: var(--primary-text); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-weight: 700; }
@keyframes pricing-ticker-scroll { from { transform: translateX(0); } to { transform: translateX(-50%); } }
@media (prefers-reduced-motion: reduce) { .ticker-track { animation: none; } }
.pricing-grid { display: grid; grid-template-columns: minmax(0, 1.08fr) minmax(360px, .92fr); gap: 16px; align-items: start; }
.tool-panel { min-width: 0; padding: 20px; background: var(--surface); border: 1px solid var(--border-soft); border-radius: 8px; box-shadow: 0 1px 2px rgba(15, 23, 42, .035); }
.tool-panel-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 14px; margin-bottom: 18px; }
.tool-title { display: flex; align-items: center; gap: 11px; min-width: 0; }
.tool-title h3 { margin: 3px 0 0; color: var(--text); font-size: 16px; font-weight: 700; }
.tool-icon { display: grid; flex: 0 0 38px; width: 38px; height: 38px; border-radius: 8px; place-items: center; }
.tool-icon.accent { color: #b45309; background: #fffbeb; }
.tool-icon.blue { color: #2563eb; background: #eff6ff; }
.tool-icon.green { color: #0f766e; background: var(--primary-soft); }
:global(:root[data-theme="dark"]) .tool-icon.accent { color: #fbbf24; background: rgba(245, 158, 11, .12); }
:global(:root[data-theme="dark"]) .tool-icon.blue { color: #93c5fd; background: rgba(37, 99, 235, .14); }
.tool-form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 13px; }
.tool-field { display: grid; align-content: start; gap: 7px; min-width: 0; color: var(--text); font-size: 12px; font-weight: 650; }
.tool-field.wide { grid-column: 1 / -1; }
.tool-field small { float: right; color: var(--muted); font-size: 10px; font-weight: 500; }
.tool-field input { width: 100%; min-height: 40px; padding: 8px 10px; color: var(--text); background: var(--surface-subtle); border: 1px solid var(--border); border-radius: 7px; outline: none; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 13px; transition: border-color 150ms ease, box-shadow 150ms ease; }
.tool-field input:focus { background: var(--surface); border-color: #14b8a6; box-shadow: 0 0 0 3px rgba(20, 184, 166, .13); }
.preset-row { display: flex; flex-wrap: wrap; gap: 6px; }
.preset { min-height: 27px; padding: 0 9px; color: var(--muted); background: var(--surface-subtle); border: 1px solid var(--border); border-radius: 5px; font-size: 10px; cursor: pointer; transition: color 150ms ease, border-color 150ms ease, background 150ms ease; }
.preset:hover, .preset.active { color: var(--primary-text); background: var(--primary-soft); border-color: color-mix(in srgb, var(--primary) 35%, transparent); }
.unit-row .preset { min-width: 48px; }
.result-grid { display: grid; grid-template-columns: 1.2fr 1fr; gap: 9px; margin-top: 18px; }
.result-card { display: flex; min-height: 96px; flex-direction: column; justify-content: center; padding: 12px 14px; background: var(--surface-subtle); border: 1px solid var(--border-soft); border-radius: 7px; }
.result-card.primary-result { background: linear-gradient(145deg, rgba(245, 158, 11, .13), rgba(245, 158, 11, .025)); border-color: rgba(245, 158, 11, .35); }
.result-card span { color: var(--muted); font-size: 11px; }
.result-card strong { margin: 5px 0 2px; color: var(--text); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 24px; line-height: 1.2; }
.primary-result strong { color: #b45309; font-size: 32px; }
:global(:root[data-theme="dark"]) .primary-result strong { color: #fbbf24; }
.result-card small { color: var(--muted); font-size: 10px; }
.ruler-block { margin-top: 22px; }
.ruler-label, .simulation-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; color: var(--muted); font-size: 11px; }
.ruler { position: relative; height: 44px; margin-top: 12px; }
.ruler::before { position: absolute; top: 20px; right: 0; left: 0; height: 2px; content: ''; background: var(--border); }
.ruler-loss { position: absolute; top: 20px; left: 0; height: 2px; background: #ef4444; border-radius: 2px; opacity: .7; }
.ruler-mark { position: absolute; top: 7px; width: 2px; height: 28px; border-radius: 2px; transform: translateX(-50%); }
.ruler-mark i { position: absolute; top: -3px; left: 50%; transform: translate(-50%, -100%); color: var(--muted); font: 600 9px ui-monospace, monospace; font-style: normal; white-space: nowrap; }
.ruler-mark.break-even { background: #ef4444; }
.ruler-mark.break-even i { color: #dc2626; }
.ruler-mark.recommended { width: 10px; height: 10px; top: 15px; background: #f59e0b; border-radius: 2px; transform: translateX(-50%) rotate(45deg); box-shadow: 0 0 0 3px rgba(245, 158, 11, .15); }
.ruler-mark.recommended i { transform: translate(-50%, -100%) rotate(-45deg); color: #b45309; }
.ruler-note, .formula-note { margin-top: 4px; color: var(--muted); font-size: 10px; line-height: 1.5; }
.simulation-block { margin-top: 20px; padding-top: 17px; border-top: 1px dashed var(--border); }
.simulation-heading strong { color: var(--primary-text); font-family: ui-monospace, monospace; }
.range-wrap { position: relative; padding: 13px 0 4px; }
.range-wrap input { width: 100%; accent-color: var(--primary); cursor: pointer; }
.range-break-even { position: absolute; top: 0; color: #dc2626; font: 600 9px ui-monospace, monospace; transform: translateX(-50%); }
.simulation-results { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin-top: 10px; }
.simulation-results > div { display: grid; gap: 3px; padding: 10px; background: var(--surface-subtle); border: 1px solid var(--border-soft); border-radius: 6px; }
.simulation-results span { color: var(--muted); font-size: 10px; }
.simulation-results strong { font-family: ui-monospace, monospace; font-size: 14px; }
.positive { color: #059669; }
.negative { color: var(--danger); }
.quota-form { margin-bottom: 4px; }
.quota-results .primary-result { grid-column: 1 / -1; min-height: 112px; }
.quota-results .primary-result strong { font-size: 30px; }
.formula-note { margin-top: 16px; }
.formula-note strong { color: var(--primary-text); }
.audit-panel { margin-top: 16px; }
.audit-grid { display: grid; grid-template-columns: minmax(0, 1.08fr) minmax(0, .92fr); gap: 26px; }
.formula-list, .tips-list { display: grid; gap: 8px; }
.formula-item { display: grid; gap: 5px; padding: 11px 13px; background: var(--surface-subtle); border: 1px solid var(--border-soft); border-radius: 7px; }
.formula-item strong { color: var(--primary-text); font-family: ui-monospace, monospace; font-size: 11px; overflow-wrap: anywhere; }
.formula-item span { color: var(--muted); font-size: 11px; line-height: 1.5; }
.formula-item.warning { border-color: rgba(220, 38, 38, .25); }
.formula-item.warning strong { color: var(--danger); }
.tips-list > div { display: flex; gap: 12px; padding: 10px 0; border-bottom: 1px dashed var(--border); }
.tips-list > div:last-child { border-bottom: 0; }
.tips-list b { flex: 0 0 22px; color: #b45309; font-family: ui-monospace, monospace; font-size: 13px; }
.tips-list span { display: grid; gap: 3px; }
.tips-list strong { color: var(--text); font-size: 12px; }
.tips-list small { color: var(--muted); font-size: 11px; line-height: 1.5; }
.tool-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 20px 2px 4px; color: var(--muted); font-size: 10px; }
.tool-footer span { display: inline-flex; align-items: center; gap: 5px; }
@media (max-width: 1023px) { .pricing-grid, .audit-grid { grid-template-columns: minmax(0, 1fr); } }
@media (max-width: 760px) { .pricing-ticker { margin: -20px -14px 18px; } }
@media (max-width: 620px) { .pricing-heading { align-items: flex-start; } .pricing-actions { width: 100%; justify-content: space-between; } .ticker-item { padding: 0 18px; } .ticker-item::before { margin-right: 8px; } .tool-panel { padding: 16px; } .tool-form-grid { grid-template-columns: minmax(0, 1fr); } .tool-field.wide { grid-column: auto; } .result-grid, .simulation-results { grid-template-columns: minmax(0, 1fr); } .quota-results .primary-result { grid-column: auto; } .tool-footer { align-items: flex-start; flex-direction: column; } }
</style>
