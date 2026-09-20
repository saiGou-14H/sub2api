import { computed, onScopeDispose, ref, watch, type Ref } from 'vue'
import { getCodexAccountStates, type CodexAccountState, type CodexAccountStatus } from '@/api/admin/codexTicket'

export const codexAccountStatuses: CodexAccountStatus[] = ['disabled', 'unsupported', 'inactive', 'waiting', 'ready', 'refreshing', 'expired', 'backoff', 'proxy_unavailable', 'unavailable']
export const safeCodexStatus = (status: string): CodexAccountStatus => codexAccountStatuses.includes(status as CodexAccountStatus) ? status as CodexAccountStatus : 'unavailable'
export { supportsCodexTurnState as supportsCodexState } from '@/utils/codexTurnState'
const unavailable = (id: number): CodexAccountState => ({ account_id: id, enabled: false, supported: true, status: 'unavailable', models: [] })

/** One cancellable scheduler for the visible page; no account owns a timer or shared fallback. */
export function useCodexAccountStates(ids: Ref<number[]>, queryKey?: Ref<string>) {
  const records = ref<Record<number, CodexAccountState>>({})
  const loading = ref(false)
  const failed = ref(false)
  const now = ref(0)
  let clockBase = 0
  let receivedAt = 0
  let generation = 0
  let alive = true
  let controller: AbortController | undefined
  let pollTimer: ReturnType<typeof setTimeout> | undefined
  let expiryTimer: ReturnType<typeof setTimeout> | undefined
  let expiryRead = false
  const visibleIds = computed(() => [...new Set(ids.value)].filter(id => Number.isSafeInteger(id) && id > 0))
  const tick = () => { now.value = clockBase + Math.max(0, performance.now() - receivedAt) }
  const states = computed(() => {
    const result: Record<number, CodexAccountState> = {}
    for (const id of visibleIds.value) {
      const record = records.value[id]
      if (!record) continue
      const models = record.models.map(model => ({ ...model, status:
        ['ready', 'refreshing'].includes(model.status) && model.expires_at && Date.parse(model.expires_at) <= now.value
          ? 'expired' as const : safeCodexStatus(model.status) }))
      const expired = models.some(model => model.status === 'expired')
      result[id] = { ...record, models, status: ['ready', 'refreshing'].includes(record.status) && expired ? 'expired' : safeCodexStatus(record.status) }
    }
    return result
  })
  function cancel() {
    generation++
    controller?.abort()
    controller = undefined
    clearTimeout(pollTimer)
    clearTimeout(expiryTimer)
    loading.value = false
  }
  function scheduleExpiry() {
    clearTimeout(expiryTimer)
    tick()
    const future = Object.values(records.value).flatMap(record => record.models)
      .filter(model => ['ready', 'refreshing'].includes(model.status) && model.expires_at)
      .map(model => Date.parse(model.expires_at!)).filter(time => time > now.value)
    if (!future.length) return
    expiryTimer = setTimeout(() => {
      tick()
      if (!expiryRead && !loading.value) { expiryRead = true; void refresh(false) }
      else scheduleExpiry()
    }, Math.min(2147483647, Math.max(1, Math.min(...future) - now.value)))
  }
  async function refresh(manual = true) {
    if (!alive || document.hidden || !visibleIds.value.length || loading.value) return
    if (manual) expiryRead = false
    clearTimeout(pollTimer)
    const version = ++generation
    const requestIds = [...visibleIds.value]
    const abort = new AbortController()
    controller = abort
    loading.value = true
    try {
      const next: Record<number, CodexAccountState> = {}
      for (let start = 0; start < requestIds.length; start += 100) {
        const chunk = requestIds.slice(start, start + 100)
        const snapshot = await getCodexAccountStates(chunk, abort.signal)
        if (!alive || version !== generation || abort.signal.aborted) return
        const serverTime = Date.parse(snapshot.server_time)
        if (!Number.isFinite(serverTime) || !Array.isArray(snapshot.accounts)) throw new Error('Invalid snapshot')
        clockBase = serverTime
        receivedAt = performance.now()
        for (const id of chunk) {
          const matches = snapshot.accounts.filter(account => account.account_id === id)
          const account = matches.length === 1 ? matches[0] : undefined
          next[id] = account && Array.isArray(account.models) ? { ...account,
            status: ['ready', 'refreshing'].includes(account.status)
              ? !account.supported ? 'unsupported' : !snapshot.global_enabled || !account.enabled ? 'disabled' : safeCodexStatus(account.status)
              : safeCodexStatus(account.status),
            models: account.models.map(model => ({ ...model, status: safeCodexStatus(model.status) }))
          } : unavailable(id)
        }
      }
      records.value = next
      failed.value = false
      scheduleExpiry()
    } catch {
      if (version !== generation || abort.signal.aborted) return
      records.value = Object.fromEntries(requestIds.map(id => [id, unavailable(id)]))
      failed.value = true
      clearTimeout(expiryTimer)
    } finally {
      if (alive && version === generation) {
        loading.value = false
        controller = undefined
        pollTimer = setTimeout(() => { expiryRead = false; void refresh(false) }, 5000)
      }
    }
  }
  const restart = () => {
    cancel()
    records.value = {}
    failed.value = false
    expiryRead = false
    void refresh()
  }
  watch(() => `${queryKey?.value ?? ''}:${visibleIds.value.join(',')}`, restart, { immediate: true, flush: 'sync' })
  const visibility = () => { if (document.hidden) cancel(); else { tick(); restart() } }
  document.addEventListener('visibilitychange', visibility)
  onScopeDispose(() => {
    alive = false
    cancel()
    document.removeEventListener('visibilitychange', visibility)
  })
  return { states, loading, failed, refresh }
}
