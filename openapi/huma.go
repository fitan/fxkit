package openapi

import (
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

// FromHuma 返回 Huma API 实时 OpenAPI 3.1 文档的 YAML 字节。
// 需要 3.0（oapi-codegen）或 JSON 时用 [Encode]。
func FromHuma(api huma.API) ([]byte, error) {
	if api == nil {
		return nil, nil
	}
	b, err := api.OpenAPI().YAML()
	if err != nil {
		return nil, fmt.Errorf("huma openapi: %w", err)
	}
	return b, nil
}

// MergeBaseHuma 将可选的 base OpenAPI YAML 与实时 Huma 文档合并。
// base 为空 → 仅 Huma；Huma 为 nil/空 → 仅 base。
func MergeBaseHuma(baseYAML []byte, api huma.API) ([]byte, error) {
	humaYAML, err := FromHuma(api)
	if err != nil {
		return nil, err
	}
	if len(humaYAML) == 0 {
		return baseYAML, nil
	}
	if len(baseYAML) == 0 {
		return humaYAML, nil
	}
	return MergeYAML(baseYAML, humaYAML)
}
