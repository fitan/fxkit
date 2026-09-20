// Package consulx 提供基于 Consul Agent 的轻量级集成能力：
//
// # 核心特性
//
//  1. 服务自注册与健康检查：
//     当 discovery.register 为 true 时，以 app.name 注册到 Consul Catalog，
//     支持自动探测本机非虚拟/VPN IPv4 作为 advertise 地址，并使用 TTL 心跳保持活跃。
//
//  2. 构建元数据自动打补丁：
//     兼容已有注册项，自动为服务补全 git_commit、version 等构建元数据。
//
//  3. 分布式锁抽象层（Distributed Locker）：
//     提供统一的 Locker 与 Lock 抽象接口，并内置生产级基于 Consul Session 与 KV 机制的分布式锁实现：
//     - 基于 Consul 会话（Session）：自动管理会话生命周期与心跳续期（Heartbeat Renew），
//     在崩溃或网络分区时由 Consul TTL（默认 15s）机制自动释放锁，避免死锁。
//     - 强一致互斥：通过 KV ?acquire=<session-id> 与 ?release=<session-id> 实现严格排他互斥。
//     - 阻塞与非阻塞模式：支持 TryLock（快速冲突失败）与 Lock（基于 Blocking Query 的事件唤醒等待）。
//     - 闭包安全封装：提供 WithLock 快捷执行闭包，保证在业务执行完毕或 panic 时自动释放锁。
//     - 本地测试/单机降级：提供 NewMemoryLocker 内存锁实现，方便单元测试与本地独立开发。
//     - Fx 容器自动注入：consulx.Module 自动向 Fx 容器提供 Locker 依赖。
package consulx
