package buildinfo

import "testing"

func TestGetDefaults(t *testing.T) {
	Version = "dev"
	Commit = "unknown"
	GitRemote = "unknown"
	GitBranch = "unknown"
	Dirty = "false"
	GoVersion = "unknown"

	info := Get()
	if info.Version != "dev" {
		t.Fatalf("version: got %q", info.Version)
	}
	if info.Dirty {
		t.Fatal("expected clean")
	}
}

func TestShortRevision(t *testing.T) {
	if got := shortRevision("c85f467627ee8cc6b695df324d141b68c0e2510c"); got != "c85f467627ee" {
		t.Fatalf("shortRevision: got %q", got)
	}
	if got := shortRevision("abc"); got != "abc" {
		t.Fatalf("shortRevision: got %q", got)
	}
}

func TestParseDirty(t *testing.T) {
	Dirty = "true"
	info := Get()
	if !info.Dirty {
		t.Fatal("expected dirty")
	}
}

func TestMetaOmitsEmpty(t *testing.T) {
	Version = "1.2.3"
	Commit = "abc123"
	GitRemote = "unknown"
	GitBranch = "main"
	Dirty = "false"
	GoVersion = "go1.26.0"
	BuildTime = ""
	BuiltBy = ""

	m := Meta()
	if m["version"] != "1.2.3" || m["build_commit"] != "abc123" {
		t.Fatalf("meta=%v", m)
	}
	if _, ok := m["build_time"]; ok {
		t.Fatal("empty build_time should be omitted")
	}
	if _, ok := m["build_built_by"]; ok {
		t.Fatal("empty built_by should be omitted")
	}
	if m["git_branch"] != "main" || m["go_version"] != "go1.26.0" {
		t.Fatalf("meta=%v", m)
	}
}
