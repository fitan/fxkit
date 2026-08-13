package huma

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/fxerrors"
)

// AsError 将 *fxerrors.Error 映射为 huma 状态错误；其他 error 变为 HTTP 500。
func AsError(err error) error {
	if err == nil {
		return nil
	}
	var fe *fxerrors.Error
	if errors.As(err, &fe) {
		var detailErrs []error
		if len(fe.Details) > 0 {
			detailErrs = append(detailErrs, &huma.ErrorDetail{
				Message: string(fe.Kind),
				Value:   fe.Details,
			})
		}
		msg := fe.Message
		if fe.Kind == fxerrors.KindInternal {
			msg = "internal error"
		}
		return huma.NewError(fe.Status, msg, detailErrs...)
	}
	return huma.NewError(http.StatusInternalServerError, "internal error")
}
