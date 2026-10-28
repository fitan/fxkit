package hatchetx

import (
	"context"
	"errors"
	"log/slog"

	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/di"
)

// outboxPublisher pushes outbox rows as Hatchet events (event key = topic name).
type outboxPublisher struct {
	client *Client
}

func (p *outboxPublisher) PublishEvent(ctx context.Context, _, topicName string, data []byte, metadata map[string]string) error {
	if p == nil || p.client == nil || !p.client.Enabled() {
		return errors.New("outbox: nil hatchet client")
	}
	return p.client.PushEventJSON(ctx, topicName, data, metadata)
}

func provideOutboxPublisher(c *Client, cfg *Config) outbox.EventPublisher {
	if cfg == nil || !cfg.Enabled || !cfg.OutboxPublisher || c == nil || !c.Enabled() {
		return nil
	}
	slog.Info("hatchetx: providing outbox event publisher")
	return &outboxPublisher{client: c}
}

func provideOutboxPublisherOption() di.Option {
	return func(i di.Injector) {
		c, _ := di.Invoke[*Client](i)
		cfg, _ := di.Invoke[*Config](i)
		if pub := provideOutboxPublisher(c, cfg); pub != nil {
			outbox.ProvidePublisher(pub)(i)
		}
	}
}
