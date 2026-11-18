<script setup lang="ts">
import type { Article, Digest } from '~/types'

const { storefront } = useFxkitClients()

const { data, status, error, refresh } = await useAsyncData<Digest>(
  'digest',
  async () => {
    const res = await storefront.GET('/digest', { params: { query: { limit: 10 } } })
    if (res.error) throw new Error(errorMessage(res.error))
    return (res.data ?? { source: '', via: '', endpoints: [], articles: null }) as Digest
  },
  {
    server: false,
    default: () => ({ source: '', via: '', endpoints: [], articles: null })
  }
)

const articles = computed<Article[]>(() => data.value?.articles?.items ?? [])
</script>

<template>
  <UDashboardPanel id="digest">
    <template #header>
      <UDashboardNavbar title="摘要">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>

        <template #right>
          <UButton
            icon="i-lucide-refresh-cw"
            color="neutral"
            variant="outline"
            label="刷新"
            :loading="status === 'pending'"
            @click="refresh()"
          />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert
        v-if="error"
        color="error"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        title="摘要加载失败"
        :description="String(error.message || error)"
        class="mb-4"
      />

      <UPageGrid class="lg:grid-cols-3 gap-4">
        <UPageCard
          title="来源"
          :description="data?.source || '—'"
          icon="i-lucide-server"
          variant="subtle"
        />
        <UPageCard
          title="经由"
          :description="data?.via || '—'"
          icon="i-lucide-route"
          variant="subtle"
        />
        <UPageCard
          title="端点"
          :description="(data?.endpoints || []).join(', ') || '—'"
          icon="i-lucide-network"
          variant="subtle"
        />
      </UPageGrid>

      <UCard class="mt-6">
        <template #header>
          <p class="font-semibold text-highlighted">
            通过 storefront 获取文章
          </p>
          <p class="text-sm text-muted">
            <code>GET /storefront/digest</code> → reqx → catalog
          </p>
        </template>

        <div v-if="status === 'pending'" class="text-sm text-muted">
          加载中…
        </div>
        <div v-else-if="!articles.length" class="text-sm text-muted">
          暂无数据（确认 catalog/storefront 已启动）。
        </div>
        <ul v-else class="divide-y divide-default">
          <li
            v-for="article in articles"
            :key="article.id"
            class="flex items-center justify-between gap-3 py-3"
          >
            <div>
              <p class="font-medium text-highlighted">
                #{{ article.id }} {{ article.title }}
              </p>
              <p class="text-xs text-muted">
                {{ article.author || '匿名' }}
              </p>
            </div>
            <UBadge variant="subtle" color="neutral">
              {{ articleStatusLabel(article.status) }}
            </UBadge>
          </li>
        </ul>
      </UCard>
    </template>
  </UDashboardPanel>
</template>
