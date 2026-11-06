package di

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 注：早期这里还有两个「全局 group 注册表不泄漏」的测试。
// 现在 group 索引是容器内的一个服务（*groupIndex），随容器回收，
// 不存在包级 map，也就不存在「忘记清理」这种失败模式。

// TestGroups_IsolatedAcrossInjectors group 必须按容器隔离：
// 一个容器注册的条目不能被另一个容器看到（尤其测试并行时）。
func TestGroups_IsolatedAcrossInjectors(t *testing.T) {
	a := New()
	defer Shutdown(a)
	b := New()
	defer Shutdown(b)

	ProvideGroupItem(a, "only-in-a")

	assert.Equal(t, []string{"only-in-a"}, GetGroup[string](a))
	assert.Empty(t, GetGroup[string](b), "group must not leak across injectors")
}

// TestGroups_SurvivesVirtualScope 回归测试：
// samber/do 解析服务时传入的是 virtualScope 包装，而非 *RootScope。
// provider 内部读 group 必须仍然可用。
func TestGroups_SurvivesVirtualScope(t *testing.T) {
	type out struct{ n int }

	root := New()
	defer Shutdown(root)

	ProvideGroupItems(root, "a", "b")

	// 在构造函数内部读 group：这与 do 解析时的 virtualScope 路径一致。
	Provide(root, func(i Injector) (*out, error) {
		return &out{n: len(GetGroup[string](i))}, nil
	})

	v, err := Invoke[*out](root)
	assert.NoError(t, err, "group lookup inside a provider (virtualScope) must work")
	assert.Equal(t, 2, v.n)
}

// TestListGroups 按容器列出已登记的元素类型。
func TestListGroups(t *testing.T) {
	a := New()
	defer Shutdown(a)
	b := New()
	defer Shutdown(b)

	ProvideGroupItems(a, "x")
	ProvideGroupItems(a, 1)

	assert.Equal(t, []string{"int", "string"}, ListGroups(a))
	assert.Empty(t, ListGroups(b))
}
