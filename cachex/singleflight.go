package cachex

import (
	"reflect"
	"strconv"
	"sync"

	"golang.org/x/sync/singleflight"
)

// defaultSFGroup 给「未实现能力接口」的自定义 Cache 共享使用。
//
// 共享一个 group 不会串结果：调用方会把实例标识并入 flight key（见 [singleflightGroup]）。
// 这样既不需要按实例建表的全局注册表（旧实现只增不删，本身就是内存泄漏），
// 又保留了自定义实现的并发合并能力。
var defaultSFGroup singleflight.Group

// singleflightGroup 返回 (group, keyPrefix)。
//
// 内置实现（MemoryCache / RedisCache / prefixedCache）通过内嵌 [sfHolder]
// 各自持有 group，随实例回收；自定义实现退化为共享 group + 实例标识前缀，
// 既不会把不同实例的结果串在一起，也不会泄漏注册表条目。
func singleflightGroup(c Cache) (*singleflight.Group, string) {
	if g, ok := c.(interface{ singleflightGroup() *singleflight.Group }); ok {
		return g.singleflightGroup(), ""
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if !v.IsNil() {
			return &defaultSFGroup, strconv.FormatUint(uint64(v.Pointer()), 16) + "\x00"
		}
	}
	// 值类型实现：无法区分实例，按类型隔离即可。
	return &defaultSFGroup, v.Type().String() + "\x00"
}

// sfHolder 供内置实现内嵌，惰性创建并持有自己的 group。
type sfHolder struct {
	once sync.Once
	sf   *singleflight.Group
}

func (h *sfHolder) singleflightGroup() *singleflight.Group {
	h.once.Do(func() { h.sf = &singleflight.Group{} })
	return h.sf
}
