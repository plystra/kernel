package intrinsic_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	healthv1 "github.com/plystra/kernel/interfaces/kernel/health/v1"
	infov1 "github.com/plystra/kernel/interfaces/kernel/info/v1"
	"github.com/plystra/kernel/intrinsic"
)

func TestInterfaceDefinitionsPublishCanonicalReservedPackages(t *testing.T) {
	t.Parallel()

	definitions := intrinsic.InterfaceDefinitions()
	if got := intrinsicDefinitionIDs(definitions); !slices.Equal(got, []string{healthv1.ID, infov1.ID}) {
		t.Fatalf("InterfaceDefinitions IDs = %v", got)
	}
	wantPackages := []string{
		"github.com/plystra/kernel/interfaces/kernel/health/v1",
		"github.com/plystra/kernel/interfaces/kernel/info/v1",
	}
	for index, definition := range definitions {
		if definition.PackagePath() != wantPackages[index] || definition.Source() != wantPackages[index]+" //plystra:interface "+definition.ID() {
			t.Fatalf("InterfaceDefinitions[%d] = ID %q package %q source %q", index, definition.ID(), definition.PackagePath(), definition.Source())
		}
	}

	definitions[0] = intrinsic.InterfaceDefinition{}
	if intrinsic.InterfaceDefinitions()[0].ID() != healthv1.ID {
		t.Fatal("InterfaceDefinitions exposed Kernel-owned slice storage")
	}
}

func TestCanonicalIntrinsicInterfaceShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		value      reflect.Type
		methodName string
		request    reflect.Type
		response   reflect.Type
	}{
		{
			name:       healthv1.ID,
			value:      reflect.TypeOf((*healthv1.Interface)(nil)).Elem(),
			methodName: "Health",
			request:    reflect.TypeOf(healthv1.Request{}),
			response:   reflect.TypeOf(healthv1.Response{}),
		},
		{
			name:       infov1.ID,
			value:      reflect.TypeOf((*infov1.Interface)(nil)).Elem(),
			methodName: "Info",
			request:    reflect.TypeOf(infov1.Request{}),
			response:   reflect.TypeOf(infov1.Response{}),
		},
	}
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.value.Kind() != reflect.Interface || test.value.NumMethod() != 1 {
				t.Fatalf("Interface shape = %v", test.value)
			}
			method := test.value.Method(0)
			if method.Name != test.methodName || method.Type.NumIn() != 2 || method.Type.In(0) != contextType || method.Type.In(1) != test.request || method.Type.NumOut() != 2 || method.Type.Out(0) != test.response || method.Type.Out(1) != errorType {
				t.Fatalf("method = %s %v", method.Name, method.Type)
			}
		})
	}
	if field, ok := reflect.TypeOf(healthv1.Response{}).FieldByName("Status"); !ok || field.Tag.Get("plystra") != "1,required" {
		t.Fatalf("health response Status tag = %q, %t", field.Tag.Get("plystra"), ok)
	}
	for index, name := range []string{"AssemblyAPI", "KernelModule", "KernelVersion"} {
		field, ok := reflect.TypeOf(infov1.Response{}).FieldByName(name)
		want := string(rune('1'+index)) + ",required"
		if !ok || field.Tag.Get("plystra") != want {
			t.Fatalf("info response %s tag = %q, %t", name, field.Tag.Get("plystra"), ok)
		}
	}
}

func intrinsicDefinitionIDs(definitions []intrinsic.InterfaceDefinition) []string {
	result := make([]string, len(definitions))
	for index, definition := range definitions {
		result[index] = definition.ID()
	}
	return result
}
