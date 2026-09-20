package mcpx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ServerConfig 定义 MCP Server 的配置参数。
type ServerConfig struct {
	Name       string            // 服务名称 (例如 "catalog-service")
	Version    string            // 服务版本 (例如 "1.0.0")
	BaseURL    string            // 后端微服务的 HTTP 基地址 (例如 "http://127.0.0.1:8080")
	Headers    map[string]string // 转发请求时附带的请求头 (如 Authorization, X-API-Key 等)
	HTTPClient *http.Client      // 用于转发下游调用的 HTTP 客户端
}

// Server 是 Model Context Protocol (MCP) 服务器。
type Server struct {
	cfg     ServerConfig
	toolsMu sync.RWMutex
	tools   map[string]Tool
}

// NewServer 创建一个新的 MCP Server 实例。
func NewServer(cfg ServerConfig, tools []Tool) *Server {
	if cfg.Name == "" {
		cfg.Name = "fxkit-mcp-server"
	}
	if cfg.Version == "" {
		cfg.Version = "1.0.0"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	s := &Server{
		cfg:   cfg,
		tools: make(map[string]Tool, len(tools)),
	}
	for _, t := range tools {
		s.tools[t.Name] = t
	}
	return s
}

// RegisterTool 动态注册或更新工具。
func (s *Server) RegisterTool(tool Tool) {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	s.tools[tool.Name] = tool
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

// JSON-RPC 2.0 数据结构
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// HandleMessage 处理单条 JSON-RPC 请求并返回响应。如果属于通知（无 ID），则返回 nil。
func (s *Server) HandleMessage(ctx context.Context, msg []byte) *jsonRPCResponse {
	var req jsonRPCRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &jsonRPCError{Code: -32700, Message: "Parse error: " + err.Error()},
		}
	}

	// 若没有 ID，视为单向 Notification，无需响应
	isNotification := req.ID == nil

	switch req.Method {
	case "initialize":
		res := map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    s.cfg.Name,
				"version": s.cfg.Version,
			},
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
			},
		}
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: res}

	case "notifications/initialized":
		return nil

	case "ping":
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		s.toolsMu.RLock()
		toolsList := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			toolsList = append(toolsList, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		s.toolsMu.RUnlock()

		if isNotification {
			return nil
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": toolsList,
			},
		}

	case "tools/call":
		var callParams struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			if isNotification {
				return nil
			}
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "Invalid params: " + err.Error()},
			}
		}

		resultText, isErr := s.executeToolCall(ctx, callParams.Name, callParams.Arguments)
		content := []map[string]any{
			{
				"type": "text",
				"text": resultText,
			},
		}
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": content,
				"isError": isErr,
			},
		}

	default:
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

// executeToolCall 将 Tool 调用转换为下游 HTTP 请求并返回响应字符串与是否出错。
func (s *Server) executeToolCall(ctx context.Context, name string, args map[string]any) (string, bool) {
	s.toolsMu.RLock()
	tool, exists := s.tools[name]
	s.toolsMu.RUnlock()

	if !exists {
		return fmt.Sprintf("Tool not found: %s", name), true
	}

	if s.cfg.BaseURL == "" {
		return "BaseURL is not configured for MCP server, cannot forward request", true
	}

	// 1. 替换 Path 中的占位符 (例如 /articles/{id})
	targetPath := tool.Path
	usedArgs := make(map[string]bool)

	for k, v := range args {
		placeholder := fmt.Sprintf("{%s}", k)
		if strings.Contains(targetPath, placeholder) {
			targetPath = strings.ReplaceAll(targetPath, placeholder, fmt.Sprintf("%v", v))
			usedArgs[k] = true
		}
	}

	fullURL := s.cfg.BaseURL + targetPath

	// 2. 区分 Method 组装 Query 参数或 JSON Body
	var bodyReader io.Reader
	remainingArgs := make(map[string]any)
	for k, v := range args {
		if !usedArgs[k] {
			remainingArgs[k] = v
		}
	}

	if strings.EqualFold(tool.Method, http.MethodGet) || strings.EqualFold(tool.Method, http.MethodDelete) {
		// GET / DELETE: 将剩余参数拼入 URL Query
		if len(remainingArgs) > 0 {
			parsedURL, err := url.Parse(fullURL)
			if err == nil {
				q := parsedURL.Query()
				for k, v := range remainingArgs {
					q.Set(k, fmt.Sprintf("%v", v))
				}
				parsedURL.RawQuery = q.Encode()
				fullURL = parsedURL.String()
			}
		}
	} else {
		// POST / PUT / PATCH: 将剩余参数作为 JSON Body
		if len(remainingArgs) > 0 {
			b, err := json.Marshal(remainingArgs)
			if err != nil {
				return fmt.Sprintf("Failed to marshal body arguments: %v", err), true
			}
			bodyReader = bytes.NewReader(b)
		}
	}

	// 3. 构建 HTTP 请求
	httpReq, err := http.NewRequestWithContext(ctx, tool.Method, fullURL, bodyReader)
	if err != nil {
		return fmt.Sprintf("Failed to create HTTP request: %v", err), true
	}
	if bodyReader != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")

	// 注入自定义 Header (如 Token)
	for k, v := range s.cfg.Headers {
		httpReq.Header.Set(k, v)
	}

	// 4. 发送请求
	resp, err := s.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Sprintf("HTTP request error: %v", err), true
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("Failed to read response body: %v", err), true
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("HTTP %d Error: %s", resp.StatusCode, strings.TrimSpace(string(respBody))), true
	}

	return string(respBody), false
}

// ServeStdio 在标准输入输出上启动 MCP Server (供 Claude Desktop, Cursor 等 IDE 进程集成)。
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// 允许单行最大 4MB 的 JSON-RPC 消息
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		resp := s.HandleMessage(ctx, line)
		if resp != nil {
			respBytes, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			respBytes = append(respBytes, '\n')
			if _, err := out.Write(respBytes); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}

// HTTPHandler 返回可挂载在现有 HTTP 路由器 (如 chi / net/http) 上的 MCP 处理端点。
func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "MCP endpoint requires POST", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Cannot read request body", http.StatusBadRequest)
			return
		}

		resp := s.HandleMessage(r.Context(), body)
		w.Header().Set("Content-Type", "application/json")
		if resp == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		_ = json.NewEncoder(w).Encode(resp)
	})
}
