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
	WithRunKey      = hatchet.WithRunKey
	WithRunMetadata = hatchet.WithRunMetadata
	WithRunPriority = hatchet.WithRunPriority
	WithRunSticky   = hatchet.WithRunSticky

	// Workflow 选项
	WithWorkflowConcurrency = hatchet.WithWorkflowConcurrency
	WithWorkflowCron        = hatchet.WithWorkflowCron
	WithWorkflowCronInput   = hatchet.WithWorkflowCronInput
	WithWorkflowEvents      = hatchet.WithWorkflowEvents
	WithWorkflowDescription = hatchet.WithWorkflowDescription
	WithWorkflowVersion     = hatchet.WithWorkflowVersion

	// Task 选项
	WithConcurrency      = hatchet.WithConcurrency
	WithCron             = hatchet.WithCron
	WithEvents           = hatchet.WithEvents
	WithParents          = hatchet.WithParents
	WithExecutionTimeout = hatchet.WithExecutionTimeout
	WithScheduleTimeout  = hatchet.WithScheduleTimeout
	WithRetries          = hatchet.WithRetries
	WithRetryBackoff     = hatchet.WithRetryBackoff
	WithRateLimits       = hatchet.WithRateLimits

	// Worker 选项
	WithSlots        = hatchet.WithSlots
	WithDurableSlots = hatchet.WithDurableSlots
	WithLabels       = hatchet.WithLabels
)

// TaskDefaults 为 workflow 级 task 默认配置而重新导出。
type TaskDefaults = create.TaskDefaults
