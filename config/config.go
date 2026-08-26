// Package config 用 koanf 加载 YAML（本地文件与 Consul KV）和 CLI 覆盖。
// 框架子系统的强类型配置在各自包内（server.Config、gormx.Config 等），
// 通过 [Load] / [Provide] 解顶层 key；宿主自有段同样如此。
//
// 优先级（高 → 低）：
//
//  1. [Options] 传入的 cobra flag（如 --port）
//  2. Consul KV YAML（Options.ConsulAddress + ConsulConfigKey；覆盖本地同名键）
//  3. Options.ConfigFile 本地 YAML
//  4. 各配置类型的 SetDefaults（[Load] 时）
//
// 配置树不从环境变量覆盖。定位配置源用 CLI `--config` / `--consul` / `--consul-key`。
// Consul ACL 仍读 CONSUL_HTTP_TOKEN（Consul 客户端惯例，不是配置树的一部分）。
// 非法值在 [Load] / [Provide] 时失败，进程退出，不会悄悄回退。
package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
	"go.uber.org/fx"
)

// Options 将 cobra 提供的覆盖项传入配置构建器。
//
// 在 main 包中通过 [fx.Supply] 填充（[cli] 包会根据 --config / --consul / --port flag 自动完成）。
type Options struct {
	ConfigFile      string // 本地 YAML 文件；为空时忽略
	ConsulAddress   string // Consul HTTP 地址（如 localhost:8500）；非空则从 KV 拉配置
	ConsulConfigKey string // Consul KV key（YAML 全文）；与 ConsulAddress 成对使用
	Port            string // 非空时覆盖 server.port
}

// Config 是运行时配置句柄。并发安全；用 [UnmarshalKey] / [Load] 解出自有段。
type Config struct {
	mu   sync.RWMutex
	k    *koanf.Koanf
	opts Options
}

// UnmarshalKey 将 key 处的值解码到 out。使用 `yaml` struct tag（与配置文件格式一致）。
// 业务配置请优先用 [Load] / [Provide] 或 [github.com/fitan/fxkit.ProvideConfig]。
func (c *Config) UnmarshalKey(key string, out any) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return unmarshalYAML(c.k, key, out)
}

// Set 写入单个键。失败时原树不变。主要用于测试。不做类型校验——非法值在随后的 [Load] 失败。
func (c *Config) Set(key string, val any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.k.Copy()
	if err := next.Set(key, val); err != nil {
		return err
	}
	c.k = next
	return nil
}

// Reload 重新读取本地 YAML（若有）与 Consul KV（若有）。每次重建底层树，避免旧 KV 残留。
// 失败时原树不变。[Provide] 注入的 *T 不会更新；要热更新请注入 *Config 再 [Load]。
func (c *Config) Reload() error {
	k, err := loadKoanf(context.Background(), c.opts)
	if err != nil {
		return fmt.Errorf("config reload: %w", err)
	}
	c.mu.Lock()
	c.k = k
	c.mu.Unlock()
	return nil
}

// New 构建 [Config]，遵循 CLI > consul/file 优先级。默认值由各段 [Load] 的 SetDefaults 填入。
func New(opts Options) (*Config, error) {
	k, err := loadKoanf(context.Background(), opts)
	if err != nil {
		return nil, err
	}
	slog.Info("config loaded",
		"file", opts.ConfigFile,
		"consul_config", strings.TrimSpace(opts.ConsulAddress) != "",
	)
	return &Config{k: k, opts: opts}, nil
}

func loadKoanf(ctx context.Context, opts Options) (*koanf.Koanf, error) {
	k := koanf.New(".")

	consulAddr := strings.TrimSpace(opts.ConsulAddress)
	consulKey := strings.TrimSpace(opts.ConsulConfigKey)
	if consulAddr != "" && consulKey == "" {
		return nil, fmt.Errorf("config: --consul-key is required when Consul address is set")
	}
	if consulKey != "" && consulAddr == "" {
		return nil, fmt.Errorf("config: --consul is required when Consul config key is set")
	}

	if opts.ConfigFile != "" {
		if err := k.Load(file.Provider(opts.ConfigFile), yaml.Parser()); err != nil {
			if consulAddr != "" && isConfigFileNotFound(err) {
				slog.Warn("local config file missing; loading from Consul only",
					"path", opts.ConfigFile,
				)
			} else {
				return nil, fmt.Errorf("config file %s: %w", opts.ConfigFile, err)
			}
		} else {
			slog.Info("config file loaded", "path", opts.ConfigFile)
		}
	}

	if consulAddr != "" {
		raw, err := fetchConsulKV(ctx, fetchConsulKVInput{
			Address: consulAddr,
			Key:     consulKey,
		})
		if err != nil {
			return nil, fmt.Errorf("config consul %s key %q: %w", consulAddr, consulKey, err)
		}
		if err := k.Load(rawbytes.Provider(raw), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("config consul merge %q: %w", consulKey, err)
		}
		slog.Info("config consul loaded", "address", consulAddr, "key", consulKey)
	}

	if opts.Port != "" {
		if err := k.Set("server.port", opts.Port); err != nil {
			return nil, fmt.Errorf("config port: %w", err)
		}
	}
	return k, nil
}

func isConfigFileNotFound(err error) bool {
	return err != nil && (os.IsNotExist(err) || errors.Is(err, os.ErrNotExist))
}

// Module 装配 [*Config] 与 [App]（yaml: app）。期望已 supply [Options]。
var Module = fx.Module("fxkit/config",
	fx.Provide(New),
	Provide[App]("app"),
)
