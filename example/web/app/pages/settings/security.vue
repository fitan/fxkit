<script setup lang="ts">
const config = useRuntimeConfig()
const { authenticated, claims, token, logto } = useLogtoState()
const devUser = useDevUser()

async function refreshToken() {
  await logto?.refresh()
}
</script>

<template>
  <UPageCard
    title="认证 / API"
    description="Nuxt runtimeConfig 与当前身份。"
    variant="subtle"
  >
    <dl class="space-y-3 text-sm">
      <div class="flex justify-between gap-4">
        <dt class="text-muted">
          API 资源
        </dt>
        <dd class="font-mono text-xs break-all">
          {{ config.public.apiResource }}
        </dd>
      </div>
      <div class="flex justify-between gap-4">
        <dt class="text-muted">
          Logto 端点
        </dt>
        <dd class="font-mono text-xs break-all">
          {{ config.public.logtoEndpoint }}
        </dd>
      </div>
      <div class="flex justify-between gap-4">
        <dt class="text-muted">
          应用 ID
        </dt>
        <dd class="font-mono text-xs break-all">
          {{ config.public.logtoAppId }}
        </dd>
      </div>
      <div class="flex justify-between gap-4">
        <dt class="text-muted">
          认证方式
        </dt>
        <dd>{{ authenticated ? 'Bearer' : `X-User: ${devUser}` }}</dd>
      </div>
      <div class="flex justify-between gap-4">
        <dt class="text-muted">
          访问令牌
        </dt>
        <dd>{{ token ? '已获取（仅客户端内存）' : '—' }}</dd>
      </div>
    </dl>

    <template #footer>
      <UButton
        label="刷新令牌"
        icon="i-lucide-refresh-cw"
        color="neutral"
        variant="outline"
        :disabled="!authenticated"
        @click="refreshToken"
      />
    </template>
  </UPageCard>

  <UPageCard title="ID token 声明" variant="subtle">
    <pre class="text-xs overflow-auto">{{ JSON.stringify(claims, null, 2) }}</pre>
  </UPageCard>
</template>
