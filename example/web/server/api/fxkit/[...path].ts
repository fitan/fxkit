// 通用 catalog 代理：/api/fxkit/version → catalog /version
// 用于平台探针、authz 管理、OpenAPI 等不在专用 BFF 路由里的端点。
export default defineEventHandler((event) => {
  const path = event.path.replace(/^\/api\/fxkit/, '')
  return proxyRequest(event, `${catalogBase(event)}${path}`)
})
