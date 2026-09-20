import { describe, expect, it } from 'vitest'
import { readCodexTurnStateEnabled, supportsCodexTurnState } from '@/utils/codexTurnState'

describe('Codex turn-state account eligibility', () => {
  it.each([undefined, null, false, 'true', 'false', 0, 1, {}, []])('defaults malformed or absent %j opt-in to false', value => {
    expect(readCodexTurnStateEnabled({ codex_turn_state_enabled: value })).toBe(false)
  })

  it('requires explicit account true without global inheritance', () => {
    expect(readCodexTurnStateEnabled()).toBe(false)
    expect(readCodexTurnStateEnabled({ enabled: true })).toBe(false)
    expect(readCodexTurnStateEnabled({ codex_turn_state_enabled: true })).toBe(true)
  })

  it.each(['oauth', 'setup-token'])('accepts %s Codex accounts including legacy transport defaults', type => {
    expect(supportsCodexTurnState({ platform: 'openai', type })).toBe(true)
    expect(supportsCodexTurnState({ platform: 'openai', type, extra: { openai_transport: 'codex' } })).toBe(true)
  })

  it.each([
    { type: 'apikey' }, { platform: 'anthropic' }, { parent_account_id: 1 },
    { extra: { openai_transport: 'web' } }, { extra: { openai_transport: 'prism' } },
    { credentials: { auth_mode: '  Agent_Identity  ' } },
    { credentials: { auth_mode: 'agentIdentity' } },
    { credentials: { openai_auth_mode: 'agent_identity' } },
    { credentials: { authMode: 'AgentIdentity' } },
    { credentials: { agent_identity: {} } }
  ])('rejects unsupported identity/transport %j', overrides => {
    expect(supportsCodexTurnState({ platform: 'openai', type: 'oauth', ...overrides })).toBe(false)
  })
})
