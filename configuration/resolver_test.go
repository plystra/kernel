package configuration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plystra/kernel/configuration"
	"go.yaml.in/yaml/v3"
)

func TestResolverResolvesEnvironmentSecretWithoutExposingIt(t *testing.T) {
	const (
		name  = "PLYSTRA_CONFIGURATION_ENV_SECRET"
		value = "person:environment-secret"
	)
	t.Setenv(name, value)
	resolver := configurationResolver(t, 128)
	reference := environmentReference(t, name)

	secret, err := resolver.Resolve(context.Background(), reference)
	if err != nil || !secret.Valid() || secret.Len() != len(value) || string(secret.Bytes()) != value {
		t.Fatalf("Resolve(environment) = %#v, %v", secret, err)
	}
	copyValue := secret.Bytes()
	copyValue[0] = 'X'
	if string(secret.Bytes()) != value {
		t.Fatal("Secret Bytes exposed mutable storage")
	}
	assertSecretRedacted(t, secret, value)
}

func TestResolverResolvesRegularFileExactly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "smtp-password")
	value := []byte("file-secret\n")
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resolver := configurationResolver(t, 128)
	secret, err := resolver.Resolve(context.Background(), fileReference(t, path))
	if err != nil || !secret.Valid() || !bytes.Equal(secret.Bytes(), value) {
		t.Fatalf("Resolve(file) = %#v, %v", secret, err)
	}
	if err := os.WriteFile(path, []byte("changed-secret"), 0o600); err != nil {
		t.Fatalf("overwrite file: %v", err)
	}
	if !bytes.Equal(secret.Bytes(), value) {
		t.Fatal("resolved Secret changed with its source file")
	}
	assertSecretRedacted(t, secret, string(value))
}

func TestResolverRejectsUnavailableAndOversizedSourcesSafely(t *testing.T) {
	const (
		missingEnvironment = "PLYSTRA_CONFIGURATION_MISSING_SECRET"
		emptyEnvironment   = "PLYSTRA_CONFIGURATION_EMPTY_SECRET"
		largeEnvironment   = "PLYSTRA_CONFIGURATION_LARGE_SECRET"
	)
	_ = os.Unsetenv(missingEnvironment)
	t.Cleanup(func() { _ = os.Unsetenv(missingEnvironment) })
	t.Setenv(emptyEnvironment, "")
	t.Setenv(largeEnvironment, "oversized-secret-value")

	root := t.TempDir()
	missingPath := filepath.Join(root, "missing-secret")
	emptyPath := filepath.Join(root, "empty-secret")
	largePath := filepath.Join(root, "large-secret")
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatalf("write empty Secret: %v", err)
	}
	if err := os.WriteFile(largePath, []byte("oversized-secret-value"), 0o600); err != nil {
		t.Fatalf("write large Secret: %v", err)
	}

	resolver := configurationResolver(t, 8)
	for _, test := range []struct {
		name      string
		reference configuration.Reference
		reason    error
		forbidden []string
	}{
		{name: "missing environment", reference: environmentReference(t, missingEnvironment), reason: configuration.ErrSecretUnavailable, forbidden: []string{missingEnvironment}},
		{name: "empty environment", reference: environmentReference(t, emptyEnvironment), reason: configuration.ErrSecretUnavailable, forbidden: []string{emptyEnvironment}},
		{name: "oversized environment", reference: environmentReference(t, largeEnvironment), reason: configuration.ErrSecretTooLarge, forbidden: []string{largeEnvironment, "oversized-secret-value"}},
		{name: "missing file", reference: fileReference(t, missingPath), reason: configuration.ErrSecretUnavailable, forbidden: []string{missingPath}},
		{name: "directory", reference: fileReference(t, root), reason: configuration.ErrSecretUnavailable, forbidden: []string{root}},
		{name: "empty file", reference: fileReference(t, emptyPath), reason: configuration.ErrSecretUnavailable, forbidden: []string{emptyPath}},
		{name: "oversized file", reference: fileReference(t, largePath), reason: configuration.ErrSecretTooLarge, forbidden: []string{largePath, "oversized-secret-value"}},
	} {
		secret, err := resolver.Resolve(context.Background(), test.reference)
		if !errors.Is(err, configuration.ErrResolve) || !errors.Is(err, test.reason) || secret.Valid() || secret.Bytes() != nil {
			t.Fatalf("%s Resolve = %#v, %v", test.name, secret, err)
		}
		for _, forbidden := range test.forbidden {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("%s error exposed %q: %v", test.name, forbidden, err)
			}
		}
	}
}

func TestResolverValidatesResolverReferenceAndContext(t *testing.T) {
	t.Parallel()

	for _, maximum := range []int{0, -1, configuration.MaximumSecretValueBytes + 1} {
		resolver, err := configuration.NewResolver(configuration.ResolverOptions{MaximumValueBytes: maximum})
		if !errors.Is(err, configuration.ErrInvalidResolver) || resolver != nil {
			t.Fatalf("NewResolver(%d) = %#v, %v", maximum, resolver, err)
		}
	}
	resolver := configurationResolver(t, configuration.MaximumSecretValueBytes)
	var zeroReference configuration.Reference
	if secret, err := resolver.Resolve(context.Background(), zeroReference); !errors.Is(err, configuration.ErrInvalidReference) || secret.Valid() {
		t.Fatalf("Resolve(zero Reference) = %#v, %v", secret, err)
	}
	var nilContext context.Context
	if secret, err := resolver.Resolve(nilContext, environmentReference(t, "VALID_ENV")); !errors.Is(err, configuration.ErrInvalidContext) || secret.Valid() {
		t.Fatalf("Resolve(nil Context) = %#v, %v", secret, err)
	}
	var nilResolver *configuration.Resolver
	if secret, err := nilResolver.Resolve(context.Background(), environmentReference(t, "VALID_ENV")); !errors.Is(err, configuration.ErrInvalidResolver) || secret.Valid() {
		t.Fatalf("nil Resolver = %#v, %v", secret, err)
	}
}

func TestResolverPreservesStandardContextCauses(t *testing.T) {
	const name = "PLYSTRA_CONFIGURATION_CONTEXT_SECRET"
	t.Setenv(name, "context-secret")
	resolver := configurationResolver(t, 128)
	reference := environmentReference(t, name)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	for _, test := range []struct {
		name  string
		ctx   context.Context
		cause error
	}{
		{name: "cancelled", ctx: cancelled, cause: context.Canceled},
		{name: "deadline", ctx: deadline, cause: context.DeadlineExceeded},
	} {
		secret, err := resolver.Resolve(test.ctx, reference)
		if !errors.Is(err, configuration.ErrResolve) || !errors.Is(err, test.cause) || secret.Valid() || strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "context-secret") {
			t.Fatalf("%s Resolve = %#v, %v", test.name, secret, err)
		}
	}
}

func TestResolverAndSecretCopiesAreSafeForConcurrentUse(t *testing.T) {
	const (
		name  = "PLYSTRA_CONFIGURATION_CONCURRENT_SECRET"
		value = "concurrent-secret"
	)
	t.Setenv(name, value)
	resolver := configurationResolver(t, 128)
	reference := environmentReference(t, name)

	const readers = 64
	var group sync.WaitGroup
	group.Add(readers)
	for range readers {
		go func() {
			defer group.Done()
			secret, err := resolver.Resolve(context.Background(), reference)
			if err != nil || string(secret.Bytes()) != value {
				t.Errorf("concurrent Resolve = %#v, %v", secret, err)
				return
			}
			bytes := secret.Bytes()
			bytes[0] = 'X'
			if string(secret.Bytes()) != value {
				t.Error("concurrent Secret copy mutated")
			}
		}()
	}
	group.Wait()
}

func TestZeroSecretFailsClosed(t *testing.T) {
	t.Parallel()

	var secret configuration.Secret
	if secret.Valid() || secret.Len() != 0 || secret.Bytes() != nil {
		t.Fatalf("zero Secret = %#v", secret)
	}
	assertSecretRedacted(t, secret, "zero-secret-must-not-appear")
}

func TestSecretAndReferenceAreRedactedByStructuredLogging(t *testing.T) {
	const (
		name  = "PLYSTRA_CONFIGURATION_LOG_SECRET"
		value = "structured-log-secret"
	)
	t.Setenv(name, value)
	reference := environmentReference(t, name)
	secret, err := configurationResolver(t, 128).Resolve(context.Background(), reference)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, handler := range []func(*bytes.Buffer) slog.Handler{
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewTextHandler(buffer, nil) },
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(buffer, nil) },
	} {
		var output bytes.Buffer
		slog.New(handler(&output)).Info("configuration", "reference", reference, "secret", secret)
		if strings.Contains(output.String(), name) || strings.Contains(output.String(), value) || !strings.Contains(output.String(), "redacted") {
			t.Fatalf("structured log exposed configuration: %s", output.String())
		}
	}
	if data, err := yaml.Marshal(struct {
		Reference configuration.Reference `yaml:"reference"`
		Secret    configuration.Secret    `yaml:"secret"`
	}{Reference: reference, Secret: secret}); !errors.Is(err, configuration.ErrSecretExposure) || data != nil || strings.Contains(err.Error(), name) || strings.Contains(err.Error(), value) {
		t.Fatalf("YAML serialization = %q, %v", data, err)
	}
}

func configurationResolver(t testing.TB, maximum int) *configuration.Resolver {
	t.Helper()
	resolver, err := configuration.NewResolver(configuration.ResolverOptions{MaximumValueBytes: maximum})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func environmentReference(t testing.TB, name string) configuration.Reference {
	t.Helper()
	reference, err := configuration.NewEnvironmentReference(name)
	if err != nil {
		t.Fatalf("NewEnvironmentReference(%q): %v", name, err)
	}
	return reference
}

func fileReference(t testing.TB, path string) configuration.Reference {
	t.Helper()
	reference, err := configuration.NewFileReference(path)
	if err != nil {
		t.Fatalf("NewFileReference(%q): %v", path, err)
	}
	return reference
}

func assertSecretRedacted(t testing.TB, secret configuration.Secret, value string) {
	t.Helper()
	formatted := []string{
		secret.String(),
		secret.GoString(),
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%+v", secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprintf("%s", secret),
		fmt.Sprintf("%q", secret),
		fmt.Sprintf("%x", secret),
	}
	for _, output := range formatted {
		if strings.Contains(output, value) || !strings.Contains(output, "redacted") {
			t.Fatalf("Secret formatting exposed %q as %q", value, output)
		}
	}
	if data, err := json.Marshal(secret); !errors.Is(err, configuration.ErrSecretExposure) || data != nil || strings.Contains(err.Error(), value) {
		t.Fatalf("Marshal(Secret) = %q, %v", data, err)
	}
	if data, err := secret.MarshalText(); !errors.Is(err, configuration.ErrSecretExposure) || data != nil || strings.Contains(err.Error(), value) {
		t.Fatalf("MarshalText(Secret) = %q, %v", data, err)
	}
}
