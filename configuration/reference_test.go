package configuration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/kernel/configuration"
)

func TestReferenceKindsAreClosedAndStable(t *testing.T) {
	t.Parallel()

	for kind, want := range map[configuration.ReferenceKind]string{
		configuration.ReferenceEnvironment: "env",
		configuration.ReferenceFile:        "file",
	} {
		if !kind.Valid() || kind.String() != want {
			t.Fatalf("ReferenceKind %q = %q, valid %t", kind, kind.String(), kind.Valid())
		}
	}
	for _, kind := range []configuration.ReferenceKind{"", "vault", "ENV"} {
		if kind.Valid() || kind.String() != "" {
			t.Fatalf("invalid ReferenceKind %q = %q, valid %t", kind, kind.String(), kind.Valid())
		}
	}
}

func TestEnvironmentReferencesAcceptPortableNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"SMTP_PASSWORD", "_TOKEN", "oidcClient2"} {
		reference, err := configuration.NewEnvironmentReference(name)
		if err != nil || !reference.Valid() || reference.Kind() != configuration.ReferenceEnvironment {
			t.Fatalf("NewEnvironmentReference(%q) = %#v, %v", name, reference, err)
		}
		assertReferenceRedacted(t, reference, name)
	}
	for _, name := range []string{"", "2TOKEN", "BAD=VALUE", "BAD-NAME", "BAD NAME", "秘密", strings.Repeat("A", 257)} {
		reference, err := configuration.NewEnvironmentReference(name)
		if !errors.Is(err, configuration.ErrInvalidReference) || reference.Valid() || reference.Kind().Valid() {
			t.Fatalf("invalid environment reference %q = %#v, %v", name, reference, err)
		}
	}
}

func TestFileReferencesRequireCleanAbsolutePaths(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "smtp-password")
	reference, err := configuration.NewFileReference(path)
	if err != nil || !reference.Valid() || reference.Kind() != configuration.ReferenceFile {
		t.Fatalf("NewFileReference = %#v, %v", reference, err)
	}
	assertReferenceRedacted(t, reference, path)

	dirty := filepath.Join(filepath.Dir(path), "nested") + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(path)
	for _, invalid := range []string{"", "relative/secret", dirty, path + "\x00suffix", path + "\nsuffix", string([]byte{0xff})} {
		reference, err := configuration.NewFileReference(invalid)
		if !errors.Is(err, configuration.ErrInvalidReference) || reference.Valid() || reference.Kind().Valid() {
			t.Fatalf("invalid file reference %q = %#v, %v", invalid, reference, err)
		}
	}
}

func TestZeroReferenceFailsClosed(t *testing.T) {
	t.Parallel()

	var reference configuration.Reference
	if reference.Valid() || reference.Kind().Valid() {
		t.Fatalf("zero Reference = %#v", reference)
	}
	if got := fmt.Sprintf("%v|%#v|%s", reference, reference, reference); strings.Contains(got, "invalid-reference-target") || !strings.Contains(got, "redacted") {
		t.Fatalf("zero Reference formatting = %q", got)
	}
	if data, err := json.Marshal(reference); !errors.Is(err, configuration.ErrSecretExposure) || data != nil {
		t.Fatalf("Marshal(zero Reference) = %q, %v", data, err)
	}
}

func FuzzSecretReferences(f *testing.F) {
	f.Add("SMTP_PASSWORD", filepath.Join(string(filepath.Separator), "run", "secrets", "smtp"))
	f.Add("bad=name", "relative")
	f.Fuzz(func(t *testing.T, environment, path string) {
		if reference, err := configuration.NewEnvironmentReference(environment); err == nil {
			if !reference.Valid() || reference.Kind() != configuration.ReferenceEnvironment {
				t.Fatalf("accepted environment Reference = %#v", reference)
			}
		} else if !errors.Is(err, configuration.ErrInvalidReference) || reference.Valid() {
			t.Fatalf("rejected environment Reference = %#v, %v", reference, err)
		}
		if reference, err := configuration.NewFileReference(path); err == nil {
			if !reference.Valid() || reference.Kind() != configuration.ReferenceFile {
				t.Fatalf("accepted file Reference = %#v", reference)
			}
		} else if !errors.Is(err, configuration.ErrInvalidReference) || reference.Valid() {
			t.Fatalf("rejected file Reference = %#v, %v", reference, err)
		}
	})
}

func assertReferenceRedacted(t testing.TB, reference configuration.Reference, target string) {
	t.Helper()
	formatted := []string{
		reference.String(),
		reference.GoString(),
		fmt.Sprintf("%v", reference),
		fmt.Sprintf("%+v", reference),
		fmt.Sprintf("%#v", reference),
		fmt.Sprintf("%s", reference),
		fmt.Sprintf("%q", reference),
		fmt.Sprintf("%x", reference),
	}
	for _, value := range formatted {
		if strings.Contains(value, target) || !strings.Contains(value, "redacted") {
			t.Fatalf("Reference formatting exposed %q as %q", target, value)
		}
	}
	if data, err := json.Marshal(reference); !errors.Is(err, configuration.ErrSecretExposure) || data != nil || strings.Contains(err.Error(), target) {
		t.Fatalf("Marshal(Reference) = %q, %v", data, err)
	}
	if data, err := reference.MarshalText(); !errors.Is(err, configuration.ErrSecretExposure) || data != nil || strings.Contains(err.Error(), target) {
		t.Fatalf("MarshalText(Reference) = %q, %v", data, err)
	}
}
