package auth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// keyringProbeTimeout bounds one keyring read made on the dispatch path. A
// variable so a test can shorten it.
var keyringProbeTimeout = 2 * time.Second

// errKeyringTimeout is returned by a bounded read that did not finish in time.
var errKeyringTimeout = errors.New("keyring: lookup timed out")

// boundedKeyring reads through inner but gives up when ctx ends or timeout
// passes, whichever comes first. go-keyring has no context: on macOS it spawns
// /usr/bin/security and on Linux it can wait on a Secret Service unlock prompt
// with no limit, so a dispatch that probes a signed-out agy could hang there,
// and /api/chat/cancel could not stop it (sec-324 F3). The read itself cannot
// be interrupted, so it finishes in the background; keyringFlights makes
// concurrent reads of one entry share it, so a read stuck on a prompt holds one
// goroutine, not one per dispatch.
type boundedKeyring struct {
	inner   KeyringBackend
	ctx     context.Context
	timeout time.Duration
}

func (b boundedKeyring) Set(service, account, secret string) error {
	return b.inner.Set(service, account, secret)
}

func (b boundedKeyring) Delete(service, account string) error {
	return b.inner.Delete(service, account)
}

// keyringFlight is one read in progress.
type keyringFlight struct {
	done chan struct{}
	val  string
	err  error
}

var keyringFlights = struct {
	sync.Mutex
	m map[string]*keyringFlight
}{m: make(map[string]*keyringFlight)}

func (b boundedKeyring) Get(service, account string) (string, error) {
	key := service + "\x00" + account
	keyringFlights.Lock()
	fl, running := keyringFlights.m[key]
	if !running {
		fl = &keyringFlight{done: make(chan struct{})}
		keyringFlights.m[key] = fl
		go func() {
			fl.val, fl.err = b.inner.Get(service, account)
			keyringFlights.Lock()
			delete(keyringFlights.m, key)
			keyringFlights.Unlock()
			close(fl.done)
		}()
	}
	keyringFlights.Unlock()

	timer := time.NewTimer(b.timeout)
	defer timer.Stop()
	select {
	case <-fl.done:
		return fl.val, fl.err
	case <-timer.C:
		return "", errKeyringTimeout
	case <-b.ctx.Done():
		return "", b.ctx.Err()
	}
}

// keyringTimedOut reports whether err is the bounded read giving up.
func keyringTimedOut(err error) bool { return errors.Is(err, errKeyringTimeout) }
