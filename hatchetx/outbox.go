package hatchetx

import (
	"context"
	"errors"
	"log/slog"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/outbox"
)

// outboxPublisher 把 outbox 记录作为 Hatchet 事件推送（event key = topic 名称）。
type outboxPublisher struct {
	// client 是推送事件用的 Hatchet client。
	client *Client
}

// PublishEvent 把 outbox 记录以 topicName 为 event key 推送到 Hatchet。
//
// client 未启用时返回错误。
func (p *outboxPublisher) PublishEvent(ctx context.Context, _, topicName string, data []byte, metadata map[string]string) error {
	if p == nil || p.client == nil || !p.client.Enabled() {
		return errors.New("outbox: nil hatchet client")
	}
	return p.client.PushEventJSON(ctx, topicName, data, metadata)
}

// provideOutboxPublisher 在 hatchet 与 outbox_publisher 均启用时返回 outbox 发布器，否则返回 nil。
func provideOutboxPublisher(c *Client, cfg *Config) outbox.EventPublisher {
	if cfg == nil || !cfg.Enabled || !cfg.OutboxPublisher || c == nil || !c.Enabled() {
		return nil
	}
	slog.Info("hatchetx: providing outbox event publisher")
	return &outboxPublisher{client: c}
}

// provideOutboxPublisherOption 在装配期按配置注册 outbox 事件发布器。
func provideOutboxPublisherOption() di.Option {
	return func(i di.Injector) {
		c, _ := di.Invoke[*Client](i)
		cfg, _ := di.Invoke[*Config](i)
		if pub := provideOutboxPublisher(c, cfg); pub != nil {
			outbox.ProvidePublisher(pub)(i)
		}
	}
}
