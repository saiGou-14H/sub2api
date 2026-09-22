<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { extractApiErrorMessage, extractApiErrorCode } from '@/utils/apiError'
import type { Proxy } from '@/types'
import {
  getCodexTicketSettings, getCodexTicketStatus, getHarvestProxyOptions,
  saveCodexTicketSettings, testHarvestProxy,
  type CodexTicketConfig, type TicketSettingsSnapshot, type TicketRuntimeStatus
} from '@/api/admin/codexTicket'

const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true })
const { t } = useI18n()
const saved = ref<TicketSettingsSnapshot | null>(null)
const draft = ref<CodexTicketConfig | null>(null)
const runtime = ref<TicketRuntimeStatus | null>(null)
const modelsText = ref('')
const revision = ref('')
const proxies = ref<Proxy[]>([])
const proxiesFresh = ref(false)
const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const readError = ref('')
const proxyError = ref('')
const runtimeError = ref('')
const saveError = ref('')
const testResult = ref('')
const notice = ref('')
const mustReconcile = ref(false)
const writeConflict = ref(false)
let alive = true
let generation = 0
let proxyGeneration = 0
let testGeneration = 0
let timer: ReturnType<typeof setTimeout> | undefined
let readAbort: AbortController | undefined
let proxyAbort: AbortController | undefined
let writeAbort: AbortController | undefined
let testAbort: AbortController | undefined

const models = computed(() => [...new Set(modelsText.value.split(/\r?\n/).map(s => s.trim()).filter(Boolean))])
const proxyOptions = computed(() => proxies.value.filter(p => p.status === 'active' &&
  ['http', 'https', 'socks5', 'socks5h'].includes(p.protocol) &&
  (p.expires_at == null || Date.parse(p.expires_at) > Date.now())))
const selectedAvailable = computed(() => proxyOptions.value.some(p => p.id === draft.value?.harvest_proxy_id))
const missingProxy = computed(() => draft.value?.harvest_proxy_id != null && !selectedAvailable.value)
// Revisions are decimal strings; never coerce them to unsafe JavaScript numbers.
// An older Redis revision after a successful DB commit is pending publication, not a CAS conflict.
const newerRevision = (candidate: string, base: string) => /^\d+$/.test(candidate) && /^\d+$/.test(base) && BigInt(candidate) > BigInt(base)
const conflict = computed(() => writeConflict.value || (!!runtime.value && newerRevision(runtime.value.desired_revision, revision.value)))
const payload = computed<CodexTicketConfig | null>(() => draft.value ? { ...draft.value, models: [...models.value] } : null)
const dirty = computed(() => {
  if (!saved.value || !payload.value) return false
  const { revision: _revision, ...config } = saved.value.settings
  return JSON.stringify({ ...config, cookie_pin_mode: config.cookie_pin_mode ?? 'optional' }) !== JSON.stringify(payload.value)
})
const integerIn = (n: number, min: number, max: number) => Number.isInteger(n) && n >= min && n <= max
const errors = computed(() => {
  const c = draft.value
  if (!c) return {}
  return {
    models: !models.value.length || models.value.length > 16 || models.value.some(m => m.length > 128),
    target_length: !integerIn(c.target_length, 64, 4096),
    ttl_seconds: !integerIn(c.ttl_seconds, 60, 3600),
    refresh_before_seconds: !integerIn(c.refresh_before_seconds, 0, c.ttl_seconds - 1),
    cookie_pin_mode: c.cookie_pin_mode !== 'required' && c.cookie_pin_mode !== 'optional',
    max_concurrency: !integerIn(c.max_concurrency, 1, 8),
    max_probes_per_minute: !integerIn(c.max_probes_per_minute, 1, 60),
    proxy: c.enabled && (!proxiesFresh.value || !selectedAvailable.value)
  }
})
const canSave = computed(() => !!draft.value && !saving.value && !loading.value && !conflict.value &&
  !mustReconcile.value && !Object.values(errors.value).some(Boolean))
const numericFields = [
  { key: 'target_length', min: 64, max: 4096 },
  { key: 'ttl_seconds', min: 60, max: 3600 },
  { key: 'refresh_before_seconds', min: 0, max: 3599 },
  { key: 'max_concurrency', min: 1, max: 8 },
  { key: 'max_probes_per_minute', min: 1, max: 60 }
] as const
const runtimeErrorCodes = [
  'model_mismatch', 'verification_failed', 'state_312', 'cookie_missing',
  'transport_unsupported', 'identity_unresolved', 'token_unavailable', 'stream_failed',
  'proxy_unavailable', 'authentication_failed', 'rate_limited', 'upstream_unavailable',
  'state_mismatch', 'random_unavailable', 'control_unavailable', 'lease_lost',
  'snapshot_unavailable', 'collector_unavailable', 'candidate_read_failed',
  'commit_unavailable', 'probe_failed', 'probe_timeout', 'retry_unavailable'
]
const runtimeErrorLabel = computed(() => {
  const code = runtime.value?.last_error_code
  if (!code) return ''
  // Only display fixed classifications. Unknown values may contain an upstream error body.
  return t(`codexTicket.runtimeErrors.${runtimeErrorCodes.includes(code) ? code : 'unknown'}`)
})
const counterScopeLabel = computed(() => t(runtime.value?.counter_scope === 'local_instance_observed'
  ? 'codexTicket.localCounterScope' : 'codexTicket.unknownCounterScope'))
const stateLabel = computed(() => {
  if (!runtime.value || runtimeError.value) return t('codexTicket.runtimeUnknown')
  if (runtime.value.applied_revision !== runtime.value.desired_revision ||
    (revision.value && runtime.value.desired_revision !== revision.value)) return t('codexTicket.awaitingApply')
  return t(`codexTicket.phases.${runtime.value.phase}`)
})
function adopt(value: TicketSettingsSnapshot) {
  const { revision: nextRevision, ...config } = value.settings
  saved.value = value
  revision.value = nextRevision
  draft.value = { ...config, cookie_pin_mode: config.cookie_pin_mode ?? 'optional', models: [...config.models] }
  modelsText.value = config.models.join('\n')
  runtime.value = value.runtime
  runtimeError.value = value.runtime_error_reason ? t('codexTicket.runtimeUnknown') : ''
}
function stopReads() {
  generation++
  proxyGeneration++
  clearTimeout(timer)
  readAbort?.abort()
  proxyAbort?.abort()
  loading.value = false
}
function scheduleStatus() {
  clearTimeout(timer)
  if (alive && props.active && !saving.value) timer = setTimeout(() => { void refreshStatus() }, 5000)
}
async function refreshStatus() {
  if (!alive || !props.active || saving.value || loading.value) return
  const run = ++generation
  readAbort?.abort()
  const controller = new AbortController()
  readAbort = controller
  try {
    const value = await getCodexTicketStatus(controller.signal)
    if (!alive || controller.signal.aborted || run !== generation) return
    runtime.value = value
    if (newerRevision(value.desired_revision, revision.value)) writeConflict.value = true
    runtimeError.value = ''
  } catch (error) {
    if (!alive || controller.signal.aborted || run !== generation) return
    runtime.value = null
    runtimeError.value = extractApiErrorMessage(error, t('codexTicket.runtimeUnknown'))
  } finally {
    if (alive && run === generation) scheduleStatus()
  }
}
async function refreshProxies() {
  const run = ++proxyGeneration
  proxyAbort?.abort()
  const controller = new AbortController()
  proxyAbort = controller
  try {
    const value = await getHarvestProxyOptions(controller.signal)
    if (!alive || controller.signal.aborted || run !== proxyGeneration) return
    proxies.value = value
    proxiesFresh.value = true
    proxyError.value = ''
  } catch (error) {
    if (!alive || controller.signal.aborted || run !== proxyGeneration) return
    proxiesFresh.value = false
    proxyError.value = extractApiErrorMessage(error, t('codexTicket.proxyLoadFailed'))
  }
}
async function reloadSaved() {
  if (!alive || !props.active || saving.value || loading.value) return
  stopReads()
  loading.value = true
  notice.value = ''
  const run = generation
  const controller = new AbortController()
  readAbort = controller
  void refreshProxies()
  try {
    const value = await getCodexTicketSettings(controller.signal)
    if (!alive || controller.signal.aborted || run !== generation) return
    adopt(value)
    readError.value = ''
    saveError.value = ''
    mustReconcile.value = false
    writeConflict.value = false
  } catch (error) {
    if (!alive || controller.signal.aborted || run !== generation) return
    readError.value = extractApiErrorMessage(error, t('codexTicket.loadFailed'))
  } finally {
    if (alive && run === generation) {
      loading.value = false
      scheduleStatus()
    }
  }
}
async function save() {
  if (!canSave.value || !payload.value) return
  stopReads()
  cancelTest()
  saving.value = true
  notice.value = ''
  saveError.value = ''
  const controller = new AbortController()
  writeAbort = controller
  try {
    const value = await saveCodexTicketSettings(payload.value, revision.value, controller.signal)
    if (!alive || controller.signal.aborted) return
    adopt(value)
    notice.value = !value.runtime ? t('codexTicket.savedUnknown')
      : value.runtime.applied_revision === value.settings.revision ? t('codexTicket.savedPublished') : t('codexTicket.savedPending')
  } catch (error) {
    if (!alive || controller.signal.aborted) return
    const status = error && typeof error === 'object' && 'status' in error ? error.status : undefined
    writeConflict.value = status === 409 || extractApiErrorCode(error) === 'CODEX_TICKET_REVISION_CONFLICT'
    // Validation failures are known not to have committed. Everything else needs explicit GET reconciliation.
    mustReconcile.value = writeConflict.value || (status !== 400 && status !== 422)
    saveError.value = extractApiErrorMessage(error, t('codexTicket.saveFailed'))
  } finally {
    if (alive) {
      saving.value = false
      scheduleStatus()
    }
  }
}
function cancelTest() {
  testGeneration++
  testAbort?.abort()
  testing.value = false
}
async function testSelected() {
  const id = draft.value?.harvest_proxy_id
  if (id == null || testing.value || !selectedAvailable.value || !proxiesFresh.value) return
  const run = ++testGeneration
  const controller = new AbortController()
  testAbort = controller
  testing.value = true
  testResult.value = ''
  try {
    const value = await testHarvestProxy(id, controller.signal)
    if (!alive || controller.signal.aborted || run !== testGeneration) return
    testResult.value = t(value.success ? 'codexTicket.testSucceeded' : 'codexTicket.testFailed', { id })
  } catch (error) {
    if (!alive || controller.signal.aborted || run !== testGeneration) return
    testResult.value = extractApiErrorMessage(error, t('codexTicket.testFailed', { id }))
  } finally {
    if (alive && run === testGeneration) testing.value = false
  }
}
function preventParentSubmit(event: KeyboardEvent) {
  if (event.key === 'Enter' && event.target instanceof HTMLInputElement) event.preventDefault()
}
watch(() => draft.value?.harvest_proxy_id, () => { cancelTest(); testResult.value = '' })
watch(() => props.active, active => {
  stopReads()
  cancelTest()
  if (!active) return
  if (!saved.value) void reloadSaved()
  else { void refreshProxies(); void refreshStatus() }
})
onMounted(() => { if (props.active) void reloadSaved() })
onUnmounted(() => { alive = false; stopReads(); cancelTest(); writeAbort?.abort() })
</script>

<template>
  <section class="card space-y-4 p-6" data-testid="codex-ticket-settings" @keydown="preventParentSubmit">
    <div>
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('codexTicket.title') }}</h2>
      <p class="input-hint">{{ t('codexTicket.description') }}</p>
      <p class="input-hint">{{ t('codexTicket.transportHint') }}</p>
    </div>
    <div class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800" aria-live="polite">
      <p data-testid="runtime-state">{{ stateLabel }}</p>
      <p v-if="runtime && !runtimeError" data-testid="runtime-counter-scope">{{ counterScopeLabel }}</p>
      <p v-if="runtime && !runtimeError">{{ t('codexTicket.runtimeCounts', { ready: runtime.ready_count, pending: runtime.pending_count, inflight: runtime.inflight_count }) }}</p>
      <p v-if="runtime">{{ t('codexTicket.revisions', { desired: runtime.desired_revision, applied: runtime.applied_revision ?? '—' }) }}</p>
      <p v-if="runtime?.proxy_state">{{ t('codexTicket.proxyState') }}: {{ runtime.proxy_state }}</p>
      <p v-if="runtimeErrorLabel" data-testid="runtime-error">{{ runtimeErrorLabel }}</p>
      <p v-if="runtime?.supported_transports">{{ t('codexTicket.supportedTransports') }}: {{ runtime.supported_transports.join(', ') || '—' }}</p>
      <p v-if="saved">{{ t('codexTicket.savedRevision', { revision }) }} · {{ t(dirty ? 'codexTicket.unsaved' : 'codexTicket.noChanges') }}</p>
    </div>
    <p v-for="(error, index) in [readError, proxyError, runtimeError, saveError].filter(Boolean)" :key="index" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="conflict" role="alert" class="text-sm text-amber-600">{{ t('codexTicket.conflict') }}</p>
    <p v-if="mustReconcile && !conflict" role="alert" class="text-sm text-amber-600">{{ t('codexTicket.reconcile') }}</p>
    <p v-if="saved?.selected_proxy" class="input-hint">{{ t('codexTicket.savedProxy', { id: saved.selected_proxy.id, name: saved.selected_proxy.name ?? '', state: saved.selected_proxy.state }) }}</p>
    <fieldset v-if="draft" :disabled="saving || loading" class="space-y-4">
      <label class="flex items-center gap-3">
        <input v-model="draft.enabled" data-testid="ticket-enabled" type="checkbox" class="h-4 w-4 rounded" />
        <span>{{ t('codexTicket.enabled') }}</span>
      </label>
      <label class="block">
        <span class="input-label">{{ t('codexTicket.proxy') }}</span>
        <select v-model="draft.harvest_proxy_id" data-testid="ticket-proxy" class="input" :disabled="!proxiesFresh">
          <option :value="null">{{ t('codexTicket.noProxy') }}</option>
          <option v-if="missingProxy" :value="draft.harvest_proxy_id">#{{ draft.harvest_proxy_id }} — {{ t(proxiesFresh ? 'codexTicket.unavailableProxy' : 'codexTicket.unverifiedProxy') }}</option>
          <option v-for="proxy in proxyOptions" :key="proxy.id" :value="proxy.id">#{{ proxy.id }} {{ proxy.name }} ({{ proxy.protocol }})</option>
        </select>
        <span v-if="errors.proxy" role="alert" class="text-sm text-red-600">{{ t('codexTicket.validation.proxy') }}</span>
      </label>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn btn-secondary" data-testid="refresh-proxies" @click="refreshProxies">{{ t('codexTicket.refreshProxies') }}</button>
        <button type="button" class="btn btn-secondary" data-testid="test-proxy" :disabled="testing || !selectedAvailable || !proxiesFresh" @click="testSelected">{{ t('codexTicket.testProxy') }}</button>
        <button v-if="testing" type="button" class="btn btn-secondary" @click="cancelTest">{{ t('codexTicket.cancelTest') }}</button>
      </div>
      <p class="input-hint">{{ t('codexTicket.testHint') }}</p>
      <p v-if="testResult" role="status">{{ testResult }}</p>
      <details data-testid="ticket-advanced">
        <summary class="cursor-pointer font-medium">{{ t('codexTicket.advanced') }}</summary>
        <div class="mt-4 grid gap-4 sm:grid-cols-2">
          <label class="block sm:col-span-2">
            <span class="input-label">{{ t('codexTicket.models') }}</span>
            <textarea v-model="modelsText" data-testid="ticket-models" class="input" rows="3" />
            <span v-if="errors.models" role="alert" class="text-sm text-red-600">{{ t('codexTicket.validation.models') }}</span>
          </label>
          <label v-for="field in numericFields" :key="field.key" class="block">
            <span class="input-label">{{ t(`codexTicket.${field.key}`) }}</span>
            <input v-model.number="draft[field.key]" :data-testid="`ticket-${field.key}`" class="input" type="number" :min="field.min" :max="field.key === 'refresh_before_seconds' ? draft.ttl_seconds - 1 : field.max" step="1" />
            <span v-if="errors[field.key]" role="alert" class="text-sm text-red-600">{{ t(`codexTicket.validation.${field.key}`) }}</span>
          </label>
          <label class="block">
            <span class="input-label">{{ t('codexTicket.missingPolicy') }}</span>
            <select v-model="draft.missing_ticket_policy" data-testid="ticket-policy" class="input">
              <option value="passthrough">{{ t('codexTicket.passthrough') }}</option>
              <option value="reject">{{ t('codexTicket.reject') }}</option>
            </select>
          </label>
          <label class="block sm:col-span-2">
            <span class="input-label">{{ t('codexTicket.cookiePinMode') }}</span>
            <select v-model="draft.cookie_pin_mode" data-testid="ticket-cookie-pin-mode" class="input">
              <option value="required">{{ t('codexTicket.cookiePinRequired') }}</option>
              <option value="optional">{{ t('codexTicket.cookiePinOptional') }}</option>
            </select>
            <span v-if="errors.cookie_pin_mode" role="alert" class="text-sm text-red-600">{{ t('codexTicket.validation.cookie_pin_mode') }}</span>
            <p class="input-hint">{{ t('codexTicket.cookiePinHint') }}</p>
          </label>
          <p class="input-hint sm:col-span-2">{{ t('codexTicket.advancedHint') }}</p>
        </div>
      </details>
      <p v-if="Object.values(errors).some(Boolean)" role="alert" class="text-sm text-red-600">{{ t('codexTicket.invalid') }}</p>
      <button type="button" data-testid="save-ticket" class="btn btn-primary" :disabled="!canSave" @click="save">{{ t(saving ? 'codexTicket.saving' : 'codexTicket.save') }}</button>
    </fieldset>
    <button type="button" data-testid="reload-ticket" class="btn btn-secondary" :disabled="saving || loading" @click="reloadSaved">{{ t(loading ? 'codexTicket.loading' : 'codexTicket.reload') }}</button>
    <p v-if="notice && !conflict" role="status" data-testid="save-notice" class="text-sm text-green-600">{{ notice }}</p>
  </section>
</template>
