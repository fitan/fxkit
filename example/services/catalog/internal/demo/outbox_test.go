package demo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fitan/fxkit/outbox"

	"github.com/fitan/fxkit-example/services/catalog/internal/demo"
)

// TestOutboxListAndRequeueFailed 验证 failed 行被列出并可重放为 pending。
func TestOutboxListAndRequeueFailed(t *testing.T) {
	svc, client := newDemoService(t, demoDeps{obcfg: &outbox.Config{Enabled: true}})
	ctx := context.Background()

	row := outbox.OutboxEvent{
		Pubsub:    "hatchet",
		Topic:     "demo-topic",
		Payload:   []byte(`{"k":"v"}`),
		Status:    outbox.StatusFailed,
		Attempts:  3,
		LastError: "boom",
	}
	require.NoError(t, client.Conn(ctx).Create(&row).Error)

	items, err := svc.OutboxFailed(ctx, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "demo-topic", items[0].Topic)
	require.Equal(t, 3, items[0].Attempts)

	res, err := svc.RequeueOutbox(ctx, demo.RequeueOutboxReq{Topics: []string{"demo-topic"}})
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Requeued)

	// 重放后不再出现在 DLQ。
	items, err = svc.OutboxFailed(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, items)

	var got outbox.OutboxEvent
	require.NoError(t, client.Conn(ctx).First(&got, row.ID).Error)
	require.Equal(t, outbox.StatusPending, got.Status)
	require.Equal(t, 0, got.Attempts)
	require.Empty(t, got.LastError)
}
