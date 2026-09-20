import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import en from '@/i18n/locales/en/codexTicket'
import type { CodexAccountState } from '@/api/admin/codexTicket'
import Badge from '../CodexAccountStateBadge.vue'
import Dialog from '../CodexAccountStateDialog.vue'
vi.mock('@/api/admin/codexTicket', () => ({ getCodexAccountStates: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key.split('.').slice(1).reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], en) ?? key }) }))
const state = (id = 1): CodexAccountState => ({ account_id: id, plan: id === 1 ? 'pro' : 'team', target_length: id === 1 ? 292 : 332, enabled: true, supported: true, status: 'ready', models: [{ model: 'gpt-test', status: 'ready', captured_at: null, expires_at: null, refresh_at: null, next_attempt_at: null, last_error_code: null, injection_count: 3, last_injected_at: null, last_request_id: 'request-one', last_outcome: 'header_set', last_reason: null, verified: true, verified_at: null, actual_model: null, verification_model: null, invalidation_count: 0, last_invalidated_at: null, last_invalidation_reason: null }] })
const global = { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' }, Icon: true } }
describe('Codex account UI', () => {
  it.each(['model_mismatch', 'verification_failed', 'state_312'] as const)('localizes two-phase error %s', code => {
    const record = state()
    record.models[0].last_error_code = code
    const wrapper = mount(Dialog, { props: { account: { id: 1, name: 'A' }, state: record, loading: false, failed: false }, global })
    expect(wrapper.text()).toContain(en.runtimeErrors[code])
  })
  it('shows independent Pro/Team policies and two-phase metadata', async () => {
    const record = state(1)
    record.models[0].actual_model = 'actual-A'
    record.models[0].verification_model = 'verified-A'
    const wrapper = mount(Dialog, { props: { account: { id: 1, name: 'A' }, state: record, loading: false, failed: false }, global })
    expect(wrapper.text()).toContain('Pro (292)')
    expect(wrapper.text()).toContain('actual-A')
    expect(wrapper.text()).toContain('verified-A')
    expect(wrapper.get('[data-testid="codex-verification"]').text()).toBe(en.accounts.verified)
    await wrapper.setProps({ account: { id: 2, name: 'B' }, state: state(2) })
    expect(wrapper.text()).toContain('Team (332)')
    expect(wrapper.text()).not.toContain('actual-A')
  })
  it.each(['expired', 'invalidated'] as const)('does not derive current verification from history when %s', status => {
    const record = state()
    record.status = status
    Object.assign(record.models[0], { status, verified: true, verified_at: '2026-01-01T00:00:00Z', invalidation_count: 2, last_invalidated_at: '2026-01-02T00:00:00Z', last_invalidation_reason: 'state_312' })
    const wrapper = mount(Dialog, { props: { account: { id: 1, name: 'A' }, state: record, loading: false, failed: false }, global })
    expect(wrapper.get('[data-testid="codex-verification"]').text()).toBe(en.accounts.notVerified)
    expect(wrapper.text()).toContain(en.accounts.invalidations.state_312)
    expect(wrapper.text()).toContain(en.accounts.verified_at)
    expect(wrapper.html()).not.toContain('text-green-600')
  })
  it('requires verified true and never displays unknown invalidation text', () => {
    const record = state()
    Object.assign(record.models[0], { verified: false, verified_at: '2026-01-01T00:00:00Z', last_invalidation_reason: 'secret-state' })
    const wrapper = mount(Dialog, { props: { account: { id: 1, name: 'A' }, state: record, loading: false, failed: false }, global })
    expect(wrapper.get('[data-testid="codex-verification"]').text()).toBe(en.accounts.notVerified)
    expect(wrapper.text()).not.toContain('secret-state')
    expect(wrapper.text()).toContain(en.accounts.invalidations.unknown)
  })
  it('keeps two accounts separate and does not equate ready with header injection', () => {
    const one = mount(Badge, { props: { accountId: 1, state: state() }, global })
    const twoState = state(2)
    twoState.models[0].last_outcome = 'skipped'
    const two = mount(Badge, { props: { accountId: 2, state: twoState }, global })
    expect(one.text()).toContain('Header set')
    expect(two.text()).toContain('State available')
    expect(two.text()).not.toContain('Header set')
    const mismatched = mount(Badge, { props: { accountId: 2, state: state() }, global })
    expect(mismatched.text()).toContain('Unavailable')
    expect(mismatched.text()).not.toContain('Header set')
  })
  it('retains dated historical injection after the latest decision skips', () => {
    const record = state()
    const timestamp = '2026-01-01T01:02:03Z'
    record.models[0].last_outcome = 'skipped'
    record.models[0].last_injected_at = timestamp
    const wrapper = mount(Badge, { props: { accountId: 1, state: record }, global })
    expect(wrapper.text()).toContain('Historical header injection')
    expect(wrapper.text()).toContain(new Date(timestamp).toLocaleString())
    expect(wrapper.text()).not.toContain('Header set')
  })
  it.each(['ticket_ready', 'ticket_missing', 'control_unavailable', 'proxy_unavailable', 'compact', 'identity_changed'] as const)('localizes the closed reason %s', reason => {
    const record = state()
    record.models[0].last_reason = reason
    const wrapper = mount(Dialog, { props: { account: { id: 1, name: 'A' }, state: record, loading: false, failed: false }, global })
    expect(wrapper.text()).toContain(en.accounts.reasons[reason])
  })
  it('shows expired historical injections without green success', () => {
    const expired = state()
    expired.status = 'expired'
    const wrapper = mount(Badge, { props: { accountId: 1, state: expired }, global })
    expect(wrapper.text()).toContain('Expired')
    expect(wrapper.text()).toContain('Historical header injection')
    expect(wrapper.html()).not.toContain('text-green-600')
  })
  it.each(['disabled', 'unsupported', 'unavailable'] as const)('renders %s safely', status => {
    const record = state()
    record.status = status
    const wrapper = mount(Badge, { props: { accountId: 1, state: record }, global })
    expect(wrapper.text()).toContain(en.accounts.statuses[status])
    expect(wrapper.html()).not.toContain('text-green-600')
  })
  it('shows model metadata, null times, closed errors and copies request IDs', async () => {
    const record = state()
    record.models[0].last_error_code = 'secret-token-error'
    record.models[0].last_reason = 'secret-state-value'
    const copy = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: copy } })
    const wrapper = mount(Dialog, { props: { account: { id: 1, name: 'Account A' }, state: record, loading: false, failed: false }, global })
    expect(wrapper.text()).toContain('gpt-test')
    expect(wrapper.text()).toContain('Injections in shared cache window')
    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).toContain('Outbound header set')
    expect(wrapper.text()).toContain('Last header injection (history)')
    expect(wrapper.text()).not.toContain('secret-')
    await wrapper.get('[aria-label="Copy request ID"]').trigger('click')
    expect(copy).toHaveBeenCalledWith('request-one')
    await wrapper.setProps({ account: { id: 2, name: 'Account B' } })
    expect(wrapper.text()).not.toContain('gpt-test')
    expect(wrapper.text()).not.toContain('request-one')
    expect(wrapper.text()).toContain('No model status')
    await wrapper.setProps({ state: undefined, failed: true })
    expect(wrapper.text()).toContain('Status could not be read')
    await wrapper.get('[aria-label="Refresh status"]').trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
  })
})
