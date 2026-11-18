<script setup lang="ts">
import type { Article, ArticleListResult } from '~/types'

const { catalog } = useFxkitClients()

const { data, status, error, refresh } = await useAsyncData<ArticleListResult>(
  'home-articles',
  async () => {
    const res = await catalog.GET('/articles', {
      params: {
        query: { limit: 100, sortBy: 'id', sortDirection: 'desc', replyWithCount: true }
      }
    })
    if (res.error) throw new Error(errorMessage(res.error))
    return res.data ?? { items: [], start: 0, limit: 100 }
  },
  {
    server: false,
    default: () => ({ items: [], start: 0, limit: 100 })
  }
)

const articles = computed<Article[]>(() => data.value?.items ?? [])
const total = computed(() => data.value?.total ?? articles.value.length)
const indexed = computed(() => articles.value.filter(article => article.status === 'indexed').length)
const drafts = computed(() => articles.value.filter(article => article.status === 'draft').length)
const authors = computed(() => new Set(articles.value.map(article => article.author).filter(Boolean)).size)
const recent = computed(() => articles.value.slice(0, 8))

const stats = computed(() => [
  { title: '文章', value: total.value, icon: 'i-lucide-newspaper' },
  { title: '已索引', value: indexed.value, icon: 'i-lucide-check-check' },
  { title: '草稿', value: drafts.value, icon: 'i-lucide-file-pen-line' },
  { title: '作者', value: authors.value, icon: 'i-lucide-users' }
])
</script>

<template>
  <UDashboardPanel id="home">
    <template #header>
      <UDashboardNavbar title="概览">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>

        <template #right>
          <UButton
            icon="i-lucide-refresh-cw"
            color="neutral"
            variant="ghost"
            :loading="status === 'pending'"
            @click="refresh()"
          />
          <UButton to="/articles" icon="i-lucide-newspaper" label="文章" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert
        v-if="error"
        color="error"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        title="加载失败"
        :description="String(error.message || error)"
        class="mb-4"
      />

      <UPageGrid class="lg:grid-cols-4 gap-4 sm:gap-6 lg:gap-px">
        <UPageCard
          v-for="stat in stats"
          :key="stat.title"
          :icon="stat.icon"
          :title="stat.title"
          variant="subtle"
          :ui="{
            container: 'gap-y-1.5',
            wrapper: 'items-start',
            leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25 flex-col',
            title: 'font-normal text-muted text-xs uppercase'
          }"
          class="lg:rounded-none first:rounded-l-lg last:rounded-r-lg hover:z-1"
        >
          <span class="text-2xl font-semibold text-highlighted">{{ stat.value }}</span>
        </UPageCard>
      </UPageGrid>

      <UCard class="mt-6">
        <template #header>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <p class="font-semibold text-highlighted">
                最近文章
              </p>
              <p class="text-sm text-muted">
                来自 catalog <code>GET /articles</code>
              </p>
            </div>
            <UButton
              to="/digest"
              icon="i-lucide-layers"
              color="neutral"
              variant="outline"
              label="摘要"
            />
          </div>
        </template>

        <div v-if="status === 'pending'" class="text-sm text-muted">
          加载中…
        </div>
        <div v-else-if="!recent.length" class="text-sm text-muted">
          暂无文章，去「文章」页创建一篇。
        </div>
        <ul v-else class="divide-y divide-default">
          <li
            v-for="article in recent"
            :key="article.id"
            class="flex items-center justify-between gap-3 py-3"
          >
            <div class="min-w-0">
              <p class="font-medium text-highlighted truncate">
                #{{ article.id }} {{ article.title }}
              </p>
              <p class="text-xs text-muted">
                {{ article.author || '匿名' }}
              </p>
            </div>
            <UBadge :color="articleStatusColor(article.status)" variant="subtle">
              {{ articleStatusLabel(article.status) }}
            </UBadge>
          </li>
        </ul>
      </UCard>
    </template>
  </UDashboardPanel>
</template>
