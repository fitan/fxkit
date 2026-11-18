<script setup lang="ts">
const toast = useToast()
const { logto, authenticated, claims } = useLogtoState()
const devUser = useDevUser()

const sub = computed(() => {
  const value = claims.value?.sub
  return typeof value === 'string' ? value : ''
})

watch(devUser, () => {
  void refreshNuxtData()
})

async function signIn() {
  await logto?.signIn()
}

async function signOut() {
  await logto?.signOut()
}

async function bindAdmin() {
  if (!sub.value) return
  try {
    await bindFxkitAdmin(sub.value)
    toast.add({ title: '已绑定管理员', description: sub.value, color: 'success' })
    await refreshNuxtData()
  } catch (err: unknown) {
    toast.add({
      title: '绑定失败',
      description: errorMessage(err),
      color: 'error'
    })
  }
}
</script>

<template>
  <UPageCard
    title="账户"
    description="Logto 登录或本地 X-User 兜底。"
    variant="subtle"
  >
    <div class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <p class="text-sm text-muted">
            认证方式
          </p>
          <p class="font-medium text-highlighted">
            {{ authenticated ? 'Logto Bearer' : `X-User: ${devUser}` }}
          </p>
        </div>
        <UButton
          v-if="!authenticated"
          label="使用 Logto 登录"
          icon="i-lucide-log-in"
          @click="signIn"
        />
        <UButton
          v-else
          label="退出登录"
          icon="i-lucide-log-out"
          color="neutral"
          variant="outline"
          @click="signOut"
        />
      </div>

      <USeparator />

      <div class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <p class="text-sm text-muted">
            Dev user（未登录时使用）
          </p>
          <p class="text-xs text-muted">
            alice = 管理员，bob = 只读，charlie = 无角色
          </p>
        </div>
        <USelect
          v-model="devUser"
          :items="[
            { label: 'alice', value: 'alice' },
            { label: 'bob', value: 'bob' },
            { label: 'charlie', value: 'charlie' }
          ]"
          class="w-40"
        />
      </div>

      <USeparator />

      <div class="flex flex-wrap items-center justify-between gap-4">
        <div class="min-w-0">
          <p class="text-sm text-muted">
            JWT sub
          </p>
          <p class="font-mono text-xs break-all">
            {{ sub || '—' }}
          </p>
        </div>
        <UButton
          label="绑定到管理员"
          icon="i-lucide-shield-check"
          :disabled="!sub"
          @click="bindAdmin"
        />
      </div>
    </div>
  </UPageCard>
</template>
