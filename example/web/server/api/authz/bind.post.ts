export default defineEventHandler(async (event) => {
  const body = await readBody<{ sub?: string }>(event)
  const sub = body?.sub?.trim()
  if (!sub) {
    setResponseStatus(event, 400)
    return { message: 'sub is required' }
  }

  // 用 dev admin（alice）把 Logto 的 sub 绑定到 admin，避免首次登录 403。
  const res = await $fetch.raw(`${catalogBase(event)}/authz/subjects/${encodeURIComponent(sub)}/roles`, {
    method: 'PUT',
    headers: {
      'X-User': 'alice',
      'Content-Type': 'application/json'
    },
    body: { roles: ['admin'] },
    ignoreResponseError: true
  })

  setResponseStatus(event, res.status)
  return res._data
})
