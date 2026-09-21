package mcpx

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// ServerConfig 定义 MCP Server 的配置参数。
type ServerConfig struct {
	Name       string            // 服务名称 (例如 "catalog-service")
	Version    string            // 服务版本 (例如 "1.0.0")
	Handler    http.Handler      // 进程内直接分发请求的 HTTP Handler (如 chi.Mux，0 网络开销原生直通 Huma)
	Headers    map[string]string // 转发请求时附带的请求头 (如 Authorization, X-API-Key 等)
	Dispatcher Dispatcher        // 自定义调度器 (若未指定则自动基于 Handler 构造 InProcessDispatcher)
}

// Server 是基于 github.com/mark3labs/mcp-go 标准库构建的 Model Context Protocol 服务器。
type Server struct {
	cfg        ServerConfig
	dispatcher Dispatcher
	mcpServer  *mcpserver.MCPServer
	toolsMu    sync.RWMutex
	tools      map[string]Tool
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

	var disp Dispatcher
	if cfg.Dispatcher != nil {
		disp = cfg.Dispatcher
	} else if cfg.Handler != nil {
		disp = NewInProcessDispatcher(cfg.Handler, cfg.Headers)
	}

	s := &Server{
		cfg:        cfg,
		dispatcher: disp,
		mcpServer:  rawServer,
		tools:      make(map[string]Tool, len(tools)),
	}

	for _, t := range tools {
		s.RegisterTool(t)
	}

	return s
}

// NewInProcessServer 构造一个纯内存直通的 MCP Server (0 网络开销，原生调用当前进程的 chi.Mux / Huma 路由)。
func NewInProcessServer(name string, handler http.Handler, tools []Tool) *Server {
	return NewServer(ServerConfig{
		Name:    name,
		Handler: handler,
	}, tools)
}

// SetDispatcher 动态替换或设置请求分发执行器。
func (s *Server) SetDispatcher(d Dispatcher) {
	s.dispatcher = d
}

// SetHandler 动态绑定进程内 HTTP Handler 并自动配置 InProcessDispatcher。
func (s *Server) SetHandler(h http.Handler) {
	s.cfg.Handler = h
	s.dispatcher = NewInProcessDispatcher(h, s.cfg.Headers)
}

// RegisterTool 动态注册或更新工具，并将其挂载到底层 MCP Server。
func (s *Server) RegisterTool(tool Tool) {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()

	s.tools[tool.Name] = tool
	mcpTool := tool.ToMCP()

	s.mcpServer.AddTool(mcpTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok && req.Params.Arguments != nil {
			b, err := json.Marshal(req.Params.Arguments)
			if err == nil {
				_ = json.Unmarshal(b, &args)
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

// Dispatcher 返回当前配置的执行调度器。
func (s *Server) Dispatcher() Dispatcher {
	return s.dispatcher
}

// executeToolCall 将 Tool 调用委托给底层的 InProcessDispatcher。
func (s *Server) executeToolCall(ctx context.Context, tool Tool, args map[string]any) (string, bool) {
	if s.dispatcher == nil {
		return "No in-process dispatcher configured for MCP server", true
	}

	result, err := s.dispatcher.Dispatch(ctx, tool, args)
	if err != nil {
		return err.Error(), true
	}
	return result, false
}

// ServeStdio 在标准输入输出上启动 MCP Server (供 Claude Desktop, Cursor 等 IDE 作为子进程直连)。
func (s *Server) ServeStdio() error {
	return mcpserver.ServeStdio(s.mcpServer)
}

// SSEServer 返回符合官方规范的 MCP Server-Sent Events (SSE) HTTP 处理器。
// 客户端通过 GET 连接建立 SSE 事件流，通过 POST 发送 RPC 消息。
func (s *Server) SSEServer(opts ...mcpserver.SSEOption) *mcpserver.SSEServer {
	return mcpserver.NewSSEServer(s.mcpServer, opts...)
}
