<script setup lang="ts">
const { logto } = useLogtoState()
const error = ref('')

onMounted(async () => {
  try {
    await logto?.handleCallback(window.location.href)
    await navigateTo('/')
  } catch (err: unknown) {
    error.value = errorMessage(err)
  }
})
</script>

<template>
  <UDashboardPanel id="callback">
    <template #body>
      <div class="flex flex-col items-center justify-center gap-3 py-24">
        <template v-if="!error">
          <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-primary" />
          <p class="text-sm text-muted">
            正在完成 Logto 登录…
          </p>
        </template>
        <template v-else>
          <UIcon name="i-lucide-triangle-alert" class="size-8 text-error" />
          <p class="text-sm text-error">
            {{ error }}
          </p>
          <UButton to="/" label="返回首页" />
        </template>
      </div>
    </template>
  </UDashboardPanel>
</template>
