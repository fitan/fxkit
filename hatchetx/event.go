package hatchetx

import (
	"github.com/fitan/fxkit/di"
)

// ProvideEventHandler 把事件消费任务注册到共享 worker。
//
// 这是声明事件驱动处理器（替代传统 MQ 消费者）的推荐一行式写法。
//
// 示例：
//
//	hatchetx.ProvideEventHandler("users-on-user-created", "user-created", func(ctx hatchetx.Context, ev UserCreatedEvent) error {
//	    return svc.HandleUserCreated(ctx, ev)
//	})
func ProvideEventHandler(name string, eventKey string, fn any, opts ...StandaloneTaskOption) di.Option {
	return ProvideRegistrar(func(c *Client) ([]WorkflowBase, error) {
		taskOpts := make([]StandaloneTaskOption, 0, len(opts)+1)
		taskOpts = append(taskOpts, WithWorkflowEvents(eventKey))
		taskOpts = append(taskOpts, opts...)
		task := c.NewStandaloneTask(name, fn, taskOpts...)
		return []WorkflowBase{task}, nil
	})
}

// ProvideTask 把 standalone task 注册到共享 worker。
//
// 示例：
//
//	hatchetx.ProvideTask("daily-cleanup", func(ctx hatchetx.Context) error {
//	    return svc.Cleanup(ctx)
//	}, hatchetx.WithWorkflowCron("@daily"))
func ProvideTask(name string, fn any, opts ...StandaloneTaskOption) di.Option {
	return ProvideRegistrar(func(c *Client) ([]WorkflowBase, error) {
		task := c.NewStandaloneTask(name, fn, opts...)
		return []WorkflowBase{task}, nil
	})
}
