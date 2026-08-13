package authz

import (
	"context"
	"net/http"
	"strings"
)

// Subject 是已认证身份，注入 handler context。
type Subject struct {
	ID          string
	OrgID       string
	Permissions []string // Logto scopes / permissions，如 "users:list"
	Roles       []string // 可选，仅供展示；鉴权看 Permissions
}

type subjectCtxKey struct{}

// WithSubject 将 Subject 存入 ctx。
func WithSubject(ctx context.Context, s Subject) context.Context {
	return context.WithValue(ctx, subjectCtxKey{}, s)
}

// SubjectFromContext 返回此前通过 [WithSubject] 存入的 Subject。
func SubjectFromContext(ctx context.Context) (Subject, bool) {
	s, ok := ctx.Value(subjectCtxKey{}).(Subject)
	return s, ok
}

// Has 报告 subject 是否拥有 permission（或通配 "*"/ "admin:*"）。
func (s Subject) Has(permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return true
	}
	for _, p := range s.Permissions {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" || p == permission {
			return true
		}
		// resource:* matches a single action segment (orders:read), not orders:a:b
		if strings.HasSuffix(p, ":*") {
			prefix := strings.TrimSuffix(p, "*")
			if !strings.HasPrefix(permission, prefix) {
				continue
			}
			rest := strings.TrimPrefix(permission, prefix)
			if rest != "" && !strings.Contains(rest, ":") {
				return true
			}
		}
	}
	return false
}

// SubjectFunc 在无法从 JWT 得到身份时，从请求头构造开发用 Subject（仅 DevHeaderUser）。
type SubjectFunc func(ctx context.Context, h http.Header) Subject

// DefaultDevSubjectFunc 从 X-User 读取 ID。权限由 Casbin 决定，不在此处授予 "*"。
func DefaultDevSubjectFunc(_ context.Context, h http.Header) Subject {
	id := strings.TrimSpace(h.Get("X-User"))
	if id == "" {
		return Subject{}
	}
	return Subject{ID: id}
}
