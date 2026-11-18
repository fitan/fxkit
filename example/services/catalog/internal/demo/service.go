package demo

import (
	"sync/atomic"

	"github.com/fitan/fxkit/cachex"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/fitan/fxkit/outbox"
)

// Service 汇总框架可选能力的演示实现：缓存、动态配置、分布式锁、
// outbox DLQ、Hatchet 定时/持久/DAG、AfterCommit。
type Service struct {
	cache   cachex.Cache
	dynamic config.Dynamic[DemoConfig]
	cfg     *DemoConfig
	locker  consulx.Locker
	store   *outbox.Store
	obcfg   *outbox.Config
	hatchet *hatchetx.Client
	db      *gormx.Client

	loads       atomic.Int64
	committed   atomic.Int64
	afterCommit atomic.Int64
}

// NewService 保存各可选依赖；某项为 nil 时对应接口会返回 503。
func NewService(
	cache cachex.Cache,
	dynamic config.Dynamic[DemoConfig],
	cfg *DemoConfig,
	locker consulx.Locker,
	store *outbox.Store,
	obcfg *outbox.Config,
	ht *hatchetx.Client,
	db *gormx.Client,
) (*Service, error) {
	if db == nil {
		return nil, fxerrors.Internal("demo: nil gormx client")
	}
	if cfg == nil {
		cfg = &DemoConfig{}
		cfg.SetDefaults()
	}
	return &Service{
		cache:   cache,
		dynamic: dynamic,
		cfg:     cfg,
		locker:  locker,
		store:   store,
		obcfg:   obcfg,
		hatchet: ht,
		db:      db,
	}, nil
}

// outboxEnabled 判断 outbox 是否真的启用（未启用时 DLQ 接口返回 503）。
func (s *Service) outboxEnabled() bool {
	return s.store != nil && s.obcfg != nil && s.obcfg.Enabled
}
