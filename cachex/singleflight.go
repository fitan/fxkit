package cachex

import (
	"reflect"
	"sync"

	"golang.org/x/sync/singleflight"
)

// singleflightRegistry 按 Cache 实例维护 singleflight group。
//
// 之前的实现是一个包级全局 group，key 只用业务 key，导致：
// 两个不同的 Cache（不同 Redis 实例、不同租户前缀、测试里的不同 fixture）
// 用同一个 key 并发访问时，后到者会拿到前者 loader 的结果。
//
// 现在用「实例地址」作 key：
//   - 不再把 Cache 接口值当 map key（不可比较的实现会 panic）；
//   - 不持有 Cache 引用，避免包级 registry 阻止实例被 GC。
//
// 仅在并发调用期间需要 group 状态，而那时调用方持有 Cache，地址不会被复用。
type singleflightRegistry struct {
	mu     sync.Mutex
	groups map[uintptr]*singleflight.Group
}

func newSingleflightRegistry() *singleflightRegistry {
	return &singleflightRegistry{groups: make(map[uintptr]*singleflight.Group)}
}

// cacheIdentity 返回指针类 Cache 的地址；非指针实现返回 false。
func cacheIdentity(c Cache) (uintptr, bool) {
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if v.IsNil() {
			return 0, false
		}
		return v.Pointer(), true
	default:
		return 0, false
	}
}

// groupFor 返回属于 c 的 singleflight group（惰性创建）。
//
// 非指针/不可寻址的实现每次返回独立的 group（不合并），
// 但保证不会 panic，也不会污染 registry。
func (r *singleflightRegistry) groupFor(c Cache) *singleflight.Group {
	id, ok := cacheIdentity(c)
	if !ok {
		return &singleflight.Group{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.groups[id]; ok {
		return g
	}
	g := &singleflight.Group{}
	r.groups[id] = g
	return g
}
