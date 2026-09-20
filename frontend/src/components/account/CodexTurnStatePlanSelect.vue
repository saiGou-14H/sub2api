<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CodexTurnStatePlan } from '@/utils/codexTurnState'
withDefaults(defineProps<{ modelValue: CodexTurnStatePlan | 'unchanged'; bulk?: boolean; id?: string }>(), { bulk: false, id: 'codex-turn-state-plan' })
const emit = defineEmits<{ 'update:modelValue': [value: CodexTurnStatePlan | 'unchanged'] }>()
const { t } = useI18n()
function change(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  if (['inherit', 'pro', 'team', 'unchanged'].includes(value)) emit('update:modelValue', value as CodexTurnStatePlan | 'unchanged')
}
</script>
<template>
  <div class="mt-3">
    <label :for="id" class="input-label">{{ t('codexTicket.plan.label') }}</label>
    <select :id="id" :value="modelValue" class="input" data-testid="codex-turn-state-plan" @change="change">
      <option v-if="bulk" value="unchanged">{{ t('codexTicket.unchanged') }}</option>
      <option value="inherit">{{ t('codexTicket.plan.inherit') }}</option>
      <option value="pro">{{ t('codexTicket.plan.pro') }}</option>
      <option value="team">{{ t('codexTicket.plan.team') }}</option>
    </select>
    <p class="input-hint">{{ t('codexTicket.plan.hint') }}</p>
    <p class="input-hint">{{ t('codexTicket.plan.requestsHint') }}</p>
  </div>
</template>
