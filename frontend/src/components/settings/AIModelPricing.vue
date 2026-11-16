<script setup lang="ts">
import type { AIModel, TokenPrices } from '@/api/ai'

defineProps<{
  models: AIModel[]
  loading: boolean
  error: string
}>()

const priceFormat = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 4,
})

// A price of zero means the provider doesn't bill that kind of token on its own.
function formatPrice(prices: TokenPrices, key: keyof TokenPrices): string {
  return prices[key] > 0 ? priceFormat.format(prices[key]) : '—'
}

function formatTokenCount(tokens: number): string {
  return tokens >= 1000 && tokens % 1000 === 0 ? `${tokens / 1000}K` : tokens.toLocaleString()
}

const priceColumns: { key: keyof TokenPrices; label: string }[] = [
  { key: 'input', label: 'Input' },
  { key: 'output', label: 'Output' },
  { key: 'cacheRead', label: 'Cache read' },
  { key: 'cacheWrite', label: 'Cache write' },
]
</script>

<template>
  <section class="settings-section">
    <h2 class="section-title">AI Model Pricing</h2>
    <p class="section-desc">
      USD per million tokens, used to work out what each agent run costs and to keep it within its budget. Prices are
      built into devpad.
    </p>

    <div v-if="error" class="alert alert-error">{{ error }}</div>

    <div v-if="loading" class="loading-state">Loading models...</div>

    <template v-else-if="!error">
      <div class="pricing-table-wrap">
        <table class="pricing-table">
          <thead>
            <tr>
              <th>Model</th>
              <th v-for="col in priceColumns" :key="col.key" class="cell-price">{{ col.label }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="model in models" :key="model.providerId + '/' + model.id">
              <td class="cell-model">
                {{ model.name }}
                <div class="cell-model-meta">
                  {{ model.providerName }}<template v-if="!model.configured"> · no key</template>
                </div>
              </td>
              <template v-if="model.pricing">
                <td v-for="col in priceColumns" :key="col.key" class="cell-price">
                  {{ formatPrice(model.pricing, col.key) }}
                  <div v-if="model.pricing.longContext && model.pricing.longContextThreshold" class="cell-long-context">
                    {{ formatPrice(model.pricing.longContext, col.key) }} over
                    {{ formatTokenCount(model.pricing.longContextThreshold) }}
                  </div>
                </td>
              </template>
              <td v-else :colspan="priceColumns.length" class="cell-unpriced">Prices unknown, so runs aren't costed</td>
            </tr>
          </tbody>
        </table>
        <div v-if="models.length === 0" class="loading-state">No models found.</div>
      </div>
      <p class="field-hint">
        — means the provider doesn't bill that kind of token separately. Where a second price is shown, it replaces the
        first for a request whose prompt is over that many tokens.
      </p>
    </template>
  </section>
</template>

<style scoped src="./ai-settings.css"></style>

<style scoped>
.pricing-table-wrap {
  border: 0.5px solid var(--border-default);
  border-radius: var(--radius-lg);
  overflow-x: auto;
}

.pricing-table {
  width: 100%;
  border-collapse: collapse;
}

.pricing-table th {
  text-align: left;
  padding: var(--space-2) var(--space-3);
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
  background: var(--bg-surface-alt);
  border-bottom: 0.5px solid var(--border-default);
}

.pricing-table td {
  padding: var(--space-2) var(--space-3);
  font-size: 0.85rem;
  color: var(--text-secondary);
  border-bottom: 0.5px solid var(--border-default);
}

.pricing-table tr:last-child td {
  border-bottom: none;
}

.pricing-table th.cell-price {
  text-align: right;
}

.pricing-table td.cell-price {
  text-align: right;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.cell-model {
  color: var(--text-primary);
  font-weight: 500;
}

.cell-model-meta,
.cell-long-context {
  font-size: 0.72rem;
  font-weight: 400;
  color: var(--text-muted);
}

.cell-unpriced {
  font-size: 0.8rem;
  color: var(--text-muted);
}
</style>
