// An amount in USD to the cent; a cost too small to show as a cent shows as
// "<$0.01" rather than "$0.00".
export function formatUsd(usd: number): string {
  if (usd > 0 && usd < 0.01) return '<$0.01'
  return `$${usd.toFixed(2)}`
}
