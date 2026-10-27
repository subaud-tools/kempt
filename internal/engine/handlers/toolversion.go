package handlers

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/release"
)

// semverRe matches a bare semantic version, optionally with a prerelease
// suffix (e.g. 0.7.0-schuettc.2). No leading v — the family download contract
// keys on bare semver.
var semverRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// InstalledToolVersion runs `<bin> version` and returns the semver token from
// its output. tools-common binaries print "<tool> <semver> (<commit>, <date>)".
// known is false when the binary is absent, the command errors, or the output
// carries no semver token (e.g. a local "dev" build) — callers then fall back
// to presence-only and never report false drift.
func InstalledToolVersion(ctx *machine.Context, bin string) (version string, known bool) {
	out, err := ctx.Runner.Run(binPath(ctx, bin), "version")
	if err != nil {
		return "", false
	}
	for _, f := range strings.Fields(out) {
		if semverRe.MatchString(f) {
			return f, true
		}
	}
	return "", false
}

// IsPinnedVersion reports whether v names a concrete version to converge to,
// as opposed to the rolling "" / "latest".
func IsPinnedVersion(v string) bool { return v != "" && v != "latest" }

// LatestVersion reads the plain-text /dl/<tool>/latest pointer off the site,
// through a signed link of product when the site is gated.
// Network — callers must be an explicit outdated/upgrade path, never Inspect.
func LatestVersion(ctx *machine.Context, site, product, tool string) (string, error) {
	body, err := release.Fetch(ctx.Releases, site, product, tool+"/latest")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// SemverNewer reports whether a is a strictly newer version than b. Both must
// match semverRe (major.minor.patch, optional -prerelease suffix); if either
// fails to parse, SemverNewer returns false — it never claims "newer" on
// garbage input. When the numeric major.minor.patch parts are equal, a
// version without a prerelease suffix is newer than one with one (a release
// supersedes its own prereleases); if both carry a prerelease suffix, they
// are compared lexically as a tiebreak.
func SemverNewer(a, b string) bool {
	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa.core[i] != pb.core[i] {
			return pa.core[i] > pb.core[i]
		}
	}
	if pa.pre == "" && pb.pre != "" {
		return true
	}
	if pa.pre != "" && pb.pre == "" {
		return false
	}
	return pa.pre > pb.pre
}

// SemverMajorBump reports whether moving from installed to target crosses a
// major version: target's leading number is higher. A 0.x minor is not a
// major bump. Either side failing to parse (a git sha, an unknown version)
// is never a major bump.
func SemverMajorBump(target, installed string) bool {
	pt, okt := parseSemver(target)
	pi, oki := parseSemver(installed)
	return okt && oki && pt.core[0] > pi.core[0]
}

type semverParts struct {
	core [3]int
	pre  string
}

func parseSemver(v string) (semverParts, bool) {
	if !semverRe.MatchString(v) {
		return semverParts{}, false
	}
	core := v
	var pre string
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		core = v[:idx]
		pre = v[idx+1:]
	}
	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return semverParts{}, false
	}
	var p semverParts
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return semverParts{}, false
		}
		p.core[i] = n
	}
	p.pre = pre
	return p, true
}
