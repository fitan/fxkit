package hatchetx

import (
	"github.com/hatchet-dev/hatchet/pkg/client/create"
	"github.com/hatchet-dev/hatchet/pkg/client/types"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

// 重新导出常用 Hatchet context 与 task 类型，提供顺畅的开发体验。
type (
	// Context 是传入任务函数的执行上下文。
	Context = hatchet.Context

	// DurableContext 是支持 SleepFor、WaitForEvent、Memo 等的持久执行上下文。
	DurableContext = hatchet.DurableContext

	// Workflow 表示 Hatchet 多步骤 DAG workflow。
	Workflow = hatchet.Workflow

	// Task 表示 workflow 内的一个 step。
	Task = hatchet.Task

	// StandaloneTask 表示独立的单步 task。
	StandaloneTask = hatchet.StandaloneTask

	// WorkflowOption 配置 Workflow。
	WorkflowOption = hatchet.WorkflowOption

	// TaskOption 配置 Task。
	TaskOption = hatchet.TaskOption

	// WorkerOption 配置 Hatchet Worker。
	WorkerOption = hatchet.WorkerOption

	// StandaloneTaskOption 配置 StandaloneTask。
	StandaloneTaskOption = hatchet.StandaloneTaskOption

	// WorkflowResult 包含已完成的 workflow 运行的返回值。
	WorkflowResult = hatchet.WorkflowResult

	// WorkflowRunRef 是 workflow 运行的轻量句柄。
	WorkflowRunRef = hatchet.WorkflowRunRef

	// RunManyOpt 指定批量 workflow 运行的输入与选项。
	RunManyOpt = hatchet.RunManyOpt

	// RunOptFunc 配置单个 workflow 运行。
	RunOptFunc = hatchet.RunOptFunc

	// RunPriority 定义 workflow 运行的执行优先级。
	RunPriority = hatchet.RunPriority

	// Concurrency 配置 workflow 或 task 的并发上限。
	Concurrency = types.Concurrency

	// EvictionPolicy 定义长时间运行的 durable task 的驱逐策略。
	EvictionPolicy = hatchet.EvictionPolicy
)

// 重新导出常用选项构造函数，减少外部 import 依赖。
var (
	// Run 选项
	// WithRunKey 设置子 workflow 运行的 key。
	WithRunKey = hatchet.WithRunKey
	// WithRunMetadata 设置 workflow 运行的附加 metadata。
	WithRunMetadata = hatchet.WithRunMetadata
	// WithRunPriority 设置 workflow 运行的优先级。
	WithRunPriority = hatchet.WithRunPriority
	// WithRunSticky 设置子 workflow 运行是否启用粘性。
	WithRunSticky = hatchet.WithRunSticky

	// Workflow 选项
	// WithWorkflowConcurrency 设置 workflow 级并发控制。
	WithWorkflowConcurrency = hatchet.WithWorkflowConcurrency
	// WithWorkflowCron 配置 workflow 的 cron 调度，可传多个表达式。
	WithWorkflowCron = hatchet.WithWorkflowCron
	// WithWorkflowCronInput 设置 cron 触发时投递的输入。
	WithWorkflowCronInput = hatchet.WithWorkflowCronInput
	// WithWorkflowEvents 配置 workflow 由指定事件触发。
	WithWorkflowEvents = hatchet.WithWorkflowEvents
	// WithWorkflowDescription 设置 workflow 的描述。
	WithWorkflowDescription = hatchet.WithWorkflowDescription
	// WithWorkflowVersion 设置 workflow 版本标识。
	WithWorkflowVersion = hatchet.WithWorkflowVersion

	// Task 选项
	// WithConcurrency 设置 task 的并发上限。
	WithConcurrency = hatchet.WithConcurrency
	// WithCron 为 standalone task 配置 cron 调度（不适用于 workflow 内的 task）。
	WithCron = hatchet.WithCron
	// WithEvents 为 standalone task 配置事件触发（不适用于 workflow 内的 task）。
	WithEvents = hatchet.WithEvents
	// WithParents 设置 task 的父任务依赖。
	WithParents = hatchet.WithParents
	// WithExecutionTimeout 设置 task 的最大执行时长。
	WithExecutionTimeout = hatchet.WithExecutionTimeout
	// WithScheduleTimeout 设置 task 等待被调度的最长时间。
	WithScheduleTimeout = hatchet.WithScheduleTimeout
	// WithRetries 设置 task 失败后的重试次数。
	WithRetries = hatchet.WithRetries
	// WithRetryBackoff 设置重试的指数退避因子与最大退避秒数。
	WithRetryBackoff = hatchet.WithRetryBackoff
	// WithRateLimits 设置 task 的限流规则。
	WithRateLimits = hatchet.WithRateLimits

	// Worker 选项
	// WithSlots 设置 worker 的最大并发运行数。
	WithSlots = hatchet.WithSlots
	// WithDurableSlots 设置 worker 的最大并发 durable task 运行数。
	WithDurableSlots = hatchet.WithDurableSlots
	// WithLabels 设置 worker 标签，用于任务路由。
	WithLabels = hatchet.WithLabels
)

// TaskDefaults 为 workflow 级 task 默认配置而重新导出。
type TaskDefaults = create.TaskDefaults
