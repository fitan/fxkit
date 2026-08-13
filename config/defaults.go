package config

import "github.com/spf13/viper"

// setDefaults 为每个 [Core] 字段填入可用默认值，避免缺少 YAML 时启动崩溃。
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", "8080")
	v.SetDefault("server.log_payloads", false)
	v.SetDefault("server.cors_allowed_origins", []string{"*"})

	v.SetDefault("app.name", "fxkit-app")
	v.SetDefault("app.seed_demo_schedules_on_start", true)

	v.SetDefault("db.driver", "")
	v.SetDefault("db.dsn", "")
	v.SetDefault("db.log_level", "info")
	v.SetDefault("db.max_idle_conns", 10)
	v.SetDefault("db.max_open_conns", 100)
	v.SetDefault("db.conn_max_lifetime_sec", 3600)

	v.SetDefault("outbox.enabled", false)
	v.SetDefault("outbox.poll_interval", "1s")
	v.SetDefault("outbox.batch_size", 50)
	v.SetDefault("outbox.max_retries", 10)
	v.SetDefault("outbox.claim_timeout", "30s")

	v.SetDefault("hatchet.enabled", false)
	v.SetDefault("hatchet.token", "")
	v.SetDefault("hatchet.host_port", "")
	v.SetDefault("hatchet.namespace", "")
	v.SetDefault("hatchet.worker_name", "fxkit-worker")
	v.SetDefault("hatchet.outbox_publisher", true)

	v.SetDefault("otel.enabled", false)
	v.SetDefault("otel.service_name", "")
	v.SetDefault("otel.service_version", "")
	v.SetDefault("otel.environment", "development")
	v.SetDefault("otel.endpoint", "localhost:4317")
	v.SetDefault("otel.protocol", "grpc")
	v.SetDefault("otel.insecure", true)
	v.SetDefault("otel.sampling", "always_on")
	v.SetDefault("otel.traces.enabled", true)
	v.SetDefault("otel.traces.exporter", "otlp")
	v.SetDefault("otel.metrics.enabled", true)
	v.SetDefault("otel.metrics.exporter", "otlp")
	v.SetDefault("otel.metrics.runtime_metrics", true)
	v.SetDefault("otel.logs.enabled", true)
	v.SetDefault("otel.logs.exporter", "otlp")

	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.issuer", "")
	v.SetDefault("auth.audience", "")
	v.SetDefault("auth.jwks_url", "")
	v.SetDefault("auth.org_claim", "organization_id")
	v.SetDefault("auth.require_org", false)
	v.SetDefault("auth.dev_header_user", false)
	v.SetDefault("auth.tls_insecure", false)
	v.SetDefault("auth.deny_unregistered", false)
	v.SetDefault("auth.casbin.enabled", false)
	v.SetDefault("auth.casbin.table_name", "casbin_rule")
	v.SetDefault("auth.casbin.auto_register_routes", true)
	v.SetDefault("auth.casbin.bootstrap_role", "admin")
	v.SetDefault("auth.casbin.auto_bind_bootstrap", false)

	v.SetDefault("discovery.consul_address", "")
	v.SetDefault("discovery.consul_passing_only", true)
	v.SetDefault("discovery.register", false)
	v.SetDefault("discovery.advertise_address", "")
}
