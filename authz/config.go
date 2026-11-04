package authz

import (
	"fmt"
	"strings"
	"time"
)

// Config 身份（JWT / X-User）与 Casbin 授权（yaml: auth）。只覆盖 Huma，不覆盖裸 chi。
type Config struct {
	// Enabled 鉴权总开关。默认 false。
	Enabled bool `yaml:"enabled"`
	// Issuer OIDC issuer。Casbin + DevHeaderUser 本地可不设。
	Issuer string `yaml:"issuer"`
	// Audience API Resource（access token 的 aud）。
	Audience string `yaml:"audience"`
	// JWKSURL 默认 {issuer}/jwks。
	JWKSURL string `yaml:"jwks_url"`
	// OrgClaim 从 JWT 写入 Subject.OrgID 的 claim 名，默认 organization_id。
	OrgClaim string `yaml:"org_claim"`
	// RequireOrg 为 true 时拒绝缺少 OrgClaim 的 token。
	RequireOrg bool `yaml:"require_org"`
	// DevHeaderUser 允许 X-User 代替 JWT（仅本地；生产务必 false）。
	DevHeaderUser bool `yaml:"dev_header_user"`
	// TLSInsecure 跳过 JWKS HTTPS 证书校验（仅本地自签 IdP）。
	TLSInsecure bool `yaml:"tls_insecure"`
	// DenyUnregistered 仅在 Casbin 关闭时生效：未出现在 HTTPRoute 表中的
	// Huma 路由返回 403。默认 false：未登记路由仍需登录（不再匿名放行）。
	// Casbin 启用时一律 Enforce，未匹配策略即 403。
	DenyUnregistered bool `yaml:"deny_unregistered"`
	// MuxMiddleware 在 chi/mux 层统一鉴权（覆盖 Huma + 裸 chi + docs + MCP）。
	//
	// 开启后不再安装 Huma 层中间件（避免重复判定），Subject 写入 request context，
	// 因此裸 chi handler 也能用 authz.SubjectFromContext 取身份。
	// 注意：Huma operation 的 authz.public 元数据仅在 Huma 层中间件生效；
	// mux 模式下请把匿名路径写进 PublicPaths。
	MuxMiddleware bool `yaml:"mux_middleware"`
	// PublicPaths 在 MuxMiddleware 模式下完全放行的路径（精确匹配或前缀）。
	PublicPaths []string `yaml:"public_paths"`
	// Casbin path/method RBAC。
	Casbin CasbinConfig `yaml:"casbin"`
}

// CasbinConfig Casbin 执行器。
type CasbinConfig struct {
	// Enabled 打开 Enforce(sub, path, METHOD)。默认 false。
	Enabled bool `yaml:"enabled"`
	// TableName 策略表名，默认 casbin_rule。
	TableName string `yaml:"table_name"`
	// AutoRegisterRoutes 启动时把 HTTPRoute 写成 p 规则。默认 true。
	AutoRegisterRoutes bool `yaml:"auto_register_routes"`
	// BootstrapRole 自动注册权限的目标角色，默认 admin。
	BootstrapRole string `yaml:"bootstrap_role"`
	// BootstrapUsers 启动时绑到 BootstrapRole 的用户；可空。
	BootstrapUsers []string `yaml:"bootstrap_users"`
	// AutoBindBootstrap 无角色 subject 首次请求自动绑 BootstrapRole。生产务必 false。
	AutoBindBootstrap bool `yaml:"auto_bind_bootstrap"`
	// ReloadInterval 多副本从 DB 重载策略的间隔。默认 5s；0 关闭。
	ReloadInterval time.Duration `yaml:"reload_interval"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.OrgClaim = "organization_id"
	c.Casbin.TableName = "casbin_rule"
	c.Casbin.AutoRegisterRoutes = true
	c.Casbin.BootstrapRole = "admin"
	c.Casbin.BootstrapUsers = []string{}
	c.Casbin.ReloadInterval = 5 * time.Second
}

// Validate 检查 Casbin 间隔，以及启用鉴权时的 issuer/audience。
func (c Config) Validate() error {
	if c.Casbin.ReloadInterval < 0 {
		return fmt.Errorf("auth.casbin.reload_interval must be >= 0")
	}
	if c.Casbin.Enabled {
		if strings.TrimSpace(c.Casbin.TableName) == "" {
			return fmt.Errorf("auth.casbin.table_name is required when casbin is enabled")
		}
		if strings.TrimSpace(c.Casbin.BootstrapRole) == "" {
			return fmt.Errorf("auth.casbin.bootstrap_role is required when casbin is enabled")
		}
	}
	if c.RequireOrg && strings.TrimSpace(c.OrgClaim) == "" {
		return fmt.Errorf("auth.org_claim is required when auth.require_org=true")
	}
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Issuer) == "" || strings.TrimSpace(c.Audience) == "" {
		if c.Casbin.Enabled && c.DevHeaderUser {
			return nil
		}
		if strings.TrimSpace(c.Issuer) == "" {
			return fmt.Errorf("auth.issuer is required when auth.enabled=true")
		}
		return fmt.Errorf("auth.audience is required when auth.enabled=true")
	}
	return nil
}
