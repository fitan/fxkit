<script setup lang="ts">
defineProps<{
  collapsed?: boolean
}>()
</script>

<template>
  <UButton
    to="/"
    icon="i-lucide-boxes"
    :label="collapsed ? undefined : 'fxkit'"
    color="neutral"
    variant="ghost"
    block
    :square="collapsed"
    class="py-2"
  />
</template>
