package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

func decodeSingleYAMLDocument(data []byte) (*yaml.Node, error) {
	if len(data) == 0 {
		return nil, errors.New("document is empty")
	}
	if len(data) > MaximumDeclarationSize {
		return nil, fmt.Errorf("document exceeds %d bytes", MaximumDeclarationSize)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode YAML: %w", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("decode trailing YAML: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, errors.New("expected one YAML document")
	}
	if err := rejectYAMLReferences(&document); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

func rejectYAMLReferences(root *yaml.Node) error {
	stack := []*yaml.Node{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]
		if node == nil {
			continue
		}
		if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
			return errors.New("YAML anchors and aliases are not allowed")
		}
		stack = append(stack, node.Content...)
	}
	return nil
}
