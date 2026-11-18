package demo

import (
	"time"

	"github.com/fitan/fxkit/hatchetx"
)

// RegisterWorker 注册 demo 的 Hatchet 任务：
//   - catalog-demo-once：一次性定时运行的普通任务
//   - catalog-demo-durable：SleepFor 持久化长任务（不占协程，重启不丢状态）
//   - catalog-demo-dag：两步依赖的 DAG
func RegisterWorker(svc *Service) hatchetx.Registrar {
	return func(client *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
		if client == nil || !client.Enabled() {
			return nil, nil
		}
		once := client.NewStandaloneTask(DemoOnceWorkflow,
			func(ctx hatchetx.Context, in DemoInput) (DemoOutput, error) {
				return svc.runTask(ctx, in)
			})

		durable := client.NewStandaloneDurableTask(DemoDurableWorkflow,
			func(ctx hatchetx.DurableContext, in DemoInput) (DemoOutput, error) {
				if _, err := ctx.SleepFor(100 * time.Millisecond); err != nil {
					return DemoOutput{}, err
				}
				return svc.runTask(ctx, in)
			})

		wf := client.NewWorkflow(DemoDAGWorkflow)
		step1 := wf.NewTask("demo-dag-step-1",
			func(ctx hatchetx.Context, in DemoInput) (DemoOutput, error) {
				return svc.runTask(ctx, in)
			})
		// NewTask 会直接把 step 注册进 workflow，返回值只在需要声明依赖时使用。
		wf.NewTask("demo-dag-step-2",
			func(ctx hatchetx.Context, in DemoInput) (DemoOutput, error) {
				out, err := svc.runTask(ctx, in)
				if err != nil {
					return out, err
				}
				out.Message += " (after step 1)"
				return out, nil
			}, hatchetx.WithParents(step1))

		return []hatchetx.WorkflowBase{once, durable, wf}, nil
	}
}
