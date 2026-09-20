package hatchetx

import (
	"context"
	"errors"
	"log/slog"

	"github.com/fitan/fxkit/outbox"
	"go.uber.org/fx"
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

func provideOutboxPublisherOption() fx.Option {
	return fx.Provide(
		fx.Annotate(
			provideOutboxPublisher,
			fx.As(new(outbox.EventPublisher)),
			fx.ResultTags(`group:"outbox_publishers"`),
		),
	)
}
