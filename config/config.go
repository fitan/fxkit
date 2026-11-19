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

	"github.com/fitan/fxkit/di"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
)

// Options 将 cobra 提供的覆盖项传入配置构建器。
type Options struct {
	ConfigFile      string // 本地 YAML 文件；为空时忽略
	ConsulAddress   string // Consul HTTP 地址（如 localhost:8500）；非空则从 KV 拉配置
	ConsulConfigKey string // Consul KV key（YAML 全文）；与 ConsulAddress 成对使用
	Port            string // 非空时覆盖 server.port
}

// Config 是运行时配置句柄。并发安全；用 [UnmarshalKey] / [Load] 解出自有段。
type Config struct {
	// mu 保护 k 的并发访问。
	mu sync.RWMutex
	// k 是已加载的配置树。
	k *koanf.Koanf
	// opts 是构造时的覆盖项；[Config.Watch] 用它判断是否长轮询 Consul。
	opts Options
}

// UnmarshalKey 将 key 处的值解码到 out。使用 `yaml` struct tag（与配置文件格式一致）。
// 业务配置请优先用 [Load] / [Provide] 或 [github.com/fitan/fxkit.ProvideConfig]。
func (c *Config) UnmarshalKey(key string, out any) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return unmarshalYAML(c.k, key, out)
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

// loadKoanf 按 本地 YAML → Consul KV → --port 覆盖 的顺序构建 koanf 实例。
// Consul 地址与 config key 必须成对提供，否则返回错误。
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

// isConfigFileNotFound 报告 err 是否表示配置文件不存在。
func isConfigFileNotFound(err error) bool {
	return err != nil && (os.IsNotExist(err) || errors.Is(err, os.ErrNotExist))
}

// Module 装配 [*Config] 与 [App]（yaml: app）。
var Module = di.Module("fxkit/config",
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Config, error) {
			// Options 由 cli 注入；未使用 cli 的宿主没有它属于合法情况，
			// 此时按「无覆盖项」处理。但其它错误（构造失败）必须中止，
			// 否则会静默得到一棵空配置树。
			opts, err := di.Invoke[Options](i)
			if err != nil {
				if !di.IsNotFound(err) {
					return nil, fmt.Errorf("config: resolve Options: %w", err)
				}
				opts = Options{}
			}
			return New(opts)
		})
	},
	Provide[App]("app"),
)
