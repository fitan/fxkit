import type { H3Event } from 'h3'

export function catalogBase(event: H3Event): string {
  return useRuntimeConfig(event).catalogBase as string
}

export function storefrontBase(event: H3Event): string {
  return useRuntimeConfig(event).storefrontBase as string
}

export function proxyToFxkit(event: H3Event, base: string) {
  // 浏览器请求 /api/articles?... → 上游 /articles?...
  const path = event.path.replace(/^\/api/, '')
  return proxyRequest(event, `${base}${path}`)
}
