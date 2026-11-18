// 仅用于 demo：由 BFF 用服务端密钥给渠道回调签名。
// 生产环境不要把 webhook secret 放到前端服务。
async function hmacHex(secret: string, body: string): Promise<string> {
  const enc = new TextEncoder()
  const key = await crypto.subtle.importKey(
    'raw',
    enc.encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  )
  const sig = await crypto.subtle.sign('HMAC', key, enc.encode(body))
  return [...new Uint8Array(sig)]
    .map(b => b.toString(16).padStart(2, '0'))
    .join('')
}

export default defineEventHandler(async (event) => {
  const cfg = useRuntimeConfig(event)
  const payload = await readBody(event)
  const raw = JSON.stringify(payload)
  const ts = Math.floor(Date.now() / 1000).toString()
  const sig = await hmacHex(cfg.paymentsWebhookSecret as string, raw)

  const res = await $fetch.raw(`${catalogBase(event)}/payments/webhook`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Webhook-Signature': sig,
      'X-Webhook-Timestamp': ts
    },
    body: raw,
    ignoreResponseError: true
  })

  return {
    status: res.status,
    body: res._data
  }
})
