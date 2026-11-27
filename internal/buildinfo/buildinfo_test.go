package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolveStampedReleaseBuild(t *testing.T) {
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.time", Value: "2026-11-26T00:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	got := resolve(Info{
		Version:   "v0.2.0",
		Commit:    "675f305999",
		BuildDate: "2026-11-27T09:00:00Z",
	}, "0.1.0", bi)

	if !got.Stamped {
		t.Fatal("expected Stamped=true")
	}
	if got.Version != "0.2.0" {
		t.Fatalf("Version = %q, want %q", got.Version, "0.2.0")
	}
	if got.Tag() != "v0.2.0" {
		t.Fatalf("Tag() = %q, want %q", got.Tag(), "v0.2.0")
	}
	if got.Commit != "675f305" {
		t.Fatalf("Commit = %q, want %q", got.Commit, "675f305")
	}
	if got.Display() != "v0.2.0" {
		t.Fatalf("Display() = %q, want %q", got.Display(), "v0.2.0")
	}
	wantSummary := "v0.2.0 commit=675f305 built=2026-11-27T09:00:00Z dirty=true"
	if got.Summary() != wantSummary {
		t.Fatalf("Summary() = %q, want %q", got.Summary(), wantSummary)
	}
}

func TestResolveDevBuildWithVCS(t *testing.T) {
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "675f305abcdef"},
			{Key: "vcs.time", Value: "2026-11-27T08:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	got := resolve(Info{}, "v0.1.0", bi)

	if got.Stamped {
		t.Fatal("expected Stamped=false")
	}
	if got.Version != "0.1.0" {
		t.Fatalf("Version = %q, want %q", got.Version, "0.1.0")
	}
	if got.Tag() != "v0.1.0" {
		t.Fatalf("Tag() = %q, want %q", got.Tag(), "v0.1.0")
	}
	if got.Display() != "v0.1.0-dev (675f305)" {
		t.Fatalf("Display() = %q, want %q", got.Display(), "v0.1.0-dev (675f305)")
	}
	wantSummary := "v0.1.0-dev commit=675f305 built=2026-11-27T08:00:00Z"
	if got.Summary() != wantSummary {
		t.Fatalf("Summary() = %q, want %q", got.Summary(), wantSummary)
	}
}

func TestResolveDevBuildDirtyAndEmptyFallback(t *testing.T) {
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "1234567890"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	got := resolve(Info{}, "", bi)

	if got.Stamped {
		t.Fatal("expected Stamped=false")
	}
	if got.Version != "0.0.0-dev" {
		t.Fatalf("Version = %q, want %q", got.Version, "0.0.0-dev")
	}
	if got.Display() != "v0.0.0-dev (1234567-dirty)" {
		t.Fatalf("Display() = %q, want %q", got.Display(), "v0.0.0-dev (1234567-dirty)")
	}
	wantSummary := "v0.0.0-dev commit=1234567 dirty=true"
	if got.Summary() != wantSummary {
		t.Fatalf("Summary() = %q, want %q", got.Summary(), wantSummary)
	}
}

func TestResolveNoBuildInfo(t *testing.T) {
	got := resolve(Info{}, " 0.3.1 ", nil)
	if got.Display() != "v0.3.1-dev" {
		t.Fatalf("Display() = %q, want %q", got.Display(), "v0.3.1-dev")
	}
	if got.Summary() != "v0.3.1-dev" {
		t.Fatalf("Summary() = %q, want %q", got.Summary(), "v0.3.1-dev")
	}
}
