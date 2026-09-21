---
name: fxkit-mcp
description: >-
  Expose fxkit Huma/OpenAPI services directly to LLM Agents via in-process
  Model Context Protocol (MCP) and Function Calling without code generation.
  Use when the user asks for MCP server, AI Agent tool calling, Cursor or Claude
  Desktop tool integration, or OpenAPI to Tool Schema conversion.
---

# fxkit MCP (Model Context Protocol) & AI Agent Tool Calling

`mcpx` 基于业界成熟的 `github.com/mark3labs/mcp-go` 专有标准库构建，**仅保留纯内存直通执行器（InProcessDispatcher）**，无需任何代码生成，也无需任何本地网络端口回环。它直接在进程内接入当前微服务注册的全部 Huma 路由，通过内存级请求直接复用 Huma 的原生反序列化与参数校验，以零网络开销向大语言模型暴露标准 MCP Tools。

---

## 1. 核心架构与直通原理

整个链路在**当前进程内纯内存完成（0 网络开销、0 端口监听）**：

```
                    ┌───────────────────────────────┐
                    │      业务注册的 Huma API      │
                    │   (入参结构体 + 校验 + Handler)   │
                    └──────────────┬────────────────┘
                                   │ 运行时自动解析 (无需代码生成)
                                   ▼
                    ┌───────────────────────────────┐
                    │      mcpx.Server 协议核心     │
                    │    (基于 mark3labs/mcp-go)    │
                    └──────────────┬────────────────┘
                                   │
                                   ▼
                    ┌───────────────────────────────┐
                    │  InProcessDispatcher 内存直通 │
                    └──────────────┬────────────────┘
                                   │
              ┌────────────────────┴────────────────────┐
              ▼                                         ▼
   模式 1：命令行原生 Stdio                      模式 2：HTTP / SSE 远程
   `mysvc mcp` (供 Cursor/Claude 直连)          `GET/POST /mcp` (供远程 Agent)
              │                                         │
              └────────────────────┬────────────────────┘
                                   │ 内存直调 (0 网络开销)
                                   ▼
                    ┌───────────────────────────────┐
                    │     当前进程 chi.Mux 路由     │
                    │  (自动由 Huma 原生反序列化     │
                    │   Path/Query/Body 并执行业务)  │
                    └───────────────────────────────┘
```

- **0 冗余工程与代码生成**：写好常规 Huma 业务路由，即天然拥有 MCP 工具支持；
- **0 网络开销与端口占用**：`tools/call` 触发时直接调用内存中的 Handler，纳秒级响应，杜绝连接拒绝、超时与防火墙阻断；
- **原生复用校验**：Huma 的结构体 tag（`path`、`query`、`json`、`minLength`、`maximum` 等）全部原生生效，校验失败直接向大模型返回结构化 422 错误供其自我纠正。

---

## 2. 方式 A：通过命令行原生 Stdio 启动（供 Claude Desktop / Cursor 直连）

任何基于 `fxkit.Run(...)` 的微服务，天然内置了 `mcp` 子命令：

```bash
# 启动 MCP Stdio 模式 (不占用外部 HTTP 端口，日志自动隔离至 stderr)
./bin/catalog mcp --config configs/config.yaml
```

### Claude Desktop / Cursor 配置
在 `claude_desktop_config.json` 中直接配置服务二进制即可：
```json
{
  "mcpServers": {
    "catalog": {
      "command": "/path/to/catalog",
      "args": ["mcp", "--config", "/path/to/configs/config.yaml"]
    }
  }
}
```

---

## 3. 方式 B：通过 HTTP / SSE 向远程 Agent 提供服务

在服务的 `config.yaml` 中增加配置：
```yaml
mcp:
  enabled: true
  name: "catalog-service"
  path: "/mcp" # 默认 /mcp
```

当服务以常规 `serve` 命令运行时，将自动在 `/mcp` 端点挂载符合官方规范的 SSE 双向事件流，内部同样通过 `InProcessDispatcher` 内存直通。

---

## 4. 直接转译为三大主流 LLM 原生 Function Calling Schema

若您在 Go 进程中自研 AI Agent，可直接调用 `mcpx` 导出的转换器将 Huma API 转换为各厂商原生格式：

```go
tools, err := mcpx.ParseHuma(api)

// 1. OpenAI Function Calling:
openaiTools := mcpx.ToOpenAI(tools)

// 2. Anthropic Claude Tool Use:
claudeTools := mcpx.ToClaude(tools)

// 3. Google Gemini Function Declarations:
geminiTools := mcpx.ToGemini(tools)
```
