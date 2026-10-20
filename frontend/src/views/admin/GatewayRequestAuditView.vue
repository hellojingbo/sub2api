<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="card p-4 sm:p-6">
          <div class="flex flex-wrap items-center justify-between gap-4">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-3">
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.gatewayAudit.title') }}</h2>
                <span :class="stateBadgeClass" class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold">
                  <span class="h-1.5 w-1.5 rounded-full" :class="state.active ? 'bg-emerald-500' : 'bg-gray-400'"></span>
                  {{ stateLabel }}
                </span>
              </div>
              <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.gatewayAudit.description') }}</p>
              <div class="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
                <span>{{ t('admin.gatewayAudit.count') }} {{ state.count }}</span>
                <span>{{ t('admin.gatewayAudit.bytes') }} {{ formatBytes(state.bytes) }}</span>
                <span v-if="state.started_at">{{ t('admin.gatewayAudit.startedAt') }} {{ formatTime(state.started_at) }}</span>
                <span v-if="state.stopped_at">{{ t('admin.gatewayAudit.stoppedAt') }} {{ formatTime(state.stopped_at) }}</span>
              </div>
            </div>

            <div class="flex flex-wrap items-center gap-2">
              <button v-if="!state.active" type="button" class="btn btn-primary" :disabled="busy" @click="startCapture">
                <Icon name="play" size="sm" class="mr-1.5" />
                {{ t('admin.gatewayAudit.start') }}
              </button>
              <button v-else type="button" class="btn btn-secondary" :disabled="busy" @click="stopCapture">
                <Icon name="x" size="sm" class="mr-1.5" />
                {{ t('admin.gatewayAudit.stop') }}
              </button>
              <button type="button" class="btn btn-danger" :disabled="busy || records.length === 0 || state.active" @click="clearCapture">
                <Icon name="trash" size="sm" class="mr-1.5" />
                {{ t('admin.gatewayAudit.clear') }}
              </button>
            </div>
          </div>
          <p v-if="state.active" class="mt-4 rounded-lg bg-primary-50 px-3 py-2 text-xs text-primary-700 dark:bg-primary-950/30 dark:text-primary-300">
            {{ t('admin.gatewayAudit.startHint') }}
          </p>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="records" :loading="loading" row-key="id">
          <template #cell-captured_at="{ value }">
            <span class="whitespace-nowrap text-gray-600 dark:text-gray-300">{{ formatTime(value) }}</span>
          </template>
          <template #cell-transport="{ value }">
            <span class="rounded bg-gray-100 px-2 py-1 font-mono text-xs text-gray-700 dark:bg-dark-700 dark:text-gray-200">{{ value }}</span>
          </template>
          <template #cell-downstream="{ row }">
            <div class="min-w-0 max-w-[360px]">
              <div class="font-mono text-xs font-semibold text-gray-800 dark:text-gray-200">{{ row.downstream_method }}</div>
              <div class="truncate font-mono text-xs text-gray-500 dark:text-gray-400" :title="row.downstream_url">{{ row.downstream_url || '—' }}</div>
            </div>
          </template>
          <template #cell-upstream="{ row }">
            <div class="min-w-0 max-w-[420px]">
              <div class="font-mono text-xs font-semibold text-gray-800 dark:text-gray-200">{{ row.upstream_method }}</div>
              <div class="truncate font-mono text-xs text-gray-500 dark:text-gray-400" :title="row.upstream_url">{{ row.upstream_url || '—' }}</div>
            </div>
          </template>
          <template #cell-account="{ row }">
            <span class="max-w-[180px] truncate text-sm text-gray-700 dark:text-gray-300" :title="row.account_name">{{ row.account_name || row.account_id || '—' }}</span>
          </template>
          <template #cell-actions="{ row }">
            <button type="button" class="font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="openDetail(row.id)">
              <Icon name="eye" size="sm" class="mr-1" />{{ t('admin.gatewayAudit.columns.detail') }}
            </button>
          </template>
          <template #empty>
            <div class="flex flex-col items-center py-12">
              <Icon name="shield" size="xl" class="mb-4 h-12 w-12 text-gray-300 dark:text-dark-600" />
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.gatewayAudit.empty') }}</p>
            </div>
          </template>
        </DataTable>
      </template>
    </TablePageLayout>

    <BaseDialog :show="detailVisible" :title="t('admin.gatewayAudit.detail.title')" width="wide" @close="detailVisible = false">
      <div v-if="detail" class="space-y-5 py-2">
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-4">
          <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-900"><div class="text-xs text-gray-400">{{ t('admin.gatewayAudit.detail.transport') }}</div><div class="mt-1 font-mono text-sm text-gray-900 dark:text-white">{{ detail.transport }}</div></div>
          <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-900"><div class="text-xs text-gray-400">{{ t('admin.gatewayAudit.detail.account') }}</div><div class="mt-1 truncate text-sm text-gray-900 dark:text-white">{{ detail.account_name || detail.account_id || '—' }}</div></div>
          <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-900"><div class="text-xs text-gray-400">{{ t('admin.gatewayAudit.detail.requestId') }}</div><div class="mt-1 truncate font-mono text-xs text-gray-900 dark:text-white">{{ detail.request_id || '—' }}</div></div>
          <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-900"><div class="text-xs text-gray-400">{{ t('admin.gatewayAudit.detail.capturedAt') }}</div><div class="mt-1 text-sm text-gray-900 dark:text-white">{{ formatTime(detail.captured_at) }}</div></div>
        </div>

        <section>
          <div class="mb-2 flex items-center justify-between"><h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.gatewayAudit.detail.headers') }}</h3><span class="text-xs text-gray-400">{{ detail.downstream_method }} → {{ detail.upstream_method }}</span></div>
          <div class="overflow-auto rounded-xl border border-gray-200 dark:border-dark-700">
            <table class="min-w-full divide-y divide-gray-200 text-left text-xs dark:divide-dark-700">
              <thead class="bg-gray-50 text-gray-500 dark:bg-dark-900 dark:text-gray-400"><tr><th class="px-3 py-2 font-semibold">Header</th><th class="px-3 py-2 font-semibold">{{ t('admin.gatewayAudit.detail.downstreamHeader') }}</th><th class="px-3 py-2 font-semibold">{{ t('admin.gatewayAudit.detail.upstreamHeader') }}</th></tr></thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800"><tr v-for="row in headerRows" :key="row.key"><td class="whitespace-nowrap px-3 py-2 font-mono font-semibold text-gray-700 dark:text-gray-200">{{ row.label }}</td><td class="max-w-[32rem] whitespace-pre-wrap break-all px-3 py-2 font-mono text-gray-600 dark:text-gray-400">{{ row.downstream || '—' }}</td><td class="max-w-[32rem] whitespace-pre-wrap break-all px-3 py-2 font-mono text-gray-600 dark:text-gray-400">{{ row.upstream || '—' }}</td></tr></tbody>
            </table>
          </div>
        </section>

        <div class="grid grid-cols-1 gap-4 xl:grid-cols-2">
          <section><h3 class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.gatewayAudit.detail.downstream') }} · {{ t('admin.gatewayAudit.detail.body') }}</h3><pre class="max-h-[32rem] min-h-40 overflow-auto rounded-xl bg-gray-950 p-4 font-mono text-xs leading-relaxed text-gray-100">{{ formatBody(detail.downstream_body, detail.downstream_body_codec) }}</pre><div class="mt-2 break-all font-mono text-xs text-gray-500 dark:text-gray-400">{{ detail.downstream_method }} {{ detail.downstream_url }}</div></section>
          <section><h3 class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.gatewayAudit.detail.upstream') }} · {{ t('admin.gatewayAudit.detail.body') }}</h3><pre class="max-h-[32rem] min-h-40 overflow-auto rounded-xl bg-gray-950 p-4 font-mono text-xs leading-relaxed text-gray-100">{{ formatBody(detail.upstream_body, detail.upstream_body_codec) }}</pre><div v-if="detail.upstream_wire_body" class="mt-3"><div class="mb-1 text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('admin.gatewayAudit.detail.wireBody') }}</div><pre class="max-h-40 overflow-auto rounded-xl bg-gray-900 p-3 font-mono text-[11px] leading-relaxed text-gray-300">{{ formatBody(detail.upstream_wire_body, detail.upstream_wire_body_codec) }}</pre></div><div class="mt-2 break-all font-mono text-xs text-gray-500 dark:text-gray-400">{{ detail.upstream_method }} {{ detail.upstream_url }}</div></section>
        </div>
      </div>
      <div v-else class="flex items-center justify-center py-16 text-sm text-gray-500">{{ t('common.loading') }}</div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI, type GatewayRequestAuditRecord, type GatewayRequestAuditState } from '@/api/admin'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Column } from '@/components/common/types'

const { t } = useI18n()
const loading = ref(false)
const busy = ref(false)
const records = ref<GatewayRequestAuditRecord[]>([])
const detail = ref<GatewayRequestAuditRecord | null>(null)
const detailVisible = ref(false)
const state = ref<GatewayRequestAuditState>({ active: false, session_id: '', count: 0, bytes: 0 })

const columns = computed<Column[]>(() => [
  { key: 'captured_at', label: t('admin.gatewayAudit.columns.time') },
  { key: 'transport', label: t('admin.gatewayAudit.columns.transport') },
  { key: 'downstream', label: t('admin.gatewayAudit.columns.downstream') },
  { key: 'upstream', label: t('admin.gatewayAudit.columns.upstream') },
  { key: 'account', label: t('admin.gatewayAudit.columns.account') },
  { key: 'actions', label: t('common.actions') }
])

const stateLabel = computed(() => state.value.active ? t('admin.gatewayAudit.active') : state.value.count > 0 ? t('admin.gatewayAudit.stopped') : t('admin.gatewayAudit.idle'))
const stateBadgeClass = computed(() => state.value.active ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300')

const headerRows = computed(() => {
  if (!detail.value) return []
  const byKey = new Map<string, { label: string; downstream: string; upstream: string }>()
  const add = (headers: Record<string, string[]>, side: 'downstream' | 'upstream') => {
    Object.entries(headers || {}).forEach(([label, values]) => {
      const key = label.toLowerCase()
      const row = byKey.get(key) || { label, downstream: '', upstream: '' }
      row[side] = Array.isArray(values) ? values.join('\n') : String(values ?? '')
      if (!row.label) row.label = label
      byKey.set(key, row)
    })
  }
  add(detail.value.downstream_headers, 'downstream')
  add(detail.value.upstream_headers, 'upstream')
  return [...byKey.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([key, row]) => ({ key, ...row }))
})

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatBytes(value: number): string {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) { size /= 1024; index++ }
  return `${size.toFixed(index ? 1 : 0)} ${units[index]}`
}

function formatBody(value?: string, codec?: string): string {
  if (!value) return '—'
  if (codec === 'base64') return `[${t('admin.gatewayAudit.detail.base64')}]\n${value}`
  try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value }
}

async function refresh() {
  loading.value = true
  try {
    const [nextState, nextRecords] = await Promise.all([adminAPI.gatewayRequestAudit.state(), adminAPI.gatewayRequestAudit.list()])
    state.value = nextState
    records.value = nextRecords
  } finally {
    loading.value = false
  }
}

async function startCapture() {
  busy.value = true
  try { state.value = await adminAPI.gatewayRequestAudit.start(); records.value = [] } finally { busy.value = false }
}

async function stopCapture() {
  busy.value = true
  try { state.value = await adminAPI.gatewayRequestAudit.stop(); await refresh() } finally { busy.value = false }
}

async function clearCapture() {
  if (!window.confirm(t('admin.gatewayAudit.clearConfirm'))) return
  busy.value = true
  try { await adminAPI.gatewayRequestAudit.clear(); detail.value = null; records.value = []; state.value = { active: false, session_id: '', count: 0, bytes: 0 } } finally { busy.value = false }
}

async function openDetail(id: string) {
  detailVisible.value = true
  detail.value = await adminAPI.gatewayRequestAudit.get(id)
}

onMounted(refresh)
</script>
