<template>
  <BaseDialog :show="show" :title="t(account ? 'clineAccount.edit' : 'clineAccount.create')" width="wide"
    :show-close-button="!saving" :close-on-escape="!saving" @close="close">
    <form id="cline-account-form" class="space-y-5" @submit.prevent="save">
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
        <label v-if="account" class="flex items-center gap-2 text-sm"><input v-model="draft.schedulable" type="checkbox" :disabled="draft.mode === 'free' || draft.mode === 'unknown'" />{{ t('clineAccount.schedulable') }}</label>
        <label class="block text-sm">{{ t('clineAccount.notes') }}<textarea v-model="draft.notes" class="input mt-1 w-full" rows="2" /></label>
      </fieldset>
      <template v-if="account && show">
        <button v-if="!showMetadata" type="button" class="btn btn-secondary" :disabled="saving" @click="showMetadata = true">{{ t('clineMetadata.open') }}</button>
        <ClineMetadataPanel v-if="showMetadata" :account-id="account.id" :mode="draft.mode" @select="addCatalogModel" />
      </template>
      <p class="text-xs text-gray-500">{{ t('clineAccount.noProbe') }}</p>
    </form>
    <template #footer><button class="btn btn-secondary" :disabled="saving" @click="close">{{ t('clineAccount.cancel') }}</button><button form="cline-account-form" type="submit" data-testid="cline-save" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'clineAccount.saving' : 'clineAccount.save') }}</button></template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, AdminGroup, CreateAccountRequest } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ClineMetadataPanel from './ClineMetadataPanel.vue'
import * as accountsAPI from '@/api/admin/accounts'
import { buildClineAccountPayload, clineAccountDraft, ClineFormError } from './clineAccountForm'

const props = withDefaults(defineProps<{ show: boolean; account?: Account | null; groups?: AdminGroup[]; allowComposite?: boolean }>(), { account: null, groups: () => [], allowComposite: true })
const emit = defineEmits<{ close: []; saved: [] }>()
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
  error.value = ''
}, { immediate: true })
const visibleGroups = computed(() => {
  const groups = props.groups.filter(g => g.platform === 'cline' || (props.allowComposite && g.platform === 'composite') || props.account?.group_ids?.includes(g.id))
    .map(g => ({ id: g.id, name: g.name, missing: false }))
  const visible = new Set(groups.map(g => g.id))
  for (const id of props.account?.group_ids ?? []) if (!visible.has(id)) groups.push({ id, name: `#${id}`, missing: true })
  return groups
})
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
    error.value = err instanceof ClineFormError ? t(`clineAccount.errors.${err.issue}`) : t('clineAccount.saveFailed')
  } finally { saving.value = false }
}
</script>
