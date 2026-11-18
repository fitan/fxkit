import createClient, { type Middleware } from 'openapi-fetch'
import type { LogtoApi, ProbeResult } from '~/types'
import type { paths as CatalogPaths } from '~/types/catalog'
import type { paths as StorefrontPaths } from '~/types/storefront'

export interface ProbeOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: Record<string, unknown>
  query?: Record<string, string | number | boolean | undefined>
  headers?: HeadersInit
}

function logtoApi(): LogtoApi | undefined {
  return (useNuxtApp() as unknown as { $logto?: LogtoApi }).$logto
}

export function useDevUser() {
  const cookie = useCookie<string>('fxkit-dev-user', {
    default: () => 'alice',
    sameSite: 'lax'
  })
  const user = useState<string>('fxkit-dev-user', () => cookie.value || 'alice')

  watch(user, (value) => {
    cookie.value = value || 'alice'
  })

  return user
}

export function useLogtoState() {
  return {
    logto: logtoApi(),
    authenticated: useState<boolean>('logto-authenticated', () => false),
    claims: useState<Record<string, unknown> | null>('logto-claims', () => null),
    token: useState<string>('logto-token', () => '')
  }
}

export function errorMessage(err: unknown): string {
  if (err && typeof err === 'object') {
    const data = (err as { data?: { detail?: string, message?: string, title?: string } }).data
    if (data?.detail) return data.detail
    if (data?.message) return data.message
    if (data?.title) return data.title

    const direct = err as { detail?: string, message?: string, title?: string }
    if (direct.detail) return direct.detail
    if (direct.message) return direct.message
    if (direct.title) return direct.title
  }
  return String(err)
}

export function useFxkitClients() {
  const devUser = useDevUser()
  // 在 setup 阶段捕获，避免在事件回调里再次调用 useNuxtApp()。
  const logto = import.meta.client ? logtoApi() : undefined

  async function authHeaders(): Promise<Record<string, string>> {
    const token = logto ? await logto.getToken() : ''
    if (token) {
      return { Authorization: `Bearer ${token}` }
    }
    return { 'X-User': devUser.value || 'alice' }
  }

  const auth: Middleware = {
    async onRequest({ request }) {
      for (const [key, value] of Object.entries(await authHeaders())) {
        request.headers.set(key, value)
      }
      return request
    }
  }

  const catalog = createClient<CatalogPaths>({ baseUrl: '/api' })
  const storefront = createClient<StorefrontPaths>({ baseUrl: '/api' })
  const catalogAdmin = createClient<CatalogPaths>({ baseUrl: '/api/fxkit' })

  catalog.use(auth)
  storefront.use(auth)
  catalogAdmin.use(auth)

  async function probe(path: string, options: ProbeOptions = {}): Promise<ProbeResult> {
    const headers = new Headers(options.headers)
    for (const [key, value] of Object.entries(await authHeaders())) {
      headers.set(key, value)
    }

    const res = await $fetch.raw(path, {
      method: options.method ?? 'GET',
      body: options.body,
      query: options.query,
      headers,
      ignoreResponseError: true
    })

    let body: unknown = res._data
    if (typeof body === 'string') {
      try {
        body = JSON.parse(body)
      } catch {
        // 保留纯文本（/healthz、/docs 等）
      }
    }

    return {
      status: res.status,
      ok: res.ok,
      headers: Object.fromEntries(res.headers.entries()),
      body
    }
  }

  return { catalog, storefront, catalogAdmin, devUser, probe }
}

export async function bindFxkitAdmin(sub: string) {
  return await $fetch('/api/authz/bind', {
    method: 'POST',
    body: { sub }
  })
}
