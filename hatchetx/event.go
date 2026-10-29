package hatchetx

import (
	"github.com/fitan/fxkit/di"
)

// ProvideEventHandler registers an event consumer task into the shared worker.
//
// This is the recommended one-liner to declare event-driven handlers (replacing traditional MQ consumers).
//
// Example:
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

// ProvideTask registers a standalone task into the shared worker.
//
// Example:
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
