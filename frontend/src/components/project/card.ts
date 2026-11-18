// How a project overview card's status pill is coloured.
export type CardTone = 'ok' | 'busy' | 'warn' | 'error' | 'neutral'

// A card's status: its pill's text and colour.
export interface CardStatus {
  label: string
  tone: CardTone
}
