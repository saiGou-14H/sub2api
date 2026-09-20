import { normalizeOpenAITransport } from './openaiTransport'

export interface CodexTurnStateAccount {
  id?: number
  platform: string
  type: string
  parent_account_id?: number | null
  credentials?: Record<string, unknown> | null
  extra?: Record<string, unknown> | null
}

export function supportsCodexTurnState(account: CodexTurnStateAccount, transport?: unknown): boolean {
  const credentials = account.credentials
  const authMode = credentials?.auth_mode ?? credentials?.openai_auth_mode ?? credentials?.authMode
  const agentIdentity = typeof authMode === 'string' &&
    authMode.trim().toLowerCase().replace(/[_-]/g, '') === 'agentidentity'
  return account.platform === 'openai' &&
    (account.type === 'oauth' || account.type === 'setup-token') &&
    account.parent_account_id == null && !agentIdentity &&
    !credentials?.agent_identity && !credentials?.agentIdentity &&
    normalizeOpenAITransport(transport ?? account.extra?.openai_transport) === 'codex'
}

/** Account opt-in is strict, defaults off, and never inherits the global switch. */
export function readCodexTurnStateEnabled(extra?: Record<string, unknown> | null): boolean {
  return extra?.codex_turn_state_enabled === true
}
