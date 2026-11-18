<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { Comment, CommentListResult } from '~/types'

const UButton = resolveComponent('UButton')

const { catalog } = useFxkitClients()
const toast = useToast()

const filter = ref('')
const selected = ref<Comment | null>(null)
const deleteOpen = ref(false)
const deleting = ref(false)

const { data, status, error, refresh } = await useAsyncData<CommentListResult>(
  'comments',
  async () => {
    const res = await catalog.GET('/comments', {
      params: {
        query: {
          limit: 100,
          sortBy: 'id',
          sortDirection: 'desc',
          replyWithCount: true,
          ...(filter.value ? { q: [`article.title~${filter.value}`] } : {})
        }
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

const comments = computed<Comment[]>(() => data.value?.items ?? [])

watchDebounced(filter, () => refresh(), { debounce: 300 })

function askDelete(comment: Comment) {
  selected.value = comment
  deleteOpen.value = true
}

async function remove() {
  if (!selected.value) return
  deleting.value = true
  try {
    const res = await catalog.DELETE('/comments/{id}', {
      params: { path: { id: String(selected.value.id) } }
    })
    if (res.error) throw new Error(errorMessage(res.error))
    toast.add({
      title: '已删除',
      description: `#${selected.value.id}`,
      color: 'success'
    })
    deleteOpen.value = false
    await refresh()
  } catch (err: unknown) {
    toast.add({
      title: '删除失败',
      description: errorMessage(err),
      color: 'error'
    })
  } finally {
    deleting.value = false
  }
}

const columns: TableColumn<Comment>[] = [
  {
    accessorKey: 'id',
    header: 'ID'
  },
  {
    accessorKey: 'articleId',
    header: '文章 ID'
  },
  {
    accessorKey: 'body',
    header: '正文'
  },
  {
    accessorKey: 'note',
    header: '备注',
    cell: ({ row }) => row.original.note || '—'
  },
  {
    accessorKey: 'author',
    header: '作者',
    cell: ({ row }) => row.original.author || '—'
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
        icon: 'i-lucide-trash',
        color: 'error',
        variant: 'ghost',
        onClick: () => askDelete(row.original)
      })
    )
  }
]
</script>

<template>
  <UDashboardPanel id="comments">
    <template #header>
      <UDashboardNavbar title="评论">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>

        <template #right>
          <CommentsCreateModal @created="refresh()" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <div class="flex flex-wrap items-center justify-between gap-2">
        <UInput
          v-model="filter"
          icon="i-lucide-search"
          placeholder="按文章标题过滤（JOIN article.title）..."
          class="max-w-md"
        />
        <div class="flex items-center gap-2">
          <span class="text-sm text-muted">{{ data?.total ?? comments.length }} 条评论</span>
          <UButton
            icon="i-lucide-refresh-cw"
            color="neutral"
            variant="outline"
            :loading="status === 'pending'"
            @click="refresh()"
          />
        </div>
      </div>

      <UAlert
        v-if="error"
        color="error"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        title="加载失败"
        :description="String(error.message || error)"
        class="my-4"
      />

      <UTable
        :data="comments"
        :columns="columns"
        :loading="status === 'pending'"
        class="mt-4 shrink-0"
        :ui="{
          base: 'table-fixed border-separate border-spacing-0',
          thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
          tbody: '[&>tr]:last:[&>td]:border-b-0',
          th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
          td: 'border-b border-default',
          separator: 'h-0'
        }"
      />

      <UModal
        v-model:open="deleteOpen"
        title="删除评论"
        :description="selected ? `#${selected.id}` : ''"
      >
        <template #body>
          <p class="text-sm text-muted">
            此操作不可撤销。
          </p>
          <div class="flex justify-end gap-2 mt-4">
            <UButton
              label="取消"
              color="neutral"
              variant="subtle"
              @click="deleteOpen = false"
            />
            <UButton
              label="删除"
              color="error"
              variant="solid"
              :loading="deleting"
              @click="remove"
            />
          </div>
        </template>
      </UModal>
    </template>
  </UDashboardPanel>
</template>
