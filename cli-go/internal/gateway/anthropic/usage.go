package anthropic

// usage.go: reads token counts out of a response as it passes. The tap sees a
// copy of the bytes after they are written to the client, never before, and
// never alters them. It keeps counts only; no text is retained beyond the
// current SSE line or (for a JSON reply) a capped buffer.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strings"
	"sync"
)

const (
	maxSSELine  = 1 << 20
	maxJSONBody = 4 << 20
)

type usage struct {
	in, out, cacheRead, cacheCreate int64
}

type usageFields struct {
	In          int64 `json:"input_tokens"`
	Out         int64 `json:"output_tokens"`
	CacheRead   int64 `json:"cache_read_input_tokens"`
	CacheCreate int64 `json:"cache_creation_input_tokens"`
}

func (u *usage) merge(f usageFields) {
	u.in = max(u.in, f.In)
	u.out = max(u.out, f.Out)
	u.cacheRead = max(u.cacheRead, f.CacheRead)
	u.cacheCreate = max(u.cacheCreate, f.CacheCreate)
}

// tap is an io.WriteCloser that accumulates usage. Close waits for a gzip
// decoder goroutine, if any, and then usage() is final.
type tap struct {
	sse  bool
	mu   sync.Mutex
	u    usage
	line []byte
	skip bool // current SSE line overflowed; drop it
	buf  []byte
	over bool

	pw   *io.PipeWriter
	done chan struct{}
}

// newTap returns a tap for a response with the given Content-Type and
// Content-Encoding. An encoding other than gzip or identity is not decoded:
// the counts stay zero.
func newTap(contentType, encoding string) io.WriteCloser {
	t := &tap{sse: strings.Contains(strings.ToLower(contentType), "text/event-stream")}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return t
	case "gzip":
		pr, pw := io.Pipe()
		t.pw, t.done = pw, make(chan struct{})
		go func() {
			defer close(t.done)
			zr, err := gzip.NewReader(pr)
			if err != nil {
				_, _ = io.Copy(io.Discard, pr)
				return
			}
			_, _ = io.Copy(t.plain(), io.LimitReader(zr, 64<<20))
			_, _ = io.Copy(io.Discard, pr)
		}()
		return t
	}
	return discardTap{}
}

type discardTap struct{}

func (discardTap) Write(p []byte) (int, error) { return len(p), nil }
func (discardTap) Close() error                { return nil }

func (t *tap) plain() io.Writer { return plainWriter{t} }

type plainWriter struct{ t *tap }

func (p plainWriter) Write(b []byte) (int, error) { p.t.feed(b); return len(b), nil }

func (t *tap) Write(b []byte) (int, error) {
	if t.pw != nil {
		_, _ = t.pw.Write(b) // the reader goroutine always drains
		return len(b), nil
	}
	t.feed(b)
	return len(b), nil
}

func (t *tap) Close() error {
	if t.pw != nil {
		_ = t.pw.Close()
		<-t.done
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sse {
		t.endLine()
	} else if !t.over {
		t.parseJSON(t.buf)
	}
	return nil
}

func (t *tap) usage() usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.u
}

func (t *tap) feed(b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.sse {
		if t.over || len(t.buf)+len(b) > maxJSONBody {
			t.over, t.buf = true, nil
			return
		}
		t.buf = append(t.buf, b...)
		return
	}
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			t.addLine(b)
			return
		}
		t.addLine(b[:i])
		t.endLine()
		b = b[i+1:]
	}
}

func (t *tap) addLine(b []byte) {
	if t.skip {
		return
	}
	if len(t.line)+len(b) > maxSSELine {
		t.skip, t.line = true, nil
		return
	}
	t.line = append(t.line, b...)
}

func (t *tap) endLine() {
	line, skip := t.line, t.skip
	t.line, t.skip = nil, false
	if skip {
		return
	}
	line = bytes.TrimRight(line, "\r")
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	var ev struct {
		Type    string       `json:"type"`
		Usage   *usageFields `json:"usage"`
		Message *struct {
			Usage *usageFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(bytes.TrimSpace(data), &ev) != nil {
		return
	}
	switch {
	case ev.Message != nil && ev.Message.Usage != nil:
		t.u.merge(*ev.Message.Usage)
	case ev.Usage != nil:
		t.u.merge(*ev.Usage)
	}
}

// parseJSON reads a non-streaming reply: {"usage":{...}} for a message,
// {"input_tokens":N} for count_tokens.
func (t *tap) parseJSON(b []byte) {
	var r struct {
		usageFields
		Usage *usageFields `json:"usage"`
	}
	if json.Unmarshal(b, &r) != nil {
		return
	}
	t.u.merge(r.usageFields)
	if r.Usage != nil {
		t.u.merge(*r.Usage)
	}
}
