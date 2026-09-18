export type OpenAITransportMode = 'codex' | 'web' | 'prism'

/** Older accounts and unknown markers retain the existing Codex default. */
export function normalizeOpenAITransport(value: unknown): OpenAITransportMode {
  const transport = typeof value === 'string' ? value.trim().toLowerCase() : ''
  return transport === 'web' || transport === 'prism' ? transport : 'codex'
}

export function openAITransportOptions(t: (key: string) => string) {
  return [
    { value: 'codex' as const, label: t('admin.accounts.openai.transportCodex') },
    { value: 'web' as const, label: t('admin.accounts.openai.transportWeb') },
    { value: 'prism' as const, label: t('admin.accounts.openai.transportPrism') }
  ]
}
