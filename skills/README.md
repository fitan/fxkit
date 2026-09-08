# fxkit Agent Skills

可复制到任意使用 [fxkit](../) 的项目，指导 Agent 按框架惯例开发。

源码包与 skill 对照见 [`fxkit/references/packages.md`](./fxkit/references/packages.md)（含「有意不单独建 skill」说明）。新增 `fxkit/<pkg>` 时先更新该表。

## 安装到其他项目

```bash
# Cursor 项目级（推荐）
cp -R skills/fxkit \
      skills/fxkit-huma-crud \
      skills/fxkit-hatchet \
      skills/fxkit-authz \
      skills/fxkit-otel \
      skills/fxkit-discovery \
      skills/fxkit-reqx \
      skills/fxkit-docs \
      <other-project>/.cursor/skills/

# 或一次拷贝全部
cp -R skills/fxkit* <other-project>/.cursor/skills/

# 个人全局
cp -R skills/fxkit* ~/.cursor/skills/
```

Claude Code：同样目录放到 `<project>/.claude/skills/`。

## Skills 一览

| Skill | 覆盖的 fxkit 包 | 何时用 |
|-------|-----------------|--------|
| `fxkit` | fxkit, cli, config, server, gormx, fxerrors, logx, buildinfo, cmd/fxkit | 新服务、模块、配置、路由、DB、错误、脚手架 |
| `fxkit-huma-crud` | huma, crudx | 列表 q=/cursor、`ListSpec`、可选 RegisterResource |
| `fxkit-hatchet` | hatchetx, outbox | 事件/MQ、workflow、cron、actor |
| `fxkit-authz` | authz | JWT、Casbin、权限登记 |
| `fxkit-otel` | otelx | Trace/Metric/Log、采样、OTLP |
| `fxkit-discovery` | consulx | Consul **自注册**、advertise、TTL |
| `fxkit-reqx` | reqx | **出站** HTTP、按服务名调下游、failover |
| `fxkit-docs` | docs, openapi | Scalar、base+Huma 合并、`openapi` dump、`gen client` |

每个目录下有独立 `SKILL.md`，按任务触发；不必把整个 README 塞进一个 skill。

## 依赖约定

- 示例 import：`github.com/fitan/fxkit`（按消费方 module / `replace` 替换）。
- 拉依赖：`GOPROXY=https://goproxy.cn,direct`（命令级 env）。
- 默认栈**不依赖 Dapr**；异步走 Hatchet。

## 本仓库

Skills 源文件在 [`skills/`](./)。消费方复制到自己的 `.cursor/skills/` 或 `.claude/skills/`。
