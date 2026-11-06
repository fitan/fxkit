package di

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/samber/do/v2"
)

// WarmupError 描述某个服务在预热时失败。
type WarmupError struct {
	// Service 是容器内部服务键（通常是完整类型名）。
	Service string
	// Err 是构造该服务时返回的错误。
	Err error
}

func (e WarmupError) Error() string {
	return fmt.Sprintf("%s: %v", e.Service, e.Err)
}

func (e WarmupError) Unwrap() error { return e.Err }

// WarmupReport 是 [Warmup] 的结果。
type WarmupReport struct {
	// Resolved 是成功构造的服务名（按名称排序）。
	Resolved []string
	// Failed 是构造失败的服务及其原因。
	Failed []WarmupError
	// Optional 是因不可用而被跳过的服务（依赖缺失，但业务可能确实不用它）。
	Optional []WarmupError
}

// OK 报告是否没有致命失败。
func (r WarmupReport) OK() bool { return len(r.Failed) == 0 }

// Error 汇总所有失败项；全部成功时返回 nil。
func (r WarmupReport) Error() error {
	if len(r.Failed) == 0 {
		return nil
	}
	var msgs []string
	for _, f := range r.Failed {
		msgs = append(msgs, f.Error())
	}
	return fmt.Errorf("di warmup: %d service(s) failed to construct:\n  %s",
		len(r.Failed), strings.Join(msgs, "\n  "))
}

// Warmup 强制解析容器中的**所有**已注册服务，把懒加载阶段的错误提前到启动期。
//
// 为什么需要它：samber/do 默认是惰性的 —— 一个服务只在第一次被 Invoke 时才构造。
// 于是「构造函数返回 error」或「依赖缺失」这类问题在启动时完全不可见，
// 要等到某个请求路径真正用到它才暴露（可能是上线后几小时的某个低频接口）。
// 实测 fxkit.Default() 启动后仍有 33 个服务里的 8 个从未被解析。
//
// 使用建议：在 `Lifecycle.PreStart` 的最后一个阶段调用（此时所有延迟构造函数
// 已求值、group 已完整），失败则中止启动。
//
//	lc.AppendPreStartPhase(di.PhaseAfterHTTPServer, di.Hook{
//	    OnStart: func(ctx context.Context) error {
//	        return di.Warmup(root).Error()
//	    },
//	})
//
// 关于 Optional：某些服务的构造依赖可选组件（例如未配置 Redis 时的 cachex），
// 这类「依赖缺失」会被归入 [WarmupReport.Optional] 而不是 Failed，
// 以免把「本来就没启用」误判为故障。构造函数**主动返回 error** 的仍算 Failed。
func Warmup(i Injector) WarmupReport {
	var report WarmupReport
	if i == nil {
		return report
	}

	names := make([]string, 0, 64)
	for _, s := range i.ListProvidedServices() {
		names = append(names, s.Service)
	}
	sort.Strings(names)

	for _, name := range names {
		// 已解析过的服务不再重复构造（do 内部有缓存，但这里避免无谓调用）。
		if isInvoked(i, name) {
			report.Resolved = append(report.Resolved, name)
			continue
		}

		if _, err := do.InvokeNamed[any](i, name); err != nil {
			if IsNotFound(err) {
				report.Optional = append(report.Optional, WarmupError{Service: name, Err: err})
				continue
			}
			report.Failed = append(report.Failed, WarmupError{Service: name, Err: err})
			continue
		}
		report.Resolved = append(report.Resolved, name)
	}

	sort.Slice(report.Optional, func(a, b int) bool {
		return report.Optional[a].Service < report.Optional[b].Service
	})
	return report
}

// isInvoked 报告某个服务是否已被解析过。
func isInvoked(i Injector, name string) bool {
	for _, s := range i.ListInvokedServices() {
		if s.Service == name {
			return true
		}
	}
	return false
}

// IsNotFound 报告 err 是否表示「容器里没有该服务」（含其上游依赖缺失）。
//
// samber/do 用 %w 包装 ErrServiceNotFound，因此本函数对经过多层包装的错误同样成立。
// 模块的可选依赖（例如未使用 cli 时缺失的 config.Options）应把它与真正的构造
// 失败区分开：前者可以忽略，后者必须中止启动。
func IsNotFound(err error) bool {
	return errors.Is(err, do.ErrServiceNotFound) || errors.Is(err, do.ErrServiceNotMatch)
}

// AssertNoUnresolved 是 [Warmup] 的严格版本：任何服务解析失败都算错误
// （包括「依赖缺失」）。适合在测试里断言「整张图都是可构造的」。
func AssertNoUnresolved(i Injector) error {
	report := Warmup(i)
	if !report.OK() {
		return report.Error()
	}
	if len(report.Optional) > 0 {
		var msgs []string
		for _, o := range report.Optional {
			msgs = append(msgs, o.Error())
		}
		return fmt.Errorf("di warmup: %d service(s) have unresolvable dependencies:\n  %s",
			len(report.Optional), strings.Join(msgs, "\n  "))
	}
	return nil
}
