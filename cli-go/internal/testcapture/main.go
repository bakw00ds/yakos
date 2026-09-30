// Command testcapture is a loopback stand-in for the decision provider, used by
// tests/run-supervisor-shadow-test.sh. It appends every request body to a file,
// one per line, and answers with a valid supervisor-prefilter reply.
//
//	testcapture <capture-file> <port-file>
//
// It binds 127.0.0.1:0 and writes the chosen port to <port-file> atomically
// (temp file, then rename), so a reader never sees a partial or empty file.
// Errors go to stderr and exit 1. It is a test helper, not part of yakos.
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
)

const reply = `{"model":"jev-1.13.0","answers":{` +
	`"risk_class":{"type":"choice","choice":"benign","probabilities":{"benign":0.9,"needs_review":0.05,"dangerous":0.05},"confidence":0.9},` +
	`"in_stated_scope":{"type":"noul","noul":0.5},` +
	`"bypasses_hard_control":{"type":"noul","noul":0.1}},` +
	`"usage":{"input_tokens":10,"output_tokens":0}}`

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: testcapture <capture-file> <port-file>")
		os.Exit(2)
	}
	capture, portFile := os.Args[1], os.Args[2]
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testcapture: listen:", err)
		os.Exit(1)
	}
	var mu sync.Mutex
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		f, err := os.OpenFile(capture, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.Write(append(body, '\n'))
			_ = f.Close()
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	})
	tmp := portFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "testcapture: port file:", err)
		os.Exit(1)
	}
	if err := os.Rename(tmp, portFile); err != nil {
		fmt.Fprintln(os.Stderr, "testcapture: port file:", err)
		os.Exit(1)
	}
	if err := http.Serve(ln, nil); err != nil {
		fmt.Fprintln(os.Stderr, "testcapture: serve:", err)
		os.Exit(1)
	}
}
