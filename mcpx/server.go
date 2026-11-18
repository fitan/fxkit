package mcpx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// ServerConfig 定义 MCP Server 的配置参数。
type ServerConfig struct {
	Name    string            // 服务名称 (例如 "catalog-service")
	Version string            // 服务版本 (例如 "1.0.0")
	Handler http.Handler      // 进程内直接分发请求的 HTTP Handler (如 chi.Mux，0 网络开销原生直通 Huma)
	Headers map[string]string // 转发请求时附带的请求头 (如 Authorization, X-API-Key 等)
	// MaxResponseBytes 限制单次 tool 调用返回的字节数（避免撑爆模型上下文）。
	// 0 = 默认 1 MiB；负数 = 不限制。
	MaxResponseBytes int
}

// Server 是基于 github.com/mark3labs/mcp-go 标准库构建的 Model Context Protocol 服务器。
type Server struct {
	cfg       ServerConfig
	disp      *InProcessDispatcher
	mcpServer *mcpserver.MCPServer
	toolsMu   sync.RWMutex
	tools     map[string]Tool
	maxResp   int
}

// NewServer 创建并初始化一个 MCP Server 实例。
func NewServer(cfg ServerConfig, tools []Tool) *Server {
	if cfg.Name == "" {
		cfg.Name = "fxkit-mcp-server"
	}
	if cfg.Version == "" {
		cfg.Version = "1.0.0"
	}

	rawServer := mcpserver.NewMCPServer(
		cfg.Name,
		cfg.Version,
		mcpserver.WithToolCapabilities(true),
	)

	var disp *InProcessDispatcher
	if cfg.Handler != nil {
		disp = NewInProcessDispatcher(cfg.Handler, cfg.Headers)
	}

	s := &Server{
		cfg:       cfg,
		disp:      disp,
		mcpServer: rawServer,
		tools:     make(map[string]Tool, len(tools)),
		maxResp:   cfg.MaxResponseBytes,
	}
	if s.maxResp == 0 {
		s.maxResp = 1 << 20 // 1 MiB
	}

	for _, t := range tools {
		s.RegisterTool(t)
	}

	return s
}

// RegisterTool 动态注册或更新工具，并将其挂载到底层 MCP Server。
func (s *Server) RegisterTool(tool Tool) {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()

	if _, exists := s.tools[tool.Name]; exists {
		slog.Warn("mcpx: duplicate tool name, overwriting", "tool", tool.Name)
	}
	s.tools[tool.Name] = tool
	mcpTool := tool.ToMCP()

	s.mcpServer.AddTool(mcpTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok && req.Params.Arguments != nil {
			b, err := json.Marshal(req.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError("invalid tool arguments: " + err.Error()), nil
			}
			if err := json.Unmarshal(b, &args); err != nil {
				// 不要静默降级为“空参数执行”——那会用一个零值请求打真实接口。
				return mcp.NewToolResultError("invalid tool arguments: " + err.Error()), nil
			}
		}
		if args == nil {
			args = make(map[string]any)
		}

		resultText, isErr := s.executeToolCall(ctx, tool, args)
		if isErr {
			return mcp.NewToolResultError(resultText), nil
		}
		return mcp.NewToolResultText(resultText), nil
	})
}

// Tools 返回当前所有工具列表。
func (s *Server) Tools() []Tool {
	s.toolsMu.RLock()
	defer s.toolsMu.RUnlock()
	out := make([]Tool, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	return out
}

// MCPServer 返回底层 github.com/mark3labs/mcp-go 的 MCPServer 实例。
func (s *Server) MCPServer() *mcpserver.MCPServer {
	return s.mcpServer
}

// executeToolCall 将 Tool 调用委托给进程内直通执行器。
func (s *Server) executeToolCall(ctx context.Context, tool Tool, args map[string]any) (string, bool) {
	if s.disp == nil {
		recordToolCall(ctx, tool.Name, 0, true)
		return "mcpx: no HTTP handler configured for the MCP server", true
	}

	start := time.Now()
	ctx, span := startToolSpan(ctx, tool)
	result, err := s.disp.Dispatch(ctx, tool, args)
	recordToolCall(ctx, tool.Name, time.Since(start), err != nil)
	endToolSpan(span, err)
	if err != nil {
		return err.Error(), true
	}
	if s.maxResp > 0 && len(result) > s.maxResp {
		orig := len(result)
		// 按字节截断可能切断 UTF-8 多字节字符，产生非法字符串
		// （JSON 编码时会被替换成 U+FFFD）。回退到最近的字符边界。
		cut := s.maxResp
		for cut > 0 && !utf8.RuneStart(result[cut]) {
			cut--
		}
		result = result[:cut] + fmt.Sprintf("\n…(truncated, %d bytes total)", orig)
	}
	return result, false
}

// identityHeadersKey 携带 MCP 调用者身份（从入站 HTTP header / stdio env 注入）。
type identityHeadersKey struct{}

var identityHeaderNames = []string{"Authorization", "X-User", "X-Org"}

func withIdentityHeaders(ctx context.Context, h http.Header) context.Context {
	if len(h) == 0 {
		return ctx
	}
	return context.WithValue(ctx, identityHeadersKey{}, h)
}

func identityHeadersFromContext(ctx context.Context) http.Header {
	h, _ := ctx.Value(identityHeadersKey{}).(http.Header)
	return h
}

func identityContextFromRequest(ctx context.Context, r *http.Request) context.Context {
	h := http.Header{}
	for _, k := range identityHeaderNames {
		if v := strings.TrimSpace(r.Header.Get(k)); v != "" {
			h.Set(k, v)
		}
	}
	return withIdentityHeaders(ctx, h)
}

func stdioIdentityContext(ctx context.Context) context.Context {
	h := http.Header{}
	for _, kv := range []struct{ env, header string }{
		{"MCP_AUTHORIZATION", "Authorization"},
		{"MCP_USER", "X-User"},
		{"MCP_ORG", "X-Org"},
	} {
		if v := strings.TrimSpace(os.Getenv(kv.env)); v != "" {
			h.Set(kv.header, v)
		}
	}
	return withIdentityHeaders(ctx, h)
}

// ServeStdio 在标准输入输出上启动 MCP Server (Claude Desktop / Cursor 作为子进程直连)。
//
// 身份可通过 MCP_AUTHORIZATION / MCP_USER / MCP_ORG 环境变量注入；
// 未提供时，走 Casbin 的调用会被拒绝（除非 authz 关闭）。
func (s *Server) ServeStdio() error {
	return mcpserver.ServeStdio(s.mcpServer, mcpserver.WithStdioContextFunc(stdioIdentityContext))
}

// SSEServer 返回符合官方规范的 MCP Server-Sent Events (SSE) HTTP 处理器。
// 客户端通过 GET 连接建立 SSE 事件流，通过 POST 发送 RPC 消息。
//
// 入站 Authorization / X-User / X-Org 会注入 tool handler 的 context，
// 并透传到进程内 HTTP 请求，供 authz（Casbin）按调用者身份授权。
func (s *Server) SSEServer(opts ...mcpserver.SSEOption) *mcpserver.SSEServer {
	opts = append([]mcpserver.SSEOption{mcpserver.WithSSEContextFunc(identityContextFromRequest)}, opts...)
	return mcpserver.NewSSEServer(s.mcpServer, opts...)
}
