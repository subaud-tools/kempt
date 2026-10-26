package handlers

import (
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
)

func ctxWith(r run.Runner, rel release.Releases) *machine.Context {
	return &machine.Context{Home: "/home/x", OS: "darwin", Arch: "arm64", Runner: r, Releases: rel, Cache: map[string]string{}}
}

func TestInstalledToolVersion(t *testing.T) {
	fr := &run.FakeRunner{Responses: map[string]run.Response{
		"/home/x/.local/bin/proj version": {Stdout: "proj 0.1.2 (9f3a22a, 2026-09-02)\n"},
		"/home/x/.local/bin/dev version":  {Stdout: "dev dev\n"},
		"/home/x/.local/bin/gone version": {Err: run.ErrForTest},
	}}
	ctx := ctxWith(fr, nil)

	if v, ok := InstalledToolVersion(ctx, "proj"); !ok || v != "0.1.2" {
		t.Fatalf("proj: got (%q,%v), want (0.1.2,true)", v, ok)
	}
	if _, ok := InstalledToolVersion(ctx, "dev"); ok {
		t.Fatalf("dev build should be unknown")
	}
	if _, ok := InstalledToolVersion(ctx, "gone"); ok {
		t.Fatalf("missing binary should be unknown")
	}
}

func TestIsPinnedVersion(t *testing.T) {
	for v, want := range map[string]bool{"0.1.2": true, "1.0.0-rc.1": true, "": false, "latest": false} {
		if got := IsPinnedVersion(v); got != want {
			t.Fatalf("IsPinnedVersion(%q)=%v want %v", v, got, want)
		}
	}
}

func TestLatestVersion(t *testing.T) {
	rel := release.FakeReleases{Files: map[string][]byte{
		"https://tackle.tools/dl/proj/latest": []byte("0.1.2\n"),
	}}
	ctx := ctxWith(nil, rel)
	v, err := LatestVersion(ctx, "tackle.tools", "proj")
	if err != nil || v != "0.1.2" {
		t.Fatalf("got (%q,%v), want (0.1.2,nil)", v, err)
	}
}

func TestSemverNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.2", "0.1.1", true},
		{"0.1.1", "0.1.2", false},
		{"0.1.1", "0.1.1", false},
		{"0.7.0", "0.7.0-schuettc.2", true},
		{"0.7.0-schuettc.2", "0.7.0", false},
		{"not-a-version", "0.1.1", false},
		{"0.1.1", "not-a-version", false},
	}
	for _, c := range cases {
		if got := SemverNewer(c.a, c.b); got != c.want {
			t.Errorf("SemverNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestSemverMajorBump: only a higher leading number is a major bump. A 0.x
// minor is not (the family ships 0.x minors routinely), and a sha or other
// unparseable version never is.
func TestSemverMajorBump(t *testing.T) {
	cases := []struct {
		target, installed string
		want              bool
	}{
		{"3.2.0", "2.38.0", true},
		{"1.0.0", "0.9.4", true},
		{"2.39.0", "2.38.0", false},
		{"0.7.0", "0.1.1", false},
		{"2.0.0", "2.0.0", false},
		{"1.0.0", "2.0.0", false},
		{"3.0.0-rc.1", "2.9.0", true},
		{"5bf4e78aaaaa", "1c2d3e4fbbbb", false},
		{"3.0.0", "", false},
	}
	for _, c := range cases {
		if got := SemverMajorBump(c.target, c.installed); got != c.want {
			t.Errorf("SemverMajorBump(%q, %q) = %v; want %v", c.target, c.installed, got, c.want)
		}
	}
}

func TestLatestVersionGated(t *testing.T) {
	rel := release.FakeReleases{
		Links: map[string]string{"diazo diazo/latest": "https://signed.example/latest?sig=s"},
		Files: map[string][]byte{"https://signed.example/latest?sig=s": []byte("0.3.0\n")},
	}
	ctx := ctxWith(nil, rel)
	v, err := LatestVersion(ctx, "diazo.tools", "diazo")
	if err != nil || v != "0.3.0" {
		t.Fatalf("got (%q,%v), want (0.3.0,nil)", v, err)
	}
}
