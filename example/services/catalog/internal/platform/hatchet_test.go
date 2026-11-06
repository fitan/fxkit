package platform_test

import (
	"context"
	"testing"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ArticleViewedEvent 表示一个用于声明式事件处理的示例事件。
type ArticleViewedEvent struct {
	ArticleID string    `json:"articleId"`
	UserID    string    `json:"userId"`
	ViewedAt  time.Time `json:"viewedAt"`
}

func TestHatchet_EscapeHatchAndSubclients(t *testing.T) {
	t.Parallel()

	// 客户端被禁用 / SDK 为 nil 时
	client := &hatchetx.Client{}
	assert.False(t, client.Enabled())
	assert.Nil(t, client.Raw(), "Raw SDK escape hatch should return nil when client is disabled")

	// 子客户端访问器必须安全返回 nil 而不 panic
	assert.Nil(t, client.Runs())
	assert.Nil(t, client.Schedules())
	assert.Nil(t, client.Crons())
	assert.Nil(t, client.RateLimits())
	assert.Nil(t, client.Events())
	assert.Nil(t, client.Workflows())
}

func TestHatchet_DeclarativeProvideEventHandler(t *testing.T) {
	t.Parallel()

	injector := di.New()

	// 1. 以声明式方式注册一个事件处理器
	eventOpt := hatchetx.ProvideEventHandler(
		"catalog-on-article-viewed",
		"article-viewed",
		func(ctx hatchetx.Context, ev ArticleViewedEvent) error {
			return nil
		},
		hatchetx.WithRetries(3),
	)
	eventOpt(injector)

	// 2. 以声明式方式注册一个独立后台任务
	taskOpt := hatchetx.ProvideTask(
		"catalog-cache-refresh",
		func(ctx hatchetx.Context) error {
			return nil
		},
		hatchetx.WithWorkflowCron("@hourly"),
	)
	taskOpt(injector)

	// 3. 验证两个 registrar 都已注册到 DI group
	registrars := di.GetGroup[hatchetx.Registrar](injector)
	require.Len(t, registrars, 2, "both event handler and task should be registered as registrars")

	client := &hatchetx.Client{}
	for i, reg := range registrars {
		wfs, err := reg(client)
		require.NoError(t, err, "registrar %d should not error", i)
		_ = wfs
	}
}

func TestHatchet_DAGWorkflowRegistration(t *testing.T) {
	t.Parallel()

	injector := di.New()

	// 使用 ProvideWorkflow 和 ProvideWorkflows 注册工作流
	opt1 := hatchetx.ProvideWorkflow(nil)
	opt1(injector)

	opt2 := hatchetx.ProvideWorkflows(nil, nil)
	opt2(injector)

	opt3 := hatchetx.ProvideWorkflowFn(func(c *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
		return nil, nil
	})
	opt3(injector)

	// ProvideWorkflow / ProvideWorkflows 走延迟注册（PhaseResolveContributors），
	// 必须先跑 PreStart 才会进 group。
	require.NoError(t, di.MustInvoke[*di.Lifecycle](injector).StartPreStartOnly(context.Background()))

	regs := di.GetGroup[hatchetx.Registrar](injector)
	assert.Len(t, regs, 3)
}

func TestHatchet_ScheduleOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := &hatchetx.Client{}

	// 1. 在禁用客户端上的校验
	_, err := client.ScheduleOnce(ctx, hatchetx.ScheduleOnceInput{
		WorkflowName: "order-delayed-check",
		RunAt:        time.Now().Add(10 * time.Minute),
		Input:        map[string]any{"orderId": "ord-123"},
	})
	assert.ErrorContains(t, err, "client disabled")

	// 2. 独立辅助函数的校验
	_, err = hatchetx.ScheduleOnce(ctx, client, hatchetx.ScheduleOnceInput{
		WorkflowName: "order-delayed-check",
		RunAt:        time.Now().Add(10 * time.Minute),
	})
	assert.ErrorContains(t, err, "client disabled")

	// 3. 取消已调度运行的校验
	err = client.CancelScheduled(ctx, "sched-999")
	assert.ErrorContains(t, err, "client disabled")

	// 4. 列出已调度运行，返回干净的 ScheduledRun 领域模型
	runs, err := client.ListScheduled(ctx, hatchetx.WorkflowScheduledListParams{})
	assert.ErrorContains(t, err, "client disabled")
	assert.Nil(t, runs)
}

func TestHatchet_RunInfoAndLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := &hatchetx.Client{}

	// 1. GetRun 领域模型校验
	info, err := client.GetRun(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")
	assert.Nil(t, info)

	// 2. GetRunStatus 领域枚举校验
	status, err := client.GetRunStatus(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")
	assert.Empty(t, status)

	// 验证领域状态枚举值
	assert.Equal(t, hatchetx.RunStatus("QUEUED"), hatchetx.RunStatusQueued)
	assert.Equal(t, hatchetx.RunStatus("RUNNING"), hatchetx.RunStatusRunning)
	assert.Equal(t, hatchetx.RunStatus("COMPLETED"), hatchetx.RunStatusCompleted)
	assert.Equal(t, hatchetx.RunStatus("FAILED"), hatchetx.RunStatusFailed)
	assert.Equal(t, hatchetx.RunStatus("CANCELLED"), hatchetx.RunStatusCancelled)

	// 3. CancelRun 校验
	err = client.CancelRun(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")

	// 4. ReplayRun 校验
	err = client.ReplayRun(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")

	// 5. SubscribeToStream 校验
	stream, err := client.SubscribeToStream(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")
	assert.Nil(t, stream)
}

func TestHatchet_RateLimiting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := &hatchetx.Client{}

	// 限流 upsert 校验
	err := client.UpsertRateLimit(ctx, "view-counter-limit", 500, hatchetx.RateLimitMinute)
	assert.ErrorContains(t, err, "client disabled")

	// 限流列表校验
	rlList, err := client.ListRateLimits(ctx, nil)
	assert.ErrorContains(t, err, "client disabled")
	assert.Nil(t, rlList)

	// RateLimit 时长常量
	assert.Equal(t, hatchetx.RateLimitDuration("second"), hatchetx.RateLimitSecond)
	assert.Equal(t, hatchetx.RateLimitDuration("minute"), hatchetx.RateLimitMinute)
	assert.Equal(t, hatchetx.RateLimitDuration("hour"), hatchetx.RateLimitHour)
	assert.Equal(t, hatchetx.RateLimitDuration("day"), hatchetx.RateLimitDay)
}

func TestHatchet_UpsertReminder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := &hatchetx.Client{}

	// UpsertReminder 校验
	_, err := hatchetx.UpsertReminder(ctx, client, hatchetx.CreateReminderInput{
		WorkflowName: "catalog-heartbeat",
		ActorID:      "node-1",
		ReminderName: "tick",
		Period:       "@hourly",
		Upsert:       true,
	})
	assert.ErrorContains(t, err, "client disabled")

	// DeleteActorReminder 校验
	err = hatchetx.DeleteActorReminder(ctx, client, "catalog-heartbeat", "tick", "node-1")
	assert.ErrorContains(t, err, "client disabled")
}
