package openapi

import (
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

// FromHuma 返回 Huma API 实时 OpenAPI 文档的 YAML 字节。
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

// MergeBaseHuma merges an optional base OpenAPI YAML with the live Huma document.
// Empty base → Huma only; nil/empty Huma → base only.
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
