import { apiClient } from '@/api/client'
import type { Proxy } from '@/types'

export interface CodexTicketConfig {
  schema_version: 1
  enabled: boolean
  harvest_proxy_id: number | null
  models: string[]
  target_length: number
  ttl_seconds: number
  refresh_before_seconds: number
  missing_ticket_policy: 'passthrough' | 'reject'
  max_concurrency: number
  max_probes_per_minute: number
}
export interface TicketRuntimeStatus {
  desired_revision: string
  applied_revision: string | null
  phase: 'disabled' | 'applying' | 'waiting' | 'collecting' | 'ready' | 'degraded' | 'proxy_unavailable'
  proxy_state: string | null
  ready_count: number
  pending_count: number
  inflight_count: number
  last_error_code: string | null
  counter_scope: string
  supported_transports?: string[]
}
export interface TicketSettingsSnapshot {
  settings: CodexTicketConfig & { revision: string }
  selected_proxy: null | { id: number; name: string | null; state: string; protocol?: string; host?: string; port?: number }
  runtime: TicketRuntimeStatus | null
  runtime_error_reason: string | null
}
const path = '/admin/settings/openai-codex-ticket'
export async function getCodexTicketSettings(signal?: AbortSignal): Promise<TicketSettingsSnapshot> {
  const { data } = await apiClient.get<TicketSettingsSnapshot>(path, { signal })
  return data
}
export async function saveCodexTicketSettings(settings: CodexTicketConfig, expectedRevision: string, signal?: AbortSignal): Promise<TicketSettingsSnapshot> {
  const { data } = await apiClient.put<TicketSettingsSnapshot>(path, { expected_revision: expectedRevision, settings }, { signal })
  return data
}
export async function getCodexTicketStatus(signal?: AbortSignal): Promise<TicketRuntimeStatus> {
  const { data } = await apiClient.get<TicketRuntimeStatus>(`${path}/status`, { signal })
  return data
}
export async function getHarvestProxyOptions(signal?: AbortSignal): Promise<Proxy[]> {
  const { data } = await apiClient.get<Proxy[]>('/admin/proxies/all', { signal })
  if (!Array.isArray(data)) throw new Error('Invalid proxy list response')
  return data
}
export async function testHarvestProxy(id: number, signal?: AbortSignal): Promise<{ success: boolean; message: string; latency_ms?: number }> {
  const { data } = await apiClient.post<{ success: boolean; message: string; latency_ms?: number }>(`/admin/proxies/${id}/test`, undefined, { signal })
  return data
}
