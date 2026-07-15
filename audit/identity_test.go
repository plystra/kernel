package audit_test

import (
	"errors"
	"testing"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/plugin"
)

func TestKernelCallerIdentityHasNoPluginIdentity(t *testing.T) {
	t.Parallel()

	identity := audit.NewKernelCallerIdentity()
	if !identity.Valid() || identity.Kind() != audit.CallerKindKernel || identity.Kind() != "kernel" {
		t.Fatalf("Kernel identity = %#v", identity)
	}
	if identity.PluginID().String() != "" {
		t.Fatalf("Kernel PluginID = %q, want empty", identity.PluginID())
	}
}

func TestPluginCallerIdentityUsesCanonicalPluginID(t *testing.T) {
	t.Parallel()

	pluginID, err := plugin.ParseID("acme.email.sender")
	if err != nil {
		t.Fatalf("ParseID: %v", err)
	}
	identity, err := audit.NewPluginCallerIdentity(pluginID)
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity: %v", err)
	}
	if !identity.Valid() || identity.Kind() != audit.CallerKindPlugin || identity.Kind() != "plugin" || identity.PluginID() != pluginID {
		t.Fatalf("Plugin identity = %#v", identity)
	}
}

func TestPluginCallerIdentityRejectsZeroPluginID(t *testing.T) {
	t.Parallel()

	identity, err := audit.NewPluginCallerIdentity(plugin.ID{})
	if !errors.Is(err, audit.ErrInvalidCallerIdentity) {
		t.Fatalf("NewPluginCallerIdentity error = %v, want ErrInvalidCallerIdentity", err)
	}
	if identity.Valid() || identity.Kind() != "" || identity.PluginID().String() != "" {
		t.Fatalf("rejected identity = %#v", identity)
	}
}

func TestZeroCallerIdentityIsInvalid(t *testing.T) {
	t.Parallel()

	var identity audit.CallerIdentity
	if identity.Valid() || identity.Kind() != "" || identity.PluginID().String() != "" {
		t.Fatalf("zero identity = %#v", identity)
	}
}
