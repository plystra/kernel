// Package healthv1 defines the intrinsic kernel.health/v1 Interface.
package healthv1

import "context"

// ID is the exact versioned Interface identity.
const ID = "kernel.health/v1"

// Interface reports intrinsic Kernel liveness.
//
//plystra:interface kernel.health/v1
type Interface interface {
	Health(context.Context, Request) (Response, error)
}

// Request is the empty health request.
type Request struct{}

// Status is the closed intrinsic liveness state.
type Status string

const (
	// StatusHealthy reports that the intrinsic Kernel endpoint is live.
	StatusHealthy Status = "healthy"
)

// Response is the bounded health response.
type Response struct {
	Status Status `json:"status" plystra:"1,required"`
}
