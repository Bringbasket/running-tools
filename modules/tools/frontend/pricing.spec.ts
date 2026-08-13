import { describe, expect, it } from 'vitest'
import { calculatePricing, calculateQuota, calculateSimulation } from './pricing'

describe('保本测算', () => {
  it('按汇率和损耗计算保本及推荐倍率', () => {
    const result = calculatePricing({ cost: 158, quota: 100, rate: 7.2, lossPercent: 3, profitPercent: 20 })

    expect(result.breakEven).toBeCloseTo(158 / (100 * 7.2 * 0.97), 8)
    expect(result.recommended).toBeCloseTo(result.breakEven * 1.2, 8)
    expect(result.unitCost).toBeCloseTo(1.58, 8)
    expect(result.breakEvenPrice).toBeCloseTo(158 / 97, 8)
  })

  it('限制无效输入，避免产生误导性的负值', () => {
    const result = calculatePricing({ cost: -1, quota: 0, rate: Number.NaN, lossPercent: 150, profitPercent: -20 })

    expect(result.cost).toBe(0)
    expect(result.rate).toBe(0)
    expect(result.lossPercent).toBe(90)
    expect(Number.isNaN(result.breakEven)).toBe(true)
    expect(Number.isNaN(result.unitCost)).toBe(true)
  })

  it('从已知比例反推总额度', () => {
    const result = calculateQuota(3, 6.5)

    expect(result.total).toBeCloseTo(216.6666667, 6)
    expect(result.perOne).toBeCloseTo(2.1666667, 6)
    expect(result.rest).toBeCloseTo(210.1666667, 6)
    expect(result.multiplier).toBeCloseTo(33.3333333, 6)
  })

  it('根据售价倍率计算售空利润和回报率', () => {
    const pricing = calculatePricing({ cost: 158, quota: 100, rate: 1, lossPercent: 3, profitPercent: 20 })
    const simulation = calculateSimulation(pricing, pricing.breakEven)

    expect(simulation.revenue).toBeCloseTo(158, 8)
    expect(simulation.netProfit).toBeCloseTo(0, 8)
    expect(simulation.roi).toBeCloseTo(0, 8)
  })
})
