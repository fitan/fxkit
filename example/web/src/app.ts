import LogtoClient from "@logto/browser";
import { catalog, storefront, setAccessToken, accessToken, unwrap, CATALOG } from "./api";

const API_RESOURCE = "https://fxkit.showcase/api";
const LOGTO_ENDPOINT = "https://10.170.34.223:3001/";
const LOGTO_APP_ID = "2h3jba3gurf7r3vsvqydb";

const $ = (id: string): HTMLElement => {
  const el = document.getElementById(id);
  if (!el) throw new Error(`#${id} missing`);
  return el;
};

const origin = window.location.origin;
const redirectUri = `${origin}/callback`;
const postLogoutRedirectUri = `${origin}/`;

const logto = new LogtoClient({
  endpoint: LOGTO_ENDPOINT,
  appId: LOGTO_APP_ID,
  resources: [API_RESOURCE],
  scopes: ["openid", "profile", "email", "offline_access"],
});

let bindAdminAttempted = false;

function escapeHtml(s: unknown) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

async function refreshAccessToken() {
  setAccessToken("");
  if (!(await logto.isAuthenticated())) {
    return;
  }
  setAccessToken(await logto.getAccessToken(API_RESOURCE));
}

async function refreshAuthUi() {
  const signedIn = await logto.isAuthenticated();
  $("sign-in").hidden = signedIn;
  $("sign-out").hidden = !signedIn;
  $("bind-admin").hidden = !signedIn;
  const status = $("auth-status");
  if (!signedIn) {
    status.textContent = `未登录 · API 走 X-User。Logto 须登记：${redirectUri}`;
    return;
  }
  try {
    const claims = await logto.getIdTokenClaims();
    const who = claims.email || claims.username || claims.sub;
    status.textContent = `Logto ${who} · sub=${claims.sub}`;
    status.title = JSON.stringify(claims, null, 2);
  } catch (err) {
    status.textContent = `已登录但读 claims 失败：${err instanceof Error ? err.message : err}`;
  }
}

async function bindJwtAdmin() {
  const claims = await logto.getIdTokenClaims();
  const sub = claims.sub;
  if (!sub) throw new Error("id token 没有 sub");
  unwrap(
    await catalog.PUT("/authz/subjects/{id}/roles", {
      params: { path: { id: sub } },
      body: { roles: ["admin"] },
      headers: { "X-User": "alice" },
    }),
  );
  return sub;
}

async function refreshList() {
  const status = $("list-status");
  const ul = $("articles");
  status.textContent = "加载中…";
  try {
    const data = unwrap(
      await catalog.GET("/articles", {
        params: {
          query: { replyWithCount: true, sortBy: "id", sortDirection: "desc", limit: 20 },
        },
      }),
    );
    const items = data.items ?? [];
    status.textContent = `共 ${data.total ?? items.length} 条（经 Traefik ${CATALOG}/articles）`;
    ul.innerHTML = items
      .map(
        (a) =>
          `<li><span>#${a.id} ${escapeHtml(a.title)}</span><span class="muted">${escapeHtml(a.status)} · ${escapeHtml(a.author || "")}</span></li>`,
      )
      .join("");
    if (!items.length) ul.innerHTML = "<li class='muted'>暂无文章</li>";
  } catch (err) {
    const msg = String(err instanceof Error ? err.message : err);
    if (accessToken && /403/.test(msg) && !bindAdminAttempted) {
      bindAdminAttempted = true;
      status.textContent = "JWT 无角色，正在绑定 admin…";
      try {
        const sub = await bindJwtAdmin();
        $("auth-status").textContent = `已把 ${sub} 绑到 admin`;
        await refreshList();
        return;
      } catch (bindErr) {
        status.textContent = `403，自动绑定失败：${bindErr instanceof Error ? bindErr.message : bindErr}`;
        ul.innerHTML = "";
        return;
      }
    }
    status.textContent = msg;
    ul.innerHTML = "";
  }
}

async function pollIndexed(id: number | string) {
  const status = $("list-status");
  const pathId = String(id);
  for (let i = 0; i < 16; i++) {
    try {
      const row = unwrap(await catalog.GET("/articles/{id}", { params: { path: { id: pathId } } }));
      if (row.status === "indexed") {
        status.textContent = `#${id} 已 indexed（outbox → Hatchet → inbox）`;
        await refreshList();
        return;
      }
    } catch {
      /* retry */
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  status.textContent = `#${id} 还没 indexed（看 Hatchet worker / token）`;
}

function bindPageHandlers() {
  $("sign-in").addEventListener("click", () => {
    void logto.signIn(redirectUri);
  });
  $("sign-out").addEventListener("click", () => {
    void logto.signOut(postLogoutRedirectUri);
  });
  $("bind-admin").addEventListener("click", async () => {
    try {
      const sub = await bindJwtAdmin();
      $("auth-status").textContent = `已把 ${sub} 绑到 admin，再拉一次列表`;
      await refreshAccessToken();
      await refreshList();
    } catch (err) {
      $("auth-status").textContent = String(err instanceof Error ? err.message : err);
    }
  });

  $("create").addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      const created = unwrap(
        await catalog.POST("/articles", {
          body: {
            title: ($("title") as HTMLInputElement).value.trim(),
            body: ($("body") as HTMLInputElement).value,
          },
        }),
      );
      ($("title") as HTMLInputElement).value = "";
      ($("body") as HTMLInputElement).value = "";
      await refreshList();
      if (created.id) {
        void pollIndexed(created.id);
      }
    } catch (err) {
      $("list-status").textContent = String(err instanceof Error ? err.message : err);
    }
  });

  $("digest-btn").addEventListener("click", async () => {
    const pre = $("digest");
    pre.textContent = "加载中…";
    try {
      const data = unwrap(await storefront.GET("/digest"));
      pre.textContent = JSON.stringify(data, null, 2);
    } catch (err) {
      pre.textContent = String(err instanceof Error ? err.message : err);
    }
  });

  $("counter-btn").addEventListener("click", async () => {
    const pre = $("hatchet");
    pre.textContent = "加载中…";
    try {
      const data = unwrap(
        await catalog.POST("/counters/{id}", {
          params: { path: { id: "demo" } },
          body: { delta: 2 },
        }),
      );
      pre.textContent = JSON.stringify({ actor: "demo", ...data }, null, 2);
    } catch (err) {
      pre.textContent = String(err instanceof Error ? err.message : err);
    }
  });

  $("heartbeat-btn").addEventListener("click", async () => {
    const pre = $("hatchet");
    pre.textContent = "加载中…";
    try {
      const data = unwrap(await catalog.GET("/heartbeat"));
      pre.textContent = JSON.stringify(data, null, 2);
    } catch (err) {
      pre.textContent = String(err instanceof Error ? err.message : err);
    }
  });

  $("fanout-btn").addEventListener("click", async () => {
    const pre = $("hatchet");
    pre.textContent = "加载中…";
    try {
      const data = unwrap(await catalog.GET("/article-fanout"));
      pre.textContent = JSON.stringify(data, null, 2);
    } catch (err) {
      pre.textContent = String(err instanceof Error ? err.message : err);
    }
  });

  $("probe-btn").addEventListener("click", async () => {
    const pre = $("probes");
    pre.textContent = "加载中…";
    const out: Record<string, unknown> = {};
    try {
      const ping = unwrap(await catalog.GET("/public/ping"));
      out.publicPing = ping;
    } catch (err) {
      out.publicPing = String(err instanceof Error ? err.message : err);
    }
    try {
      out.me = unwrap(await catalog.GET("/me"));
    } catch (err) {
      out.me = String(err instanceof Error ? err.message : err);
    }
    try {
      const echo = await fetch(`${CATALOG}/chi/echo?msg=hi`);
      out.chiEcho = { status: echo.status, body: await echo.json(), chi: echo.headers.get("X-Fxkit-Chi") };
    } catch (err) {
      out.chiEcho = String(err instanceof Error ? err.message : err);
    }
    pre.textContent = JSON.stringify(out, null, 2);
  });
}

async function boot() {
  bindPageHandlers();
  await refreshList();

  const onCallback = window.location.pathname === "/callback" || window.location.pathname === "/callback/";
  if (onCallback) {
    try {
      await logto.handleSignInCallback(window.location.href);
    } catch (err) {
      $("auth-status").textContent = `登录回调失败：${err instanceof Error ? err.message : err}`;
      $("sign-in").hidden = false;
      return;
    }
    history.replaceState({}, "", "/");
  }

  try {
    await refreshAccessToken();
  } catch (err) {
    setAccessToken("");
    $("sign-in").hidden = true;
    $("sign-out").hidden = false;
    $("bind-admin").hidden = false;
    $("auth-status").textContent =
      `已登录但拿不到 access token（Logto 控制台请给应用勾选 API Resource ${API_RESOURCE}）：${err instanceof Error ? err.message : err}`;
    return;
  }
  await refreshAuthUi();
  if (accessToken) {
    await refreshList();
  }
}

boot().catch((err: unknown) => {
  const msg = String(err instanceof Error ? err.message : err);
  const certHint = /fetch|SSL|certificate|CERT|Failed to fetch|NetworkError/i.test(msg)
    ? `无法连接 Logto ${LOGTO_ENDPOINT}。先用同一浏览器打开该地址并信任自签证书，并在控制台配置 CORS。 `
    : "";
  $("auth-status").textContent = certHint + msg;
  $("sign-in").hidden = false;
});
