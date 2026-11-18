<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'

defineProps<{
  collapsed?: boolean
}>()

const colorMode = useColorMode()
const appConfig = useAppConfig()
const toast = useToast()
const { logto, authenticated, claims } = useLogtoState()
const devUser = useDevUser()

const colors = ['red', 'orange', 'amber', 'yellow', 'lime', 'green', 'emerald', 'teal', 'cyan', 'sky', 'blue', 'indigo', 'violet', 'purple', 'fuchsia', 'pink', 'rose']
const neutrals = ['slate', 'gray', 'zinc', 'neutral', 'stone', 'taupe', 'mauve', 'mist', 'olive']

const label = computed(() => {
  if (authenticated.value) {
    return String(claims.value?.email || claims.value?.username || claims.value?.sub || 'Logto 用户')
  }
  return `X-User: ${devUser.value}`
})

async function signIn() {
  await logto?.signIn()
}

async function signOut() {
  await logto?.signOut()
}

async function bindAdmin() {
  const sub = claims.value?.sub
  if (typeof sub !== 'string' || !sub) return
  try {
    await bindFxkitAdmin(sub)
    toast.add({ title: '已绑定管理员', description: sub, color: 'success' })
    await refreshNuxtData()
  } catch (err: unknown) {
    toast.add({
      title: '绑定失败',
      description: errorMessage(err),
      color: 'error'
    })
  }
}

const items = computed<DropdownMenuItem[][]>(() => {
  const account: DropdownMenuItem[] = authenticated.value
    ? [{
        type: 'label',
        label: label.value
      }, {
        label: '绑定 JWT sub 到管理员',
        icon: 'i-lucide-shield-check',
        onSelect: () => { void bindAdmin() }
      }, {
        label: '退出登录',
        icon: 'i-lucide-log-out',
        onSelect: () => { void signOut() }
      }]
    : [{
        type: 'label',
        label: label.value
      }, {
        label: '使用 Logto 登录',
        icon: 'i-lucide-log-in',
        onSelect: () => { void signIn() }
      }, {
        label: '开发用户',
        icon: 'i-lucide-user-cog',
        children: ['alice', 'bob', 'charlie'].map(user => ({
          label: user,
          type: 'checkbox' as const,
          checked: devUser.value === user,
          onSelect: (e: Event) => {
            e.preventDefault()
            devUser.value = user
            void refreshNuxtData()
          }
        }))
      }]

  return [
    account,
    [{
      label: '设置',
      icon: 'i-lucide-settings',
      to: '/settings'
    }],
    [{
      label: '主题',
      icon: 'i-lucide-palette',
      children: [{
        label: '主色',
        slot: 'chip',
        chip: appConfig.ui.colors.primary,
        content: {
          align: 'center',
          collisionPadding: 16
        },
        children: colors.map(color => ({
          label: color,
          chip: color,
          slot: 'chip',
          checked: appConfig.ui.colors.primary === color,
          type: 'checkbox',
          onSelect: (e) => {
            e.preventDefault()
            appConfig.ui.colors.primary = color
          }
        }))
      }, {
        label: '中性色',
        slot: 'chip',
        chip: appConfig.ui.colors.neutral === 'neutral' ? 'old-neutral' : appConfig.ui.colors.neutral,
        content: {
          align: 'end',
          collisionPadding: 16
        },
        children: neutrals.map(color => ({
          label: color,
          chip: color === 'neutral' ? 'old-neutral' : color,
          slot: 'chip',
          type: 'checkbox',
          checked: appConfig.ui.colors.neutral === color,
          onSelect: (e) => {
            e.preventDefault()
            appConfig.ui.colors.neutral = color
          }
        }))
      }, {
        label: '外观',
        icon: 'i-lucide-sun-moon',
        children: [{
          label: '浅色',
          icon: 'i-lucide-sun',
          type: 'checkbox',
          checked: colorMode.value === 'light',
          onSelect(e: Event) {
            e.preventDefault()
            colorMode.preference = 'light'
          }
        }, {
          label: '深色',
          icon: 'i-lucide-moon',
          type: 'checkbox',
          checked: colorMode.value === 'dark',
          onSelect(e: Event) {
            e.preventDefault()
            colorMode.preference = 'dark'
          }
        }]
      }]
    }]
  ]
})
</script>

<template>
  <UDropdownMenu
    :items="items"
    :content="{ align: 'center', collisionPadding: 12 }"
    :ui="{ content: collapsed ? 'w-48' : 'w-(--reka-dropdown-menu-trigger-width)' }"
  >
    <UButton
      :label="collapsed ? undefined : label"
      :icon="authenticated ? 'i-lucide-shield-check' : 'i-lucide-user'"
      color="neutral"
      variant="ghost"
      block
      :square="collapsed"
      class="data-[state=open]:bg-elevated"
    />

    <template #chip-leading="{ item }">
      <div class="inline-flex items-center justify-center shrink-0 size-5">
        <span
          class="rounded-full ring ring-bg bg-(--chip-light) dark:bg-(--chip-dark) size-2"
          :style="{
            '--chip-light': `var(--color-${(item as any).chip}-500)`,
            '--chip-dark': `var(--color-${(item as any).chip}-400)`
          }"
        />
      </div>
    </template>
  </UDropdownMenu>
</template>
