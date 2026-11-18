<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const open = ref(false)

const links = [[{
  label: '首页',
  icon: 'i-lucide-house',
  to: '/',
  onSelect: () => {
    open.value = false
  }
}, {
  label: '文章',
  icon: 'i-lucide-newspaper',
  to: '/articles',
  onSelect: () => {
    open.value = false
  }
}, {
  label: '评论',
  icon: 'i-lucide-message-square',
  to: '/comments',
  onSelect: () => {
    open.value = false
  }
}, {
  label: '摘要',
  icon: 'i-lucide-layers',
  to: '/digest',
  onSelect: () => {
    open.value = false
  }
}, {
  label: '支付',
  icon: 'i-lucide-credit-card',
  to: '/payments',
  onSelect: () => {
    open.value = false
  }
}, {
  label: '能力演示',
  icon: 'i-lucide-flask-conical',
  to: '/demo',
  onSelect: () => {
    open.value = false
  }
}, {
  label: '平台',
  icon: 'i-lucide-activity',
  to: '/platform',
  onSelect: () => {
    open.value = false
  }
}, {
  label: '设置',
  to: '/settings',
  icon: 'i-lucide-settings',
  defaultOpen: true,
  type: 'trigger',
  children: [{
    label: '账户',
    to: '/settings',
    exact: true,
    onSelect: () => {
      open.value = false
    }
  }, {
    label: '安全',
    to: '/settings/security',
    onSelect: () => {
      open.value = false
    }
  }]
}]] satisfies NavigationMenuItem[][]

const groups = computed(() => [{
  id: 'links',
  label: '前往',
  items: links.flat()
}])

useDashboard()
</script>

<template>
  <UDashboardGroup unit="rem">
    <UDashboardSidebar
      id="default"
      v-model:open="open"
      collapsible
      resizable
      class="bg-elevated/25"
      :ui="{ footer: 'lg:border-t lg:border-default' }"
    >
      <template #header="{ collapsed }">
        <TeamsMenu :collapsed="collapsed" />
      </template>

      <template #default="{ collapsed }">
        <UDashboardSearchButton :collapsed="collapsed" class="bg-transparent ring-default" />

        <UNavigationMenu
          :collapsed="collapsed"
          :items="links[0]"
          orientation="vertical"
          tooltip
          popover
        />
      </template>

      <template #footer="{ collapsed }">
        <UserMenu :collapsed="collapsed" />
      </template>
    </UDashboardSidebar>

    <UDashboardSearch :groups="groups" />

    <slot />
  </UDashboardGroup>
</template>
