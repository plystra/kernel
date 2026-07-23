package intrinsic

import (
	"github.com/plystra/kernel/capability"
	healthv1 "github.com/plystra/kernel/interfaces/kernel/health/v1"
	infov1 "github.com/plystra/kernel/interfaces/kernel/info/v1"
)

const (
	// ModulePath is the canonical Go Module that owns every intrinsic endpoint.
	ModulePath = "github.com/plystra/kernel"
	// ProviderPackage is the canonical implementation package recorded in
	// intrinsic runtime bindings.
	ProviderPackage = ModulePath + "/intrinsic"
)

var (
	healthContract = capability.MustParseContract[healthv1.Request, healthv1.Response](healthv1.ID)
	infoContract   = capability.MustParseContract[infov1.Request, infov1.Response](infov1.ID)
)

// HealthContract returns the exact typed kernel.health/v1 declaration used by
// both its intrinsic endpoint and generated callers.
func HealthContract() capability.Contract[healthv1.Request, healthv1.Response] {
	return healthContract
}

// InfoContract returns the exact typed kernel.info/v1 declaration used by
// both its intrinsic endpoint and generated callers.
func InfoContract() capability.Contract[infov1.Request, infov1.Response] {
	return infoContract
}
