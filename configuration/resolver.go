package configuration

import (
	"context"
	"errors"
	"io"
	"os"
)

const (
	// MaximumSecretValueBytes is the largest allowed configured resolution
	// bound. Individual runtimes may select a smaller positive limit.
	MaximumSecretValueBytes = 1 << 20
)

var (
	// ErrInvalidResolver reports a nil resolver or invalid resolution bound.
	ErrInvalidResolver = errors.New("invalid Secret resolver")
	// ErrInvalidContext reports a nil resolution context.
	ErrInvalidContext = errors.New("invalid Secret resolution context")
	// ErrResolve reports a safe Secret-resolution failure.
	ErrResolve = errors.New("resolve Secret reference")
	// ErrSecretUnavailable reports an absent, empty, unreadable, or non-regular
	// Secret source.
	ErrSecretUnavailable = errors.New("Secret is unavailable")
	// ErrSecretTooLarge reports a value beyond the configured byte bound.
	ErrSecretTooLarge = errors.New("Secret exceeds configured size")
)

// ResolverOptions configures intrinsic Secret resolution bounds.
type ResolverOptions struct {
	MaximumValueBytes int
}

// Resolver resolves validated environment and regular-file references without
// exposing targets or values through errors.
type Resolver struct {
	maximumValueBytes int
}

// NewResolver validates one immutable resolver configuration.
func NewResolver(options ResolverOptions) (*Resolver, error) {
	if options.MaximumValueBytes <= 0 || options.MaximumValueBytes > MaximumSecretValueBytes {
		return nil, ErrInvalidResolver
	}
	return &Resolver{maximumValueBytes: options.MaximumValueBytes}, nil
}

// Resolve resolves one validated reference into an opaque Secret.
func (r *Resolver) Resolve(ctx context.Context, reference Reference) (Secret, error) {
	if r == nil || r.maximumValueBytes <= 0 || r.maximumValueBytes > MaximumSecretValueBytes {
		return Secret{}, ErrInvalidResolver
	}
	if ctx == nil {
		return Secret{}, ErrInvalidContext
	}
	if !reference.Valid() {
		return Secret{}, ErrInvalidReference
	}
	if err := ctx.Err(); err != nil {
		return Secret{}, newResolutionError(reference.kind, nil, err)
	}

	var (
		value  []byte
		reason error
	)
	switch reference.kind {
	case ReferenceEnvironment:
		value, reason = r.resolveEnvironment(reference.target)
	case ReferenceFile:
		value, reason = r.resolveFile(reference.target)
	default:
		return Secret{}, ErrInvalidReference
	}
	if reason != nil {
		clear(value)
		return Secret{}, newResolutionError(reference.kind, reason, nil)
	}
	if err := ctx.Err(); err != nil {
		clear(value)
		return Secret{}, newResolutionError(reference.kind, nil, err)
	}
	secret := newSecret(value)
	clear(value)
	if !secret.Valid() {
		return Secret{}, newResolutionError(reference.kind, ErrSecretUnavailable, nil)
	}
	return secret, nil
}

func (r *Resolver) resolveEnvironment(name string) ([]byte, error) {
	value, exists := os.LookupEnv(name)
	if !exists || value == "" {
		return nil, ErrSecretUnavailable
	}
	if len(value) > r.maximumValueBytes {
		return nil, ErrSecretTooLarge
	}
	return []byte(value), nil
}

func (r *Resolver) resolveFile(path string) ([]byte, error) {
	initial, err := os.Stat(path)
	if err != nil || !initial.Mode().IsRegular() {
		return nil, ErrSecretUnavailable
	}
	if initial.Size() > int64(r.maximumValueBytes) {
		return nil, ErrSecretTooLarge
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrSecretUnavailable
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrSecretUnavailable
	}
	if info.Size() > int64(r.maximumValueBytes) {
		return nil, ErrSecretTooLarge
	}
	value, err := io.ReadAll(io.LimitReader(file, int64(r.maximumValueBytes)+1))
	if err != nil {
		return nil, ErrSecretUnavailable
	}
	if len(value) == 0 {
		return nil, ErrSecretUnavailable
	}
	if len(value) > r.maximumValueBytes {
		clear(value)
		return nil, ErrSecretTooLarge
	}
	return value, nil
}

type resolutionError struct {
	kind   ReferenceKind
	reason error
	cause  error
}

func newResolutionError(kind ReferenceKind, reason, cause error) error {
	return &resolutionError{kind: kind, reason: reason, cause: contextError(cause)}
}

func (e *resolutionError) Error() string {
	message := ErrResolve.Error() + ": " + e.kind.String()
	if e.reason != nil {
		message += ": " + e.reason.Error()
	}
	if e.cause != nil {
		message += ": " + e.cause.Error()
	}
	return message
}

func (e *resolutionError) Is(target error) bool {
	return e != nil && (target == ErrResolve || target == e.reason || e.cause != nil && target == e.cause)
}

func contextError(err error) error {
	switch err {
	case context.Canceled:
		return context.Canceled
	case context.DeadlineExceeded:
		return context.DeadlineExceeded
	default:
		return nil
	}
}
