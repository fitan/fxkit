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

// authorize 在身份已经确认之后做授权。
//
// Casbin 启用时 Enforce(sub, objPath, method)。未启用时登录即可，
// 不再按 HTTPRoute.Permission 做第二套判定。
func authorize(ctx context.Context, subj Subject, method, objPath string, enf *Enforcer) authzDecision {
	if enf == nil || !enf.Enabled() {
		return decisionAllow
	}
	allowed, err := enf.Enforce(ctx, EnforceInput{Sub: subj.ID, Dom: subj.OrgID, Obj: objPath, Act: method})
	if err != nil {
		// 判定失败必须拒绝（fail closed），由调用方翻译成 500。
		return decisionInternal
	}
	if !allowed {
		return decisionForbidden
	}
	return decisionAllow
}
