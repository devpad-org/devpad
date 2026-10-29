const usd = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2 })
const usdCompact = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', notation: 'compact', maximumFractionDigits: 1 })
const compact = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 })
const whole = new Intl.NumberFormat('en-US')

// A cost to the cent; a cost under a cent but over nothing says so.
export function formatCost(usdValue: number): string {
  if (usdValue > 0 && usdValue < 0.005) return '<$0.01'
  return usd.format(usdValue)
}

// An axis tick: whole dollars compact from $1,000 up, cents below $1.
export function formatAxisCost(usdValue: number): string {
  if (usdValue === 0) return '$0'
  if (usdValue < 1) return usd.format(usdValue)
  if (usdValue < 1000) return `$${whole.format(Number(usdValue.toFixed(2)))}`
  return usdCompact.format(usdValue)
}

export function formatTokens(tokens: number): string {
  return tokens < 10_000 ? whole.format(tokens) : compact.format(tokens)
}

export function formatCount(n: number): string {
  return whole.format(n)
}
