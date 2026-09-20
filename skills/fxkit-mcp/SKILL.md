---
name: fxkit-mcp
description: >-
  Expose fxkit Huma/OpenAPI services to LLM Agents via Model Context Protocol
  (MCP) and Function Calling (OpenAI, Claude, Gemini). Use when the user asks
  for MCP server, fxkit gen mcp, AI Agent tool calling, Cursor or Claude Desktop
  tool integration, or OpenAPI to Tool Schema conversion.
---

# fxkit MCP (Model Context Protocol) & AI Agent Tool Calling

`mcpx` 将 `fxkit` 服务的 OpenAPI 3.0 / 3.1 契约自动转译为大语言模型（LLM）的 Tool Schema，并提供标准 MCP JSON-RPC 2.0 协议服务器。

## 1. 一键生成独立 MCP Server (`fxkit gen mcp`)

从服务导出的 OpenAPI 规范生成开箱即用的 MCP 服务器：

```bash
# 1. 导出服务的 OpenAPI Spec
services/catalog openapi -o /tmp/catalog.openapi.yaml

# 2. 生成独立的 MCP Server 工程
fxkit gen mcp \
  --spec /tmp/catalog.openapi.yaml \
  --out ./cmd/catalog-mcp \
  --base-url http://127.0.0.1:8080
```

生成的命令支持两种标准传输模式：
- **Stdio 模式（默认）**：供 Claude Desktop、Cursor、Cline 等作为本地子进程直连；
- **HTTP 模式**：`./catalog-mcp --transport http --port 9090`，以标准 JSON-RPC 2.0 提供远程服务。

### Claude Desktop 配置 (`claude_desktop_config.json`)
生成的工程会自带配置片段：
```json
{
  "mcpServers": {
    "catalog": {
      "command": "/path/to/catalog-mcp",
      "args": ["--base-url", "http://127.0.0.1:8080"]
    }
  }
}
```

## 2. 运行时嵌入现有服务 (`mcpx.Module`)

只需在服务的 `fxkit.Run(...)` 中引入 `mcpx.Module`，即可自动解析 Huma 路由并在 `/mcp` 端点上暴露 MCP 协议：

```go
fxkit.Run(
    fxkit.Default(),
    huma.Module,
    mcpx.Module, // 自动提供 *mcpx.Server 并在 /mcp 挂载 HTTP JSON-RPC 处理端点
    article.Module,
)
```

在 `config.yaml` 中配置：
```yaml
mcp:
  enabled: true
  name: "catalog-service"
  path: "/mcp"
  base_url: "http://127.0.0.1:8080"
```

## 3. 直接转译为三大主流 LLM Function Calling Schema

若在 Go 代码中自研 Agent，可直接使用 `mcpx` 导出的适配器：

```go
tools, err := mcpx.ParseOpenAPI(specBytes)
// 或直接从运行时提取：
// tools, err := mcpx.ParseHuma(api)

// 1. OpenAI Function Calling:
openaiTools := mcpx.ToOpenAI(tools)

// 2. Anthropic Claude Tool Use:
claudeTools := mcpx.ToClaude(tools)

// 3. Google Gemini Function Declarations:
geminiTools := mcpx.ToGemini(tools)
```
