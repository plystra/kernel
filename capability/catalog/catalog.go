// Package catalog exposes canonical official Plystra capability definitions.
package catalog

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/plugin/manifest"
)

//go:embed definitions
var definitionFiles embed.FS

var officialDefinitions = mustLoadDefinitions()

// Definition is one immutable official capability definition.
type Definition struct {
	contract manifest.Capability
	source   []byte
	digest   [sha256.Size]byte
}

// ID returns the exact official capability identity.
func (d Definition) ID() capability.Identifier {
	return d.contract.ID()
}

// Contract returns the validated capability declaration.
func (d Definition) Contract() manifest.Capability {
	return d.contract
}

// Source returns a defensive copy of the canonical LF-only capability.yaml.
func (d Definition) Source() []byte {
	return append([]byte(nil), d.source...)
}

// SchemaDigest returns the canonical semantic schema digest.
func (d Definition) SchemaDigest() [sha256.Size]byte {
	return d.digest
}

// Definitions returns all official definitions in canonical identity order.
func Definitions() []Definition {
	return append([]Definition(nil), officialDefinitions...)
}

// Lookup returns the official definition for one exact capability identity.
func Lookup(id capability.Identifier) (Definition, bool) {
	value := id.String()
	index := sort.Search(len(officialDefinitions), func(index int) bool {
		return officialDefinitions[index].ID().String() >= value
	})
	if value == "" || index >= len(officialDefinitions) || officialDefinitions[index].ID() != id {
		return Definition{}, false
	}
	return officialDefinitions[index], true
}

func mustLoadDefinitions() []Definition {
	var definitions []Definition
	err := fs.WalkDir(definitionFiles, "definitions", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		parts := strings.Split(path, "/")
		if len(parts) != 4 || parts[0] != "definitions" || parts[3] != "capability.yaml" {
			return fmt.Errorf("unexpected catalog file %q", path)
		}
		expectedID := parts[1] + "/" + parts[2]
		source, err := definitionFiles.ReadFile(path)
		if err != nil {
			return err
		}
		source = canonicalSource(source)
		contract, err := manifest.ParseCapability(source)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if contract.ID().String() != expectedID {
			return fmt.Errorf("%s declares %q, want %q", path, contract.ID(), expectedID)
		}
		digest, err := contract.SchemaDigest()
		if err != nil {
			return fmt.Errorf("digest %s: %w", path, err)
		}
		definitions = append(definitions, Definition{contract: contract, source: source, digest: digest})
		return nil
	})
	if err != nil {
		panic("load official capability catalog: " + err.Error())
	}
	sort.Slice(definitions, func(left, right int) bool {
		return definitions[left].ID().String() < definitions[right].ID().String()
	})
	for index := 1; index < len(definitions); index++ {
		if definitions[index-1].ID() == definitions[index].ID() {
			panic("load official capability catalog: duplicate " + definitions[index].ID().String())
		}
	}
	return definitions
}

func canonicalSource(source []byte) []byte {
	source = bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
	source = bytes.ReplaceAll(source, []byte("\r"), []byte("\n"))
	source = bytes.TrimRight(source, "\n")
	return append(source, '\n')
}
