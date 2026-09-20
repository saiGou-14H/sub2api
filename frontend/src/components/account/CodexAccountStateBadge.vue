<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CodexAccountState } from '@/api/admin/codexTicket'
import { safeCodexStatus } from '@/composables/useCodexAccountStates'
const props = defineProps<{ accountId: number; state?: CodexAccountState }>()
defineEmits<{ open: [] }>()
const { t } = useI18n()
const own = computed(() => props.state?.account_id === props.accountId ? props.state : undefined)
const status = computed(() => safeCodexStatus(own.value?.status ?? 'unavailable'))
const injected = computed(() => own.value?.models.some(model => model.last_outcome === 'header_set'))
const lastInjection = computed(() => {
  const times = (own.value?.models ?? []).filter(model => model.injection_count > 0 && model.last_injected_at)
    .map(model => Date.parse(model.last_injected_at!)).filter(Number.isFinite)
  return times.length ? new Date(Math.max(...times)).toLocaleString() : ''
})
</script>

<template>
  <button type="button" class="mt-1 flex max-w-full flex-wrap items-center gap-x-2 text-left text-xs text-gray-500 hover:underline dark:text-gray-400" :title="t('codexTicket.accounts.headerHint')" @click.stop="$emit('open')">
    <span :class="status === 'ready' ? 'text-green-600 dark:text-green-400' : ''">Codex: {{ t(`codexTicket.accounts.statuses.${status}`) }}</span>
    <span v-if="injected || lastInjection" class="text-gray-500 dark:text-gray-400">{{ t(status === 'ready' && injected ? 'codexTicket.accounts.injected' : 'codexTicket.accounts.history') }}<span v-if="lastInjection"> · {{ lastInjection }}</span></span>
  </button>
</template>
