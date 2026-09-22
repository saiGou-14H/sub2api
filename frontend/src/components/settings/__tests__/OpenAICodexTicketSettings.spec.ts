import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAICodexTicketSettings from '../OpenAICodexTicketSettings.vue'
import type { TicketSettingsSnapshot, TicketRuntimeStatus } from '@/api/admin/codexTicket'

const api = vi.hoisted(() => ({
  getCodexTicketSettings: vi.fn(), getCodexTicketStatus: vi.fn(), getHarvestProxyOptions: vi.fn(),
  saveCodexTicketSettings: vi.fn(), testHarvestProxy: vi.fn()
}))
vi.mock('@/api/admin/codexTicket', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const runtime = (revision = '1'): TicketRuntimeStatus => ({
  desired_revision: revision, applied_revision: revision, phase: 'disabled', proxy_state: null,
  ready_count: 0, pending_count: 0, inflight_count: 0, last_error_code: null,
  counter_scope: 'local_instance_observed',
  supported_transports: ['responses_http', 'responses_websocket_http_bridge']
})
const snapshot = (revision = '1'): TicketSettingsSnapshot => ({
  settings: { schema_version: 1, revision, enabled: false, harvest_proxy_id: 7, models: ['custom-model'],
    target_length: 332, ttl_seconds: 3600, refresh_before_seconds: 600,
    missing_ticket_policy: 'passthrough', max_concurrency: 2, max_probes_per_minute: 12 },
  selected_proxy: { id: 7, name: 'managed', state: 'active' }, runtime: runtime(revision), runtime_error_reason: null
})
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const wrappers: ReturnType<typeof mount>[] = []
async function render(active = true) {
  const wrapper = mount(OpenAICodexTicketSettings, { props: { active } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.useFakeTimers()
  Object.values(api).forEach(fn => fn.mockReset())
  api.getCodexTicketSettings.mockResolvedValue(snapshot())
  api.getCodexTicketStatus.mockResolvedValue(runtime())
  api.getHarvestProxyOptions.mockResolvedValue([{ id: 7, name: 'managed', status: 'active', protocol: 'socks5h', expires_at: null }])
  api.saveCodexTicketSettings.mockResolvedValue(snapshot('2'))
})
afterEach(() => { wrappers.splice(0).forEach(w => w.unmount()); vi.useRealTimers() })

describe('Codex ticket settings', () => {
  it('preserves an omitted legacy cookie mode as optional without marking the saved draft dirty', async () => {
    const wrapper = await render()
    expect((wrapper.get('[data-testid="ticket-cookie-pin-mode"]').element as HTMLSelectElement).value).toBe('optional')
    expect(wrapper.text()).toContain('codexTicket.noChanges')
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    expect(api.saveCodexTicketSettings.mock.calls[0][0]).toMatchObject({ cookie_pin_mode: 'optional', ttl_seconds: 3600, refresh_before_seconds: 600 })
  })
  it('preserves saved timings when explicitly switching legacy settings to required', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="ticket-cookie-pin-mode"]').setValue('required')
    expect(wrapper.text()).toContain('codexTicket.unsaved')
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    expect(api.saveCodexTicketSettings.mock.calls[0][0]).toMatchObject({ cookie_pin_mode: 'required', ttl_seconds: 3600, refresh_before_seconds: 600 })
  })
  it('shows backend timing defaults and saves an explicitly optional cookie mode', async () => {
    const value = snapshot()
    Object.assign(value.settings, { cookie_pin_mode: 'required', ttl_seconds: 240, refresh_before_seconds: 210 })
    api.getCodexTicketSettings.mockResolvedValue(value)
    const wrapper = await render()
    expect((wrapper.get('[data-testid="ticket-cookie-pin-mode"]').element as HTMLSelectElement).value).toBe('required')
    expect((wrapper.get('[data-testid="ticket-ttl_seconds"]').element as HTMLInputElement).value).toBe('240')
    expect((wrapper.get('[data-testid="ticket-refresh_before_seconds"]').element as HTMLInputElement).value).toBe('210')
    expect(wrapper.text()).toContain('codexTicket.noChanges')
    await wrapper.get('[data-testid="ticket-cookie-pin-mode"]').setValue('optional')
    expect(wrapper.text()).toContain('codexTicket.unsaved')
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    expect(api.saveCodexTicketSettings.mock.calls[0][0]).toMatchObject({ cookie_pin_mode: 'optional', ttl_seconds: 240, refresh_before_seconds: 210 })
  })
  it('blocks an unknown cookie mode until a supported mode is selected', async () => {
    const value = snapshot()
    Object.assign(value.settings, { cookie_pin_mode: 'unknown' })
    api.getCodexTicketSettings.mockResolvedValue(value)
    const wrapper = await render()
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('codexTicket.validation.cookie_pin_mode')
    await wrapper.get('[data-testid="ticket-cookie-pin-mode"]').setValue('required')
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeUndefined()
  })
  it('labels counts as local observations and displays the supported WebSocket bridge', async () => {
    const wrapper = await render()
    expect(wrapper.get('[data-testid="runtime-counter-scope"]').text()).toBe('codexTicket.localCounterScope')
    expect(wrapper.text()).toContain('responses_websocket_http_bridge')
    api.getCodexTicketStatus.mockResolvedValue({ ...runtime(), counter_scope: 'future_scope' })
    await vi.advanceTimersByTimeAsync(5000)
    expect(wrapper.get('[data-testid="runtime-counter-scope"]').text()).toBe('codexTicket.unknownCounterScope')
    expect(wrapper.text()).not.toContain('future_scope')
  })
  it.each(['cookie_missing', 'transport_unsupported', 'rate_limited', 'authentication_failed', 'probe_timeout', 'retry_unavailable'])('maps the fixed runtime classification %s', async code => {
    api.getCodexTicketSettings.mockResolvedValue({ ...snapshot(), runtime: { ...runtime(), last_error_code: code } })
    const wrapper = await render()
    expect(wrapper.get('[data-testid="runtime-error"]').text()).toBe(`codexTicket.runtimeErrors.${code}`)
  })
  it.each(['probe_timeout', 'retry_unavailable'])('updates the runtime classification to %s after polling', async code => {
    const wrapper = await render()
    expect(wrapper.find('[data-testid="runtime-error"]').exists()).toBe(false)
    api.getCodexTicketStatus.mockResolvedValue({ ...runtime(), phase: 'degraded', last_error_code: code })
    await vi.advanceTimersByTimeAsync(5000)
    expect(wrapper.get('[data-testid="runtime-error"]').text()).toBe(`codexTicket.runtimeErrors.${code}`)
    expect(wrapper.get('[data-testid="runtime-state"]').text()).toBe('codexTicket.phases.degraded')
  })
  it('never exposes an unrecognized last_error_code or upstream error body', async () => {
    const rawError = '{"access_token":"do-not-display","error":"upstream body"}'
    api.getCodexTicketSettings.mockResolvedValue({ ...snapshot(), runtime: { ...runtime(), last_error_code: rawError } })
    const wrapper = await render()
    expect(wrapper.get('[data-testid="runtime-error"]').text()).toBe('codexTicket.runtimeErrors.unknown')
    expect(wrapper.text()).not.toContain(rawError)
    expect(wrapper.text()).not.toContain('do-not-display')
  })
  it('saves independently with a full advanced payload and never tests automatically', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="ticket-enabled"]').setValue(true)
    await wrapper.get('[data-testid="ticket-models"]').setValue('custom-a\ncustom-b\ncustom-a')
    await wrapper.get('[data-testid="ticket-target_length"]').setValue(356)
    await wrapper.get('[data-testid="ticket-policy"]').setValue('reject')
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    await flushPromises()
    expect(api.saveCodexTicketSettings).toHaveBeenCalledWith({
      schema_version: 1, enabled: true, harvest_proxy_id: 7, models: ['custom-a', 'custom-b'],
      target_length: 356, ttl_seconds: 3600, refresh_before_seconds: 600,
      missing_ticket_policy: 'reject', max_concurrency: 2, max_probes_per_minute: 12, cookie_pin_mode: 'optional'
    }, '1', expect.any(AbortSignal))
    expect(api.testHarvestProxy).not.toHaveBeenCalled()
    expect(wrapper.find('form').exists()).toBe(false)
    wrapper.findAll('button').forEach(button => expect(button.attributes('type')).toBe('button'))
    expect(wrapper.get('[data-testid="save-notice"]').text()).toBe('codexTicket.savedPublished')
  })
  it('retains a missing proxy ID, never substitutes direct access, and allows disabling and clearing', async () => {
    api.getHarvestProxyOptions.mockResolvedValue([])
    const initial = snapshot()
    initial.settings.enabled = true
    api.getCodexTicketSettings.mockResolvedValue(initial)
    const wrapper = await render()
    expect((wrapper.get('[data-testid="ticket-proxy"]').element as HTMLSelectElement).value).toBe('7')
    expect(wrapper.text()).toContain('codexTicket.unavailableProxy')
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="ticket-enabled"]').setValue(false)
    const select = wrapper.get('[data-testid="ticket-proxy"]')
    ;(select.element as HTMLSelectElement).selectedIndex = 0
    await select.trigger('change')
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    await flushPromises()
    expect(api.saveCodexTicketSettings.mock.calls[0][0]).toMatchObject({ enabled: false, harvest_proxy_id: null })
  })
  it('keeps proxy-list failure separate from an empty list and configuration load', async () => {
    api.getHarvestProxyOptions.mockRejectedValue(new Error('proxy-list-unavailable'))
    const wrapper = await render()
    expect(wrapper.text()).toContain('proxy-list-unavailable')
    expect(wrapper.text()).toContain('codexTicket.unverifiedProxy')
    expect(wrapper.get('[data-testid="ticket-proxy"]').attributes('disabled')).toBeDefined()
    expect((wrapper.get('[data-testid="ticket-models"]').element as HTMLTextAreaElement).value).toBe('custom-model')
  })
  it('preserves the draft on runtime revision conflict and on 409 writes', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="ticket-target_length"]').setValue(356)
    api.saveCodexTicketSettings.mockRejectedValue({ status: 409, reason: 'CODEX_TICKET_REVISION_CONFLICT', message: 'revision-conflict' })
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('codexTicket.conflict')
    expect((wrapper.get('[data-testid="ticket-target_length"]').element as HTMLInputElement).value).toBe('356')
    api.getCodexTicketStatus.mockResolvedValue(runtime('3'))
    await vi.advanceTimersByTimeAsync(5000)
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeDefined()
    expect(api.saveCodexTicketSettings).toHaveBeenCalledTimes(1)
  })
  it('allows validation correction but requires explicit reconciliation for uncertain writes', async () => {
    const wrapper = await render()
    api.saveCodexTicketSettings.mockRejectedValueOnce({ status: 422, message: 'invalid-model' })
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeUndefined()
    api.saveCodexTicketSettings.mockRejectedValueOnce(new Error('timeout'))
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    expect(wrapper.text()).toContain('timeout')
    expect(wrapper.text()).toContain('codexTicket.reconcile')
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeDefined()
    expect(api.saveCodexTicketSettings).toHaveBeenCalledTimes(2)
    await wrapper.get('[data-testid="reload-ticket"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('timeout')
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeUndefined()
  })
  it('reports DB save success with unavailable runtime and does not fake an applied revision', async () => {
    api.saveCodexTicketSettings.mockResolvedValue({ ...snapshot('2'), runtime: null, runtime_error_reason: 'CODEX_TICKET_CONTROL_UNAVAILABLE' })
    const wrapper = await render()
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="save-notice"]').text()).toBe('codexTicket.savedUnknown')
    expect(wrapper.get('[data-testid="runtime-state"]').text()).toBe('codexTicket.runtimeUnknown')
    expect(wrapper.text()).not.toContain('codexTicket.revisions')
  })
  it('treats an older Redis revision as pending publication, not a conflicting draft', async () => {
    api.saveCodexTicketSettings.mockResolvedValue({ ...snapshot('9007199254740994'), runtime: runtime('9007199254740993') })
    const wrapper = await render()
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="save-notice"]').text()).toBe('codexTicket.savedPending')
    expect(wrapper.get('[data-testid="runtime-state"]').text()).toBe('codexTicket.awaitingApply')
    expect(wrapper.text()).not.toContain('codexTicket.conflict')
    api.getCodexTicketStatus.mockResolvedValue(runtime('9007199254740995'))
    await vi.advanceTimersByTimeAsync(5000)
    expect(wrapper.text()).toContain('codexTicket.conflict')
    api.getCodexTicketStatus.mockRejectedValueOnce({ status: 503 })
    await vi.advanceTimersByTimeAsync(5000)
    expect(wrapper.text()).toContain('codexTicket.conflict')
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeDefined()
  })
  it('prevents Enter in an input from submitting the parent settings form', async () => {
    const wrapper = await render()
    const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    wrapper.get('[data-testid="ticket-target_length"]').element.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    expect(api.saveCodexTicketSettings).not.toHaveBeenCalled()
  })
  it('ignores a stale status GET after a PUT even when cancellation is ignored', async () => {
    const wrapper = await render()
    const pending = deferred<TicketRuntimeStatus>()
    api.getCodexTicketStatus.mockReturnValueOnce(pending.promise)
    await vi.advanceTimersByTimeAsync(5000)
    const signal = api.getCodexTicketStatus.mock.calls[0][0] as AbortSignal
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    pending.resolve(runtime('0'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('codexTicket.conflict')
    expect(wrapper.get('[data-testid="save-notice"]').text()).toBe('codexTicket.savedPublished')
  })
  it('locks fields during explicit reload and aborts reads while hidden, preserving the draft', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="ticket-target_length"]').setValue(356)
    const pending = deferred<TicketSettingsSnapshot>()
    api.getCodexTicketSettings.mockReturnValueOnce(pending.promise)
    await wrapper.get('[data-testid="reload-ticket"]').trigger('click')
    expect(wrapper.get('fieldset').attributes('disabled')).toBeDefined()
    const signal = api.getCodexTicketSettings.mock.calls[1][0] as AbortSignal
    await wrapper.setProps({ active: false })
    expect(signal.aborted).toBe(true)
    pending.resolve(snapshot('0'))
    await flushPromises()
    expect((wrapper.get('[data-testid="ticket-target_length"]').element as HTMLInputElement).value).toBe('356')
    await vi.advanceTimersByTimeAsync(15000)
    expect(api.getCodexTicketStatus).not.toHaveBeenCalled()
    await wrapper.setProps({ active: true })
    await flushPromises()
    expect((wrapper.get('[data-testid="ticket-target_length"]').element as HTMLInputElement).value).toBe('356')
  })
  it('does not load inactive cards and aborts pending reads and writes on unmount', async () => {
    const wrapper = await render(false)
    expect(api.getCodexTicketSettings).not.toHaveBeenCalled()
    await wrapper.setProps({ active: true })
    await flushPromises()
    api.saveCodexTicketSettings.mockReturnValueOnce(new Promise(() => {}))
    await wrapper.get('[data-testid="save-ticket"]').trigger('click')
    const signal = api.saveCodexTicketSettings.mock.calls[0][2] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })
  it('only tests the selected proxy after a click and cancels on tab hide', async () => {
    const wrapper = await render()
    expect(api.testHarvestProxy).not.toHaveBeenCalled()
    api.testHarvestProxy.mockReturnValueOnce(new Promise(() => {}))
    await wrapper.get('[data-testid="test-proxy"]').trigger('click')
    expect(api.testHarvestProxy).toHaveBeenCalledWith(7, expect.any(AbortSignal))
    const signal = api.testHarvestProxy.mock.calls[0][1] as AbortSignal
    await wrapper.setProps({ active: false })
    expect(signal.aborted).toBe(true)
  })
  it('validates numeric ranges, refresh ordering, and model count', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="ticket-ttl_seconds"]').setValue(59)
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="ticket-ttl_seconds"]').setValue(600)
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="ticket-refresh_before_seconds"]').setValue(0)
    await wrapper.get('[data-testid="ticket-models"]').setValue('')
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="ticket-models"]').setValue('custom')
    expect(wrapper.get('[data-testid="save-ticket"]').attributes('disabled')).toBeUndefined()
  })
  it('keeps disabled distinct from rejection and reports status read failures honestly', async () => {
    const wrapper = await render()
    expect(wrapper.get('[data-testid="runtime-state"]').text()).toBe('codexTicket.phases.disabled')
    api.getCodexTicketStatus.mockRejectedValue({ status: 503, message: 'redis-unavailable' })
    await vi.advanceTimersByTimeAsync(5000)
    expect(wrapper.get('[data-testid="runtime-state"]').text()).toBe('codexTicket.runtimeUnknown')
    expect(wrapper.text()).toContain('redis-unavailable')
    expect((wrapper.get('[data-testid="ticket-models"]').element as HTMLTextAreaElement).value).toBe('custom-model')
  })
})
