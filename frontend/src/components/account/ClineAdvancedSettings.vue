<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import { adminAPI } from '@/api/admin'
import * as accountsAPI from '@/api/admin/accounts'
import { refreshClineMetadata } from '@/api/admin/clineMetadata'
import QuotaLimitCard from './QuotaLimitCard.vue'
import { CLINE_QUOTA_DIMS, type ClineAdvancedDraft } from './clineAdvancedSettings'

const props = defineProps<{ account?: Account | null; canOperate: boolean }>()
const draft = defineModel<ClineAdvancedDraft>({ required: true })
const emit = defineEmits<{ operated: []; busy: [value: boolean] }>()
const { t } = useI18n()
const globalNotify = ref(false)
let disposed = false
onMounted(async () => {
  try {
    const capabilities = await adminAPI.accounts.getManagementCapabilities()
    if (!disposed) globalNotify.value = capabilities.account_quota_notify_enabled === true
  } catch { /* Unknown global capability is not permission to send notifications. */ }
})
onBeforeUnmount(() => { disposed = true })
const action = ref<'quota' | 'pause' | 'recheck' | null>(null)
const running = ref(false)
const failed = ref(false)
async function operate() {
  if (!props.account || !props.canOperate || running.value || !action.value) return
  running.value = true; failed.value = false; emit('busy', true)
  let succeeded = false
  try {
    if (action.value === 'quota') await accountsAPI.resetAccountQuota(props.account.id)
    else if (action.value === 'pause') await accountsAPI.resetTempUnschedulable(props.account.id)
    else await refreshClineMetadata(props.account.id)
    if (!disposed) { action.value = null; succeeded = true }
  } catch { if (!disposed) failed.value = true }
  finally { running.value = false; emit('busy', false) }
  if (succeeded && !disposed) emit('operated')
}
function addRule() {
  draft.value.rules.push({ error_code: 503, keywords: '', duration_minutes: 5, description: '' })
}
function moveRule(index: number, step: number) {
  const next = index + step
  if (next < 0 || next >= draft.value.rules.length) return
  const [rule] = draft.value.rules.splice(index, 1)
  draft.value.rules.splice(next, 0, rule)
}
</script>

<template>
  <details class="rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="cline-advanced">
    <summary class="cursor-pointer font-medium">{{ t('clineAccount.advanced.title') }}</summary>
    <div class="mt-4 space-y-5">
      <p class="text-sm text-amber-700 dark:text-amber-300" data-testid="cline-local-quota-notice">{{ t('clineAccount.advanced.localHint') }}</p>
      <QuotaLimitCard
        v-model:total-limit="draft.totalLimit" v-model:daily-limit="draft.dailyLimit" v-model:weekly-limit="draft.weeklyLimit"
        v-model:daily-reset-mode="draft.dailyResetMode" v-model:daily-reset-hour="draft.dailyResetHour"
        v-model:weekly-reset-mode="draft.weeklyResetMode" v-model:weekly-reset-day="draft.weeklyResetDay" v-model:weekly-reset-hour="draft.weeklyResetHour"
        v-model:reset-timezone="draft.resetTimezone" :quota-notify-global-enabled="false"
      />
      <!-- Preserve a valid saved IANA zone even when it is not a preset in the shared card. -->
      <label v-if="draft.dailyResetMode === 'fixed' || draft.weeklyResetMode === 'fixed'" class="block text-sm">
        {{ t('admin.accounts.quotaResetTimezone') }}
        <input v-model="draft.resetTimezone" data-testid="cline-quota-timezone" class="input mt-1 w-full" placeholder="UTC" maxlength="100" />
      </label>
      <section class="space-y-3" data-testid="cline-quota-notifications">
        <h4 class="font-medium">{{ t('clineAccount.advanced.notifications') }}</h4>
        <p v-if="!globalNotify" class="text-xs text-amber-700 dark:text-amber-300">{{ t('clineAccount.advanced.notificationGate') }}</p>
        <div v-for="dim in CLINE_QUOTA_DIMS" :key="dim" class="flex flex-wrap items-center gap-3" :data-testid="'cline-notify-' + dim">
          <label class="flex items-center gap-2 text-sm"><input v-model="draft.notify[dim].enabled" type="checkbox" />{{ t('clineAccount.advanced.' + dim) }}</label>
          <template v-if="draft.notify[dim].enabled">
            <input v-model.number="draft.notify[dim].threshold" type="number" min="0" :max="draft.notify[dim].thresholdType === 'percentage' ? 100 : 1000000000000" step="any" class="input w-32" :aria-label="t('clineAccount.advanced.threshold')" :placeholder="t('clineAccount.advanced.globalThreshold')" />
            <select v-model="draft.notify[dim].thresholdType" class="input w-24" :aria-label="t('clineAccount.advanced.thresholdType')"><option :value="null">{{ t('clineAccount.advanced.defaultThresholdType') }}</option><option value="fixed">$</option><option value="percentage">%</option></select>
          </template>
        </div>
      </section>
      <section class="space-y-2">
        <p class="rounded bg-slate-100 p-3 text-xs dark:bg-dark-700">{{ t('clineAccount.advanced.protectedHint') }}</p>
        <label class="flex items-center gap-2 text-sm font-medium"><input v-model="draft.customErrorEnabled" type="checkbox" data-testid="cline-custom-errors" />{{ t('clineAccount.advanced.customErrors') }}</label>
        <p class="text-xs text-gray-500">{{ t('clineAccount.advanced.customErrorsHint') }}</p>
        <input v-model="draft.customErrorCodes" data-testid="cline-error-codes" class="input w-full" placeholder="500, 502, 503" :aria-label="t('clineAccount.advanced.customErrors')" />
      </section>
      <section class="space-y-3">
        <label class="flex items-center gap-2 text-sm font-medium"><input v-model="draft.tempEnabled" type="checkbox" data-testid="cline-temp-enabled" />{{ t('admin.accounts.tempUnschedulable.title') }}</label>
        <p class="text-xs text-gray-500">{{ t('clineAccount.advanced.rulesHint') }}</p>
        <div v-for="(rule, index) in draft.rules" :key="index" class="space-y-2 rounded border border-gray-200 p-3 dark:border-dark-600" data-testid="cline-temp-rule">
          <div class="flex items-center gap-2"><span class="flex-1 text-sm">{{ t('admin.accounts.tempUnschedulable.ruleIndex', { index: index + 1 }) }}</span><button type="button" class="btn btn-secondary" :disabled="index === 0" @click="moveRule(index, -1)">{{ t('clineAccount.advanced.moveUp') }}</button><button type="button" class="btn btn-secondary" :disabled="index === draft.rules.length - 1" @click="moveRule(index, 1)">{{ t('clineAccount.advanced.moveDown') }}</button><button type="button" class="btn btn-secondary" @click="draft.rules.splice(index, 1)">{{ t('clineAccount.remove') }}</button></div>
          <div class="grid gap-3 sm:grid-cols-2"><label class="text-sm">{{ t('admin.accounts.tempUnschedulable.errorCode') }}<input v-model.number="rule.error_code" type="number" min="400" max="599" step="1" class="input mt-1 w-full" /></label><label class="text-sm">{{ t('admin.accounts.tempUnschedulable.durationMinutes') }}<input v-model.number="rule.duration_minutes" type="number" min="1" max="10080" step="1" class="input mt-1 w-full" /></label></div>
          <label class="block text-sm">{{ t('admin.accounts.tempUnschedulable.keywords') }}<textarea v-model="rule.keywords" rows="2" class="input mt-1 w-full" /></label>
          <label class="block text-sm">{{ t('admin.accounts.tempUnschedulable.description') }}<input v-model="rule.description" maxlength="512" class="input mt-1 w-full" /></label>
        </div>
        <button type="button" class="btn btn-secondary" data-testid="cline-add-rule" :disabled="draft.rules.length >= 32" @click="addRule">{{ t('clineAccount.advanced.addRule') }}</button>
      </section>
      <section v-if="account" class="space-y-3 border-t border-gray-200 pt-3 dark:border-dark-600">
        <p class="text-xs text-gray-500">{{ t('clineAccount.advanced.operationsHint') }}</p>
        <div class="flex flex-wrap gap-2"><button v-for="kind in (['quota', 'pause', 'recheck'] as const)" :key="kind" type="button" class="btn btn-secondary" :data-testid="'cline-action-' + kind" :disabled="!canOperate || running" @click="action = kind; failed = false">{{ t('clineAccount.advanced.action.' + kind) }}</button></div>
        <div v-if="action" role="alert" class="space-y-2 rounded bg-amber-50 p-3 dark:bg-amber-900/20"><p class="text-sm">{{ t('clineAccount.advanced.confirm.' + action) }}</p><button type="button" class="btn btn-primary" data-testid="cline-confirm-operation" :disabled="!canOperate || running" @click="operate">{{ t('common.confirm') }}</button><button type="button" class="btn btn-secondary" :disabled="running" @click="action = null">{{ t('common.cancel') }}</button></div>
        <p v-if="failed" role="alert" class="text-sm text-red-600">{{ t('clineAccount.advanced.operationFailed') }}</p>
      </section>
    </div>
  </details>
</template>
