<script setup lang="ts">
import type { ProbeOptions } from '~/composables/useFxkit'
import type { ProbeResult } from '~/types'

const { probe } = useFxkitClients()
const toast = useToast()

const running = ref<string | null>(null)
const results = ref<Record<string, ProbeResult | null>>({})

async function run(key: string, path: string, options?: ProbeOptions) {
  running.value = key
  try {
    results.value[key] = await probe(path, options)
  } catch (err: unknown) {
    results.value[key] = {
      status: 0,
      ok: false,
      headers: {},
      body: { error: errorMessage(err) }
    }
  } finally {
    running.value = null
  }
}

async function createArticle() {
  const title = `outbox-${Date.now()}`
  try {
    const res = await probe('/api/articles', {
      method: 'POST',
      body: { title, body: 'platform outbox demo' }
    })
    if (res.ok) {
      toast.add({ title: '已创建', description: `${title}，稍后查看 fanout / 文章状态`, color: 'success' })
    } else {
      toast.add({ title: '创建失败', description: errorMessage(res.body), color: 'error' })
    }
  } catch (err: unknown) {
    toast.add({ title: '创建失败', description: errorMessage(err), color: 'error' })
  }
}

const capabilities = [
  { pkg: 'fxkit', demo: 'catalog / storefront 装配（Default + docs.MergedModule）', link: '/' },
  { pkg: 'cli', demo: 'make openapi / gen-client / doctor', link: '' },
  { pkg: 'config', demo: 'YAML + Consul KV + --port（见 example/README.md）', link: '' },
  { pkg: 'server', demo: '健康探针、chi 路由、宿主中间件', link: '#health' },
  { pkg: 'fxerrors', demo: 'chi /chi/errors/{kind} 错误映射', link: '#chi' },
  { pkg: 'gormx', demo: 'Articles 事务创建 + AutoMigrate', link: '/articles' },
  { pkg: 'cachex', demo: 'GET /cache-demo（Get/Set）', link: '#cache' },
  { pkg: 'buildinfo', demo: 'GET /version', link: '#health' },
  { pkg: 'logx / otelx', demo: 'slog + OTLP（服务端日志/链路）', link: '' },
  { pkg: 'huma', demo: 'Articles / Comments / Digest typed client', link: '/articles' },
  { pkg: 'crudx', demo: '搜索、排序、游标、JOIN article.title', link: '/comments' },
  { pkg: 'authz', demo: 'Logto / X-User、authz 管理探针', link: '/settings' },
  { pkg: 'idempotency', demo: '支付页：同一个 Idempotency-Key 重放同一响应', link: '/payments' },
  { pkg: 'payments / ledger', demo: '创建/授权/扣款/部分退款/账本/对账', link: '/payments' },
  { pkg: 'demo 能力', demo: '缓存/动态配置/锁/DLQ/Hatchet/AfterCommit', link: '/demo' },
  { pkg: 'atlas / migrations', demo: 'make migrate-baseline / atlas-*（见 migrations/README.md）', link: '' },
  { pkg: 'hatchetx / outbox', demo: 'counter / heartbeat / fanout 探针', link: '#hatchet' },
  { pkg: 'consulx', demo: 'TTL 注册 + Traefik tags（见 README）', link: '' },
  { pkg: 'reqx', demo: 'Digest 页：storefront → reqx → catalog', link: '/digest' },
  { pkg: 'docs / openapi', demo: 'GET /openapi.yaml + openapi-typescript/openapi-fetch', link: '#docs' },
  { pkg: 'mcpx', demo: 'GET /mcp/sse 状态（默认关闭）', link: '#mcp' }
]
</script>

<template>
  <UDashboardPanel id="platform">
    <template #header>
      <UDashboardNavbar title="平台探针">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <section id="health" class="space-y-4">
        <h2 class="text-lg font-semibold text-highlighted">
          版本与健康
        </h2>
        <UPageGrid class="lg:grid-cols-2 gap-4">
          <PlatformProbeCard
            title="版本 buildinfo"
            description="GET /version"
            :result="results.version"
            :loading="running === 'version'"
            @run="run('version', '/api/fxkit/version')"
          />
          <PlatformProbeCard
            title="存活探针"
            description="GET /healthz"
            :result="results.healthz"
            :loading="running === 'healthz'"
            @run="run('healthz', '/api/fxkit/healthz')"
          />
          <PlatformProbeCard
            title="就绪探针"
            description="GET /readyz（DB / cache 聚合）"
            :result="results.readyz"
            :loading="running === 'readyz'"
            @run="run('readyz', '/api/fxkit/readyz')"
          />
          <PlatformProbeCard
            title="公开 ping"
            description="GET /public/ping（auth.public_paths）"
            :result="results.ping"
            :loading="running === 'ping'"
            @run="run('ping', '/api/fxkit/public/ping')"
          />
          <PlatformProbeCard
            title="当前身份"
            description="GET /me（authz.SubjectFromContext）"
            :result="results.me"
            :loading="running === 'me'"
            @run="run('me', '/api/fxkit/me')"
          />
        </UPageGrid>
      </section>

      <section id="chi" class="space-y-4 mt-8">
        <h2 class="text-lg font-semibold text-highlighted">
          裸 chi 与 fxerrors
        </h2>
        <UPageGrid class="lg:grid-cols-2 gap-4">
          <PlatformProbeCard
            title="chi 回声"
            description="GET /chi/echo?msg=你好"
            :result="results.chiEcho"
            :loading="running === 'chiEcho'"
            @run="run('chiEcho', '/api/fxkit/chi/echo', { query: { msg: '你好' } })"
          />
          <PlatformProbeCard
            title="fxerrors：未找到"
            description="GET /chi/errors/notfound"
            :result="results.chiNotFound"
            :loading="running === 'chiNotFound'"
            @run="run('chiNotFound', '/api/fxkit/chi/errors/notfound')"
          />
          <PlatformProbeCard
            title="fxerrors：冲突"
            description="GET /chi/errors/conflict"
            :result="results.chiConflict"
            :loading="running === 'chiConflict'"
            @run="run('chiConflict', '/api/fxkit/chi/errors/conflict')"
          />
          <PlatformProbeCard
            title="fxerrors：校验失败"
            description="GET /chi/errors/validation"
            :result="results.chiValidation"
            :loading="running === 'chiValidation'"
            @run="run('chiValidation', '/api/fxkit/chi/errors/validation')"
          />
          <PlatformProbeCard
            title="fxerrors：内部错误"
            description="GET /chi/errors/boom"
            :result="results.chiBoom"
            :loading="running === 'chiBoom'"
            @run="run('chiBoom', '/api/fxkit/chi/errors/boom')"
          />
        </UPageGrid>
      </section>

      <section id="hatchet" class="space-y-4 mt-8">
        <h2 class="text-lg font-semibold text-highlighted">
          Hatchet / outbox
        </h2>
        <p class="text-sm text-muted">
          Hatchet 未启用时这些端点返回 503；启用后先创建文章，outbox → Hatchet → 文章 indexed + fanout。
        </p>
        <div class="flex flex-wrap gap-2">
          <UButton label="创建测试文章（触发 outbox）" icon="i-lucide-plus" @click="createArticle" />
        </div>
        <UPageGrid class="lg:grid-cols-2 gap-4">
          <PlatformProbeCard
            title="读 counter"
            description="GET /counters/demo"
            :result="results.counterGet"
            :loading="running === 'counterGet'"
            @run="run('counterGet', '/api/fxkit/counters/demo')"
          />
          <PlatformProbeCard
            title="Actor 计数 +1"
            description="POST /counters/demo"
            :result="results.counterInc"
            :loading="running === 'counterInc'"
            @run="run('counterInc', '/api/fxkit/counters/demo', { method: 'POST', body: { delta: 1 } })"
          />
          <PlatformProbeCard
            title="Reminder 心跳"
            description="GET /heartbeat"
            :result="results.heartbeat"
            :loading="running === 'heartbeat'"
            @run="run('heartbeat', '/api/fxkit/heartbeat')"
          />
          <PlatformProbeCard
            title="触发 heartbeat Actor"
            description="POST /heartbeat"
            :result="results.heartbeatCall"
            :loading="running === 'heartbeatCall'"
            @run="run('heartbeatCall', '/api/fxkit/heartbeat', { method: 'POST' })"
          />
          <PlatformProbeCard
            title="事件 fan-out"
            description="GET /article-fanout"
            :result="results.fanout"
            :loading="running === 'fanout'"
            @run="run('fanout', '/api/fxkit/article-fanout')"
          />
        </UPageGrid>
      </section>

      <section id="cache" class="space-y-4 mt-8">
        <h2 class="text-lg font-semibold text-highlighted">
          cachex
        </h2>
        <UPageGrid class="lg:grid-cols-2 gap-4">
          <PlatformProbeCard
            title="缓存 Get/Set"
            description="GET /cache-demo（make demo 会启动 Redis；未启用时 enabled=false）"
            :result="results.cache"
            :loading="running === 'cache'"
            @run="run('cache', '/api/fxkit/cache-demo')"
          />
        </UPageGrid>
      </section>

      <section id="authz" class="space-y-4 mt-8">
        <h2 class="text-lg font-semibold text-highlighted">
          authz 管理 API
        </h2>
        <UPageGrid class="lg:grid-cols-2 gap-4">
          <PlatformProbeCard
            title="角色"
            description="GET /authz/roles"
            :result="results.roles"
            :loading="running === 'roles'"
            @run="run('roles', '/api/fxkit/authz/roles')"
          />
          <PlatformProbeCard
            title="权限目录"
            description="GET /authz/permissions"
            :result="results.permissions"
            :loading="running === 'permissions'"
            @run="run('permissions', '/api/fxkit/authz/permissions')"
          />
          <PlatformProbeCard
            title="用户绑定"
            description="GET /authz/bindings"
            :result="results.bindings"
            :loading="running === 'bindings'"
            @run="run('bindings', '/api/fxkit/authz/bindings')"
          />
          <PlatformProbeCard
            title="Casbin 策略"
            description="GET /authz/policies"
            :result="results.policies"
            :loading="running === 'policies'"
            @run="run('policies', '/api/fxkit/authz/policies')"
          />
        </UPageGrid>
      </section>

      <section id="mcp" class="space-y-4 mt-8">
        <h2 class="text-lg font-semibold text-highlighted">
          MCP
        </h2>
        <UPageGrid class="lg:grid-cols-2 gap-4">
          <PlatformProbeCard
            title="MCP SSE"
            description="GET /mcp/sse（默认关闭；未注册路由会被 authz 拦截）"
            :result="results.mcp"
            :loading="running === 'mcp'"
            @run="run('mcp', '/api/fxkit/mcp/sse')"
          />
        </UPageGrid>
      </section>

      <section id="docs" class="space-y-4 mt-8">
        <h2 class="text-lg font-semibold text-highlighted">
          文档与 OpenAPI
        </h2>
        <div class="flex flex-wrap gap-2">
          <UButton
            to="http://127.0.0.1:8081/docs"
            target="_blank"
            icon="i-lucide-book-open"
            label="打开 catalog /docs"
            variant="outline"
          />
          <UButton
            to="http://127.0.0.1:8081/openapi.yaml"
            target="_blank"
            icon="i-lucide-file-code"
            label="catalog /openapi.yaml"
            variant="outline"
          />
        </div>
        <UPageGrid class="lg:grid-cols-2 gap-4">
          <PlatformProbeCard
            title="合并后的 OpenAPI"
            description="GET /openapi.yaml（docs.MergedModule）"
            :result="results.openapi"
            :loading="running === 'openapi'"
            @run="run('openapi', '/api/fxkit/openapi.yaml')"
          />
        </UPageGrid>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold text-highlighted">
          框架能力矩阵
        </h2>
        <UCard class="mt-4">
          <ul class="divide-y divide-default">
            <li
              v-for="item in capabilities"
              :key="item.pkg"
              class="flex items-start justify-between gap-4 py-3"
            >
              <div>
                <p class="font-medium text-highlighted">
                  {{ item.pkg }}
                </p>
                <p class="text-sm text-muted">
                  {{ item.demo }}
                </p>
              </div>
              <UButton
                v-if="item.link"
                :to="item.link"
                size="xs"
                variant="outline"
                label="查看"
              />
            </li>
          </ul>
        </UCard>
      </section>
    </template>
  </UDashboardPanel>
</template>
