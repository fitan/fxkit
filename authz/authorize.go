package authz

import (
	"context"
)

// authzDecision 是「身份已知后」的授权判定结果。
type authzDecision int

const (
	decisionAllow authzDecision = iota
	decisionForbidden
	decisionInternal
)

// authorize 是 Huma 层与 mux 层共用的授权核心。
//
// 判定顺序：
//  1. Casbin 启用 → Enforce(sub, objPath, method)
//  2. 否则查路由表：命中且声明了 Permission → 校验 Subject
//  3. 否则未命中且 denyUnregistered → 拒绝
//
// 早期两层各写一遍这段逻辑，任何一处修 bug 都要同步改两遍，而且已经分叉
// （Huma 层有 auto-bind 与 op.Path 兜底，mux 层有 PublicPaths）。
//
// objPath 用于 Casbin 判定（真实请求路径）；routePath 用于查路由表
// （Huma 层可能回退到 operation 模板）。routePath 为空时等同于 objPath。
func authorize(
	ctx context.Context,
	subj Subject,
	method, objPath, routePath string,
	enf *Enforcer,
	idx routeIndex,
	denyUnregistered bool,
) authzDecision {
	if enf != nil && enf.Enabled() {
		allowed, err := enf.Enforce(ctx, EnforceInput{Sub: subj.ID, Obj: objPath, Act: method})
		if err != nil {
			// 判定失败必须拒绝（fail closed），由调用方翻译成 500。
			return decisionInternal
		}
		if !allowed {
			return decisionForbidden
		}
		return decisionAllow
	}

	if routePath == "" {
		routePath = objPath
	}
	route, ok := idx.find(method, routePath)
	if ok && route.Permission != "" && !subj.Has(route.Permission) {
		return decisionForbidden
	}
	if !ok && denyUnregistered {
		return decisionForbidden
	}
	return decisionAllow
}
