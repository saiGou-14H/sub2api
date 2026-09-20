import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '@/api/client'
import { getCodexTicketSettings, saveCodexTicketSettings, getCodexTicketStatus, getHarvestProxyOptions, testHarvestProxy, type CodexTicketConfig } from '../codexTicket'
vi.mock('@/api/client', () => ({ apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn() } }))
beforeEach(() => vi.clearAllMocks())
describe('Codex ticket admin API', () => {
  it('uses the admin path once and consumes already-unwrapped response data', async () => {
    const signal = new AbortController().signal
    const data = { settings: { revision: 'large-revision-9007199254740993' } }
    vi.mocked(apiClient.get).mockResolvedValue({ data })
    expect(await getCodexTicketSettings(signal)).toBe(data)
    expect(apiClient.get).toHaveBeenCalledWith('/admin/settings/openai-codex-ticket', { signal })
  })
  it('sends the exact full replacement body, including false and null', async () => {
    const settings: CodexTicketConfig = { schema_version: 1, enabled: false, harvest_proxy_id: null,
      models: ['custom'], target_length: 332, ttl_seconds: 3600, refresh_before_seconds: 600,
      missing_ticket_policy: 'passthrough', max_concurrency: 2, max_probes_per_minute: 12 }
    const signal = new AbortController().signal
    const result = { settings: { ...settings, revision: '9007199254740994' }, runtime: null }
    vi.mocked(apiClient.put).mockResolvedValue({ data: result })
    expect(await saveCodexTicketSettings(settings, '9007199254740993', signal)).toBe(result)
    expect(apiClient.put).toHaveBeenCalledWith('/admin/settings/openai-codex-ticket', {
      expected_revision: '9007199254740993', settings
    }, { signal })
  })
  it('reads the bare status structure and forwards cancellation', async () => {
    const signal = new AbortController().signal
    const result = { phase: 'ready', counter_scope: 'local_instance_observed', supported_transports: ['responses_websocket_http_bridge'] }
    vi.mocked(apiClient.get).mockResolvedValue({ data: result })
    expect(await getCodexTicketStatus(signal)).toBe(result)
    expect(apiClient.get).toHaveBeenCalledWith('/admin/settings/openai-codex-ticket/status', { signal })
  })
  it('distinguishes invalid proxy list responses from a real empty list', async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: {} }).mockResolvedValueOnce({ data: [] })
    await expect(getHarvestProxyOptions()).rejects.toThrow('Invalid proxy list response')
    await expect(getHarvestProxyOptions()).resolves.toEqual([])
  })
  it('only posts one selected proxy test and never retries a failed PUT', async () => {
    const signal = new AbortController().signal
    vi.mocked(apiClient.post).mockResolvedValue({ data: { success: true } })
    await testHarvestProxy(42, signal)
    expect(apiClient.post).toHaveBeenCalledWith('/admin/proxies/42/test', undefined, { signal })
    vi.mocked(apiClient.put).mockRejectedValue({ status: 409 })
    await expect(saveCodexTicketSettings({} as CodexTicketConfig, '1')).rejects.toEqual({ status: 409 })
    expect(apiClient.put).toHaveBeenCalledTimes(1)
  })
})
