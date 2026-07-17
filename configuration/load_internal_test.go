package configuration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDocumentDetectsPathReplacementAfterReading(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "runtime-document")
	replacementPath := filepath.Join(root, "replacement-document")
	if err := os.WriteFile(path, []byte("private-original-value"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	if err := os.WriteFile(replacementPath, []byte("private-replacement-value"), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	replacement, err := os.Lstat(replacementPath)
	if err != nil {
		t.Fatalf("Lstat replacement: %v", err)
	}

	calls := 0
	data, err := loadDocument(path, func(current string) (os.FileInfo, error) {
		calls++
		if calls == 2 {
			return replacement, nil
		}
		return os.Lstat(current)
	})
	if data != nil || calls != 2 || !errors.Is(err, ErrLoadDocument) || !errors.Is(err, ErrDocumentChanged) {
		t.Fatalf("loadDocument(replaced) = %q, calls %d, %v", data, calls, err)
	}
}

func TestLoadDocumentDetectsReplacementBeforeOpen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "runtime-document")
	replacementPath := filepath.Join(root, "replacement-document")
	if err := os.WriteFile(path, []byte("private-original-value"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	if err := os.WriteFile(replacementPath, []byte("private-replacement-value"), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	replacement, err := os.Lstat(replacementPath)
	if err != nil {
		t.Fatalf("Lstat replacement: %v", err)
	}

	data, err := loadDocument(path, func(string) (os.FileInfo, error) {
		return replacement, nil
	})
	if data != nil || !errors.Is(err, ErrLoadDocument) || !errors.Is(err, ErrDocumentChanged) {
		t.Fatalf("loadDocument(replaced before open) = %q, %v", data, err)
	}
}

func TestLoadDocumentDetectsModificationAfterReading(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "runtime-document")
	if err := os.WriteFile(path, []byte("private-original-value"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}

	calls := 0
	data, err := loadDocument(path, func(current string) (os.FileInfo, error) {
		calls++
		if calls == 2 {
			if err := os.WriteFile(current, []byte("private-modified-value-with-a-different-size"), 0o600); err != nil {
				t.Fatalf("modify document: %v", err)
			}
		}
		return os.Lstat(current)
	})
	if data != nil || calls != 2 || !errors.Is(err, ErrLoadDocument) || !errors.Is(err, ErrDocumentChanged) {
		t.Fatalf("loadDocument(modified) = %q, calls %d, %v", data, calls, err)
	}
}
