import createClient, { type Middleware } from "openapi-fetch";
import type { paths as CatalogPaths } from "./catalog";
import type { paths as StorefrontPaths } from "./storefront";

export const CATALOG = "/catalog";
export const STOREFRONT = "/storefront";

export let accessToken = "";

export function setAccessToken(token: string) {
  accessToken = token;
}

function devUser(): string {
  const el = document.getElementById("user") as HTMLInputElement | null;
  return el?.value.trim() || "alice";
}

const auth: Middleware = {
  async onRequest({ request }) {
    const headers = new Headers(request.headers);
    if (headers.has("X-User")) {
      headers.delete("Authorization");
      return new Request(request, { headers });
    }
    if (accessToken) {
      headers.set("Authorization", `Bearer ${accessToken}`);
    } else {
      headers.set("X-User", devUser());
    }
    return new Request(request, { headers });
  },
};

export const catalog = createClient<CatalogPaths>({ baseUrl: CATALOG });
export const storefront = createClient<StorefrontPaths>({ baseUrl: STOREFRONT });
catalog.use(auth);
storefront.use(auth);

type Problem = { detail?: string; title?: string; message?: string };

export function unwrap<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.response.ok && res.data !== undefined) {
    return res.data;
  }
  const err = res.error as Problem | undefined;
  const msg = err?.detail || err?.title || err?.message || res.response.statusText || "request failed";
  throw new Error(`${res.response.status} ${msg}`);
}
