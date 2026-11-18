package article

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/fitan/fxkit/hatchetx"
	"github.com/fitan/fxkit/outbox"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

const articleCreatedTopic = "article-created"

// ArticleCreatedEvent 是 article-created 的 outbox / Hatchet 负载。
type ArticleCreatedEvent struct {
	// ID 是文章 ID。
	ID int64 `json:"id"`
	// Title 是标题。
	Title string `json:"title"`
	// Author 是作者。
	Author string `json:"author"`
}

// HandleCreated 是 article-created 的第一个消费者：把文章标记为 indexed。
// inbox 按 事件 key 去重，保证至少一次投递下不会重复处理。
func (s *Service) HandleCreated(ctx context.Context, ev ArticleCreatedEvent) error {
	fn := func(ctx context.Context) error {
		return s.articles.MarkIndexed(ctx, ev.ID)
	}
	if s.inbox == nil {
		return fn(ctx)
	}
	return s.inbox.Once(ctx, outbox.OnceInput{
		Key:   fmt.Sprintf("%s:%d", articleCreatedTopic, ev.ID),
		Topic: articleCreatedTopic,
		Fn:    fn,
	})
}

// HandleCreatedFanout 是同一事件的第二个消费组，写入 fan-out 副表。
// 与 HandleCreated 使用不同 inbox key，互不影响。
func (s *Service) HandleCreatedFanout(ctx context.Context, ev ArticleCreatedEvent) error {
	fn := func(ctx context.Context) error {
		row := entity.ArticleFanout{ArticleID: ev.ID, Title: ev.Title}
		if err := s.articles.InsertFanout(ctx, &row); err != nil {
			return err
		}
		slog.Info("article-created fan-out", "article_id", ev.ID, "title", ev.Title)
		return nil
	}
	if s.inbox == nil {
		return fn(ctx)
	}
	return s.inbox.Once(ctx, outbox.OnceInput{
		Key:   fmt.Sprintf("%s-fanout:%d", articleCreatedTopic, ev.ID),
		Topic: articleCreatedTopic,
		Fn:    fn,
	})
}

// RegisterWorker 注册两个独立 task：同一个 Hatchet event key、不同 task 名，
// 形成两个消费组（MQ fan-out）。
func RegisterWorker(svc *Service) hatchetx.Registrar {
	return func(client *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
		if client == nil || !client.Enabled() {
			return nil, nil
		}
		task := client.SDK.NewStandaloneTask(
			"catalog-on-article-created",
			func(ctx hatchet.Context, ev ArticleCreatedEvent) (map[string]any, error) {
				err := svc.HandleCreated(ctx, ev)
				return map[string]any{"ok": err == nil, "id": ev.ID}, err
			},
			hatchet.WithWorkflowEvents(articleCreatedTopic),
		)
		// 同一个事件 key、不同 task 名称 = MQ fan-out（第二个消费组）。
		fanout := client.SDK.NewStandaloneTask(
			"catalog-on-article-created-fanout",
			func(ctx hatchet.Context, ev ArticleCreatedEvent) (map[string]any, error) {
				err := svc.HandleCreatedFanout(ctx, ev)
				return map[string]any{"ok": err == nil, "id": ev.ID}, err
			},
			hatchet.WithWorkflowEvents(articleCreatedTopic),
		)
		return []hatchetx.WorkflowBase{task, fanout}, nil
	}
}
