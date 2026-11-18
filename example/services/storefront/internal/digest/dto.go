package digest

import "encoding/json"

// DigestInput 是 /digest 的查询参数；Limit 默认 10、最大 100。
type DigestInput struct {
	// Limit 是拉取文章数量，默认 10、最大 100。
	Limit int `query:"limit" default:"10"`
}

// DigestOutput 是 Huma 的响应包装。
type DigestOutput struct {
	// Body 是响应体。
	Body DigestBody
}

// DigestBody 保留上游原始 JSON，避免 storefront 与 catalog 的类型漂移。
type DigestBody struct {
	// Source 是上游服务名。
	Source string `json:"source"`
	// Via 是调用方式（reqx.NewFromFactory）。
	Via string `json:"via"`
	// Endpoints 是当前解析到的上游端点。
	Endpoints []string `json:"endpoints"`
	// Articles 是上游原始 JSON。
	Articles json.RawMessage `json:"articles"`
}
