// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  modules: [
    '@nuxt/eslint',
    '@nuxt/ui',
    '@vueuse/nuxt'
  ],

  devtools: {
    enabled: true
  },

  css: ['~/assets/css/main.css'],

  runtimeConfig: {
    // BFF 直接连 Go 服务；经 Traefik 时改成 http://127.0.0.1:8880/catalog 等。
    catalogBase: 'http://127.0.0.1:8081',
    storefrontBase: 'http://127.0.0.1:8082',
    // 仅供 demo 的 webhook 模拟使用；可用 NUXT_PAYMENTS_WEBHOOK_SECRET 覆盖。
    // 生产不要把渠道密钥放在前端服务。
    paymentsWebhookSecret: 'dev-webhook-secret',
    public: {
      apiResource: 'https://fxkit.showcase/api',
      logtoEndpoint: 'https://10.170.34.223:3001/',
      logtoAppId: '2h3jba3gurf7r3vsvqydb'
    }
  },

  routeRules: {
    '/api/**': {
      cors: true
    }
  },

  compatibilityDate: '2026-06-30',

  eslint: {
    config: {
      stylistic: {
        commaDangle: 'never',
        braceStyle: '1tbs'
      }
    }
  }
})
