<template>
  <BaseDialog :show="show" :title="t(account ? 'clineAccount.edit' : 'clineAccount.create')" width="wide"
    :show-close-button="!saving" :close-on-escape="!saving" @close="close">
    <form id="cline-account-form" class="space-y-5" @submit.prevent="save">
      <button v-if="showPlatformBack" type="button" class="btn btn-secondary" :disabled="saving" data-testid="cline-back-platform" @click="back">{{ t('clineAccount.choosePlatform') }}</button>
      <p class="rounded-lg bg-slate-100 p-3 text-sm text-slate-700 dark:bg-dark-700 dark:text-slate-200">{{ t('clineAccount.boundary') }}</p>
      <p v-if="draft.mode === 'free' || draft.mode === 'unknown'" role="status" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">{{ t(draft.mode === 'free' ? 'clineAccount.freeNotice' : 'clineAccount.unknownNotice') }}</p>
      <div v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</div>
      <fieldset :disabled="saving" class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block text-sm">{{ t('clineAccount.name') }}<input v-model="draft.name" data-testid="cline-name" class="input mt-1 w-full" required maxlength="100" /></label>
          <label class="block text-sm">{{ t('clineAccount.mode') }}<select v-model="draft.mode" data-testid="cline-mode" class="input mt-1 w-full"><option value="pass">Cline Pass</option><option value="free">Cline Free</option><option value="payg">Cline PAYG</option><option value="unknown">{{ t('clineAccount.unknown') }}</option></select></label>
          <label class="block text-sm">{{ t('clineAccount.authType') }}<select v-model="draft.authType" data-testid="cline-auth" class="input mt-1 w-full" required><option value="" disabled>{{ t('clineAccount.selectAuth') }}</option><option value="api_key">API Key</option><option value="account_token">Account Token</option></select></label>
          <label class="block text-sm">{{ t(account ? 'clineAccount.replaceKey' : 'clineAccount.key') }}<input v-model="draft.apiKey" data-testid="cline-key" type="password" autocomplete="new-password" spellcheck="false" class="input mt-1 w-full" :required="!account" /></label>
        </div>
        <p v-if="draft.authType === 'account_token'" data-testid="cline-manual-token-notice" class="text-sm text-amber-700 dark:text-amber-300">{{ t('clineAccount.manualTokenNotice') }}</p>
        <p v-if="account" class="text-xs text-gray-500">{{ t('clineAccount.keepKey') }}</p>
        <label class="block text-sm">{{ t('clineAccount.baseURL') }}<input v-model="draft.baseURL" data-testid="cline-base" class="input mt-1 w-full font-mono text-sm" type="url" /></label>
        <div class="space-y-2">
          <div class="flex items-center justify-between gap-3"><h4 class="font-medium">{{ t('clineAccount.models') }}</h4><button type="button" class="btn btn-secondary" data-testid="cline-add-model" @click="draft.models.push({ publicID: '', upstreamID: '' })">{{ t('clineAccount.addModel') }}</button></div>
          <p class="text-xs text-gray-500">{{ t('clineAccount.modelsHint') }}</p>
          <div v-for="(row, index) in draft.models" :key="index" class="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
            <input v-model="row.publicID" :aria-label="t('clineAccount.publicModel')" :placeholder="t('clineAccount.publicModel')" data-testid="cline-public-model" class="input min-w-0 font-mono text-sm" required />
            <input v-model="row.upstreamID" :aria-label="t('clineAccount.upstreamModel')" :placeholder="draft.mode === 'pass' ? 'cline-pass/…' : 'vendor/model'" data-testid="cline-upstream-model" class="input min-w-0 font-mono text-sm" required />
            <button type="button" class="btn btn-secondary" :aria-label="t('clineAccount.removeModel')" @click="draft.models.splice(index, 1)">{{ t('clineAccount.remove') }}</button>
          </div>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block text-sm">{{ t('clineAccount.concurrency') }}<input v-model.number="draft.concurrency" type="number" min="1" max="1000" step="1" class="input mt-1 w-full" required /></label>
          <label class="block text-sm">{{ t('clineAccount.priority') }}<input v-model.number="draft.priority" type="number" min="0" max="10000" step="1" class="input mt-1 w-full" required /></label>
        </div>
        <div><h4 class="mb-2 text-sm font-medium">{{ t('clineAccount.groups') }}</h4><p v-if="!visibleGroups.length" class="text-sm text-gray-500">{{ t('clineAccount.noGroups') }}</p><div class="flex max-h-36 flex-wrap gap-3 overflow-auto"><label v-for="group in visibleGroups" :key="group.id" class="flex items-center gap-2 text-sm"><input v-model="draft.groupIDs" type="checkbox" :value="group.id" />{{ group.name }} <span v-if="group.missing" class="text-amber-600">{{ t('clineAccount.unavailableGroup') }}</span></label></div></div>
        <label class="flex items-center gap-2 text-sm"><input v-model="draft.schedulable" data-testid="cline-schedulable" type="checkbox" :disabled="draft.mode === 'free' || draft.mode === 'unknown'" />{{ t('clineAccount.schedulable') }}</label>
        <AccountGroupModelLimits v-if="account" v-model="draft.groupAllowedModels" :groups="groupsForLimits" platform="cline" :account-id="account.id" />
        <details class="rounded-lg border border-gray-200 p-3 dark:border-dark-600" open>
          <summary class="cursor-pointer font-medium">{{ t('clineAccount.commonSettings') }}</summary>
          <div class="mt-3 grid gap-4 sm:grid-cols-2">
            <label class="block text-sm">{{ t('clineAccount.proxy') }}<select v-model="draft.proxyID" data-testid="cline-proxy" class="input mt-1 w-full"><option :value="null">{{ t('clineAccount.noProxy') }}</option><option v-for="proxy in visibleProxies" :key="proxy.id" :value="proxy.id" :disabled="proxy.unavailable && proxy.id !== draft.proxyID">{{ proxy.name }}{{ proxy.unavailable ? ' · ' + t('clineAccount.unavailableGroup') : '' }}</option></select></label>
            <label class="block text-sm">{{ t('clineAccount.loadFactor') }}<input v-model.number="draft.loadFactor" data-testid="cline-load-factor" type="number" min="1" max="10000" step="1" class="input mt-1 w-full" :placeholder="t('clineAccount.inheritConcurrency')" /></label>
            <label class="block text-sm">{{ t('clineAccount.rateMultiplier') }}<input v-model.number="draft.rateMultiplier" data-testid="cline-rate" type="number" min="0" step="any" required class="input mt-1 w-full" /></label>
            <label class="block text-sm">{{ t('clineAccount.costMultiplier') }}<input v-model.number="draft.costMultiplier" data-testid="cline-cost" type="number" min="0" max="1000000" step="any" required class="input mt-1 w-full" /><span class="text-xs text-gray-500">{{ t('clineAccount.costHint') }}</span></label>
            <label class="block text-sm">{{ t('clineAccount.groupRateMultiplier') }}<input v-model.number="draft.groupRateMultiplier" data-testid="cline-group-rate" type="number" min="0" step="any" required class="input mt-1 w-full" /></label>
            <label class="block text-sm">{{ t('clineAccount.expiresAt') }}<input v-model="draft.expiresAt" data-testid="cline-expiry" type="datetime-local" step="1" class="input mt-1 w-full" /></label>
            <label class="flex items-center gap-2 text-sm"><input v-model="draft.autoPauseOnExpired" data-testid="cline-auto-pause" type="checkbox" />{{ t('clineAccount.autoPauseOnExpired') }}</label>
            <label v-if="account" class="block text-sm">{{ t('clineAccount.status') }}<select v-model="draft.status" data-testid="cline-status" class="input mt-1 w-full"><option value="active">{{ t('common.active') }}</option><option value="inactive">{{ t('common.inactive') }}</option><option value="error">{{ t('common.error') }}</option></select></label>
          </div>
          <p class="mt-2 text-xs text-gray-500">{{ t('clineAccount.settingsHint') }}</p>
        </details>
        <div class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <label class="flex items-center gap-2 text-sm font-medium"><input v-model="draft.headerOverrideEnabled" data-testid="cline-headers-enabled" type="checkbox" />{{ t('clineAccount.headerDefaults') }}</label>
          <p class="text-xs text-gray-500">{{ t('clineAccount.headersHint') }}</p>
          <HeaderOverrideEditor v-if="draft.headerOverrideEnabled" :rows="draft.headerOverrideRows" @update:rows="draft.headerOverrideRows = $event" />
        </div>
        <label class="block text-sm">{{ t('clineAccount.notes') }}<textarea v-model="draft.notes" class="input mt-1 w-full" rows="2" /></label>
      </fieldset>
      <template v-if="account && show">
        <button v-if="!showMetadata" type="button" class="btn btn-secondary" :disabled="saving" @click="showMetadata = true">{{ t('clineMetadata.open') }}</button>
        <p v-if="showMetadata" class="text-xs text-gray-500">{{ t('clineAccount.savedMetadata') }}</p>
        <ClineMetadataPanel v-if="showMetadata" :account-id="account.id" :mode="clineMode(account.credentials?.account_mode)" @select="addCatalogModel" />
      </template>
      <p class="text-xs text-gray-500">{{ t('clineAccount.noProbe') }}</p>
    </form>
    <template #footer><button class="btn btn-secondary" :disabled="saving" @click="close">{{ t('clineAccount.cancel') }}</button><button form="cline-account-form" type="submit" data-testid="cline-save" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'clineAccount.saving' : 'clineAccount.save') }}</button></template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, AdminGroup, CreateAccountRequest, Proxy } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ClineMetadataPanel from './ClineMetadataPanel.vue'
import AccountGroupModelLimits from './AccountGroupModelLimits.vue'
import HeaderOverrideEditor from './HeaderOverrideEditor.vue'
import { ClineSettingsError } from './clineAccountSettings'
import * as accountsAPI from '@/api/admin/accounts'
import { buildClineAccountPayload, clineAccountDraft, clineMode, ClineFormError } from './clineAccountForm'

const props = withDefaults(defineProps<{ show: boolean; account?: Account | null; groups?: AdminGroup[]; proxies?: Proxy[]; allowComposite?: boolean; initial?: { name: string; notes: string }; showPlatformBack?: boolean }>(), { account: null, groups: () => [], proxies: () => [], allowComposite: true, showPlatformBack: false })
const emit = defineEmits<{ close: []; saved: []; back: [] }>()
const { t } = useI18n()
const draft = reactive(clineAccountDraft())
const saving = ref(false)
const showMetadata = ref(false)
function addCatalogModel(id: string) {
  if (saving.value || draft.models.some(row => row.publicID === id)) return
  draft.models.push({ publicID: id, upstreamID: id })
}
const error = ref('')
watch(() => [props.show, props.account] as const, ([show]) => {
  showMetadata.value = false
  if (!show) { draft.apiKey = ''; return }
  Object.assign(draft, clineAccountDraft(props.account))
  if (!props.account && props.initial) { draft.name = props.initial.name; draft.notes = props.initial.notes }
  error.value = ''
}, { immediate: true })
const visibleGroups = computed(() => {
  const groups = props.groups.filter(g => g.platform === 'cline' || (props.allowComposite && g.platform === 'composite') || props.account?.group_ids?.includes(g.id))
    .map(g => ({ id: g.id, name: g.name, missing: false }))
  const visible = new Set(groups.map(g => g.id))
  for (const id of props.account?.group_ids ?? []) if (!visible.has(id)) groups.push({ id, name: `#${id}`, missing: true })
  return groups
})
const groupsForLimits = computed(() => visibleGroups.value.filter(g => draft.groupIDs.includes(g.id)))
const visibleProxies = computed(() => {
  const options = props.proxies.filter(p => p.status === 'active' || p.id === draft.proxyID)
    .map(p => ({ id: p.id, name: p.name, unavailable: p.status !== 'active' }))
  if (draft.proxyID !== null && !options.some(p => p.id === draft.proxyID)) options.push({ id: draft.proxyID, name: `#${draft.proxyID}`, unavailable: true })
  return options
})
function back() {
  if (saving.value) return
  draft.apiKey = ''
  emit('back')
}
function close() {
  if (saving.value) return
  draft.apiKey = ''
  emit('close')
}
async function save() {
  if (saving.value) return
  error.value = ''
  try {
    const payload = buildClineAccountPayload(draft, props.account)
    saving.value = true
    if (props.account) await accountsAPI.update(props.account.id, payload)
    else await accountsAPI.create(payload as CreateAccountRequest)
    draft.apiKey = ''
    emit('saved')
    emit('close')
  } catch (err) {
    // Axios errors can contain the submitted secret. Never log/stringify them.
    error.value = err instanceof ClineFormError || err instanceof ClineSettingsError ? t(`clineAccount.errors.${err.issue}`) : t('clineAccount.saveFailed')
  } finally { saving.value = false }
}
</script>
