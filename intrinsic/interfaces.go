package intrinsic

import (
	"reflect"
	"sort"
	"strings"

	healthv1 "github.com/plystra/kernel/interfaces/kernel/health/v1"
	infov1 "github.com/plystra/kernel/interfaces/kernel/info/v1"
)

var interfaceDefinitions = mustInterfaceDefinitions()

// InterfaceDefinition identifies one canonical reserved Kernel Interface
// package. Intrinsic Interfaces are registered by Kernel construction and do
// not participate in ordinary application Implementation selection.
type InterfaceDefinition struct {
	id          string
	packagePath string
}

// ID returns the exact reserved Interface ID.
func (d InterfaceDefinition) ID() string { return d.id }

// PackagePath returns the canonical Go Interface package import path.
func (d InterfaceDefinition) PackagePath() string { return d.packagePath }

// Source returns stable non-secret package provenance for diagnostics.
func (d InterfaceDefinition) Source() string {
	if d.id == "" || d.packagePath == "" {
		return ""
	}
	return d.packagePath + " //plystra:interface " + d.id
}

// InterfaceDefinitions returns every reserved intrinsic Interface in exact ID
// order. The returned slice is independent of Kernel-owned storage.
func InterfaceDefinitions() []InterfaceDefinition {
	return append([]InterfaceDefinition(nil), interfaceDefinitions...)
}

func mustInterfaceDefinitions() []InterfaceDefinition {
	definitions := []InterfaceDefinition{
		newInterfaceDefinition(healthv1.ID, reflect.TypeOf((*healthv1.Interface)(nil)).Elem()),
		newInterfaceDefinition(infov1.ID, reflect.TypeOf((*infov1.Interface)(nil)).Elem()),
	}
	sort.Slice(definitions, func(left, right int) bool {
		return definitions[left].id < definitions[right].id
	})
	for index, definition := range definitions {
		if !strings.HasPrefix(definition.id, "kernel.") || definition.packagePath == "" {
			panic("invalid intrinsic Kernel Interface definition")
		}
		if index != 0 && definitions[index-1].id == definition.id {
			panic("duplicate intrinsic Kernel Interface definition")
		}
	}
	return definitions
}

func newInterfaceDefinition(id string, interfaceType reflect.Type) InterfaceDefinition {
	if interfaceType == nil || interfaceType.Kind() != reflect.Interface || interfaceType.NumMethod() != 1 {
		panic("intrinsic Kernel Interface must be one named Go interface method")
	}
	return InterfaceDefinition{id: id, packagePath: interfaceType.PkgPath()}
}
