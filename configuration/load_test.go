package configuration_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/plystra/kernel/configuration"
	"github.com/plystra/kernel/plugin/manifest"
)

func TestLoadDocumentReadsBoundedRegularFilesExactly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "empty", data: []byte{}},
		{name: "private configuration", data: []byte("config: {acme.plugin: {token: private-runtime-value}}\n")},
		{name: "maximum size", data: bytes.Repeat([]byte{'x'}, manifest.MaximumDeclarationSize)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-"))
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			loaded, err := configuration.LoadDocument(path)
			if err != nil || !bytes.Equal(loaded, test.data) {
				t.Fatalf("LoadDocument = %d bytes, %v", len(loaded), err)
			}
			if len(loaded) > 0 {
				loaded[0] ^= 0xff
				onDisk, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(onDisk, test.data) {
					t.Fatalf("caller-owned result changed source = %d bytes, %v", len(onDisk), readErr)
				}
			}
			clear(loaded)
		})
	}
}

func TestLoadDocumentRejectsUnsafeSourcesWithoutExposingPathsOrValues(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	privatePath := filepath.Join(root, "private-runtime-document")
	largeValue := []byte("private-runtime-value-" + strings.Repeat("x", manifest.MaximumDeclarationSize))
	if err := os.WriteFile(privatePath, largeValue, 0o600); err != nil {
		t.Fatalf("write oversized document: %v", err)
	}

	tests := []struct {
		name   string
		path   string
		reason error
	}{
		{name: "missing", path: filepath.Join(root, "missing-private-document"), reason: configuration.ErrDocumentUnavailable},
		{name: "directory", path: root, reason: configuration.ErrDocumentUnavailable},
		{name: "oversized", path: privatePath, reason: configuration.ErrDocumentTooLarge},
	}
	for _, test := range tests {
		data, err := configuration.LoadDocument(test.path)
		if data != nil || !errors.Is(err, configuration.ErrLoadDocument) || !errors.Is(err, test.reason) {
			t.Fatalf("%s LoadDocument = %d bytes, %v", test.name, len(data), err)
		}
		for _, forbidden := range []string{root, filepath.Base(test.path), "private-runtime-value"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("%s error exposed %q: %v", test.name, forbidden, err)
			}
		}
	}
}

func TestLoadDocumentRejectsSymbolicFinalPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "private-target")
	link := filepath.Join(root, "public-link")
	if err := os.WriteFile(target, []byte("private-runtime-value"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	data, err := configuration.LoadDocument(link)
	if data != nil || !errors.Is(err, configuration.ErrLoadDocument) || !errors.Is(err, configuration.ErrDocumentUnavailable) {
		t.Fatalf("LoadDocument(symbolic link) = %q, %v", data, err)
	}
	for _, forbidden := range []string{target, link, "private-runtime-value"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error exposed %q: %v", forbidden, err)
		}
	}
}

func TestLoadDocumentIsSafeForConcurrentReaders(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "runtime-document")
	want := []byte("config: {acme.plugin: {value: concurrent-private-value}}\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	const readers = 64
	var group sync.WaitGroup
	group.Add(readers)
	for range readers {
		go func() {
			defer group.Done()
			data, err := configuration.LoadDocument(path)
			if err != nil || !bytes.Equal(data, want) {
				t.Errorf("concurrent LoadDocument = %d bytes, %v", len(data), err)
				return
			}
			clear(data)
		}()
	}
	group.Wait()
}
