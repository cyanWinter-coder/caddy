// Copyright 2015 Matthew Holt and The Caddy Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reverseproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/caddyserver/caddy/v2"
)

type hardeningFlushRecorder645 struct {
	*httptest.ResponseRecorder
	mu       sync.Mutex
	flushes  int
	flushErr error
}

func (r *hardeningFlushRecorder645) FlushError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushes++
	return r.flushErr
}

func (r *hardeningFlushRecorder645) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushes
}

type hardeningBlockingBody645 struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newHardeningBlockingBody645(start bool) *hardeningBlockingBody645 {
	b := &hardeningBlockingBody645{release: make(chan struct{})}
	if start {
		b.started = make(chan struct{})
	}
	return b
}

func (b *hardeningBlockingBody645) Read([]byte) (int, error) {
	if b.started != nil {
		close(b.started)
	}
	<-b.release
	return 0, io.ErrClosedPipe
}

func (b *hardeningBlockingBody645) Close() error {
	b.once.Do(func() { close(b.release) })
	return nil
}

func TestStreamForwardingHardening645(t *testing.T) {
	t.Run("chunk flushes and flush errors propagate", func(t *testing.T) {
		rec := &hardeningFlushRecorder645{ResponseRecorder: httptest.NewRecorder()}
		h := Handler{logger: zap.NewNop()}
		data := strings.Repeat("chunk-data-", 4096)

		if err := h.copyResponse(rec, strings.NewReader(data), 0, zap.NewNop()); err != nil {
			t.Fatalf("copyResponse() error = %v", err)
		}
		if rec.Body.String() != data {
			t.Fatalf("copied %d bytes, want %d", rec.Body.Len(), len(data))
		}
		if got := rec.count(); got < 2 {
			t.Fatalf("flushes = %d, want at least chunk flushes", got)
		}

		wantErr := errors.New("client gone")
		failing := &hardeningFlushRecorder645{
			ResponseRecorder: httptest.NewRecorder(),
			flushErr:         wantErr,
		}
		err := h.copyResponse(failing, strings.NewReader("payload"), -1, zap.NewNop())
		if !errors.Is(err, errWritingDownstream) || !errors.Is(err, wantErr) {
			t.Fatalf("copyResponse() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("async flush failure closes upstream body", func(t *testing.T) {
		body := newHardeningBlockingBody645(true)
		flushErr := errors.New("async flush failed")
		rec := &hardeningFlushRecorder645{
			ResponseRecorder: httptest.NewRecorder(),
			flushErr:         flushErr,
		}
		h := Handler{logger: zap.NewNop()}

		done := make(chan error, 1)
		go func() {
			done <- h.copyResponse(rec, body, 5*time.Millisecond, zap.NewNop())
		}()

		select {
		case <-body.started:
		case <-time.After(time.Second):
			t.Fatal("copy never started reading")
		}

		select {
		case err := <-done:
			if !errors.Is(err, errWritingDownstream) || !errors.Is(err, flushErr) {
				t.Fatalf("copyResponse() error = %v, want flush error", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("copy remained blocked after asynchronous flush failure")
		}
	})

	t.Run("client cancellation closes upstream body", func(t *testing.T) {
		body := newHardeningBlockingBody645(false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/file", nil)
		ctx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(ctx)
		res := &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{},
			Body:          body,
			ContentLength: -1,
		}

		cancel()
		func() {
			defer func() {
				if recovered := recover(); recovered != http.ErrAbortHandler {
					t.Fatalf("recover() = %v, want http.ErrAbortHandler", recovered)
				}
			}()
			_ = (&Handler{logger: zap.NewNop()}).finalizeResponse(
				httptest.NewRecorder(), req, res, caddy.NewReplacer(), fakeStart, zap.NewNop(), "upstream:1",
			)
		}()

		select {
		case <-body.release:
		case <-time.After(time.Second):
			t.Fatal("client cancellation did not close upstream response body")
		}
	})

	t.Run("duplicate headers survive hop-by-hop stripping", func(t *testing.T) {
		src := http.Header{}
		src.Add("X-Duplicate", "one")
		src.Add("X-Duplicate", "two")
		src.Add("Connection", "X-Token")
		src.Add("X-Token", "strip")
		src.Add("Keep-Alive", "timeout=10")
		src.Add("X-Kept", "kept")

		stripHopByHopHeaders(src)
		dst := http.Header{}
		copyHeader(dst, src)

		if got := dst.Values("X-Duplicate"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
			t.Fatalf("X-Duplicate = %#v, want [one two]", got)
		}
		if got := dst.Get("X-Kept"); got != "kept" {
			t.Fatalf("X-Kept = %q, want kept", got)
		}
		if got := dst.Get("X-Token"); got != "" {
			t.Fatalf("X-Token = %q, want stripped", got)
		}
		if got := dst.Get("Keep-Alive"); got != "" {
			t.Fatalf("Keep-Alive = %q, want stripped", got)
		}
	})
}
