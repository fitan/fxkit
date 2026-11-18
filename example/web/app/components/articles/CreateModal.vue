<script setup lang="ts">
import * as z from 'zod'
import type { FormSubmitEvent } from '@nuxt/ui'

const emit = defineEmits<{ created: [] }>()

const open = ref(false)
const saving = ref(false)
const toast = useToast()
const { catalog } = useFxkitClients()

const schema = z.object({
  title: z.string().min(1, '标题不能为空'),
  body: z.string().optional()
})

type Schema = z.output<typeof schema>

const state = reactive<Partial<Schema>>({
  title: '',
  body: ''
})

async function onSubmit(event: FormSubmitEvent<Schema>) {
  saving.value = true
  try {
    const res = await catalog.POST('/articles', { body: event.data })
    if (res.error) throw new Error(errorMessage(res.error))
    toast.add({
      title: '已创建',
      description: event.data.title,
      color: 'success'
    })
    open.value = false
    state.title = ''
    state.body = ''
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
  <UModal v-model:open="open" title="新建文章" description="调用 catalog POST /articles">
    <UButton label="新建文章" icon="i-lucide-plus" />

    <template #body>
      <UForm
        :schema="schema"
        :state="state"
        class="space-y-4"
        @submit="onSubmit"
      >
        <UFormField label="标题" name="title" required>
          <UInput v-model="state.title" placeholder="文章标题" class="w-full" />
        </UFormField>

        <UFormField label="正文" name="body">
          <UTextarea
            v-model="state.body"
            placeholder="可选正文"
            :rows="4"
            class="w-full"
          />
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
