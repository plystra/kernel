package audit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/kernel/audit"
)

func TestModuleBuildAcceptsCanonicalVersionOrBuildIdentity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		path     string
		version  string
		identity string
	}{
		{name: "released module", path: "github.com/acme/email", version: "v1.4.2"},
		{name: "major suffix", path: "github.com/acme/email/v2", version: "v2.0.0", identity: "git:0123456789abcdef"},
		{name: "gopkg path", path: "gopkg.in/yaml.v3", version: "v3.0.4"},
		{name: "pseudo version", path: "example.com/acme/provider", version: "v0.0.0-20260102030405-abcdefabcdef"},
		{name: "local build", path: "example.com/acme/provider", identity: "sha256:0123456789abcdef"},
		{name: "maximum build identity", path: "example.com/acme/provider", identity: strings.Repeat("a", audit.MaximumBuildIdentitySize)},
	} {
		build, err := audit.NewModuleBuild(test.path, test.version, test.identity)
		if err != nil {
			t.Fatalf("%s NewModuleBuild: %v", test.name, err)
		}
		if !build.Valid() || build.ModulePath() != test.path || build.ModuleVersion() != test.version || build.BuildIdentity() != test.identity {
			t.Fatalf("%s build = %#v", test.name, build)
		}
		if copied := build; copied != build {
			t.Fatalf("%s build copy changed: %#v", test.name, copied)
		}
	}
}

func TestModuleBuildRejectsUnsafeOrContradictoryMetadata(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		path     string
		version  string
		identity string
	}{
		{name: "missing path", version: "v1.0.0"},
		{name: "invalid path", path: "../provider", version: "v1.0.0"},
		{name: "uppercase domain", path: "GitHub.com/acme/provider", version: "v1.0.0"},
		{name: "v1 path suffix", path: "example.com/acme/provider/v1", version: "v1.0.0"},
		{name: "oversized path", path: "example.com/" + strings.Repeat("a", audit.MaximumModulePathSize), version: "v1.0.0"},
		{name: "missing provenance", path: "example.com/acme/provider"},
		{name: "missing version prefix", path: "example.com/acme/provider", version: "1.2.3"},
		{name: "incomplete version", path: "example.com/acme/provider", version: "v1.2"},
		{name: "noncanonical version metadata", path: "example.com/acme/provider", version: "v1.2.3+metadata"},
		{name: "mismatched major suffix", path: "example.com/acme/provider/v2", version: "v1.2.3"},
		{name: "missing major suffix", path: "example.com/acme/provider", version: "v2.0.0"},
		{name: "oversized version", path: "example.com/acme/provider", version: "v1.0.0-" + strings.Repeat("a", audit.MaximumModuleVersionSize)},
		{name: "identity leading separator", path: "example.com/acme/provider", identity: ":revision"},
		{name: "identity trailing separator", path: "example.com/acme/provider", identity: "revision:"},
		{name: "identity repeated separator", path: "example.com/acme/provider", identity: "git::revision"},
		{name: "identity slash", path: "example.com/acme/provider", identity: "git/revision"},
		{name: "identity whitespace", path: "example.com/acme/provider", identity: "git revision"},
		{name: "identity unicode", path: "example.com/acme/provider", identity: "git:修订"},
		{name: "oversized identity", path: "example.com/acme/provider", identity: strings.Repeat("a", audit.MaximumBuildIdentitySize+1)},
	} {
		build, err := audit.NewModuleBuild(test.path, test.version, test.identity)
		if !errors.Is(err, audit.ErrInvalidModuleBuild) || build.Valid() {
			t.Fatalf("%s NewModuleBuild = %#v, %v", test.name, build, err)
		}
		if err != nil && err.Error() != audit.ErrInvalidModuleBuild.Error() {
			t.Fatalf("%s error exposed input: %v", test.name, err)
		}
	}
}

func TestZeroModuleBuildFailsClosed(t *testing.T) {
	t.Parallel()

	var build audit.ModuleBuild
	if build.Valid() || build.ModulePath() != "" || build.ModuleVersion() != "" || build.BuildIdentity() != "" {
		t.Fatalf("zero ModuleBuild = %#v", build)
	}
}

func FuzzModuleBuild(f *testing.F) {
	f.Add("github.com/acme/email", "v1.4.2", "")
	f.Add("example.com/acme/provider", "", "sha256:0123456789abcdef")
	f.Add("../provider", "v1.0.0", "")
	f.Fuzz(func(t *testing.T, path, version, identity string) {
		build, err := audit.NewModuleBuild(path, version, identity)
		if err != nil {
			if !errors.Is(err, audit.ErrInvalidModuleBuild) || build.Valid() {
				t.Fatalf("rejected build = %#v, %v", build, err)
			}
			return
		}
		if !build.Valid() || build.ModulePath() != path || build.ModuleVersion() != version || build.BuildIdentity() != identity {
			t.Fatalf("accepted build = %#v", build)
		}
	})
}
