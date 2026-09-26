package contextmetadata_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plystra/kernel/contextmetadata"
)

func TestMetadataCopiesPayloadsAndIsolatesContextBranches(t *testing.T) {
	t.Parallel()
	root := context.Background()
	payload := []byte{0, 255, 42}
	first, err := contextmetadata.WithBytes(root, "example.first/v1", payload)
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = 99
	second, err := contextmetadata.WithBytes(first, "example.second/v1", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := contextmetadata.WithBytes(first, "example.second/v1", []byte("sibling"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{first, second, sibling} {
		got, exists, err := contextmetadata.Bytes(ctx, "example.first/v1")
		if err != nil || !exists || !bytes.Equal(got, []byte{0, 255, 42}) {
			t.Fatalf("first = %v %t %v", got, exists, err)
		}
		got[1] = 0
	}
	for _, test := range []struct {
		ctx  context.Context
		want string
	}{{second, "second"}, {sibling, "sibling"}} {
		got, exists, err := contextmetadata.Bytes(test.ctx, "example.second/v1")
		if err != nil || !exists || string(got) != test.want {
			t.Fatalf("branch = %q %t %v", got, exists, err)
		}
	}
	for _, ctx := range []context.Context{root, first} {
		got, exists, err := contextmetadata.Bytes(ctx, "example.second/v1")
		if got != nil || exists || err != nil {
			t.Fatalf("parent mutated: %v %t %v", got, exists, err)
		}
	}
	if duplicate, err := contextmetadata.WithBytes(second, "example.first/v1", []byte("replacement")); duplicate != nil || !errors.Is(err, contextmetadata.ErrMetadataConflict) {
		t.Fatalf("duplicate = %v %v", duplicate, err)
	}
	got, _, _ := contextmetadata.Bytes(second, "example.first/v1")
	if !bytes.Equal(got, []byte{0, 255, 42}) {
		t.Fatalf("failed insertion or read mutated data: %v", got)
	}
}

func TestMetadataRejectsInvalidInputWithoutEchoingIt(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"", "single/v1", "Example.key/v1", "example..key/v1", "example.key/v0", "example.key/v01", "example.key/v+1", "example.key/v18446744073709551616", "example.key/v1 ", "example.key/v1/extra", "example.\u00e9/v1", "example.key\x00/v1", "a." + strings.Repeat("b", 124) + "/v1"} {
		if ctx, err := contextmetadata.WithBytes(context.Background(), key, []byte("private payload")); ctx != nil || !errors.Is(err, contextmetadata.ErrInvalidMetadata) || strings.Contains(err.Error(), "private payload") {
			t.Fatalf("invalid insert = %v %v", ctx, err)
		}
		if got, exists, err := contextmetadata.Bytes(context.Background(), key); got != nil || exists || !errors.Is(err, contextmetadata.ErrInvalidMetadata) {
			t.Fatalf("invalid read = %v %t %v", got, exists, err)
		}
	}
	var absent context.Context
	if ctx, err := contextmetadata.WithBytes(absent, "example.key/v1", []byte("value")); ctx != nil || !errors.Is(err, contextmetadata.ErrInvalidMetadata) {
		t.Fatalf("nil insert = %v %v", ctx, err)
	}
	if got, exists, err := contextmetadata.Bytes(absent, "example.key/v1"); got != nil || exists || !errors.Is(err, contextmetadata.ErrInvalidMetadata) {
		t.Fatalf("nil read = %v %t %v", got, exists, err)
	}
	for _, payload := range [][]byte{nil, {}, make([]byte, 262_145)} {
		if ctx, err := contextmetadata.WithBytes(context.Background(), "example.key/v1", payload); ctx != nil || !errors.Is(err, contextmetadata.ErrInvalidMetadata) {
			t.Fatalf("invalid size = %v %v", ctx, err)
		}
	}
	for _, key := range []string{"a." + strings.Repeat("b", 123) + "/v1", "example.key/v18446744073709551615"} {
		if _, err := contextmetadata.WithBytes(context.Background(), key, []byte{1}); err != nil {
			t.Fatalf("valid key of %d bytes: %v", len(key), err)
		}
	}
}

func TestMetadataEnforcesEntryAndAggregateBounds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for index := 0; index < 32; index++ {
		var err error
		ctx, err = contextmetadata.WithBytes(ctx, fmt.Sprintf("example.key%d/v1", index), []byte{1})
		if err != nil {
			t.Fatalf("insert %d: %v", index, err)
		}
	}
	if next, err := contextmetadata.WithBytes(ctx, "example.overflow/v1", []byte{1}); next != nil || !errors.Is(err, contextmetadata.ErrInvalidMetadata) {
		t.Fatalf("entry overflow = %v %v", next, err)
	}
	if next, err := contextmetadata.WithBytes(ctx, "example.key0/v1", []byte{2}); next != nil || !errors.Is(err, contextmetadata.ErrMetadataConflict) {
		t.Fatalf("full duplicate = %v %v", next, err)
	}
	const firstKey = "example.first/v1"
	const secondKey = "example.second/v1"
	first, err := contextmetadata.WithBytes(context.Background(), firstKey, make([]byte, 262_144))
	if err != nil {
		t.Fatal(err)
	}
	remaining := 524_288 - len(firstKey) - 262_144 - len(secondKey)
	if next, err := contextmetadata.WithBytes(first, secondKey, make([]byte, remaining+1)); next != nil || !errors.Is(err, contextmetadata.ErrInvalidMetadata) {
		t.Fatalf("byte overflow = %v %v", next, err)
	}
	exact, err := contextmetadata.WithBytes(first, secondKey, make([]byte, remaining))
	if err != nil {
		t.Fatalf("exact aggregate bound: %v", err)
	}
	if next, err := contextmetadata.WithBytes(exact, "example.third/v1", []byte{1}); next != nil || !errors.Is(err, contextmetadata.ErrInvalidMetadata) {
		t.Fatalf("full byte insert = %v %v", next, err)
	}
	got, exists, err := contextmetadata.Bytes(exact, secondKey)
	if err != nil || !exists || len(got) != remaining {
		t.Fatalf("exact payload = size %d %t %v", len(got), exists, err)
	}
}

func TestMetadataPreservesContextAndDoesNotExposePayloads(t *testing.T) {
	t.Parallel()
	type ordinaryKey struct{}
	deadline := time.Now().Add(time.Hour)
	root, cancel := context.WithDeadline(context.WithValue(context.Background(), ordinaryKey{}, "ordinary"), deadline)
	defer cancel()
	const secret = "metadata-private-payload-marker"
	ctx, err := contextmetadata.WithBytes(root, "example.private/v1", []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if got, exists := ctx.Deadline(); !exists || !got.Equal(deadline) || ctx.Value(ordinaryKey{}) != "ordinary" {
		t.Fatal("derived context lost parent behavior")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, ctx), secret) {
			t.Fatalf("context formatting exposed payload using %s", format)
		}
	}
	encoded, err := json.Marshal(ctx)
	if err != nil || bytes.Contains(encoded, []byte(secret)) {
		t.Fatalf("context JSON = %s %v", encoded, err)
	}
	cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("cancellation was lost")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error = %v", ctx.Err())
	}
	if got, exists, err := contextmetadata.Bytes(context.WithoutCancel(ctx), "example.private/v1"); err != nil || !exists || string(got) != secret {
		t.Fatalf("ordinary derived read = %q %t %v", got, exists, err)
	}
}

func TestMetadataConcurrentReadsAndDerivedContexts(t *testing.T) {
	t.Parallel()
	root, err := contextmetadata.WithBytes(context.Background(), "example.shared/v1", []byte("shared"))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for index := 0; index < 64; index++ {
		workers.Go(func() {
			ctx, err := contextmetadata.WithBytes(root, "example.branch/v1", []byte{byte(index)})
			if err != nil {
				t.Error(err)
				return
			}
			got, exists, err := contextmetadata.Bytes(ctx, "example.shared/v1")
			if err != nil || !exists || string(got) != "shared" {
				t.Errorf("concurrent read = %q %t %v", got, exists, err)
				return
			}
			got[0] = 'X'
		})
	}
	workers.Wait()
	got, exists, err := contextmetadata.Bytes(root, "example.shared/v1")
	if err != nil || !exists || string(got) != "shared" {
		t.Fatalf("shared data mutated = %q %t %v", got, exists, err)
	}
}

func FuzzMetadataRoundTrip(f *testing.F) {
	f.Add("example.key/v1", []byte{0, 255, 1})
	f.Add("invalid", []byte("private"))
	f.Add("example.key/v18446744073709551615", []byte("value"))
	f.Fuzz(func(t *testing.T, key string, payload []byte) {
		ctx, err := contextmetadata.WithBytes(context.Background(), key, payload)
		if err != nil {
			if !errors.Is(err, contextmetadata.ErrInvalidMetadata) || ctx != nil {
				t.Fatalf("invalid insert = %v %v", ctx, err)
			}
			return
		}
		want := bytes.Clone(payload)
		got, exists, err := contextmetadata.Bytes(ctx, key)
		if err != nil || !exists || !bytes.Equal(got, want) {
			t.Fatalf("round trip failed: exists %t err %v", exists, err)
		}
		payload[0] ^= 255
		got[0] ^= 255
		fresh, _, _ := contextmetadata.Bytes(ctx, key)
		if !bytes.Equal(fresh, want) {
			t.Fatal("payload storage aliased input or read")
		}
	})
}
