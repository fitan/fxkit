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
//
// 历史上负缓存只写入这一个固定字节串；现在其后会追加一段 JSON 描述原始
// 「未找到」错误的类别，以便命中负缓存时恢复 errors.Is / fxerrors.Is 语义。
// 仅写入该前缀（无 JSON 尾巴）的旧数据仍然兼容。
var nullPlaceholder = []byte("<<__FXKIT_CACHE_NULL__>>")

// ErrNegativeHit 是「命中负缓存」的哨兵错误。
//
// 用法：
//
//	_, err := cachex.GetOrSet(ctx, c, key, loader)
//	if errors.Is(err, cachex.ErrNegativeHit) { ... }
//
// 返回的具体错误同时会保留原始「未找到」错误的可判定性，见 [NegativeHitError]。
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

type notFoundFlags uint8

const (
	flagGorm notFoundFlags = 1 << iota
	flagSQL
	flagPgx
	flagFX
)

// NegativeHitError 表示本次读取命中了负缓存。
//
// 它实现了 Is/As，使命中负缓存时的错误语义与 loader 第一次返回的错误一致：
//
//	errors.Is(err, gorm.ErrRecordNotFound)  // loader 返回 gorm.ErrRecordNotFound
//	errors.Is(err, sql.ErrNoRows)           // loader 返回 sql.ErrNoRows
//	errors.Is(err, pgx.ErrNoRows)           // loader 返回 pgx.ErrNoRows
//	fxerrors.Is(err, fxerrors.KindNotFound) // loader 返回 gorm.ErrRecordNotFound 或 fxerrors NotFound
//	errors.Is(err, cachex.ErrNegativeHit)   // 命中负缓存
type NegativeHitError struct {
	// Key 是触发负缓存的业务 key。
	Key string
	// Message 是首次 loader 返回的错误文案。
	Message string

	flags notFoundFlags
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

// Is 让 errors.Is 能识别负缓存哨兵以及原始的「未找到」哨兵。
func (e *NegativeHitError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch {
	case target == ErrNegativeHit:
		return true
	case target == gorm.ErrRecordNotFound:
		return e.flags&flagGorm != 0
	case target == sql.ErrNoRows:
		return e.flags&flagSQL != 0
	case target == pgx.ErrNoRows:
		return e.flags&flagPgx != 0
	default:
		return false
	}
}

// As 让 errors.As / fxerrors.Is 能拿到一个 KindNotFound 的 *fxerrors.Error。
func (e *NegativeHitError) As(target any) bool {
	if e == nil || e.flags&flagFX == 0 {
		return false
	}
	p, ok := target.(**fxerrors.Error)
	if !ok {
		return false
	}
	msg := e.Message
	if msg == "" {
		msg = "not found"
	}
	*p = &fxerrors.Error{
		Kind:    fxerrors.KindNotFound,
		Message: msg,
	}
	return true
}

// classifyNotFound 判断错误是否属于「资源未找到」，并记录其来源类别。
//
// 优先使用 errors.Is / errors.As 判定（gorm、database/sql、pgx、fxerrors），
// 这样被 %w 包装的错误也能识别。最后保留对历史文案的兼容匹配：某些项目会
// 用 errors.New("record not found") 这类非标准哨兵，若不兼容会退化为无负缓存。
func classifyNotFound(err error) (notFoundFlags, bool) {
	if err == nil {
		return 0, false
	}

	var flags notFoundFlags
	if errors.Is(err, gorm.ErrRecordNotFound) {
		flags |= flagGorm | flagFX
	}
	if errors.Is(err, sql.ErrNoRows) {
		flags |= flagSQL
	}
	if errors.Is(err, pgx.ErrNoRows) {
		flags |= flagPgx
	}
	var fxe *fxerrors.Error
	if errors.As(err, &fxe) && fxe.Kind == fxerrors.KindNotFound {
		flags |= flagFX
	}
	if flags != 0 {
		return flags, true
	}

	// 兼容非标准哨兵：只认**完全相等**的文案。
	//
	// 不能用 HasPrefix("record not found: ")：那样 `fmt.Errorf("record not found: %w", realErr)`
	// 这类包装过的真实故障也会被判成「未找到」，进而写进负缓存，
	// 把一次数据库错误变成一分钟的「数据不存在」。
	msg := err.Error()
	if msg == "record not found" || msg == "sql: no rows in result set" {
		return 0, true
	}
	return 0, false
}

func isNotFoundError(err error) bool {
	_, ok := classifyNotFound(err)
	return ok
}

type negativeEntry struct {
	Gorm bool   `json:"gorm,omitempty"`
	SQL  bool   `json:"sql,omitempty"`
	Pgx  bool   `json:"pgx,omitempty"`
	FX   bool   `json:"fx,omitempty"`
	Msg  string `json:"msg,omitempty"`
}

// encodeNegative 把 loader 返回的「未找到」错误编码为负缓存 payload。
func encodeNegative(err error) []byte {
	flags, _ := classifyNotFound(err)
	entry := negativeEntry{
		Gorm: flags&flagGorm != 0,
		SQL:  flags&flagSQL != 0,
		Pgx:  flags&flagPgx != 0,
		FX:   flags&flagFX != 0,
		Msg:  err.Error(),
	}
	body, _ := json.Marshal(entry)
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
		// 旧数据没有 JSON 尾巴；解析失败也按「通用未找到」处理。
		_ = json.Unmarshal(rest, &entry)
	}
	var flags notFoundFlags
	if entry.Gorm {
		flags |= flagGorm
	}
	if entry.SQL {
		flags |= flagSQL
	}
	if entry.Pgx {
		flags |= flagPgx
	}
	if entry.FX {
		flags |= flagFX
	}
	return &NegativeHitError{Key: key, Message: entry.Msg, flags: flags}, true
}
