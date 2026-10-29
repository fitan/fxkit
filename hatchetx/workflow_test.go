package hatchetx

import (
	"context"
	"strings"
	"testing"

	"github.com/fitan/fxkit/di"
)

type dummyWorkflow struct {
	name string
}

func TestClient_DisabledAccessors(t *testing.T) {
	t.Parallel()
	c := &Client{}

	if c.Raw() != nil {
		t.Fatal("expected nil Raw client when disabled")
	}
	if c.Crons() != nil {
		t.Fatal("expected nil Crons client when disabled")
	}
	if c.Runs() != nil {
		t.Fatal("expected nil Runs client when disabled")
	}
	if c.Schedules() != nil {
		t.Fatal("expected nil Schedules client when disabled")
	}
	if c.RateLimits() != nil {
		t.Fatal("expected nil RateLimits client when disabled")
	}
	if c.Events() != nil {
		t.Fatal("expected nil Events client when disabled")
	}
	if c.Workflows() != nil {
		t.Fatal("expected nil Workflows client when disabled")
	}
	if c.Workers() != nil {
		t.Fatal("expected nil Workers client when disabled")
	}
	if c.Filters() != nil {
		t.Fatal("expected nil Filters client when disabled")
	}
	if c.Metrics() != nil {
		t.Fatal("expected nil Metrics client when disabled")
	}
	if c.Logs() != nil {
		t.Fatal("expected nil Logs client when disabled")
	}
	if c.CEL() != nil {
		t.Fatal("expected nil CEL client when disabled")
	}
	if c.Webhooks() != nil {
		t.Fatal("expected nil Webhooks client when disabled")
	}
	if c.NewWorkflow("wf") != nil {
		t.Fatal("expected nil NewWorkflow when disabled")
	}
	if c.NewStandaloneTask("task", func() {}) != nil {
		t.Fatal("expected nil NewStandaloneTask when disabled")
	}
	if c.NewStandaloneDurableTask("task", func() {}) != nil {
		t.Fatal("expected nil NewStandaloneDurableTask when disabled")
	}

	ctx := context.Background()
	if _, err := c.Run(ctx, "wf", nil); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}
	if _, err := c.RunMany(ctx, "wf", nil); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}
}

func TestProvideWorkflow_Registration(t *testing.T) {
	t.Parallel()

	injector := di.New()

	// 1. ProvideWorkflow
	opt1 := ProvideWorkflow(nil)
	opt1(injector)

	// 2. ProvideWorkflows
	opt2 := ProvideWorkflows(nil, nil)
	opt2(injector)

	// 3. ProvideWorkflowFn
	opt3 := ProvideWorkflowFn(func(*Client) ([]WorkflowBase, error) {
		return nil, nil
	})
	opt3(injector)

	// 4. ProvideRegistrar with single WorkflowBase func
	opt4 := ProvideRegistrar(func(*Client) (WorkflowBase, error) {
		return nil, nil
	})
	opt4(injector)

	// 5. ProvideEventHandler
	opt5 := ProvideEventHandler("test-handler", "test-event", func() {})
	opt5(injector)

	// 6. ProvideTask
	opt6 := ProvideTask("test-task", func() {})
	opt6(injector)

	regs := di.GetGroup[Registrar](injector)
	if len(regs) != 6 {
		t.Fatalf("expected 6 registrars, got %d", len(regs))
	}

	client := &Client{}
	for i, reg := range regs {
		wfs, err := reg(client)
		if err != nil {
			t.Fatalf("registrar %d failed: %v", i, err)
		}
		// when client is disabled, NewStandaloneTask returns nil, so wfs may contain nil elements or be empty
		_ = wfs
	}
}
