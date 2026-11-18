<script setup lang="ts">
const { probe } = useFxkitClients()
const toast = useToast()
const busy = ref<string | null>(null)

const config = ref<{ welcome: string, source: string } | null>(null)
const cacheName = ref('demo')
const cache = ref<{ key: string, value: string, loadedAt: string, loads: number } | null>(null)
const lockName = ref('order-1')
const holdMs = ref('50')
const lock = ref<{ key: string, fencingToken: number, heldMs: number } | null>(null)
const dlq = ref<Array<{ id: number, topic: string, attempts: number, lastError?: string }>>([])
const requeueTopics = ref('')
const scheduleName = ref('demo')
const scheduleDelay = ref('2000')
const scheduledId = ref('')
const dagRunId = ref('')
const durableRunId = ref('')
const rlKey = ref('demo-rl')
const rlLimit = ref('5')
const rlOk = ref(false)
const txFail = ref(false)
const tx = ref<{ committed: boolean, hooks: number } | null>(null)

async function run(key: string, fn: () => Promise<void>) {
  busy.value = key
  try {
    await fn()
  } finally {
    busy.value = null
  }
}

function fail(title: string, body: unknown) {
  toast.add({ title, description: errorMessage(body), color: 'error' })
}

async function loadConfig() {
  await run('config', async () => {
    const res = await probe('/api/fxkit/demo/config')
    if (!res.ok) return fail('配置加载失败', res.body)
    config.value = res.body as { welcome: string, source: string }
  })
}

async function loadCache() {
  await run('cache', async () => {
    const res = await probe(`/api/fxkit/demo/cache/${encodeURIComponent(cacheName.value)}`)
    if (!res.ok) return fail('缓存调用失败', res.body)
    cache.value = res.body as { key: string, value: string, loadedAt: string, loads: number }
  })
}

async function acquireLock() {
  await run('lock', async () => {
    const res = await probe(`/api/fxkit/demo/lock/${encodeURIComponent(lockName.value)}`, {
      method: 'POST',
      body: { holdMs: Number(holdMs.value) }
    })
    if (!res.ok) return fail('加锁失败', res.body)
    lock.value = res.body as { key: string, fencingToken: number, heldMs: number }
  })
}

async function loadDlq() {
  await run('dlq', async () => {
    const res = await probe('/api/fxkit/demo/outbox/failed', { query: { limit: 20 } })
    if (!res.ok) return fail('DLQ 加载失败', res.body)
    dlq.value = (res.body as { items?: Array<{ id: number, topic: string, attempts: number, lastError?: string }> }).items ?? []
  })
}

async function requeue() {
  await run('requeue', async () => {
    const topics = requeueTopics.value.split(',').map(s => s.trim()).filter(Boolean)
    const res = await probe('/api/fxkit/demo/outbox/requeue', {
      method: 'POST',
      body: { topics, limit: 50 }
    })
    if (!res.ok) return fail('重放失败', res.body)
    const body = res.body as { requeued?: number }
    toast.add({ title: '已重放', description: `${body.requeued ?? 0} 行`, color: 'success' })
    await loadDlq()
  })
}

async function scheduleOnce() {
  await run('schedule', async () => {
    const res = await probe('/api/fxkit/demo/hatchet/schedule', {
      method: 'POST',
      body: { name: scheduleName.value, delayMs: Number(scheduleDelay.value) }
    })
    if (!res.ok) return fail('定时运行失败', res.body)
    scheduledId.value = (res.body as { scheduledRunId?: string }).scheduledRunId ?? ''
    toast.add({ title: '已安排', description: scheduledId.value, color: 'success' })
  })
}

async function runDag() {
  await run('dag', async () => {
    const res = await probe('/api/fxkit/demo/hatchet/dag', {
      method: 'POST',
      body: { name: scheduleName.value }
    })
    if (!res.ok) return fail('DAG 触发失败', res.body)
    dagRunId.value = (res.body as { runId?: string }).runId ?? ''
    toast.add({ title: 'DAG 已触发', description: dagRunId.value, color: 'success' })
  })
}

async function runDurable() {
  await run('durable', async () => {
    const res = await probe('/api/fxkit/demo/hatchet/durable', {
      method: 'POST',
      body: { name: scheduleName.value }
    })
    if (!res.ok) return fail('持久化任务触发失败', res.body)
    durableRunId.value = (res.body as { runId?: string }).runId ?? ''
    toast.add({ title: '持久化任务已触发', description: durableRunId.value, color: 'success' })
  })
}

async function setRateLimit() {
  await run('rate', async () => {
    const res = await probe('/api/fxkit/demo/hatchet/rate-limit', {
      method: 'POST',
      body: { key: rlKey.value, limit: Number(rlLimit.value) }
    })
    if (!res.ok) return fail('限流规则失败', res.body)
    rlOk.value = true
  })
}

async function runTx() {
  await run('tx', async () => {
    const res = await probe('/api/fxkit/demo/tx', {
      method: 'POST',
      query: { fail: txFail.value }
    })
    if (!res.ok) return fail('事务演示失败', res.body)
    tx.value = res.body as { committed: boolean, hooks: number }
  })
}

onMounted(() => {
  loadConfig()
  loadDlq()
})
</script>

<template>
  <UDashboardPanel id="demo">
    <template #header>
      <UDashboardNavbar title="能力演示">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert
        color="neutral"
        variant="subtle"
        icon="i-lucide-info"
        title="依赖未配置时对应接口返回 503"
        description="cache/Consul/Hatchet 未启用时，下面的卡片会显示对应错误；这是框架的显式降级行为。"
        class="mb-4"
      />

      <UPageGrid class="lg:grid-cols-2 gap-4">
        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              动态配置
            </p>
            <p class="text-sm text-muted">
              GET /demo/config · config.Watch / Dynamic
            </p>
          </template>
          <UButton
            label="读取当前快照"
            icon="i-lucide-refresh-cw"
            :loading="busy === 'config'"
            @click="loadConfig"
          />
          <div v-if="config" class="mt-3 text-sm">
            <p>欢迎语：<span class="font-medium text-highlighted">{{ config.welcome }}</span></p>
            <p class="text-muted">
              来源：{{ config.source }}
            </p>
          </div>
        </UCard>

        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              缓存 GetOrSet
            </p>
            <p class="text-sm text-muted">
              GET /demo/cache/{name} · singleflight + TTL 抖动
            </p>
          </template>
          <div class="flex flex-wrap items-center gap-2">
            <UInput v-model="cacheName" placeholder="name" class="max-w-48" />
            <UButton
              label="读取"
              icon="i-lucide-database"
              :loading="busy === 'cache'"
              @click="loadCache"
            />
          </div>
          <div v-if="cache" class="mt-3 text-sm">
            <p>值：<span class="font-medium text-highlighted">{{ cache.value }}</span></p>
            <p class="text-muted">
              loadedAt：{{ cache.loadedAt }}
            </p>
            <p class="text-muted">
              进程内 loader 次数：{{ cache.loads }}（并发请求应合并为 1）
            </p>
          </div>
        </UCard>

        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              分布式锁
            </p>
            <p class="text-sm text-muted">
              POST /demo/lock/{name} · consulx.Locker + fencing token
            </p>
          </template>
          <div class="flex flex-wrap items-center gap-2">
            <UInput v-model="lockName" placeholder="lock name" class="max-w-48" />
            <UInput
              v-model="holdMs"
              type="number"
              placeholder="holdMs"
              class="max-w-32"
            />
            <UButton
              label="加锁"
              icon="i-lucide-lock"
              :loading="busy === 'lock'"
              @click="acquireLock"
            />
          </div>
          <div v-if="lock" class="mt-3 text-sm">
            <p>key：<span class="font-medium text-highlighted">{{ lock.key }}</span></p>
            <p>fencingToken：{{ lock.fencingToken }}</p>
            <p class="text-muted">
              heldMs：{{ lock.heldMs }}
            </p>
          </div>
        </UCard>

        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              Outbox DLQ
            </p>
            <p class="text-sm text-muted">
              GET /demo/outbox/failed · POST /demo/outbox/requeue
            </p>
          </template>
          <div class="flex flex-wrap items-center gap-2">
            <UButton
              label="刷新 DLQ"
              icon="i-lucide-refresh-cw"
              :loading="busy === 'dlq'"
              @click="loadDlq"
            />
            <UInput v-model="requeueTopics" placeholder="topics（逗号分隔，留空=全部）" class="max-w-64" />
            <UButton
              label="重放"
              icon="i-lucide-recycle"
              color="warning"
              :loading="busy === 'requeue'"
              @click="requeue"
            />
          </div>
          <ul v-if="dlq.length" class="mt-3 divide-y divide-default">
            <li v-for="row in dlq" :key="row.id" class="py-2 text-sm">
              <span class="font-medium text-highlighted">#{{ row.id }}</span>
              {{ row.topic }} · attempts={{ row.attempts }}
              <span v-if="row.lastError" class="text-error"> · {{ row.lastError }}</span>
            </li>
          </ul>
          <p v-else class="mt-3 text-sm text-muted">
            暂无 failed 行
          </p>
        </UCard>

        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              Hatchet 定时 / DAG
            </p>
            <p class="text-sm text-muted">
              POST /demo/hatchet/{schedule,dag}
            </p>
          </template>
          <div class="grid gap-2">
            <div class="flex flex-wrap items-center gap-2">
              <UInput v-model="scheduleName" placeholder="name" class="max-w-40" />
              <UInput
                v-model="scheduleDelay"
                type="number"
                placeholder="delayMs"
                class="max-w-32"
              />
              <UButton
                label="ScheduleOnce"
                icon="i-lucide-timer"
                :loading="busy === 'schedule'"
                @click="scheduleOnce"
              />
              <UButton
                label="触发 DAG"
                icon="i-lucide-git-branch"
                color="neutral"
                variant="outline"
                :loading="busy === 'dag'"
                @click="runDag"
              />
              <UButton
                label="持久化任务"
                icon="i-lucide-hourglass"
                color="neutral"
                variant="outline"
                :loading="busy === 'durable'"
                @click="runDurable"
              />
            </div>
            <p v-if="scheduledId" class="text-xs text-muted">
              scheduledRunId：{{ scheduledId }}
            </p>
            <p v-if="dagRunId" class="text-xs text-muted">
              dagRunId：{{ dagRunId }}
            </p>
            <p v-if="durableRunId" class="text-xs text-muted">
              durableRunId：{{ durableRunId }}
            </p>
          </div>
        </UCard>

        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              Hatchet 限流
            </p>
            <p class="text-sm text-muted">
              POST /demo/hatchet/rate-limit
            </p>
          </template>
          <div class="flex flex-wrap items-center gap-2">
            <UInput v-model="rlKey" placeholder="key" class="max-w-40" />
            <UInput
              v-model="rlLimit"
              type="number"
              placeholder="limit/min"
              class="max-w-32"
            />
            <UButton
              label="Upsert"
              icon="i-lucide-gauge"
              :loading="busy === 'rate'"
              @click="setRateLimit"
            />
          </div>
          <p v-if="rlOk" class="mt-2 text-sm text-success">
            限流规则已更新
          </p>
        </UCard>

        <UCard>
          <template #header>
            <p class="font-semibold text-highlighted">
              AfterCommit
            </p>
            <p class="text-sm text-muted">
              POST /demo/tx · 提交才执行，回滚不执行
            </p>
          </template>
          <div class="flex flex-wrap items-center gap-3">
            <USwitch v-model="txFail" label="强制回滚" />
            <UButton
              label="运行事务"
              icon="i-lucide-database-zap"
              :loading="busy === 'tx'"
              @click="runTx"
            />
          </div>
          <div v-if="tx" class="mt-3 text-sm">
            <p>committed：{{ tx.committed }}</p>
            <p>hooks：{{ tx.hooks }}</p>
          </div>
        </UCard>
      </UPageGrid>
    </template>
  </UDashboardPanel>
</template>
