import { describe, expect, it } from 'vitest'
import { normalizeOpenAITransport } from '../openaiTransport'

describe('OpenAI transport compatibility', () => {
  it.each([undefined, null, '', 'unknown', false])('retains Codex for legacy or invalid marker %s', (value) => {
    expect(normalizeOpenAITransport(value)).toBe('codex')
  })

  it.each(['codex', 'web', 'prism'] as const)('accepts stored %s markers with whitespace and mixed case', (value) => {
    expect(normalizeOpenAITransport(` ${value.toUpperCase()} `)).toBe(value)
  })
})
