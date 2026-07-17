package invocation_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/kernel/invocation"
)

func TestSemanticErrorExposesOnlyImmutableSafeState(t *testing.T) {
	t.Parallel()

	errorType := reflect.TypeFor[invocation.SemanticError]()
	for index := range errorType.NumField() {
		if field := errorType.Field(index); field.IsExported() {
			t.Fatalf("SemanticError field %q exposes mutable state", field.Name)
		}
	}
	zero := &invocation.SemanticError{}
	if zero.SemanticErrorCode() != "" || zero.Error() == "" || strings.Contains(zero.Error(), "provider") {
		t.Fatalf("zero SemanticError is unsafe: %q / %q", zero.SemanticErrorCode(), zero.Error())
	}
}
