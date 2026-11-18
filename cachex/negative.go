package cachex

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"
)

// nullPlaceholder 是空值（负缓存）条目的前缀标记。
var nullPlaceholder = []byte("<<__FXKIT_CACHE_NULL__>>")

// ErrNegativeHit 是「命中负缓存」的哨兵错误。
//
//	_, err := cachex.GetOrSet(ctx, c, key, loader)
//	if errors.Is(err, cachex.ErrNegativeHit) { ... }
//
// 命中后不再伪装成 gorm / database/sql / pgx 的哨兵。
// loader 返回的是 fxerrors.NotFound 时，命中结果仍能 errors.As 成 KindNotFound，Huma 继续 404。
// loader 返回的是 gorm/sql/pgx 的未找到时，第一次请求经 fxerrors.Wrap 是 500，命中后也是 500。
var ErrNegativeHit = errors.New("cache: negative hit")

// IsNegativeHit 报告 err 是否为负缓存命中。
//
// 保留该辅助函数是为了兼容旧调用方式；新代码建议直接使用
// errors.Is(err, ErrNegativeHit)。
func IsNegativeHit(err error) bool {
	return errors.Is(err, ErrNegativeHit)
}

// IsNullVal 检查缓存的二进制是否为空值（负缓存）占位。
func IsNullVal(data []byte) bool {
	return bytes.HasPrefix(data, nullPlaceholder)
}

// NegativeHitError 表示本次读取命中了负缓存。
//
// errors.Is 只认 ErrNegativeHit。fxNotFound 为真时，errors.As 得到 KindNotFound，
// 这样 fxerrors.NotFound 的第二次请求仍是 404。
type NegativeHitError struct {
	Key        string
	Message    string
	fxNotFound bool
}

func (e *NegativeHitError) Error() string {
	if e == nil {
		return ErrNegativeHit.Error()
	}
	msg := e.Message
	if msg == "" {
		msg = "not found"
	}
	if e.Key != "" {
		return fmt.Sprintf("cache: negative hit for %q: %s", e.Key, msg)
	}
	return "cache: negative hit: " + msg
}

// Is 只认负缓存哨兵。命中后不再假装自己是 gorm/sql/pgx 的原始错误。
func (e *NegativeHitError) Is(target error) bool {
	return e != nil && target == ErrNegativeHit
}

// As 只在当初写入的是 fxerrors.NotFound 时给出 KindNotFound。
// 文案用固定的 "not found"，不把 loader 的包装文本放进 HTTP 响应。
func (e *NegativeHitError) As(target any) bool {
	if e == nil || !e.fxNotFound {
		return false
	}
	p, ok := target.(**fxerrors.Error)
	if !ok {
		return false
	}
	*p = &fxerrors.Error{
		Kind:    fxerrors.KindNotFound,
		Message: "not found",
	}
	return true
}

// classifyNotFound 判断这个错误要不要写入负缓存。
// 第二个返回值表示它本身是 fxerrors.KindNotFound（命中后仍应是 404）。
func classifyNotFound(err error) (fxNotFound bool, ok bool) {
	if err == nil {
		return false, false
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		ok = true
	}
	var fxe *fxerrors.Error
	if errors.As(err, &fxe) && fxe.Kind == fxerrors.KindNotFound {
		return true, true
	}
	return false, ok
}

func isNotFoundError(err error) bool {
	_, ok := classifyNotFound(err)
	return ok
}

type negativeEntry struct {
	FX bool `json:"fx,omitempty"`
}

// encodeNegative 把 loader 返回的「未找到」错误编码为负缓存 payload。
func encodeNegative(err error) []byte {
	fx, _ := classifyNotFound(err)
	body, _ := json.Marshal(negativeEntry{FX: fx})
	out := make([]byte, 0, len(nullPlaceholder)+len(body))
	out = append(out, nullPlaceholder...)
	out = append(out, body...)
	return out
}

// decodeNegative 解析负缓存 payload。ok 为 false 表示不是负缓存条目。
func decodeNegative(data []byte, key string) (*NegativeHitError, bool) {
	if !IsNullVal(data) {
		return nil, false
	}
	entry := negativeEntry{}
	if rest := data[len(nullPlaceholder):]; len(rest) > 0 {
		_ = json.Unmarshal(rest, &entry)
	}
	return &NegativeHitError{Key: key, fxNotFound: entry.FX}, true
}
