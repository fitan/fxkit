package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/di"
	"github.com/go-chi/chi/v5"
)

// HealthChecker 由各子系统实现并注册到 DI group，供 /readyz 聚合。
//
// 引入这个接口是为了让 server 不再反向依赖具体的基础设施包
// （早期 server 直接 import gormx 只为 ping 数据库，属于职责越界）。
// gormx / cachex / hatchetz 等各自注册自己的检查项。
type HealthChecker interface {
	// Name 是检查项名称，出现在 /readyz 响应里（如 "db"、"redis"）。
	Name() string
	// Check 返回 nil 表示健康。ctx 已带超时。
	Check(ctx context.Context) error
}

// HealthParams 在共享 mux 上装配存活与就绪探针。
type HealthParams struct {
	Mux      *chi.Mux
	Checkers []HealthChecker
	// Timeout 是 /readyz 聚合检查的总超时，默认 2s。
	Timeout time.Duration
}

// healthReport 是 /readyz 的 JSON 响应体。
type healthReport struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// RegisterHealth 挂载 /healthz（存活）、/readyz（就绪）与 GET /version。
//
// /readyz 会并发执行所有已注册的 [HealthChecker]：
//   - 全部通过 → 200 {"status":"ok","checks":{...}}
//   - 任一失败 → 503 {"status":"unavailable","checks":{"db":"..."}}
//
// 所有检查项必须非 nil（包括 typed nil）；nil 会让注册失败，而不是在请求期
// panic 或被静默跳过。需要「本环境不适用」时用 [ProvideHealthCheckerLazy]
// 的 ok=false。
func RegisterHealth(p HealthParams) error {
	if p.Mux == nil {
		return fmt.Errorf("server: RegisterHealth called with nil mux")
	}
	for idx, c := range p.Checkers {
		if isNilChecker(c) {
			return fmt.Errorf("server: health checker[%d] is nil; use ProvideHealthCheckerLazy's ok=false to skip", idx)
		}
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	checkers := append([]HealthChecker(nil), p.Checkers...)

	p.Mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	p.Mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		report := runChecks(ctx, checkers)
		status := http.StatusOK
		if report.Status != "ok" {
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(report)
	})

	p.Mux.Method(http.MethodGet, "/version", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		info := buildinfo.Get()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	}))
	return nil
}

// runChecks 并发执行所有检查项并汇总结果。
func runChecks(ctx context.Context, checkers []HealthChecker) healthReport {
	report := healthReport{Status: "ok", Checks: map[string]string{}}
	if len(checkers) == 0 {
		return report
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, c := range checkers {
		wg.Add(1)
		go func(c HealthChecker) {
			defer wg.Done()
			name := c.Name()
			start := time.Now()
			err := c.Check(ctx)
			recordReadyz(ctx, name, time.Since(start), err)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				report.Status = "unavailable"
				report.Checks[name] = err.Error()
				return
			}
			report.Checks[name] = "ok"
		}(c)
	}
	wg.Wait()

	// 稳定输出顺序，便于断言与人工比对。
	if len(report.Checks) == 0 {
		report.Checks = nil
	}
	return report
}

// isNilChecker 报告 hc 是否为空，包括 typed-nil（接口里装着 nil 指针）。
// 只在注册期/启动期使用，不在请求热路径上。
func isNilChecker(hc HealthChecker) bool {
	if hc == nil {
		return true
	}
	v := reflect.ValueOf(hc)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// registerHealthRoutes 是 Module 内部的接线：收集 group 里的 HealthChecker。
func registerHealthRoutes(i di.Injector, mux *chi.Mux) error {
	checkers := di.GetGroup[HealthChecker](i)
	// 必须在 sort 之前校验：比较器会对元素调用 Name()，nil 会 panic。
	for idx, hc := range checkers {
		if isNilChecker(hc) {
			return fmt.Errorf("server: health checker[%d] in DI group is nil; use ProvideHealthCheckerLazy's ok=false to skip", idx)
		}
	}
	sort.SliceStable(checkers, func(a, b int) bool {
		return checkers[a].Name() < checkers[b].Name()
	})
	return RegisterHealth(HealthParams{Mux: mux, Checkers: checkers})
}

// ProvideHealthChecker 发布若干已构造好的 [HealthChecker]。
//
// nil（含 typed nil）是装配错误，直接 panic：它会让 /readyz 在排序或调用时
// panic，而静默跳过又会让运维以为检查项存在。需要依赖、或需要条件跳过时，
// 用 [ProvideHealthCheckerLazy]（ok=false 表示本环境不适用）。
func ProvideHealthChecker(hcs ...HealthChecker) di.Option {
	return func(i di.Injector) {
		for _, hc := range hcs {
			if isNilChecker(hc) {
				panic("server: ProvideHealthChecker called with a nil checker; register a real checker, or use ProvideHealthCheckerLazy with ok=false to skip")
			}
			di.ProvideGroupItem(i, hc)
		}
	}
}

// ProvideHealthCheckerLazy 在 PreStart 求值（可访问完整依赖图）。
//
// ok=false 表示「本环境不适用」（例如依赖未配置），该检查项不会注册；
// ok=true 但 checker 为 nil（含 typed nil）会让启动失败。
func ProvideHealthCheckerLazy(fn func(di.Injector) (HealthChecker, bool, error)) di.Option {
	if fn == nil {
		return nil
	}
	return func(i di.Injector) {
		di.ProvideLazyOptionalE[HealthChecker](i, func(i di.Injector) (HealthChecker, bool, error) {
			hc, ok, err := fn(i)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, nil
			}
			if isNilChecker(hc) {
				return nil, false, fmt.Errorf("server: health checker factory returned ok=true with a nil checker")
			}
			return hc, true, nil
		})
	}
}
