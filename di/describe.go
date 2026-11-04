package di

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/samber/do/v2"
)

// ServiceInfo 是容器中一个已注册服务的诊断信息。
type ServiceInfo struct {
	// Name 是容器内部使用的服务键（通常是完整类型名）。
	Name string
	// Scope 是注册所在的 scope 名。
	Scope string
	// Kind 是 lazy / eager / transient / alias。
	Kind string
	// Invoked 表示该服务是否已被解析过（false 通常意味着从未被使用）。
	Invoked bool
}

// GroupInfo 是容器中一个 group 的诊断信息。
type GroupInfo struct {
	// ElemType 是 group 的元素类型（如 `github.com/fitan/fxkit/server.Route`）。
	ElemType string
	// Count 是当前条目数。为 0 通常意味着「贡献点写法有问题」。
	Count int
}

// Describe 返回容器的结构化诊断信息，用于 `fxkit di-graph` 与启动自检。
//
// 它解决的核心问题是：反射装配入口一旦签名不匹配，过去会静默丢弃，
// 表现为「模块装上了但服务/grouper 是空的」。有了 Describe，
// 可以在 CI 里断言「这些 group 必须有 N 个条目」。
func Describe(i Injector) (services []ServiceInfo, groups []GroupInfo) {
	if i == nil {
		return nil, nil
	}

	for _, s := range i.ListProvidedServices() {
		services = append(services, ServiceInfo{
			Name:  s.Service,
			Scope: s.ScopeName,
		})
	}
	sort.Slice(services, func(a, b int) bool { return services[a].Name < services[b].Name })

	invoked := map[string]bool{}
	for _, s := range i.ListInvokedServices() {
		invoked[s.Service] = true
	}
	for idx := range services {
		services[idx].Invoked = invoked[services[idx].Name]
		services[idx].Kind = serviceKind(i, services[idx].Name)
	}

	for _, elem := range listGroupTypes(i) {
		groups = append(groups, GroupInfo{
			ElemType: elem.String(),
			Count:    groupLen(i, elem),
		})
	}
	return services, groups
}

// Explain 返回 samber/do 原生的依赖图字符串（含 scope 树与依赖关系）。
func Explain(i Injector) string {
	if i == nil {
		return ""
	}
	out := do.ExplainInjector(i)
	return out.String()
}

// Report 以人类可读的文本汇总容器状态。供启动自检与 CLI 使用。
func Report(i Injector) string {
	services, groups := Describe(i)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("services: %d\n", len(services)))
	for _, s := range services {
		marker := " "
		if !s.Invoked {
			marker = "·" // 已注册但从未被解析
		}
		b.WriteString(fmt.Sprintf("  %s [%s] %s\n", marker, s.Kind, s.Name))
	}

	b.WriteString(fmt.Sprintf("groups: %d\n", len(groups)))
	for _, g := range groups {
		flag := ""
		if g.Count == 0 {
			flag = "  <-- EMPTY (contribution wiring likely broken)"
		}
		b.WriteString(fmt.Sprintf("  %-60s %d%s\n", g.ElemType, g.Count, flag))
	}
	return b.String()
}

// AssertGroupsNonEmpty 在启动早期校验指定 group 必须至少有一个条目。
//
// 推荐在 `serve` 命令启动前调用，把「贡献点静默丢失」变成启动期硬失败：
//
//	di.AssertGroupsNonEmpty(root, di.GroupOf[server.Route]())
//
// 注意：只能在**装配完成之后**调用（Lifecycle.PreStart 之前即可），
// 因为延迟构造函数要到 PreStart 才会写入 group。
func AssertGroupsNonEmpty(i Injector, elems ...reflect.Type) error {
	if i == nil {
		return fmt.Errorf("di: nil injector")
	}
	var missing []string
	for _, elem := range elems {
		if n := groupLen(i, elem); n == 0 {
			missing = append(missing, elem.String())
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"di: these groups are empty but required: %s (check that the providing module is included in Run(...) and its constructor signature is supported)",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

// serviceKind 返回服务的生命周期类型说明。
//
// samber/do 未导出该字段，因此这里统一返回 "lazy"（默认注册方式），
// 仅作为诊断展示用途，不参与装配逻辑。
func serviceKind(Injector, string) string {
	return "lazy"
}

// groupLen 返回容器中某元素类型的 group 条目数。
func groupLen(i Injector, elem reflect.Type) int {
	items, ok := groupValues(i, elem)
	if !ok {
		return 0
	}
	return len(items)
}

// GroupOf 返回 group 元素类型，配合 [AssertGroupsNonEmpty] 使用。
//
//	di.AssertGroupsNonEmpty(root, di.GroupOf[server.Route]())
func GroupOf[T any]() reflect.Type {
	return reflect.TypeFor[T]()
}
