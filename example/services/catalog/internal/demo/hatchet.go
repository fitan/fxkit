package demo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/hatchetx"
)

const (
	// DemoOnceWorkflow 由 ScheduleOnce 在指定时刻触发一次。
	DemoOnceWorkflow = "catalog-demo-once"
	// DemoDurableWorkflow 是带 SleepFor 的持久化长任务。
	DemoDurableWorkflow = "catalog-demo-durable"
	// DemoDAGWorkflow 是两步依赖的 DAG。
	DemoDAGWorkflow = "catalog-demo-dag"
)

// hatchetReady 统一把「未启用」转换为 503，避免各方法重复判断。
func (s *Service) hatchetReady() error {
	if s.hatchet == nil || !s.hatchet.Enabled() {
		return fxerrors.Unavailable("hatchet disabled")
	}
	return nil
}

// ScheduleOnceDemo 在未来某一时刻触发一次 workflow（不是 cron 提醒）。
func (s *Service) ScheduleOnceDemo(ctx context.Context, in DemoInput) (string, error) {
	if err := s.hatchetReady(); err != nil {
		return "", err
	}
	delay := time.Duration(in.DelayMs) * time.Millisecond
	if delay <= 0 {
		delay = time.Second
	}
	id, err := s.hatchet.ScheduleOnce(ctx, hatchetx.ScheduleOnceInput{
		WorkflowName: DemoOnceWorkflow,
		RunAt:        time.Now().Add(delay),
		Input:        in,
	})
	if err != nil {
		return "", fxerrors.Unavailable("schedule: %v", err)
	}
	return id, nil
}

// RunDAGDemo 异步触发两步 DAG（不等待结果）。
func (s *Service) RunDAGDemo(ctx context.Context, in DemoInput) (string, error) {
	if err := s.hatchetReady(); err != nil {
		return "", err
	}
	id, err := s.hatchet.RunNoWait(ctx, DemoDAGWorkflow, in)
	if err != nil {
		return "", fxerrors.Unavailable("run dag: %v", err)
	}
	return id, nil
}

// RunDurableDemo 异步触发带 SleepFor 的持久化任务。
func (s *Service) RunDurableDemo(ctx context.Context, in DemoInput) (string, error) {
	if err := s.hatchetReady(); err != nil {
		return "", err
	}
	id, err := s.hatchet.RunNoWait(ctx, DemoDurableWorkflow, in)
	if err != nil {
		return "", fxerrors.Unavailable("run durable: %v", err)
	}
	return id, nil
}

// SetRateLimitDemo 创建/更新 Hatchet 原生限流规则（每分钟 limit 次）。
func (s *Service) SetRateLimitDemo(ctx context.Context, req RateLimitReq) error {
	if err := s.hatchetReady(); err != nil {
		return err
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return fxerrors.Validation(map[string]string{"key": "required"})
	}
	if req.Limit <= 0 {
		return fxerrors.Validation(map[string]string{"limit": "must be > 0"})
	}
	if err := s.hatchet.UpsertRateLimit(ctx, key, req.Limit, hatchetx.RateLimitMinute); err != nil {
		return fxerrors.Unavailable("rate limit: %v", err)
	}
	return nil
}

// runTask 是各任务共享的业务实现。
func (s *Service) runTask(_ context.Context, in DemoInput) (DemoOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "demo"
	}
	return DemoOutput{
		Name:    name,
		Message: fmt.Sprintf("hello %s", name),
		Worker:  "catalog",
		At:      time.Now().UTC(),
	}, nil
}
