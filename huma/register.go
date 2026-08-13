package huma

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// Register 类似 [huma.Register]，但用 [AsError] 转换返回的 error。
func Register[I, O any](api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	huma.Register(api, op, func(ctx context.Context, input *I) (*O, error) {
		out, err := handler(ctx, input)
		return out, AsError(err)
	})
}
