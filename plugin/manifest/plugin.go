package manifest

import (
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/kernel/capability"
	kernelplugin "github.com/plystra/kernel/plugin"
	"go.yaml.in/yaml/v3"
)

// ErrInvalidPlugin reports an invalid plugin.yaml declaration.
var ErrInvalidPlugin = errors.New("invalid plugin manifest")

// Plugin is one immutable validated plugin.yaml declaration.
type Plugin struct {
	id       kernelplugin.ID
	provides []capability.Identifier
	requires []capability.Identifier
	config   Config
}

// ID returns the concrete plugin identity.
func (p Plugin) ID() kernelplugin.ID {
	return p.id
}

// Provides returns exact capability versions implemented by the plugin in
// canonical identity order.
func (p Plugin) Provides() []capability.Identifier {
	return append([]capability.Identifier(nil), p.provides...)
}

// Requires returns non-inferable capability requirements in canonical
// identity order.
func (p Plugin) Requires() []capability.Identifier {
	return append([]capability.Identifier(nil), p.requires...)
}

// Config returns the plugin configuration declaration.
func (p Plugin) Config() Config {
	return p.config
}

// ParsePlugin parses and validates one strict plugin.yaml declaration.
func ParsePlugin(data []byte) (Plugin, error) {
	root, err := decodeSingleYAMLDocument(data)
	if err != nil {
		return Plugin{}, invalidPlugin("%v", err)
	}
	if root.Kind != yaml.MappingNode {
		return Plugin{}, invalidPlugin("document must be a mapping")
	}

	var manifest Plugin
	var idNode, providesNode, requiresNode, configNode *yaml.Node
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		keyNode, valueNode := root.Content[index], root.Content[index+1]
		key, err := strictString(keyNode)
		if err != nil {
			return Plugin{}, invalidPlugin("document contains a non-string key")
		}
		if _, duplicate := seen[key]; duplicate {
			return Plugin{}, invalidPlugin("duplicate key %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "id":
			idNode = valueNode
		case "provides":
			providesNode = valueNode
		case "requires":
			requiresNode = valueNode
		case "config":
			configNode = valueNode
		default:
			return Plugin{}, invalidPlugin("unknown key %q", key)
		}
	}

	if idNode == nil {
		return Plugin{}, invalidPlugin("id is required")
	}
	idValue, err := strictString(idNode)
	if err != nil {
		return Plugin{}, invalidPlugin("id must be a string")
	}
	manifest.id, err = kernelplugin.ParseID(idValue)
	if err != nil {
		return Plugin{}, invalidPlugin("id %q is not canonical", idValue)
	}
	if providesNode != nil {
		manifest.provides, err = parseCapabilityList("provides", providesNode)
		if err != nil {
			return Plugin{}, err
		}
	}
	if requiresNode != nil {
		manifest.requires, err = parseCapabilityList("requires", requiresNode)
		if err != nil {
			return Plugin{}, err
		}
	}
	if configNode != nil {
		manifest.config, err = parseConfigNode(configNode)
		if err != nil {
			return Plugin{}, invalidPlugin("config: %v", err)
		}
	}
	return manifest, nil
}

func parseCapabilityList(field string, node *yaml.Node) ([]capability.Identifier, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, invalidPlugin("%s must be a sequence", field)
	}
	identifiers := make([]capability.Identifier, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for index, item := range node.Content {
		value, err := strictString(item)
		if err != nil {
			return nil, invalidPlugin("%s[%d] must be a string", field, index)
		}
		identifier, err := capability.ParseIdentifier(value)
		if err != nil {
			return nil, invalidPlugin("%s[%d] %q is not canonical", field, index, value)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, invalidPlugin("%s contains duplicate capability %q", field, value)
		}
		seen[value] = struct{}{}
		identifiers = append(identifiers, identifier)
	}
	sort.Slice(identifiers, func(left, right int) bool {
		return identifiers[left].String() < identifiers[right].String()
	})
	return identifiers, nil
}

func invalidPlugin(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPlugin, fmt.Sprintf(format, arguments...))
}
