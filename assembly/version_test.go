package assembly_test

import (
	"errors"
	"testing"

	"github.com/plystra/kernel/assembly"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  assembly.Version
	}{
		{name: "current", value: "v1", want: assembly.V1},
		{name: "future", value: "v42", want: assembly.Version(42)},
		{name: "maximum", value: "v4294967295", want: assembly.Version(^uint32(0))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := assembly.ParseVersion(test.value)
			if err != nil {
				t.Fatalf("ParseVersion(%q): %v", test.value, err)
			}
			if got != test.want {
				t.Fatalf("ParseVersion(%q) = %v, want %v", test.value, got, test.want)
			}
			if got.String() != test.value {
				t.Fatalf("Version.String() = %q, want %q", got.String(), test.value)
			}
		})
	}
}

func TestParseVersionRejectsNonCanonicalInput(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "v", "v0", "v01", "V1", "1", " v1", "v1 ", "v+1", "v-1", "v4294967296"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			got, err := assembly.ParseVersion(value)
			if !errors.Is(err, assembly.ErrInvalidVersion) {
				t.Fatalf("ParseVersion(%q) error = %v, want ErrInvalidVersion", value, err)
			}
			if got != 0 {
				t.Fatalf("ParseVersion(%q) = %v, want zero", value, got)
			}
		})
	}
}

func TestRequireVersion(t *testing.T) {
	t.Parallel()

	if err := assembly.RequireVersion(assembly.Current); err != nil {
		t.Fatalf("RequireVersion(Current): %v", err)
	}
	if err := assembly.RequireVersion(0); !errors.Is(err, assembly.ErrInvalidVersion) {
		t.Fatalf("RequireVersion(0) error = %v, want ErrInvalidVersion", err)
	}
	if err := assembly.RequireVersion(assembly.Version(2)); !errors.Is(err, assembly.ErrUnsupportedVersion) {
		t.Fatalf("RequireVersion(v2) error = %v, want ErrUnsupportedVersion", err)
	}
}

func FuzzParseVersion(f *testing.F) {
	for _, seed := range []string{"v1", "v42", "", "v0", "v01", "V1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		version, err := assembly.ParseVersion(value)
		if err != nil {
			if !errors.Is(err, assembly.ErrInvalidVersion) {
				t.Fatalf("ParseVersion(%q) returned unexpected error: %v", value, err)
			}
			return
		}
		if version == 0 {
			t.Fatalf("ParseVersion(%q) returned zero without an error", value)
		}
		if version.String() != value {
			t.Fatalf("ParseVersion(%q).String() = %q", value, version.String())
		}
	})
}
