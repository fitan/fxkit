<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'

interface Payment {
  id: string
  amount: number
  capturedAmount: number
  refundedAmount: number
  currency: string
  status: string
  version: number
  provider?: string
  providerRef?: string
  createdAt: string
  updatedAt: string
}

interface LedgerEntry {
  txnId: string
  accountId: string
  amount: number
  currency: string
  createdAt: string
}

interface ReconcileDiff {
  key: string
  kind: string
  internal?: { paymentId: string, amount: number, currency: string }
  external?: { paymentId: string, amount: number, currency: string }
}

const UBadge = resolveComponent('UBadge')
const UButton = resolveComponent('UButton')

const { probe } = useFxkitClients()
const toast = useToast()

const items = ref<Payment[]>([])
const selected = ref<Payment | null>(null)
const ledger = ref<LedgerEntry[]>([])
const reconcile = ref<{ diffs: ReconcileDiff[], summary: Record<string, number> } | null>(null)
const busy = ref<string | null>(null)

const createForm = reactive({
  amount: '1000',
  currency: 'CNY',
  provider: '',
  idem: ''
})
const refundAmount = ref('')
const webhookForm = reactive({ eventId: '', type: 'captured' })

onMounted(() => {
  if (!createForm.idem) createForm.idem = crypto.randomUUID()
  refresh()
})

function newKey() {
  createForm.idem = crypto.randomUUID()
}

function statusColor(status: string): 'success' | 'warning' | 'error' | 'neutral' {
  switch (status) {
    case 'captured':
      return 'success'
    case 'authorized':
      return 'warning'
    case 'refunded':
      return 'neutral'
    case 'failed':
    case 'canceled':
      return 'error'
    default:
      return 'neutral'
  }
}

async function refresh() {
  busy.value = 'list'
  try {
    const res = await probe('/api/fxkit/payments', { query: { limit: 50 } })
    if (!res.ok) {
      toast.add({ title: '加载支付单失败', description: errorMessage(res.body), color: 'error' })
      return
    }
    items.value = (res.body as { items?: Payment[] }).items ?? []
    if (selected.value) {
      selected.value = items.value.find(i => i.id === selected.value?.id) ?? null
    }
  } finally {
    busy.value = null
  }
}

async function createPayment(replay = false) {
  if (!createForm.idem) createForm.idem = crypto.randomUUID()
  busy.value = replay ? 'replay' : 'create'
  try {
    const res = await probe('/api/fxkit/payments', {
      method: 'POST',
      headers: { 'Idempotency-Key': createForm.idem },
      body: {
        amount: Number(createForm.amount),
        currency: createForm.currency,
        provider: createForm.provider || undefined
      }
    })
    if (!res.ok) {
      toast.add({ title: '创建失败', description: errorMessage(res.body), color: 'error' })
      return
    }
    const p = res.body as Payment
    selected.value = p
    refundAmount.value = ''
    toast.add({
      title: replay ? '幂等重放' : '已创建',
      description: `${p.id} · ${p.status}`,
      color: 'success'
    })
    await refresh()
  } finally {
    busy.value = null
  }
}

async function act(kind: 'authorize' | 'capture') {
  if (!selected.value) return
  busy.value = kind
  try {
    const res = await probe(`/api/fxkit/payments/${selected.value.id}/${kind}`, { method: 'POST' })
    if (!res.ok) {
      toast.add({ title: '操作失败', description: errorMessage(res.body), color: 'error' })
      return
    }
    selected.value = res.body as Payment
    toast.add({ title: '成功', description: `${kind} → ${selected.value.status}`, color: 'success' })
    await refresh()
  } finally {
    busy.value = null
  }
}

async function refund() {
  if (!selected.value) return
  busy.value = 'refund'
  try {
    const amount = refundAmount.value.trim() === '' ? undefined : Number(refundAmount.value)
    const res = await probe(`/api/fxkit/payments/${selected.value.id}/refund`, {
      method: 'POST',
      body: amount ? { amount } : {}
    })
    if (!res.ok) {
      toast.add({ title: '退款失败', description: errorMessage(res.body), color: 'error' })
      return
    }
    selected.value = res.body as Payment
    refundAmount.value = ''
    toast.add({
      title: '退款成功',
      description: `${selected.value.status} · 已退 ${selected.value.refundedAmount}/${selected.value.capturedAmount}`,
      color: 'success'
    })
    await Promise.all([refresh(), loadLedger()])
  } finally {
    busy.value = null
  }
}

async function loadLedger() {
  if (!selected.value) return
  busy.value = 'ledger'
  try {
    const res = await probe(`/api/fxkit/payments/${selected.value.id}/ledger`)
    if (!res.ok) {
      toast.add({ title: '账本加载失败', description: errorMessage(res.body), color: 'error' })
      return
    }
    ledger.value = (res.body as { items?: LedgerEntry[] }).items ?? []
  } finally {
    busy.value = null
  }
}

async function runReconcile() {
  busy.value = 'reconcile'
  try {
    const res = await probe('/api/fxkit/payments/reconcile', { method: 'POST' })
    if (!res.ok) {
      toast.add({ title: '对账失败', description: errorMessage(res.body), color: 'error' })
      return
    }
    reconcile.value = res.body as { diffs: ReconcileDiff[], summary: Record<string, number> }
  } finally {
    busy.value = null
  }
}

async function sendWebhook() {
  if (!selected.value) return
  busy.value = 'webhook'
  try {
    const eventId = webhookForm.eventId || `evt_${Date.now()}`
    const res = await probe('/api/demo/webhook', {
      method: 'POST',
      body: {
        eventId,
        paymentId: selected.value.id,
        type: webhookForm.type,
        amount: selected.value.amount,
        currency: selected.value.currency
      }
    })
    const payload = res.body as { status?: number, body?: unknown } | undefined
    const status = payload?.status ?? res.status
    const color: 'success' | 'error' = status < 300 ? 'success' : 'error'
    toast.add({
      title: `Webhook ${status}`,
      description: errorMessage(payload?.body ?? res.body),
      color
    })
    await refresh()
  } finally {
    busy.value = null
  }
}

function select(p: Payment) {
  selected.value = p
  ledger.value = []
  webhookForm.eventId = ''
  refundAmount.value = p.capturedAmount > p.refundedAmount
    ? String(p.capturedAmount - p.refundedAmount)
    : ''
}

const columns: TableColumn<Payment>[] = [
  { accessorKey: 'id', header: 'ID' },
  {
    accessorKey: 'amount',
    header: '金额',
    cell: ({ row }) => `${row.original.amount} ${row.original.currency}`
  },
  {
    accessorKey: 'status',
    header: '状态',
    cell: ({ row }) => h(
      UBadge,
      { color: statusColor(row.original.status), variant: 'subtle' },
      () => row.original.status
    )
  },
  {
    id: 'progress',
    header: '捕获 / 退款',
    cell: ({ row }) => `${row.original.capturedAmount} / ${row.original.refundedAmount}`
  },
  {
    accessorKey: 'createdAt',
    header: '创建时间',
    cell: ({ row }) => new Date(row.original.createdAt).toLocaleString()
  },
  {
    id: 'actions',
    cell: ({ row }) => h(
      'div',
      { class: 'text-right' },
      h(UButton, {
        size: 'xs',
        variant: 'outline',
        label: '操作',
        onClick: () => select(row.original)
      })
    )
  }
]
</script>

<template>
  <UDashboardPanel id="payments">
    <template #header>
      <UDashboardNavbar title="支付">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
        <template #right>
          <UButton
            icon="i-lucide-refresh-cw"
            color="neutral"
            variant="outline"
            :loading="busy === 'list'"
            @click="refresh()"
          />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UPageGrid class="lg:grid-cols-2 gap-4">
        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              创建支付单
            </p>
            <p class="text-sm text-muted">
              POST /payments · 带 Idempotency-Key；重复提交应返回同一 ID
            </p>
          </template>
          <div class="grid gap-3">
            <UFormField label="金额（最小单位）">
              <UInput v-model="createForm.amount" type="number" min="1" />
            </UFormField>
            <UFormField label="币种">
              <UInput v-model="createForm.currency" />
            </UFormField>
            <UFormField label="渠道（可选）">
              <UInput v-model="createForm.provider" placeholder="mock" />
            </UFormField>
            <UFormField label="Idempotency-Key">
              <UInput v-model="createForm.idem" />
            </UFormField>
            <div class="flex flex-wrap gap-2">
              <UButton
                label="创建"
                icon="i-lucide-plus"
                :loading="busy === 'create'"
                @click="createPayment()"
              />
              <UButton
                label="同 key 重放"
                color="neutral"
                variant="outline"
                icon="i-lucide-repeat"
                :loading="busy === 'replay'"
                @click="createPayment(true)"
              />
              <UButton
                label="新 key"
                color="neutral"
                variant="ghost"
                icon="i-lucide-key"
                @click="newKey()"
              />
            </div>
          </div>
        </UCard>

        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              对账
            </p>
            <p class="text-sm text-muted">
              POST /payments/reconcile · 对比内部 captured 与渠道 settlements
            </p>
          </template>
          <UButton
            label="运行对账"
            icon="i-lucide-scale"
            :loading="busy === 'reconcile'"
            @click="runReconcile"
          />
          <div v-if="reconcile" class="mt-3 text-sm">
            <p class="text-muted">
              差异 {{ reconcile.diffs.length }} 条
            </p>
            <ul v-if="reconcile.diffs.length" class="mt-2 space-y-1">
              <li v-for="d in reconcile.diffs" :key="d.key" class="text-error">
                {{ d.kind }} · {{ d.key }}
              </li>
            </ul>
            <p v-else class="mt-2 text-success">
              账实一致
            </p>
          </div>
        </UCard>
      </UPageGrid>

      <UCard class="mt-6">
        <template #header>
          <p class="font-semibold text-highlighted">
            最近支付单
          </p>
          <p class="text-sm text-muted">
            GET /payments
          </p>
        </template>
        <UTable
          :data="items"
          :columns="columns"
          :loading="busy === 'list'"
          :ui="{
            base: 'table-fixed border-separate border-spacing-0',
            thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
            tbody: '[&>tr]:last:[&>td]:border-b-0',
            th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
            td: 'border-b border-default',
            separator: 'h-0'
          }"
        />
      </UCard>

      <UCard v-if="selected" class="mt-6">
        <template #header>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <p class="font-semibold text-highlighted">
                {{ selected.id }}
              </p>
              <p class="text-sm text-muted">
                {{ selected.amount }} {{ selected.currency }} · v{{ selected.version }}
              </p>
            </div>
            <UBadge :color="statusColor(selected.status)" variant="subtle">
              {{ selected.status }}
            </UBadge>
          </div>
        </template>

        <div class="grid gap-3">
          <div class="flex flex-wrap gap-2">
            <UButton
              label="授权"
              icon="i-lucide-shield-check"
              :disabled="selected.status !== 'created'"
              :loading="busy === 'authorize'"
              @click="act('authorize')"
            />
            <UButton
              label="扣款"
              icon="i-lucide-banknote"
              :disabled="selected.status !== 'authorized'"
              :loading="busy === 'capture'"
              @click="act('capture')"
            />
            <UButton
              label="查看账本"
              icon="i-lucide-book-open"
              color="neutral"
              variant="outline"
              :loading="busy === 'ledger'"
              @click="loadLedger"
            />
          </div>

          <div class="rounded-lg border border-default p-3">
            <p class="text-sm font-medium text-highlighted">
              部分退款
            </p>
            <p class="text-xs text-muted">
              已捕获 {{ selected.capturedAmount }}，已退 {{ selected.refundedAmount }}，剩余
              {{ Math.max(selected.capturedAmount - selected.refundedAmount, 0) }}
            </p>
            <div class="mt-2 flex flex-wrap items-center gap-2">
              <UInput
                v-model="refundAmount"
                type="number"
                min="1"
                placeholder="留空 = 退剩余"
                class="max-w-48"
              />
              <UButton
                label="退款"
                icon="i-lucide-undo-2"
                color="warning"
                :disabled="selected.status !== 'captured'"
                :loading="busy === 'refund'"
                @click="refund"
              />
            </div>
          </div>

          <div class="rounded-lg border border-default p-3">
            <p class="text-sm font-medium text-highlighted">
              模拟渠道 webhook
            </p>
            <p class="text-xs text-muted">
              由 BFF 用服务端密钥计算 HMAC，再调用 /payments/webhook
            </p>
            <div class="mt-2 flex flex-wrap items-center gap-2">
              <USelect
                v-model="webhookForm.type"
                :items="['captured', 'refunded', 'failed']"
                class="max-w-40"
              />
              <UInput v-model="webhookForm.eventId" placeholder="eventId（留空自动生成）" class="max-w-64" />
              <UButton
                label="发送"
                icon="i-lucide-webhook"
                color="neutral"
                variant="outline"
                :loading="busy === 'webhook'"
                @click="sendWebhook"
              />
            </div>
          </div>

          <div v-if="ledger.length">
            <p class="text-sm font-medium text-highlighted">
              账本分录
            </p>
            <ul class="mt-2 divide-y divide-default">
              <li
                v-for="row in ledger"
                :key="row.txnId + row.accountId"
                class="flex items-center justify-between gap-3 py-2 text-sm"
              >
                <span class="text-muted">{{ row.txnId }}</span>
                <span>{{ row.accountId }}</span>
                <span :class="row.amount < 0 ? 'text-error' : 'text-success'">
                  {{ row.amount }} {{ row.currency }}
                </span>
              </li>
            </ul>
          </div>
        </div>
      </UCard>
    </template>
  </UDashboardPanel>
</template>
