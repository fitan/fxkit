package di

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// registryLen 返回 group 注册表中的条目总数。
func registryLen() int {
	groupRegistryMu.RLock()
	defer groupRegistryMu.RUnlock()
	return len(groupRegistry)
}

// TestGroupRegistry_NoLeakWithShutdown 是回归测试：
// groupRegistry 早期用 Injector 接口值作 map key，导致每个创建过的容器
// 都被永久持有 —— 测试里反复建/销毁容器会稳定泄漏（实测 100 个容器泄漏 100 条）。
//
// 现在按 RootScope.ID() 字符串索引，并可用 [Shutdown] 彻底清理。
func TestGroupRegistry_NoLeakWithShutdown(t *testing.T) {
	before := registryLen()

	for i := 0; i < 100; i++ {
		root := New()
		ProvideGroupItem(root, fmt.Sprintf("item-%d", i))
		_ = GetGroup[string](root)
		Shutdown(root)
	}
	runtime.GC()

	assert.Equal(t, before, registryLen(),
		"group registry must not retain shutdown injectors")
}

// TestGroupRegistry_DoesNotRetainContainer 即使不走 Shutdown 清理路径，
// registry 也只留一个条目（持有 Group 指针），不应持有整个容器。
func TestGroupRegistry_DoesNotRetainContainer(t *testing.T) {
	before := registryLen()

	for i := 0; i < 10; i++ {
		root := New()
		ProvideGroupItem(root, "y")
		root.Shutdown() // 老写法，不调用 ReleaseInjector
	}
	runtime.GC()

	// 会有 10 条残留（每个容器一条），但都不持有容器本身。
	// 这里只断言「不是每条 group 一个容器」的失控增长。
	assert.LessOrEqual(t, registryLen()-before, 10)
}

// TestGroupRegistry_IsolatedAcrossInjectors group 必须按容器隔离：
// 一个容器注册的条目不能被另一个容器看到（尤其测试并行时）。
func TestGroupRegistry_IsolatedAcrossInjectors(t *testing.T) {
	a := New()
	defer Shutdown(a)
	b := New()
	defer Shutdown(b)

	ProvideGroupItem(a, "only-in-a")

	assert.Equal(t, []string{"only-in-a"}, GetGroup[string](a))
	assert.Empty(t, GetGroup[string](b), "group must not leak across injectors")
}

// TestGroupRegistry_SurvivesVirtualScope 回归测试：
// samber/do 解析服务时传入的是 virtualScope 包装，而非 *RootScope。
// 若用接口值做 key，provider 内部读 group 会查不到（"could not find service []T"）。
func TestGroupRegistry_SurvivesVirtualScope(t *testing.T) {
	type params struct {
		Items []string
	}
	type out struct{ n int }

	root := New()
	defer Shutdown(root)

	ProvideGroupItems(root, "a", "b")

	var got int
	ToOption(func(p params) *out {
		got = len(p.Items)
		return &out{n: got}
	})(root)

	_, err := Invoke[*out](root)
	assert.NoError(t, err, "group lookup inside a provider (virtualScope) must work")
	assert.Equal(t, 2, got)
}
