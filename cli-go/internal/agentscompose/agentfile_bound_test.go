package agentscompose

// agentfile_bound_test.go — the read is bounded, whatever the inspection saw
// (rev-324: nothing tested the bound or the check after it). The size the
// inspection sees does not bound the read: a file can grow after it, and some
// regular files report no size at all. The input here is finite, so a missing
// bound shows as an assertion, not as an exhausted machine.

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// countingReader yields n bytes of 'x' and then EOF, and counts what was taken.
type countingReader struct{ n, read int64 }

func (c *countingReader) Read(p []byte) (int, error) {
	if c.read >= c.n {
		return 0, io.EOF
	}
	k := int64(len(p))
	if rest := c.n - c.read; k > rest {
		k = rest
	}
	for i := int64(0); i < k; i++ {
		p[i] = 'x'
	}
	c.read += k
	return int(k), nil
}

// An input far over the cap is read for one byte past it and no further.
func TestReadBounded_StopsOneBytePastTheCap(t *testing.T) {
	r := &countingReader{n: 3 * MaxAgentFileBytes}
	data, skip, err := readBounded(r)
	if err != nil {
		t.Fatalf("readBounded = %v", err)
	}
	if len(data) != 0 || skip != ProblemTooLarge.warning("") {
		t.Errorf("data = %d bytes, skip = %q, want a skip %q", len(data), skip, ProblemTooLarge.warning(""))
	}
	if r.read != MaxAgentFileBytes+1 {
		t.Errorf("%d bytes were read of an input of %d, want exactly %d (one past the cap)", r.read, r.n, MaxAgentFileBytes+1)
	}
}

// The cap itself is allowed.
func TestReadBounded_TheCapItselfIsAllowed(t *testing.T) {
	data, skip, err := readBounded(&countingReader{n: MaxAgentFileBytes})
	if err != nil || skip != "" || len(data) != MaxAgentFileBytes {
		t.Errorf("data = %d bytes, skip = %q, err = %v; want the whole input", len(data), skip, err)
	}
}

// One byte over the cap is a skip, and is not cut to the cap and passed on: the
// check after the read is what tells it.
func TestReadBounded_OneByteOverTheCapIsASkip(t *testing.T) {
	data, skip, err := readBounded(&countingReader{n: MaxAgentFileBytes + 1})
	if err != nil || len(data) != 0 || skip != ProblemTooLarge.warning("") {
		t.Errorf("data = %d bytes, skip = %q, err = %v; want a skip %q", len(data), skip, err, ProblemTooLarge.warning(""))
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

// A read error is an error, not a skip and not an empty file.
func TestReadBounded_AReadErrorIsAnError(t *testing.T) {
	data, skip, err := readBounded(io.MultiReader(strings.NewReader("x"), failingReader{}))
	if err == nil || len(data) != 0 || skip != "" {
		t.Errorf("data = %d bytes, skip = %q, err = %v; want an error", len(data), skip, err)
	}
}
