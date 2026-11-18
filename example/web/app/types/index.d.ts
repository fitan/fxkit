import type { LogtoClient } from '@logto/browser'
import type { Ref } from 'vue'
import type { components as CatalogComponents } from './catalog'
import type { components as StorefrontComponents } from './storefront'

export type Article = CatalogComponents['schemas']['ArticleListRow']
export type ArticleDetail = CatalogComponents['schemas']['ArticleDetail']
export type ArticleListResult = CatalogComponents['schemas']['ListResultArticleListRow']
export type Comment = CatalogComponents['schemas']['CommentRow']
export type CommentListResult = CatalogComponents['schemas']['ListResultCommentRow']
export type Digest = Omit<StorefrontComponents['schemas']['DigestBody'], 'articles'> & {
  articles: ArticleListResult | null
}

export interface ProbeResult {
  status: number
  ok: boolean
  headers: Record<string, string>
  body: unknown
}

export interface LogtoClaims {
  sub?: string
  email?: string
  username?: string
  [key: string]: unknown
}

export interface LogtoApi {
  client: LogtoClient
  authenticated: Ref<boolean>
  claims: Ref<LogtoClaims | null>
  token: Ref<string>
  refresh: () => Promise<void>
  signIn: () => Promise<void>
  signOut: () => Promise<void>
  handleCallback: (url: string) => Promise<void>
  getToken: () => Promise<string>
}
