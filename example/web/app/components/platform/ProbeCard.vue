<script setup lang="ts">
import type { ProbeResult } from '~/types'

defineProps<{
  title: string
  description?: string
  result?: ProbeResult | null
  loading?: boolean
}>()

defineEmits<{ run: [] }>()
</script>

<template>
  <UCard>
    <template #header>
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <p class="font-medium text-highlighted">
            {{ title }}
          </p>
          <p v-if="description" class="text-xs text-muted break-all">
            {{ description }}
          </p>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <UBadge v-if="result" :color="result.ok ? 'success' : 'error'" variant="subtle">
            {{ result.status || 'ERR' }}
          </UBadge>
          <UButton
            size="xs"
            icon="i-lucide-play"
            label="运行"
            :loading="loading"
            @click="$emit('run')"
          />
        </div>
      </div>
    </template>

    <div v-if="loading" class="text-sm text-muted">
      请求中…
    </div>
    <template v-else-if="result">
      <p
        v-if="result.headers['x-fxkit-chi'] || result.headers['x-fxkit-huma']"
        class="text-xs text-muted mb-2"
      >
        <span v-if="result.headers['x-fxkit-chi']">X-Fxkit-Chi: {{ result.headers['x-fxkit-chi'] }}</span>
        <span v-if="result.headers['x-fxkit-huma']">X-Fxkit-Huma: {{ result.headers['x-fxkit-huma'] }}</span>
      </p>
      <pre class="text-xs overflow-auto max-h-64 whitespace-pre-wrap break-all">{{ typeof result.body === 'string' ? result.body : JSON.stringify(result.body, null, 2) }}</pre>
    </template>
    <p v-else class="text-sm text-muted">
      尚未运行
    </p>
  </UCard>
</template>
