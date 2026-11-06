package server

import (
	"context"
	"encoding/json"
	"net/http"
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
func RegisterHealth(p HealthParams) {
	if p.Mux == nil {
		return
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
		if c == nil {
			continue
		}
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

// registerHealthRoutes 是 Module 内部的接线：收集 group 里的 HealthChecker。
func registerHealthRoutes(i di.Injector, mux *chi.Mux) {
	checkers := di.GetGroup[HealthChecker](i)
	sort.SliceStable(checkers, func(a, b int) bool {
		return checkers[a].Name() < checkers[b].Name()
	})
	RegisterHealth(HealthParams{Mux: mux, Checkers: checkers})
}

// ProvideHealthChecker 把一个 [HealthChecker] 发布到 DI group。
//
// 支持 HealthChecker 值、func() HealthChecker、func(di.Injector) HealthChecker，
// 以及 func(dep...) HealthChecker（延迟到 PreStart 解析依赖后构造）。
// ProvideHealthChecker 发布若干 [HealthChecker]。需要依赖时用 [ProvideHealthCheckerLazy]。
func ProvideHealthChecker(hcs ...HealthChecker) di.Option {
	return func(i di.Injector) {
		for _, hc := range hcs {
			if hc != nil {
				di.ProvideGroupItem(i, hc)
			}
		}
	}
}

// ProvideHealthCheckerLazy 在 PreStart 求值（可访问完整依赖图）。
func ProvideHealthCheckerLazy(fn func(di.Injector) (HealthChecker, error)) di.Option {
	return func(i di.Injector) {
		di.ProvideLazyE[HealthChecker](i, func(i di.Injector) (HealthChecker, error) {
			hc, err := fn(i)
			if err != nil || hc == nil {
				// 返回空检查器时写入一个 no-op，避免 group 出现 nil 条目。
				return nil, err
			}
			return hc, nil
		})
	}
}
