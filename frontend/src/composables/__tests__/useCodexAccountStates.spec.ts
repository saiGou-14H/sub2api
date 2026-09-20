import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref, type EffectScope } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { getCodexAccountStates, type CodexAccountState, type CodexAccountsSnapshot } from '@/api/admin/codexTicket'
import { useCodexAccountStates } from '../useCodexAccountStates'
vi.mock('@/api/admin/codexTicket', () => ({ getCodexAccountStates: vi.fn() }))
const api = vi.mocked(getCodexAccountStates)
let scope: EffectScope
const base = Date.parse('2026-01-01T00:00:00Z')
const account = (id: number): CodexAccountState => ({ account_id: id, enabled: true, supported: true, status: 'ready', models: [{ model: `model-${id}`, status: 'ready', captured_at: null, expires_at: new Date(base + 1000).toISOString(), refresh_at: null, next_attempt_at: null, last_error_code: null, injection_count: 2, last_injected_at: new Date(base - 1000).toISOString(), last_request_id: `req-${id}`, last_outcome: 'header_set', last_reason: null }] })
const snapshot = (ids = [1]): CodexAccountsSnapshot => ({ desired_revision: '1', applied_revision: '1', global_enabled: true, proxy_state: 'ready', server_time: new Date(base).toISOString(), counter_scope: 'shared_cache_window', accounts: ids.map(account) })
function setup(values = [1]) {
  const ids = ref(values)
  scope = effectScope()
  const state = scope.run(() => useCodexAccountStates(ids))!
  return { ids, ...state }
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date', 'performance'] })
  vi.setSystemTime(base)
  vi.resetAllMocks()
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  api.mockImplementation(async ids => snapshot(ids))
})
afterEach(() => { scope?.stop(); vi.useRealTimers() })
describe('visible Codex account state', () => {
  it('batches deduplicated pages and never requests an empty page', async () => {
    const state = setup([])
    expect(api).not.toHaveBeenCalled()
    state.ids.value = [...Array.from({ length: 201 }, (_, i) => i + 1), 1]
    await flushPromises()
    expect(api.mock.calls.map(call => call[0].length)).toEqual([100, 100, 1])
    expect(state.states.value[201].account_id).toBe(201)
  })
  it('ignores stale responses and aborts on page changes and disposal', async () => {
    let resolve!: (value: CodexAccountsSnapshot) => void
    api.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const state = setup()
    const signal = api.mock.calls[0][1]!
    state.ids.value = [2]
    await flushPromises()
    expect(signal.aborted).toBe(true)
    resolve(snapshot([1]))
    await flushPromises()
    expect(state.states.value[1]).toBeUndefined()
    expect(state.states.value[2].models[0].model).toBe('model-2')
    api.mockReturnValueOnce(new Promise(() => {}))
    void state.refresh()
    const last = api.mock.calls.at(-1)![1]!
    scope.stop()
    expect(last.aborted).toBe(true)
  })
  it('does not reuse another account or retain green state after failures', async () => {
    api.mockResolvedValueOnce(snapshot([1]))
    const state = setup([1, 2])
    await flushPromises()
    expect(state.states.value[2].status).toBe('unavailable')
    expect(state.states.value[2].models).toEqual([])
    api.mockRejectedValue(new Error('secret must not reach UI'))
    await state.refresh()
    expect(state.states.value[1].status).toBe('unavailable')
    expect(state.failed.value).toBe(true)
    await vi.advanceTimersByTimeAsync(4999)
    expect(api).toHaveBeenCalledTimes(2)
  })
  it('stops while hidden and immediately reads again on visibility', async () => {
    const state = setup()
    await flushPromises()
    Object.defineProperty(document, 'hidden', { value: true })
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(15000)
    expect(api).toHaveBeenCalledTimes(1)
    Object.defineProperty(document, 'hidden', { value: false })
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(api).toHaveBeenCalledTimes(2)
    expect(state.states.value[1]).toBeDefined()
  })
  it('expires on the server clock, retains only historical injection and refreshes once', async () => {
    const state = setup()
    await flushPromises()
    api.mockReturnValue(new Promise(() => {}))
    await vi.advanceTimersByTimeAsync(1001)
    expect(state.states.value[1].status).toBe('expired')
    expect(state.states.value[1].models[0].status).toBe('expired')
    expect(state.states.value[1].models[0].last_outcome).toBe('header_set')
    expect(api).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1000)
    expect(api).toHaveBeenCalledTimes(2)
  })
  it('invalidates a pending read when filters change even with the same visible IDs', async () => {
    let resolve!: (value: CodexAccountsSnapshot) => void
    api.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const ids = ref([1])
    const key = ref('filter-a')
    scope = effectScope()
    const state = scope.run(() => useCodexAccountStates(ids, key))!
    const oldSignal = api.mock.calls[0][1]!
    const current = snapshot([1])
    current.accounts[0].status = 'backoff'
    api.mockResolvedValue(current)
    key.value = 'filter-b'
    await flushPromises()
    resolve(snapshot([1]))
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    expect(state.states.value[1].status).toBe('backoff')
  })
  it('aggregates expiry over another still-valid refreshing model', async () => {
    const payload = snapshot([1])
    payload.accounts[0].status = 'refreshing'
    const live = { ...account(1).models[0], model: 'live', status: 'refreshing' as const, expires_at: new Date(base + 10000).toISOString() }
    payload.accounts[0].models.push(live)
    api.mockResolvedValueOnce(payload)
    const state = setup()
    await flushPromises()
    api.mockReturnValue(new Promise(() => {}))
    await vi.advanceTimersByTimeAsync(1001)
    expect(state.states.value[1].models.map(model => model.status)).toEqual(['expired', 'refreshing'])
    expect(state.states.value[1].status).toBe('expired')
    await vi.advanceTimersByTimeAsync(9000)
    expect(state.states.value[1].status).toBe('expired')
    expect(api).toHaveBeenCalledTimes(2)
  })
  it.each(['unavailable', 'unsupported', 'disabled', 'inactive', 'proxy_unavailable', 'backoff'] as const)('preserves more severe %s when a model expires locally', async status => {
    const payload = snapshot([1])
    payload.accounts[0].status = status
    api.mockResolvedValueOnce(payload)
    const state = setup()
    await flushPromises()
    api.mockReturnValue(new Promise(() => {}))
    await vi.advanceTimersByTimeAsync(1001)
    expect(state.states.value[1].models[0].status).toBe('expired')
    expect(state.states.value[1].status).toBe(status)
  })
  it('preserves explicit unavailable and unsupported even when global enabled is false', async () => {
    const payload = snapshot([1, 2])
    payload.global_enabled = false
    payload.accounts[0].status = 'unavailable'
    payload.accounts[0].enabled = false
    payload.accounts[1].status = 'unsupported'
    payload.accounts[1].enabled = false
    payload.accounts[1].supported = false
    api.mockResolvedValue(payload)
    const state = setup([1, 2])
    await flushPromises()
    expect(state.states.value[1].status).toBe('unavailable')
    expect(state.states.value[2].status).toBe('unsupported')
  })
  it('honors disabled and unsupported states', async () => {
    const payload = snapshot([1, 2])
    payload.accounts[0].enabled = false
    payload.accounts[1].supported = false
    api.mockResolvedValue(payload)
    const state = setup([1, 2])
    await flushPromises()
    expect(state.states.value[1].status).toBe('disabled')
    expect(state.states.value[2].status).toBe('unsupported')
  })
})
