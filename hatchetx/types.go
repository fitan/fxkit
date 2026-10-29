package hatchetx

import (
	"github.com/hatchet-dev/hatchet/pkg/client/create"
	"github.com/hatchet-dev/hatchet/pkg/client/types"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

// Re-export common Hatchet context and task types for seamless developer experience.
type (
	// Context is the execution context passed into task functions.
	Context = hatchet.Context

	// DurableContext is the durable execution context supporting SleepFor, WaitForEvent, Memo, etc.
	DurableContext = hatchet.DurableContext

	// Workflow represents a Hatchet multi-step DAG workflow.
	Workflow = hatchet.Workflow

	// Task represents a step inside a workflow.
	Task = hatchet.Task

	// StandaloneTask represents an independent single-step task.
	StandaloneTask = hatchet.StandaloneTask

	// WorkflowOption configures a Workflow.
	WorkflowOption = hatchet.WorkflowOption

	// TaskOption configures a Task.
	TaskOption = hatchet.TaskOption

	// WorkerOption configures a Hatchet Worker.
	WorkerOption = hatchet.WorkerOption

	// StandaloneTaskOption configures a StandaloneTask.
	StandaloneTaskOption = hatchet.StandaloneTaskOption

	// WorkflowResult contains the return value of a completed workflow run.
	WorkflowResult = hatchet.WorkflowResult

	// WorkflowRunRef is a lightweight handle to a workflow run.
	WorkflowRunRef = hatchet.WorkflowRunRef

	// RunManyOpt specifies input and options for bulk workflow runs.
	RunManyOpt = hatchet.RunManyOpt

	// RunOptFunc configures an individual workflow run.
	RunOptFunc = hatchet.RunOptFunc

	// RunPriority defines the execution priority of a workflow run.
	RunPriority = hatchet.RunPriority

	// Concurrency configures workflow or task concurrency limits.
	Concurrency = types.Concurrency

	// EvictionPolicy defines the eviction strategy for long-running durable tasks.
	EvictionPolicy = hatchet.EvictionPolicy
)

// Re-export common option constructors to minimize foreign import dependencies.
var (
	// Run options
	WithRunKey      = hatchet.WithRunKey
	WithRunMetadata = hatchet.WithRunMetadata
	WithRunPriority = hatchet.WithRunPriority
	WithRunSticky   = hatchet.WithRunSticky

	// Workflow options
	WithWorkflowConcurrency = hatchet.WithWorkflowConcurrency
	WithWorkflowCron        = hatchet.WithWorkflowCron
	WithWorkflowCronInput   = hatchet.WithWorkflowCronInput
	WithWorkflowEvents      = hatchet.WithWorkflowEvents
	WithWorkflowDescription = hatchet.WithWorkflowDescription
	WithWorkflowVersion     = hatchet.WithWorkflowVersion

	// Task options
	WithConcurrency      = hatchet.WithConcurrency
	WithCron             = hatchet.WithCron
	WithEvents           = hatchet.WithEvents
	WithParents          = hatchet.WithParents
	WithExecutionTimeout = hatchet.WithExecutionTimeout
	WithScheduleTimeout  = hatchet.WithScheduleTimeout
	WithRetries          = hatchet.WithRetries
	WithRetryBackoff     = hatchet.WithRetryBackoff
	WithRateLimits       = hatchet.WithRateLimits

	// Worker options
	WithSlots        = hatchet.WithSlots
	WithDurableSlots = hatchet.WithDurableSlots
	WithLabels       = hatchet.WithLabels
)

// TaskDefaults is re-exported for workflow-level task default configurations.
type TaskDefaults = create.TaskDefaults
