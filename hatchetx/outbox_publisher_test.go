package hatchetx

import (
	"context"
	"strings"
	"testing"

	"github.com/fitan/fxkit/di"
)

// TestOutboxPublisher_Disabled 校验 client 未初始化时不会 panic，而是返回明确错误。
func TestOutboxPublisher_Disabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var nilPublisher *outboxPublisher
	if err := nilPublisher.PublishEvent(ctx, "pubsub", "topic", []byte(`{}`), nil); err == nil {
		t.Fatal("nil publisher must return an error")
	}

	p := &outboxPublisher{client: &Client{}}
	if err := p.PublishEvent(ctx, "pubsub", "topic", []byte(`{}`), nil); err == nil || !strings.Contains(err.Error(), "nil hatchet client") {
		t.Fatalf("expected nil client error, got %v", err)
	}
}

// TestProvideOutboxPublisher_NilCombinations 保证配置/客户端缺失时返回 nil，而不是半初始化发布器。
func TestProvideOutboxPublisher_NilCombinations(t *testing.T) {
	t.Parallel()
	if got := provideOutboxPublisher(nil, nil); got != nil {
		t.Fatalf("nil client+config should yield nil, got %T", got)
	}
	if got := provideOutboxPublisher(nil, &Config{Enabled: true, OutboxPublisher: true}); got != nil {
		t.Fatalf("nil client should yield nil, got %T", got)
	}
	if got := provideOutboxPublisher(&Client{}, &Config{Enabled: true, OutboxPublisher: true}); got != nil {
		t.Fatalf("disabled client should yield nil, got %T", got)
	}
}

// TestRegisterLifecycle_Noop 空参数直接返回（不注册钩子，也不 panic）。
func TestRegisterLifecycle_Noop(t *testing.T) {
	t.Parallel()
	registerLifecycle(lifecycleParams{})
}

// TestRegisterLifecycle_DisabledConfig 配置未启用时不启动 worker。
func TestRegisterLifecycle_DisabledConfig(t *testing.T) {
	t.Parallel()
	root := di.New()
	di.ProvideValue(root, &Config{})
	lc := di.NewLifecycle()
	registerLifecycle(lifecycleParams{LC: lc, Injector: root})
	if err := lc.Start(t.Context()); err != nil {
		t.Fatalf("disabled config should not fail: %v", err)
	}
}

// TestRegisterLifecycle_EnabledWithoutClient 启用了但 client 未初始化时必须启动失败。
func TestRegisterLifecycle_EnabledWithoutClient(t *testing.T) {
	t.Parallel()
	root := di.New()
	di.ProvideValue(root, &Config{Enabled: true})
	di.ProvideValue(root, &Client{})
	lc := di.NewLifecycle()
	registerLifecycle(lifecycleParams{LC: lc, Injector: root})
	err := lc.Start(t.Context())
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not-initialized error, got %v", err)
	}
}

// TestProvideOutboxPublisherOption_DisabledClient 未启用 client 时不注册发布器。
func TestProvideOutboxPublisherOption_DisabledClient(t *testing.T) {
	t.Parallel()
	root := di.New()
	di.ProvideValue(root, &Client{})
	di.ProvideValue(root, &Config{Enabled: true, OutboxPublisher: true})
	provideOutboxPublisherOption()(root)
}
