<template>
  <section class="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-dark-600" aria-live="polite">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h4 class="font-medium">{{ t('clineMetadata.title') }}</h4>
      <button type="button" data-testid="cline-refresh-metadata" class="btn btn-secondary" :disabled="busy" @click="load(true)">{{ t(busy ? 'clineMetadata.loading' : 'clineMetadata.refresh') }}</button>
    </div>
    <p class="text-xs text-gray-500">{{ t('clineMetadata.savedCredential') }}</p>
    <p v-if="error" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ t('clineMetadata.requestFailed') }}</p>
    <template v-if="metadata">
      <p v-if="metadata.quota_status === 'unknown' || metadata.quota_status === 'partial'" class="text-sm text-amber-700 dark:text-amber-300">{{ t('clineMetadata.unknownNotice') }}</p>
      <p v-if="metadata.quota_status === 'not_applicable'" class="text-sm text-gray-500">{{ t('clineMetadata.passOnly') }}</p>
      <dl class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div v-for="kind in windows" :key="kind" class="rounded-md bg-slate-50 p-2 dark:bg-dark-800">
          <dt class="text-xs text-gray-500">{{ t(`clineMetadata.${kind}`) }}</dt>
          <dd :data-testid="`cline-window-${kind}`" class="text-lg font-semibold">{{ percent(kind) }}</dd>
          <dd v-if="resetAt(kind)" class="break-words text-xs text-gray-500">{{ t('clineMetadata.resets') }} {{ resetAt(kind) }}</dd>
        </div>
      </dl>
      <p v-if="metadata.auto_refresh" data-testid="cline-auto-refresh" class="text-xs text-gray-500">{{ t('clineMetadata.automatic') }} {{ metadata.next_refresh_at || '—' }}</p>
      <div v-if="metadata.cooldowns?.length" data-testid="cline-cooldowns" class="space-y-1 rounded-md bg-amber-50 p-2 text-sm dark:bg-dark-800">
        <p>{{ t('clineMetadata.cooldownNotice') }}</p>
        <p v-for="block in metadata.cooldowns" :key="`${block.type}:${block.source}`">
          {{ windowLabel(block.type) }} · {{ t(block.source === 'inference' ? 'clineMetadata.inferenceSource' : 'clineMetadata.usageSource') }}:
          <span>{{ cooldownText(block) }}</span>
        </p>
      </div>
      <p class="break-words text-xs text-gray-500">{{ t('clineMetadata.lastSuccess') }} {{ metadata.last_success_at || '—' }}</p>
      <p class="text-xs text-gray-500">{{ t(metadata.identity_verified ? 'clineMetadata.identityVerified' : 'clineMetadata.identityUnknown') }}</p>
      <p class="text-xs text-gray-500">{{ t('clineMetadata.costNotice') }}</p>
      <div v-if="models.length" class="space-y-2">
        <h5 class="text-sm font-medium">{{ t('clineMetadata.catalog') }}</h5>
        <p v-if="metadata.catalog_status !== 'ok'" class="text-xs text-amber-700 dark:text-amber-300">{{ t('clineMetadata.staleCatalog') }}</p>
        <p class="text-xs text-gray-500">{{ t('clineMetadata.catalogNotice') }}</p>
        <div class="max-h-48 space-y-1 overflow-auto">
          <div v-for="model in models" :key="model.id" class="flex items-center justify-between gap-3 text-sm">
            <code class="min-w-0 break-all">{{ model.id }}</code>
            <button type="button" class="btn btn-secondary shrink-0" :disabled="busy || !selectable(model.id)" @click="emit('select', model.id)">{{ t('clineMetadata.add') }}</button>
          </div>
        </div>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getClineMetadata, refreshClineMetadata, type ClineMetadata } from '@/api/admin/clineMetadata'

const props = defineProps<{ accountId: number; mode: string }>()
const emit = defineEmits<{ select: [modelId: string] }>()
const { t } = useI18n()
const metadata = ref<ClineMetadata | null>(null)
const busy = ref(false)
const error = ref(false)
const clock = ref(Date.now())
let lastReadAt = 0
const timer = setInterval(() => {
  clock.value = Date.now()
  if (!busy.value && clock.value - lastReadAt >= 30000 && document.visibilityState !== 'hidden') void load(false)
}, 1000)
const windows = ['five_hour', 'weekly', 'monthly'] as const
let sequence = 0
let controller: AbortController | undefined

watch(() => props.accountId, () => { metadata.value = null; void load(false) }, { immediate: true })
onBeforeUnmount(() => { sequence++; controller?.abort(); clearInterval(timer) })
async function load(refresh: boolean) {
  if (refresh && busy.value) return
  const id = props.accountId
  const version = ++sequence
  controller?.abort()
  controller = new AbortController()
  const signal = controller.signal
  busy.value = true
  lastReadAt = Date.now()
  error.value = false
  try {
    const result = await (refresh ? refreshClineMetadata(id, signal) : getClineMetadata(id, signal))
    if (version === sequence && id === props.accountId && !signal.aborted) metadata.value = result
  } catch {
    // Network errors may embed credentials/headers. Do not log or stringify.
    if (version === sequence && !signal.aborted) error.value = true
  } finally {
    if (version === sequence) busy.value = false
  }
}
function percent(kind: string): string {
  const at = Date.parse(metadata.value?.last_success_at || '')
  if (error.value || !Number.isFinite(at) || clock.value - at >= 300000 || at > clock.value + 120000) return '—'
  const value = metadata.value?.windows.find(w => w.type === kind)?.percent_used
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 100 ? `${value}%` : '—'
}
function resetAt(kind: string): string {
  return metadata.value?.windows.find(w => w.type === kind)?.resets_at || ''
}
function windowLabel(kind: string): string {
  return t(`clineMetadata.${windows.includes(kind as typeof windows[number]) ? kind : 'otherWindow'}`)
}
function cooldownText(block: NonNullable<ClineMetadata['cooldowns']>[number]): string {
  const raw = block.reset_at || block.retry_at
  const at = Date.parse(raw || '')
  if (!Number.isFinite(at)) return t('clineMetadata.unknownReset')
  if (at <= clock.value) return t('clineMetadata.pendingRecheck')
  const seconds = Math.ceil((at - clock.value) / 1000)
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds % 86400 / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  const remaining = `${days > 0 ? `${days}d ` : ''}${hours}h ${minutes}m ${seconds % 60}s`
  return `${t(block.reset_at ? 'clineMetadata.remaining' : 'clineMetadata.retryOnly')} ${remaining} · ${raw}`
}
const models = computed(() => {
  const catalog = metadata.value?.catalog
  if (!catalog) return []
  return (props.mode === 'pass' ? catalog.clinePass : props.mode === 'payg' ? catalog.recommended : props.mode === 'free' ? catalog.free : []) || []
})
function selectable(id: string): boolean {
  // Keep literal forbidden characters out of a Unicode regexp character class.
  if (!id || id.length > 256 || /\s/u.test(id) || id.includes('*') || id.includes('\\') || id.includes(String.fromCharCode(0))) return false
  return props.mode === 'pass' ? id.startsWith('cline-pass/') && id.length > 11 : props.mode === 'payg' && !id.startsWith('cline-pass/')
}
</script>
