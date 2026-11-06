package outbox_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// partitionTestDB 用**文件型** sqlite：`:memory:` 在每个连接上是独立库，
// 并发 worker 会拿到新连接以至于找不到表。
// busy_timeout 避免并发 markPublished 时直接 SQLITE_BUSY。
func partitionTestDB(t *testing.T) *gormx.Client {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "partition.db") + "?_busy_timeout=5000&_journal_mode=WAL"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&outbox.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	return gormx.NewTestClient(db)
}

// orderedPublisher 记录每个 key 的投递顺序，并在投递时制造并发窗口。
type orderedPublisher struct {
	mu     sync.Mutex
	perKey map[string][]string
	seen   []string
	// concurrent 记录观察到的最大并发数。
	inFlight    int
	maxInFlight int
	hold        time.Duration
}

func newOrderedPublisher(hold time.Duration) *orderedPublisher {
	return &orderedPublisher{perKey: map[string][]string{}, hold: hold}
}

func (p *orderedPublisher) PublishEvent(_ context.Context, _, _ string, data []byte, _ map[string]string) error {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.maxInFlight {
		p.maxInFlight = p.inFlight
	}
	p.mu.Unlock()

	time.Sleep(p.hold) // 放大并发窗口

	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight--
	key := jsonKeyOf(data)
	p.perKey[key] = append(p.perKey[key], string(data))
	p.seen = append(p.seen, string(data))
	return nil
}

func (p *orderedPublisher) orderFor(key string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.perKey[key]...)
}

// jsonKeyOf 从 payload 里取 "k" 字段（测试用极简解析）。
func jsonKeyOf(data []byte) string {
	// payload 形如 {"k":"a","i":1}
	for i := 0; i+5 < len(data); i++ {
		if data[i] == '"' && data[i+1] == 'k' && data[i+2] == '"' {
			// 找 :"
			j := i + 3
			for j < len(data) && data[j] != ':' {
				j++
			}
			j++
			for j < len(data) && data[j] != '"' {
				j++
			}
			j++
			start := j
			for j < len(data) && data[j] != '"' {
				j++
			}
			return string(data[start:j])
		}
	}
	return ""
}

// TestRelay_PartitionFieldPreservesPerKeyOrder 是核心回归测试：
// 开启 partition_field 后，同一 key 的事件必须按产生顺序投递，
// 即使 publish_concurrency > 1（组间并行）。
func TestRelay_PartitionFieldPreservesPerKeyOrder(t *testing.T) {
	client := partitionTestDB(t)
	ctx := context.Background()

	// 三个 key，每个 key 5 条，交错写入。
	var rows []outbox.OutboxEvent
	for i := 0; i < 5; i++ {
		for _, k := range []string{"a", "b", "c"} {
			rows = append(rows, outbox.OutboxEvent{
				Pubsub:  "p",
				Topic:   "t",
				Payload: []byte(`{"k":"` + k + `","i":` + string(rune('0'+i)) + `}`),
				Status:  outbox.StatusPending,
			})
		}
	}
	for i := range rows {
		require.NoError(t, client.Conn(ctx).Create(&rows[i]).Error)
	}

	cfg := mustOutboxCfg(t)
	cfg.PublishConcurrency = 3
	cfg.PartitionField = "k"

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	pub := newOrderedPublisher(5 * time.Millisecond)
	relay.SetPublisher(pub)

	n, err := relay.ProcessBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, 15, n)

	// 每个 key 内部必须保序（i 递增）。
	for _, k := range []string{"a", "b", "c"} {
		got := pub.orderFor(k)
		require.Len(t, got, 5, "key %s", k)
		for i, payload := range got {
			want := `"i":` + string(rune('0'+i))
			assert.Contains(t, payload, want, "key %s position %d", k, i)
		}
	}

	// 不同 key 之间应当发生过并行（否则分片没生效）。
	assert.Greater(t, pub.maxInFlight, 1, "different keys should publish in parallel")
}

// TestRelay_PartitionFieldUnorderedKeyFallsBackToSerial 取不到分组键时，
// 这些事件归入同一个串行组（宁可牺牲并发也不打乱顺序）。
func TestRelay_PartitionFieldUnorderedKeyFallsBackToSerial(t *testing.T) {
	client := partitionTestDB(t)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		require.NoError(t, client.Conn(ctx).Create(&outbox.OutboxEvent{
			Pubsub:  "p",
			Topic:   "t",
			Payload: []byte(`{"noKey":true}`),
			Status:  outbox.StatusPending,
		}).Error)
	}

	cfg := mustOutboxCfg(t)
	cfg.PublishConcurrency = 4
	cfg.PartitionField = "k"

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	pub := newOrderedPublisher(3 * time.Millisecond)
	relay.SetPublisher(pub)

	n, err := relay.ProcessBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, n)
	assert.Equal(t, 1, pub.maxInFlight, "events without partition key must be serialized")
}

// TestConfig_PartitionFieldValidation 非法分组字段必须启动失败。
func TestConfig_PartitionFieldValidation(t *testing.T) {
	cfg := outbox.Config{PollInterval: time.Second, BatchSize: 10, ClaimTimeout: time.Second}
	cfg.PartitionField = "a b"
	assert.Error(t, cfg.Validate())

	cfg.PartitionField = "actorId"
	assert.NoError(t, cfg.Validate())
}
