<template>
  <div data-testid="cline-usage-cell" class="space-y-1 text-xs" :title="t('clineAccount.savedUsage')">
    <span v-if="mode !== 'pass'" class="text-gray-500">{{ t('clineMetadata.passOnly') }}</span>
    <template v-else>
      <div v-for="name in windows" :key="name" class="flex items-center gap-2" :data-testid="`cline-usage-${name}`">
        <span class="text-gray-500">{{ t(`clineMetadata.${name}`) }}</span>
        <span class="tabular-nums">{{ percent(name) }}</span>
      </div>
      <p v-if="metadata?.cooldowns?.length" data-testid="cline-usage-cooldown" class="text-amber-600 dark:text-amber-400">{{ t('clineAccount.usagePending') }}</p>
      <p v-if="metadata?.last_success_at" class="text-[10px] text-gray-500">{{ t('clineAccount.usageObserved') }}: {{ metadata.last_success_at }}</p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import { clineMode } from './clineAccountForm'

const props = defineProps<{ account: Account }>()
const { t } = useI18n()
const windows = ['five_hour', 'weekly', 'monthly'] as const
const mode = computed(() => clineMode(props.account.credentials?.account_mode))
const metadata = computed(() => props.account.cline_usage)
const now = ref(Date.now())
// Only age a saved projection. No per-row provider or metadata API requests.
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { timer = setInterval(() => { now.value = Date.now() }, 30000) })
onBeforeUnmount(() => { if (timer !== undefined) clearInterval(timer) })
function percent(name: string): string {
  const state = metadata.value
  const last = Date.parse(state?.last_success_at ?? '')
  const window = state?.windows?.find(w => w.type === name)
  const value = window?.percent_used
  const reset = window?.resets_at ? Date.parse(window.resets_at) : undefined
  if (!state?.persisted || !['ok', 'partial'].includes(state.quota_status) || !Number.isFinite(last) || last > now.value || now.value - last >= 300000 || (reset !== undefined && (!Number.isFinite(reset) || reset <= now.value)) || typeof value !== 'number' || !Number.isFinite(value) || value < 0 || value > 100) return t('clineAccount.usageUnknown')
  return `${value.toFixed(1)}%`
}
</script>
