package config

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/internal/consuladdr"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
)

const watchConsulWait = 5 * time.Minute

// Dynamic 是只读的动态配置接口。消费方只依赖它，便于测试与替换。
// Watch[T] 实现它；框架对象（listener/db/auth/otel）不通过它热替换。
type Dynamic[T any] interface {
	Get() *T
}

// Watch 是 opt-in 的热更新配置句柄。
//
// 初始值来自当前 Config；若配置了 Consul，会在后台长轮询 Consul KV，
// 变化时原子替换。Get 返回的旧指针仍然有效。
//
// 只建议用于宿主业务配置；框架配置（server/db/auth/otel）仍应使用 ProvideConfig 的启动快照。
type Watch[T any] struct {
	mu     sync.RWMutex
	v      *T
	cancel context.CancelFunc
	once   sync.Once
}

// Get 返回当前配置快照。返回值只读，不要修改。
func (w *Watch[T]) Get() *T {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.v
}

// Close 停止后台 watch。可重复调用。
func (w *Watch[T]) Close() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
	})
}

func (w *Watch[T]) set(v T) {
	w.mu.Lock()
	w.v = &v
	w.mu.Unlock()
}

// Watch 构造一个 opt-in 热更新句柄。
// 只 watch Consul KV；本地 YAML 变化不监听。
func (c *Config) Watch[T any](key string) (*Watch[T], error) {
	if c == nil {
		return nil, fmt.Errorf("config: nil config")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("config: watch key is required")
	}
	var v T
	if err := c.UnmarshalKey(key, &v); err != nil {
		return nil, fmt.Errorf("config: watch %q: %w", key, err)
	}
	w := &Watch[T]{v: &v}

	addr := strings.TrimSpace(c.opts.ConsulAddress)
	if addr == "" || strings.TrimSpace(c.opts.ConsulConfigKey) == "" {
		return w, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go watchConsulLoop(ctx, addr, c.opts.ConsulConfigKey, key, w)
	return w, nil
}

// ProvideWatch 是 opt-in 的 DI provider：注入 *Watch[T]，并在 Lifecycle.Stop 时关闭。
func ProvideWatch[T any](key string) di.Option {
	return func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Watch[T], error) {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, err
			}
			w, err := cfg.Watch[T](key)
			if err != nil {
				return nil, err
			}
			if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
				lc.Append(di.Hook{OnStop: func(context.Context) error {
					w.Close()
					return nil
				}})
			}
			return w, nil
		})
		di.Provide(i, func(i di.Injector) (Dynamic[T], error) {
			w, err := di.Invoke[*Watch[T]](i)
			if err != nil {
				return nil, err
			}
			return w, nil
		})
	}
}

func watchConsulLoop[T any](ctx context.Context, addr, consulKey, section string, w *Watch[T]) {
	var index uint64
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		raw, nextIndex, err := watchConsulKV(ctx, addr, consulKey, index)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("config watch failed", "key", consulKey, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		if nextIndex != 0 {
			index = nextIndex
		} else {
			// Consul 没返回 index 时退避，避免无 wait 的忙循环。
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		if len(raw) == 0 {
			continue
		}
		var next T
		k := koanf.New(".")
		if err := k.Load(rawbytes.Provider(raw), yaml.Parser()); err != nil {
			slog.Warn("config watch yaml", "key", consulKey, "error", err)
			continue
		}
		if err := unmarshalYAML(k, section, &next); err != nil {
			slog.Warn("config watch decode", "key", consulKey, "section", section, "error", err)
			continue
		}
		w.set(next)
	}
}

func watchConsulKV(ctx context.Context, addr, key string, index uint64) ([]byte, uint64, error) {
	base, err := consuladdr.Normalize(addr)
	if err != nil {
		return nil, 0, err
	}
	key = strings.Trim(strings.TrimSpace(key), "/")
	if key == "" {
		return nil, 0, fmt.Errorf("consul config key is required")
	}
	u, err := url.Parse(base + "/v1/kv/" + key)
	if err != nil {
		return nil, 0, fmt.Errorf("consul kv url: %w", err)
	}
	q := u.Query()
	q.Set("raw", "true")
	if index > 0 {
		q.Set("index", strconv.FormatUint(index, 10))
		q.Set("wait", watchConsulWait.String())
	}
	u.RawQuery = q.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, watchConsulWait+30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	ApplyConsulToken(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, 0, fmt.Errorf("consul kv key %q not found", key)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("consul kv get %q status=%d body=%s", key, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var nextIndex uint64
	if raw := strings.TrimSpace(resp.Header.Get("X-Consul-Index")); raw != "" {
		if n, perr := strconv.ParseUint(raw, 10, 64); perr == nil {
			nextIndex = n
		}
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nextIndex, nil
	}
	return body, nextIndex, nil
}
