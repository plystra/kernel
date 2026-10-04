// Package lifecycle coordinates optional in-process Implementation and named
// Resource startup and shutdown in the dependency-first order supplied by
// generated assembly. Resource provider provenance never replaces instance
// identity, and Resource calls remain ordinary Go calls outside invocation.
package lifecycle
