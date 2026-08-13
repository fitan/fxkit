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
