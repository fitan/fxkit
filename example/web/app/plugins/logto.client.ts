import LogtoClient from '@logto/browser'
import type { LogtoClaims } from '~/types'

export default defineNuxtPlugin(() => {
  const config = useRuntimeConfig()
  const resource = config.public.apiResource

  const client = new LogtoClient({
    endpoint: config.public.logtoEndpoint,
    appId: config.public.logtoAppId,
    resources: [resource],
    scopes: ['openid', 'profile', 'email', 'offline_access']
  })

  const authenticated = useState<boolean>('logto-authenticated', () => false)
  const claims = useState<LogtoClaims | null>('logto-claims', () => null)
  const token = useState<string>('logto-token', () => '')

  async function refresh() {
    try {
      const ok = await client.isAuthenticated()
      authenticated.value = ok
      if (!ok) {
        claims.value = null
        token.value = ''
        return
      }
      claims.value = await client.getIdTokenClaims() as unknown as LogtoClaims
      try {
        token.value = await client.getAccessToken(resource)
      } catch (error) {
        // 已登录但没勾选 API resource：保留登录态，让用户在 Settings 里看到原因。
        token.value = ''
        console.warn('[logto] getAccessToken failed', error)
      }
    } catch (error) {
      console.warn('[logto] refresh failed', error)
      authenticated.value = false
      claims.value = null
      token.value = ''
    }
  }

  async function signIn() {
    await client.signIn(`${window.location.origin}/callback`)
  }

  async function signOut() {
    await client.signOut(`${window.location.origin}/`)
  }

  async function handleCallback(url: string) {
    await client.handleSignInCallback(url)
    await refresh()
  }

  async function getToken() {
    if (!authenticated.value) {
      return ''
    }
    try {
      token.value = await client.getAccessToken(resource)
    } catch (error) {
      console.warn('[logto] getAccessToken failed', error)
    }
    return token.value
  }

  // 不阻塞首屏：登录状态就绪后通过 useState 响应式更新。
  void refresh()

  return {
    provide: {
      logto: { client, authenticated, claims, token, refresh, signIn, signOut, handleCallback, getToken }
    }
  }
})
