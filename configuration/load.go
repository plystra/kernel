package configuration

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/plystra/kernel/plugin/manifest"
)

var (
	// ErrLoadDocument reports a safe runtime-document loading failure.
	ErrLoadDocument = errors.New("load runtime configuration document")
	// ErrDocumentUnavailable reports an absent, unreadable, symbolic, or
	// non-regular runtime document.
	ErrDocumentUnavailable = errors.New("runtime configuration document is unavailable")
	// ErrDocumentTooLarge reports a runtime document beyond the declaration
	// byte bound shared by configuration parsing.
	ErrDocumentTooLarge = errors.New("runtime configuration document exceeds size limit")
	// ErrDocumentChanged reports a runtime document whose file identity or
	// observable contents changed while it was being loaded.
	ErrDocumentChanged = errors.New("runtime configuration document changed while loading")
)

// LoadDocument reads one bounded regular runtime document without following a
// symbolic final path component. It verifies that the opened file is the path
// inspected before reading and remains that path after reading. The function
// does not interpret the path, filename, document format, or application
// fields. Returned bytes are owned by the caller and may contain private
// runtime configuration; callers must clear them as soon as decoding finishes.
func LoadDocument(path string) ([]byte, error) {
	return loadDocument(path, os.Lstat)
}

func loadDocument(path string, lstat func(string) (os.FileInfo, error)) ([]byte, error) {
	initial, err := lstat(path)
	if err != nil || initial == nil || !initial.Mode().IsRegular() {
		return nil, documentLoadError(ErrDocumentUnavailable)
	}
	if initial.Size() > int64(manifest.MaximumDeclarationSize) {
		return nil, documentLoadError(ErrDocumentTooLarge)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, documentLoadError(ErrDocumentUnavailable)
	}

	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, documentLoadError(ErrDocumentUnavailable)
	}
	if !os.SameFile(initial, opened) {
		_ = file.Close()
		return nil, documentLoadError(ErrDocumentChanged)
	}
	if opened.Size() > int64(manifest.MaximumDeclarationSize) {
		_ = file.Close()
		return nil, documentLoadError(ErrDocumentTooLarge)
	}

	data, readErr := io.ReadAll(io.LimitReader(file, int64(manifest.MaximumDeclarationSize)+1))
	afterRead, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		clear(data)
		return nil, documentLoadError(ErrDocumentUnavailable)
	}
	if len(data) > manifest.MaximumDeclarationSize {
		clear(data)
		return nil, documentLoadError(ErrDocumentTooLarge)
	}
	if !sameDocumentSnapshot(opened, afterRead) {
		clear(data)
		return nil, documentLoadError(ErrDocumentChanged)
	}

	current, err := lstat(path)
	if err != nil || current == nil || !current.Mode().IsRegular() || !os.SameFile(afterRead, current) {
		clear(data)
		return nil, documentLoadError(ErrDocumentChanged)
	}
	if !sameDocumentSnapshot(afterRead, current) {
		clear(data)
		return nil, documentLoadError(ErrDocumentChanged)
	}
	return data, nil
}

func sameDocumentSnapshot(before, after os.FileInfo) bool {
	return before != nil && after != nil &&
		before.Mode().IsRegular() && after.Mode().IsRegular() &&
		os.SameFile(before, after) &&
		before.Size() == after.Size() &&
		before.Mode() == after.Mode() &&
		before.ModTime().Equal(after.ModTime())
}

func documentLoadError(reason error) error {
	return fmt.Errorf("%w: %w", ErrLoadDocument, reason)
}
