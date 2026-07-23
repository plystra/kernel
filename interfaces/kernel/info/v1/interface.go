// Package infov1 defines the intrinsic kernel.info/v1 Interface.
package infov1

import "context"

// ID is the exact versioned Interface identity.
const ID = "kernel.info/v1"

// Interface reports non-sensitive Kernel compatibility information.
//
//plystra:interface kernel.info/v1
type Interface interface {
	Info(context.Context, Request) (Response, error)
}

// Request is the empty information request.
type Request struct{}

// Response contains non-sensitive Kernel compatibility information.
type Response struct {
	AssemblyAPI   string `json:"assembly_api" plystra:"1,required"`
	KernelModule  string `json:"kernel_module" plystra:"2,required"`
	KernelVersion string `json:"kernel_version" plystra:"3,required"`
}
