package huma

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/fxerrors"
)

// AsError 将任意 error 映射为 huma 状态错误。
//
// *fxerrors.Error 使用其 kind/status；context 取消/超时由 [fxerrors.Wrap] 映射；
// 其余错误统一 500 且**记录原始错误**——否则线上 500 既不在响应里、也不在日志里。
func AsError(err error) error {
	if err == nil {
		return nil
	}

	fe := fxerrors.Wrap(err)

	var detailErrs []error
	if len(fe.Details) > 0 {
		detailErrs = append(detailErrs, &huma.ErrorDetail{
			Message: string(fe.Kind),
			Value:   fe.Details,
		})
	}

	if fe.Kind == fxerrors.KindInternal {
		slog.Error("huma: internal error", "error", err)
		return huma.NewError(http.StatusInternalServerError, "internal error", detailErrs...)
	}

	return huma.NewError(fe.Kind.Status(), fe.Message, detailErrs...)
}
