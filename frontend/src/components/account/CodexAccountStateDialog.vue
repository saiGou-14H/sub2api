<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Icon from '@/components/icons/Icon.vue'
import type { CodexAccountState } from '@/api/admin/codexTicket'
import { safeCodexStatus } from '@/composables/useCodexAccountStates'
const props = defineProps<{ account: { id: number; name: string } | null; state?: CodexAccountState; loading: boolean; failed: boolean }>()
defineEmits<{ close: []; refresh: [] }>()
const { t } = useI18n()
const copied = ref('')
const own = computed(() => props.account && props.state?.account_id === props.account.id ? props.state : undefined)
const canShowModelVerification = computed(() => own.value?.enabled === true && own.value.supported === true &&
  !['unavailable', 'unsupported', 'disabled', 'inactive', 'proxy_unavailable'].includes(safeCodexStatus(own.value.status)))
const dates = ['captured_at', 'expires_at', 'refresh_at', 'next_attempt_at', 'verified_at', 'last_injected_at', 'last_invalidated_at'] as const
const errors = ['model_mismatch', 'verification_failed', 'state_312', 'transport_unsupported', 'identity_unresolved', 'token_unavailable', 'stream_failed', 'proxy_unavailable', 'authentication_failed', 'rate_limited', 'upstream_unavailable', 'state_mismatch', 'random_unavailable', 'control_unavailable', 'lease_lost', 'snapshot_unavailable', 'collector_unavailable', 'candidate_read_failed', 'commit_unavailable', 'probe_failed', 'probe_timeout', 'retry_unavailable']
const reasons = ['ticket_ready', 'ticket_missing', 'control_unavailable', 'proxy_unavailable', 'compact', 'identity_changed']
const reasonLabel = (code: string) => t(`codexTicket.accounts.reasons.${reasons.includes(code) ? code : 'unknown'}`)
const errorLabel = (code: string) => t(`codexTicket.runtimeErrors.${errors.includes(code) ? code : 'unknown'}`)
const planLabel = computed(() => t(`codexTicket.plan.${['inherit', 'pro', 'team'].includes(own.value?.plan ?? '') ? own.value!.plan : 'unknown'}`))
const invalidationLabel = (reason: string | null) => reason ? t(`codexTicket.accounts.invalidations.${['model_mismatch', 'state_312'].includes(reason) ? reason : 'unknown'}`) : '-'
const outcomeLabel = (outcome: string | null) => outcome ? t(`codexTicket.accounts.outcomes.${['header_set', 'skipped', 'rejected'].includes(outcome) ? outcome : 'unknown'}`) : '-'
const dateLabel = (value: string | null) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : '-'
watch(() => props.account?.id, () => { copied.value = '' })
async function copy(value: string) {
  const id = props.account?.id
  try {
    await navigator.clipboard.writeText(value)
    if (props.account?.id === id) copied.value = t('codexTicket.accounts.copied')
  } catch { if (props.account?.id === id) copied.value = t('codexTicket.accounts.copyFailed') }
}
</script>

<template>
  <BaseDialog :show="!!account" :title="t('codexTicket.accounts.title')" width="wide" @close="$emit('close')">
    <h4 class="mb-4 min-w-0 break-all text-base font-semibold text-gray-900 dark:text-gray-100">{{ account?.name }} <span class="text-gray-500">#{{ account?.id }}</span></h4>
    <div class="flex items-center justify-between gap-3">
      <span class="text-sm" :class="own?.status === 'ready' ? 'text-green-600 dark:text-green-400' : 'text-gray-500'">{{ t(`codexTicket.accounts.statuses.${safeCodexStatus(own?.status ?? 'unavailable')}`) }}</span>
      <button class="btn btn-secondary p-2" type="button" :disabled="loading" :title="t('codexTicket.accounts.refresh')" :aria-label="t('codexTicket.accounts.refresh')" @click="$emit('refresh')"><Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" /></button>
    </div>
    <div v-if="own" class="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-sm text-gray-500">
      <span>{{ t('codexTicket.plan.label') }}: {{ planLabel }}</span>
      <span>{{ t('codexTicket.plan.targetLength') }}: {{ own.target_length > 0 ? own.target_length : '-' }}</span>
    </div>
    <p class="mt-2 text-xs text-gray-500">{{ t('codexTicket.plan.requestsHint') }}</p>
    <p v-if="failed" role="alert" class="mt-3 text-sm text-red-600">{{ t('codexTicket.accounts.failed') }}</p>
    <p v-if="!own?.models.length" class="py-6 text-sm text-gray-500">{{ t(loading ? 'codexTicket.accounts.loading' : 'codexTicket.accounts.empty') }}</p>
    <section v-for="(model, index) in own?.models ?? []" :key="`${model.model}-${index}`" class="mt-4 border-t border-gray-200 pt-4 dark:border-dark-600">
      <div class="flex flex-wrap justify-between gap-2 text-sm font-medium"><span class="break-all">{{ model.model }}</span><span :class="model.status === 'ready' && own?.status === 'ready' ? 'text-green-600 dark:text-green-400' : 'text-gray-500'">{{ t(`codexTicket.accounts.statuses.${safeCodexStatus(model.status)}`) }}</span></div>
      <p class="mt-2 text-xs text-gray-500" data-testid="codex-verification">{{ t(model.verified === true && ['ready', 'refreshing'].includes(model.status) && canShowModelVerification ? 'codexTicket.accounts.verified' : 'codexTicket.accounts.notVerified') }}</p>
      <dl class="mt-3 grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
        <div><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.actual_model') }}</dt><dd class="break-all">{{ model.actual_model || '-' }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.verification_model') }}</dt><dd class="break-all">{{ model.verification_model || '-' }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.invalidation_count') }}</dt><dd>{{ Number.isFinite(model.invalidation_count) ? model.invalidation_count : '-' }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.last_invalidation_reason') }}</dt><dd>{{ invalidationLabel(model.last_invalidation_reason) }}</dd></div>
        <div v-for="field in dates" :key="field"><dt class="text-xs text-gray-500">{{ t(`codexTicket.accounts.${field}`) }}</dt><dd>{{ dateLabel(model[field]) }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.injection_count') }}</dt><dd>{{ Number.isFinite(model.injection_count) ? model.injection_count : '-' }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.decision') }}</dt><dd>{{ outcomeLabel(model.last_outcome) }}</dd></div>
        <div v-if="model.last_reason"><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.reason') }}</dt><dd>{{ reasonLabel(model.last_reason) }}</dd></div>
        <div v-if="model.last_error_code" class="sm:col-span-2"><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.error') }}</dt><dd>{{ errorLabel(model.last_error_code) }}</dd></div>
        <div class="sm:col-span-2"><dt class="text-xs text-gray-500">{{ t('codexTicket.accounts.request') }}</dt><dd class="flex items-start gap-2"><span class="min-w-0 break-all font-mono text-xs">{{ model.last_request_id || '-' }}</span><button v-if="model.last_request_id" type="button" class="shrink-0" :title="t('codexTicket.accounts.copy')" :aria-label="t('codexTicket.accounts.copy')" @click="copy(model.last_request_id)"><Icon name="copy" size="sm" /></button></dd></div>
      </dl>
    </section>
    <p role="status" class="mt-2 text-xs text-gray-500">{{ copied }}</p>
    <template #footer><span class="inline-flex items-center gap-1 text-xs text-gray-500">{{ t('codexTicket.accounts.injected') }}<HelpTooltip :content="`${t('codexTicket.accounts.headerHint')} ${t('codexTicket.plan.verificationHint')}`" /></span></template>
  </BaseDialog>
</template>
