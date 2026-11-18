<script setup lang="ts">
import * as z from 'zod'
import type { FormSubmitEvent } from '@nuxt/ui'

const emit = defineEmits<{ created: [] }>()

const open = ref(false)
const saving = ref(false)
const toast = useToast()
const { catalog } = useFxkitClients()

const schema = z.object({
  articleId: z.coerce.number().int().positive('请输入文章 ID'),
  body: z.string().min(1, '正文不能为空'),
  note: z.string().optional()
})

type Schema = z.output<typeof schema>

const state = reactive<Partial<Schema>>({
  articleId: undefined,
  body: '',
  note: ''
})

async function onSubmit(event: FormSubmitEvent<Schema>) {
  saving.value = true
  try {
    const res = await catalog.POST('/comments', {
      body: {
        articleId: event.data.articleId,
        body: event.data.body,
        note: event.data.note
      }
    })
    if (res.error) throw new Error(errorMessage(res.error))
    toast.add({
      title: '已创建',
      description: `文章 #${event.data.articleId} 的评论`,
      color: 'success'
    })
    open.value = false
    state.articleId = undefined
    state.body = ''
    state.note = ''
    emit('created')
  } catch (err: unknown) {
    toast.add({
      title: '创建失败',
      description: errorMessage(err),
      color: 'error'
    })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" title="新建评论" description="调用 catalog POST /comments">
    <UButton label="新建评论" icon="i-lucide-plus" />

    <template #body>
      <UForm
        :schema="schema"
        :state="state"
        class="space-y-4"
        @submit="onSubmit"
      >
        <UFormField label="文章 ID" name="articleId" required>
          <UInput
            v-model="state.articleId"
            type="number"
            placeholder="例如 1"
            class="w-full"
          />
        </UFormField>

        <UFormField label="正文" name="body" required>
          <UTextarea
            v-model="state.body"
            placeholder="评论内容"
            :rows="3"
            class="w-full"
          />
        </UFormField>

        <UFormField label="备注" name="note">
          <UInput v-model="state.note" placeholder="可选备注" class="w-full" />
        </UFormField>

        <div class="flex justify-end gap-2">
          <UButton
            label="取消"
            color="neutral"
            variant="subtle"
            @click="open = false"
          />
          <UButton
            label="创建"
            color="primary"
            type="submit"
            :loading="saving"
          />
        </div>
      </UForm>
    </template>
  </UModal>
</template>
