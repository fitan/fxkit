package mcpx

import "testing"

func TestToolEnabled(t *testing.T) {
	all := Config{}
	if !toolEnabled(all, "anything") {
		t.Fatal("empty config should allow all tools")
	}

	inc := Config{IncludeTools: []string{"listArticles", "getArticle"}}
	if !toolEnabled(inc, "listArticles") {
		t.Fatal("included tool should be enabled")
	}
	if toolEnabled(inc, "deleteArticle") {
		t.Fatal("tool outside include list must be disabled")
	}

	exc := Config{ExcludeTools: []string{"listAuthzRoles"}}
	if toolEnabled(exc, "listAuthzRoles") {
		t.Fatal("excluded tool must be disabled")
	}
	if !toolEnabled(exc, "listArticles") {
		t.Fatal("non-excluded tool should be enabled")
	}

	both := Config{
		IncludeTools: []string{"listAuthzRoles", "listArticles"},
		ExcludeTools: []string{"listAuthzRoles"},
	}
	if toolEnabled(both, "listAuthzRoles") {
		t.Fatal("exclude must win over include")
	}
	if !toolEnabled(both, "listArticles") {
		t.Fatal("included non-excluded tool should be enabled")
	}
}
