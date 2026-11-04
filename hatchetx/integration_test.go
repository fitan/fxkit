package hatchetx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"
	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 这组测试跑在**真实** Hatchet 引擎上，默认 skip。
//
//	FXKIT_TEST_HATCHET_TOKEN=...            # 必填（或 FXKIT_TEST_HATCHET_TOKEN_FILE）
//	FXKIT_TEST_HATCHET_TOKEN_FILE=/path     # 从文件读 token（避免 secret 进命令行）
//	FXKIT_TEST_HATCHET_HOST_PORT=host:7077  # 默认 10.170.34.223:7077
//	FXKIT_TEST_HATCHET_TLS_STRATEGY=none    # 默认 none（引擎 SERVER_GRPC_INSECURE=t）
//	FXKIT_TEST_HATCHET_SERVER_URL=http://host:8088
//
// 副作用：临时 workflow / run / cron，测试结束会尽力清理。

func integrationToken(t *testing.T) string {
	t.Helper()
	token := strings.TrimSpace(os.Getenv("FXKIT_TEST_HATCHET_TOKEN"))
	if token == "" {
		if path := strings.TrimSpace(os.Getenv("FXKIT_TEST_HATCHET_TOKEN_FILE")); path != "" {
			raw, err := os.ReadFile(path)
			require.NoError(t, err, "read token file")
			token = strings.TrimSpace(string(raw))
		}
	}
	if token == "" {
		t.Skip("set FXKIT_TEST_HATCHET_TOKEN (or _TOKEN_FILE) to run against a real Hatchet engine")
	}
	return token
}

func integrationClient(t *testing.T) (*Client, func()) {
	t.Helper()
	cfg := &Config{
		Enabled:     true,
		Token:       integrationToken(t),
		HostPort:    envOrDefault("FXKIT_TEST_HATCHET_HOST_PORT", "10.170.34.223:7077"),
		TLSStrategy: envOrDefault("FXKIT_TEST_HATCHET_TLS_STRATEGY", "none"),
		ServerURL:   envOrDefault("FXKIT_TEST_HATCHET_SERVER_URL", "http://10.170.34.223:8088"),
		WorkerName:  fmt.Sprintf("fxkit-itest-%d", time.Now().UnixNano()),
	}
	client, err := NewClient(cfg)
	require.NoError(t, err, "new client")
	require.True(t, client.Enabled(), "client disabled; check token/host_port")
	return client, func() {}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TestWorkerIntegration：注册 standalone task → 起 worker → 推事件 → 断言 handler 执行。
func TestWorkerIntegration(t *testing.T) {
	client, _ := integrationClient(t)
	ctx := context.Background()

	eventKey := fmt.Sprintf("fxkit-itest-%d", time.Now().UnixNano())
	got := make(chan string, 1)

	task := client.SDK.NewStandaloneTask(
		fmt.Sprintf("fxkit-itest-task-%d", time.Now().UnixNano()),
		func(_ hatchet.Context, in itestInput) (map[string]any, error) {
			got <- in.Msg
			return map[string]any{"ok": true}, nil
		},
		hatchet.WithWorkflowEvents(eventKey),
	)

	worker, err := client.SDK.NewWorker(fmt.Sprintf("fxkit-itest-w-%d", time.Now().UnixNano()), hatchet.WithWorkflows(task))
	require.NoError(t, err, "new worker")
	cleanup, err := worker.Start()
	require.NoError(t, err, "start worker")
	defer func() { _ = cleanup() }()

	require.NoError(t, client.PushEventJSON(ctx, eventKey, []byte(`{"msg":"hello-from-fxkit"}`), nil), "push event")

	select {
	case msg := <-got:
		require.Equal(t, "hello-from-fxkit", msg)
	case <-time.After(60 * time.Second):
		t.Fatal("task did not execute within 60s (engine unreachable, token invalid, or worker not scheduled)")
	}
}

type itestInput struct {
	Msg string `json:"msg"`
}

// TestActorIntegration：同一个 actorId 的并发调用必须串行（MaxRuns=1 邮箱语义）。
func TestActorIntegration(t *testing.T) {
	client, _ := integrationClient(t)
	ctx := context.Background()

	var current, maxConc, runs atomic.Int64
	actor := NewActor[itestActorInput, itestActorOut](
		fmt.Sprintf("fxkit-itest-actor-%d", time.Now().UnixNano()),
		func(_ context.Context, in itestActorInput) (itestActorOut, error) {
			cur := current.Add(1)
			for {
				m := maxConc.Load()
				if cur <= m || maxConc.CompareAndSwap(m, cur) {
					break
				}
			}
			// 留出重叠窗口：若并发控制失效，maxConc 会 > 1。
			time.Sleep(40 * time.Millisecond)
			current.Add(-1)
			runs.Add(1)
			return itestActorOut{OK: true, Seq: in.Seq}, nil
		},
	)

	workflows, err := actor.Register(client)
	require.NoError(t, err)
	require.NotEmpty(t, workflows)

	worker, err := client.SDK.NewWorker(fmt.Sprintf("fxkit-itest-aw-%d", time.Now().UnixNano()), hatchet.WithWorkflows(workflows...))
	require.NoError(t, err)
	cleanup, err := worker.Start()
	require.NoError(t, err)
	defer func() { _ = cleanup() }()

	const n = 5
	for i := 0; i < n; i++ {
		_, err := actor.CallAsync(ctx, client, itestActorInput{
			ActorRef: ActorRef{ActorID: "mailbox-1"},
			Seq:      i,
		})
		require.NoError(t, err, "call async %d", i)
	}

	deadline := time.Now().Add(90 * time.Second)
	for runs.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d actor runs executed", runs.Load(), n)
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, int64(n), runs.Load())
	require.Equal(t, int64(1), maxConc.Load(), "actor mailbox must serialize per actorId")
}

// TestActorParallelAcrossActorIDs：不同 actorId 必须可以并行，而各自内部仍串行。
func TestActorParallelAcrossActorIDs(t *testing.T) {
	client, _ := integrationClient(t)
	ctx := context.Background()

	var mu sync.Mutex
	perID := map[string]int{}
	maxPerID := map[string]int{}
	inFlight, maxAll := 0, 0
	var runs atomic.Int64

	actor := NewActor[itestActorInput, itestActorOut](
		fmt.Sprintf("fxkit-itest-parallel-%d", time.Now().UnixNano()),
		func(_ context.Context, in itestActorInput) (itestActorOut, error) {
			mu.Lock()
			inFlight++
			perID[in.ActorID]++
			if perID[in.ActorID] > maxPerID[in.ActorID] {
				maxPerID[in.ActorID] = perID[in.ActorID]
			}
			if inFlight > maxAll {
				maxAll = inFlight
			}
			mu.Unlock()

			time.Sleep(250 * time.Millisecond)

			mu.Lock()
			perID[in.ActorID]--
			inFlight--
			mu.Unlock()
			runs.Add(1)
			return itestActorOut{OK: true, Seq: in.Seq}, nil
		},
	)

	workflows, err := actor.Register(client)
	require.NoError(t, err)
	worker, err := client.SDK.NewWorker(fmt.Sprintf("fxkit-itest-pw-%d", time.Now().UnixNano()),
		hatchet.WithWorkflows(workflows...), hatchet.WithSlots(10))
	require.NoError(t, err)
	cleanup, err := worker.Start()
	require.NoError(t, err)
	defer func() { _ = cleanup() }()

	for _, id := range []string{"mailbox-a", "mailbox-b"} {
		for i := 0; i < 2; i++ {
			_, err := actor.CallAsync(ctx, client, itestActorInput{ActorRef: ActorRef{ActorID: id}, Seq: i})
			require.NoError(t, err)
		}
	}

	deadline := time.Now().Add(90 * time.Second)
	for runs.Load() < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/4 runs executed", runs.Load())
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, maxPerID["mailbox-a"], "same actorId must stay serialized")
	require.Equal(t, 1, maxPerID["mailbox-b"], "same actorId must stay serialized")
	require.GreaterOrEqual(t, maxAll, 2, "different actorIds should run in parallel")
}

type itestActorInput struct {
	ActorRef
	Seq int `json:"seq"`
}

type itestActorOut struct {
	OK  bool `json:"ok"`
	Seq int  `json:"seq"`
}

// TestReminderIntegration：走 dashboard REST（Crons API）创建 → 查询 → 删除。
func TestReminderIntegration(t *testing.T) {
	client, _ := integrationClient(t)
	require.NotNil(t, client.Crons(), "crons client requires server_url + token")
	ctx := context.Background()

	ts := time.Now().UnixNano()
	wfName := fmt.Sprintf("fxkit-itest-reminder-wf-%d", ts)
	eventKey := wfName + "-evt"
	cronName := "itest-cron"

	// 先让 workflow 在 tenant 里存在（cron 需要引用一个已注册的 workflow）。
	task := client.SDK.NewStandaloneTask(wfName, func(_ hatchet.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	}, hatchet.WithWorkflowEvents(eventKey))
	worker, err := client.SDK.NewWorker(fmt.Sprintf("fxkit-itest-rw-%d", ts), hatchet.WithWorkflows(task))
	require.NoError(t, err)
	cleanup, err := worker.Start()
	require.NoError(t, err)
	defer func() { _ = cleanup() }()

	require.NoError(t, client.PushEventJSON(ctx, eventKey, []byte(`{"seed":true}`), nil))
	time.Sleep(3 * time.Second) // 等 workflow 注册/执行完成

	id, err := CreateReminder(ctx, client, CreateReminderInput{
		WorkflowName: wfName,
		ActorID:      "sched-1",
		ReminderName: cronName,
		Name:         cronName,
		Period:       "*/30 * * * * *",
		Data:         `{"ping":true}`,
	})
	require.NoError(t, err, "create reminder")
	require.NotEmpty(t, id)
	t.Cleanup(func() { _ = DeleteReminder(context.Background(), client, id) })

	// List 可能略有延迟，允许短暂重试。
	var found *rest.CronWorkflows
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, findErr := FindReminder(ctx, client, wfName, cronName)
		require.NoError(t, findErr)
		if got != nil {
			found = got
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	require.NotNil(t, found, "reminder should be findable after create")
	require.Equal(t, id, found.Metadata.Id)

	require.NoError(t, DeleteReminder(ctx, client, id), "delete reminder")

	deadline = time.Now().Add(10 * time.Second)
	for {
		gone, findErr := FindReminder(ctx, client, wfName, cronName)
		require.NoError(t, findErr)
		if gone == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reminder still present after delete")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// TestReminderFires 验证 cron 真的会按周期投递（不只是 CRUD）。
func TestReminderFires(t *testing.T) {
	client, _ := integrationClient(t)
	ctx := context.Background()
	ts := time.Now().UnixNano()
	wf := fmt.Sprintf("fxkit-itest-reminder-fire-%d", ts)
	fired := make(chan string, 8)

	task := client.SDK.NewStandaloneTask(wf, func(_ hatchet.Context, in reminderFireInput) (map[string]any, error) {
		select {
		case fired <- in.ActorID:
		default:
		}
		return map[string]any{"ok": true}, nil
	})
	worker, err := client.SDK.NewWorker(fmt.Sprintf("fxkit-itest-fw-%d", ts), hatchet.WithWorkflows(task))
	require.NoError(t, err)
	cleanup, err := worker.Start()
	require.NoError(t, err)
	defer func() { _ = cleanup() }()

	id, err := CreateReminder(ctx, client, CreateReminderInput{
		WorkflowName: wf,
		ActorID:      "sched-fire",
		ReminderName: "fire",
		Name:         "fire-cron",
		Period:       "*/5 * * * * *", // 每 5 秒
		Data:         `{"ping":true}`,
	})
	require.NoError(t, err, "create reminder")
	require.NotEmpty(t, id)
	t.Cleanup(func() { _ = DeleteReminder(context.Background(), client, id) })

	select {
	case got := <-fired:
		require.Equal(t, "sched-fire", got)
	case <-time.After(45 * time.Second):
		t.Fatal("reminder did not fire within 45s")
	}
	require.NoError(t, DeleteReminder(ctx, client, id))
}

type reminderFireInput struct {
	ActorID      string `json:"actorId"`
	ReminderName string `json:"reminderName"`
	Data         string `json:"data"`
}

// TestOutboxToHatchet 验证「事务 outbox → 真实 Hatchet 事件 → worker 消费」完整链路。
func TestOutboxToHatchet(t *testing.T) {
	token := integrationToken(t)
	cfg := &Config{
		Enabled:         true,
		Token:           token,
		HostPort:        envOrDefault("FXKIT_TEST_HATCHET_HOST_PORT", "10.170.34.223:7077"),
		TLSStrategy:     envOrDefault("FXKIT_TEST_HATCHET_TLS_STRATEGY", "none"),
		ServerURL:       envOrDefault("FXKIT_TEST_HATCHET_SERVER_URL", "http://10.170.34.223:8088"),
		OutboxPublisher: true,
		WorkerName:      fmt.Sprintf("fxkit-itest-ow-%d", time.Now().UnixNano()),
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)
	require.True(t, client.Enabled())

	pub := provideOutboxPublisher(client, cfg)
	require.NotNil(t, pub, "outbox publisher should be enabled by hatchet.outbox_publisher")

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "outbox.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&outbox.OutboxEvent{}))
	gclient := gormx.NewTestClient(db)
	store := outbox.NewStore(gclient)

	ctx := context.Background()
	eventKey := fmt.Sprintf("fxkit-itest-outbox-%d", time.Now().UnixNano())
	got := make(chan string, 1)

	task := client.SDK.NewStandaloneTask("wf-"+eventKey, func(_ hatchet.Context, in itestInput) (map[string]any, error) {
		select {
		case got <- in.Msg:
		default:
		}
		return map[string]any{"ok": true}, nil
	}, hatchet.WithWorkflowEvents(eventKey))
	worker, err := client.SDK.NewWorker("w-"+eventKey, hatchet.WithWorkflows(task))
	require.NoError(t, err)
	cleanup, err := worker.Start()
	require.NoError(t, err)
	defer func() { _ = cleanup() }()

	require.NoError(t, store.Enqueue(ctx, outbox.Message{
		Topic:   eventKey,
		Payload: []byte(`{"msg":"via-outbox"}`),
	}))

	relay := outbox.NewRelay(outbox.NewRelayParams{
		Client: gclient,
		Outbox: &outbox.Config{
			Enabled:            true,
			PollInterval:       time.Second,
			BatchSize:          10,
			MaxRetries:         3,
			ClaimTimeout:       30 * time.Second,
			PublishConcurrency: 1,
		},
		Publishers: []outbox.EventPublisher{pub},
	})
	n, err := relay.ProcessBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "one outbox event should be published")

	select {
	case msg := <-got:
		require.Equal(t, "via-outbox", msg)
	case <-time.After(60 * time.Second):
		t.Fatal("outbox event was not delivered through Hatchet")
	}
}
