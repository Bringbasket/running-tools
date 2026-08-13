export interface PricingInput {
  cost: number
  quota: number
  rate: number
  lossPercent: number
  profitPercent: number
}

export interface PricingResult extends PricingInput {
  netRate: number
  breakEven: number
  recommended: number
  unitCost: number
  breakEvenPrice: number
}

export interface QuotaResult {
  percent: number
  amount: number
  valid: boolean
  total: number
  perOne: number
  rest: number
  multiplier: number
}

export interface SimulationResult {
  revenue: number
  netProfit: number
  roi: number
}

function finiteOrZero(value: number): number {
  return Number.isFinite(value) ? value : 0
}

function nonNegative(value: number): number {
  return Math.max(0, finiteOrZero(value))
}

export function calculatePricing(input: PricingInput): PricingResult {
  const cost = nonNegative(input.cost)
  const quota = nonNegative(input.quota)
  const rate = nonNegative(input.rate)
  const lossPercent = Math.min(90, nonNegative(input.lossPercent))
  const profitPercent = nonNegative(input.profitPercent)
  const netRate = 1 - lossPercent / 100
  const denominator = quota * rate * netRate
  const breakEven = denominator > 0 ? cost / denominator : Number.NaN

  return {
    cost,
    quota,
    rate,
    lossPercent,
    profitPercent,
    netRate,
    breakEven,
    recommended: Number.isFinite(breakEven) ? breakEven * (1 + profitPercent / 100) : Number.NaN,
    unitCost: quota > 0 ? cost / quota : Number.NaN,
    breakEvenPrice: quota > 0 && netRate > 0 ? cost / (quota * netRate) : Number.NaN,
  }
}

export function calculateQuota(percentValue: number, amountValue: number): QuotaResult {
  const percent = Math.min(100, nonNegative(percentValue))
  const amount = nonNegative(amountValue)
  const valid = percent > 0
  const total = valid ? amount / (percent / 100) : Number.NaN

  return {
    percent,
    amount,
    valid,
    total,
    perOne: valid ? amount / percent : Number.NaN,
    rest: valid ? total - amount : Number.NaN,
    multiplier: valid ? 100 / percent : Number.NaN,
  }
}

export function calculateSimulation(pricing: PricingResult, multiplierValue: number): SimulationResult {
  const multiplier = nonNegative(multiplierValue)
  const revenue = pricing.quota * multiplier * pricing.rate * pricing.netRate
  const netProfit = revenue - pricing.cost

  return {
    revenue,
    netProfit,
    roi: pricing.cost > 0 ? netProfit / pricing.cost * 100 : Number.NaN,
  }
}
