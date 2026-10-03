package invocation

import (
	"errors"
	"strings"

	"golang.org/x/mod/module"
)

const (
	// MaximumModulePathSize bounds embedded Go module path provenance.
	MaximumModulePathSize = 512
	// MaximumModuleVersionSize bounds an optional canonical Go module version.
	MaximumModuleVersionSize = 128
	// MaximumBuildIdentitySize bounds an optional safe generated or VCS build ID.
	MaximumBuildIdentitySize = 128
)

// ErrInvalidModuleBuild reports incomplete, unsafe, or contradictory module
// provenance.
var ErrInvalidModuleBuild = errors.New("invalid runtime module build provenance")

// ModuleBuild is immutable embedded Go module provenance for a runtime
// implementation. Go remains authoritative for dependency resolution; this is
// observability metadata, not a second lockfile.
type ModuleBuild struct {
	modulePath    string
	moduleVersion string
	buildIdentity string
	initialized   bool
}

// NewModuleBuild validates a canonical Go module path and either its canonical
// module version, a safe build identity, or both. Local and development modules
// without a version must provide an independently generated build identity.
// An unversioned local Project may use a single-component Go import path.
func NewModuleBuild(modulePath, moduleVersion, buildIdentity string) (ModuleBuild, error) {
	build := ModuleBuild{
		modulePath:    modulePath,
		moduleVersion: moduleVersion,
		buildIdentity: buildIdentity,
	}
	if !validModuleBuild(build) {
		return ModuleBuild{}, ErrInvalidModuleBuild
	}
	build.initialized = true
	return build, nil
}

// ModulePath returns the canonical Go module path containing the implementation.
func (b ModuleBuild) ModulePath() string {
	if !b.Valid() {
		return ""
	}
	return b.modulePath
}

// ModuleVersion returns the canonical Go module version when the build has one.
func (b ModuleBuild) ModuleVersion() string {
	if !b.Valid() {
		return ""
	}
	return b.moduleVersion
}

// BuildIdentity returns the optional safe generated or VCS build identity.
func (b ModuleBuild) BuildIdentity() string {
	if !b.Valid() {
		return ""
	}
	return b.buildIdentity
}

// Valid reports whether the immutable provenance passed constructor validation.
func (b ModuleBuild) Valid() bool {
	return b.initialized
}

func validModuleBuild(b ModuleBuild) bool {
	if len(b.modulePath) == 0 || len(b.modulePath) > MaximumModulePathSize {
		return false
	}
	if module.CheckPath(b.modulePath) != nil && (b.moduleVersion != "" || strings.Contains(b.modulePath, "/") || module.CheckImportPath(b.modulePath) != nil) {
		return false
	}
	if b.moduleVersion == "" && b.buildIdentity == "" {
		return false
	}
	if b.moduleVersion != "" && !validModuleVersion(b.modulePath, b.moduleVersion) {
		return false
	}
	return b.buildIdentity == "" || validBuildIdentity(b.buildIdentity)
}

func validModuleVersion(modulePath, version string) bool {
	if len(version) > MaximumModuleVersionSize || module.CanonicalVersion(version) != version {
		return false
	}
	_, pathMajor, ok := module.SplitPathVersion(modulePath)
	return ok && module.CheckPathMajor(version, pathMajor) == nil
}

func validBuildIdentity(identity string) bool {
	if len(identity) == 0 || len(identity) > MaximumBuildIdentitySize {
		return false
	}
	separator := false
	for index := 0; index < len(identity); index++ {
		character := identity[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9':
			separator = false
		case index != 0 && index != len(identity)-1 && !separator &&
			(character == '.' || character == '_' || character == ':' || character == '-'):
			separator = true
		default:
			return false
		}
	}
	return true
}
