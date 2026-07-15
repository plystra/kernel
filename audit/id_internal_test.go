package audit

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRuntimeIDGenerationFailsClosedWhenRandomnessIsUnavailable(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("randomness unavailable")
	for _, test := range []struct {
		name   string
		source io.Reader
		is     error
	}{
		{name: "nil source"},
		{name: "source error", source: failingRuntimeIDReader{err: wantErr}, is: wantErr},
		{name: "short source", source: bytes.NewReader(make([]byte, 15)), is: io.ErrUnexpectedEOF},
		{name: "repeated zero", source: bytes.NewReader(make([]byte, 16*maximumRuntimeIDAttempts))},
	} {
		value, err := randomRuntimeIDFrom(test.source)
		if err == nil || value != "" {
			t.Fatalf("%s result = %q, %v", test.name, value, err)
		}
		if test.is != nil && !errors.Is(err, test.is) {
			t.Fatalf("%s error = %v, want %v", test.name, err, test.is)
		}
	}
}

func TestRuntimeIDGenerationRetriesOneZeroValue(t *testing.T) {
	t.Parallel()

	data := append(make([]byte, 16), bytes.Repeat([]byte{0xab}, 16)...)
	value, err := randomRuntimeIDFrom(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("randomRuntimeIDFrom: %v", err)
	}
	if value != strings.Repeat("ab", 16) || !validRuntimeID(value) {
		t.Fatalf("generated value = %q", value)
	}
}

type failingRuntimeIDReader struct {
	err error
}

func (r failingRuntimeIDReader) Read([]byte) (int, error) {
	return 0, r.err
}
