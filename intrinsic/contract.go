package intrinsic

import "github.com/plystra/kernel/capability"

const (
	// ModulePath is the canonical Go Module that owns every intrinsic endpoint.
	ModulePath = "github.com/plystra/kernel"
	// ProviderPackage is the canonical implementation package recorded in
	// intrinsic runtime bindings.
	ProviderPackage = ModulePath + "/intrinsic"
)

// HealthRequest is the empty request for kernel.health/v1.
type HealthRequest struct{}

// HealthStatus is the closed liveness state returned by kernel.health/v1.
type HealthStatus string

const (
	// HealthStatusHealthy reports that the intrinsic Kernel endpoint is live.
	HealthStatusHealthy HealthStatus = "healthy"
)

// HealthResponse is the bounded response for kernel.health/v1.
type HealthResponse struct {
	Status HealthStatus `json:"status"`
}

// InfoRequest is the empty request for kernel.info/v1.
type InfoRequest struct{}

// InfoResponse contains non-sensitive Kernel compatibility information.
type InfoResponse struct {
	AssemblyAPI   string `json:"assembly_api"`
	KernelModule  string `json:"kernel_module"`
	KernelVersion string `json:"kernel_version"`
}

var (
	healthContract = capability.MustParseContract[HealthRequest, HealthResponse]("kernel.health/v1")
	infoContract   = capability.MustParseContract[InfoRequest, InfoResponse]("kernel.info/v1")
)

// HealthContract returns the exact typed kernel.health/v1 declaration used by
// both its intrinsic endpoint and generated callers.
func HealthContract() capability.Contract[HealthRequest, HealthResponse] {
	return healthContract
}

// InfoContract returns the exact typed kernel.info/v1 declaration used by
// both its intrinsic endpoint and generated callers.
func InfoContract() capability.Contract[InfoRequest, InfoResponse] {
	return infoContract
}
