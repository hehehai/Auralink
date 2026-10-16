//go:build demo

package main

// The demo build's service traffic (demo.go): it records the speech and
// language services' answers on the first take and replays them on the
// next ones, so that every take of a recording shows the same text.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ---- record and replay ----

var demoRec *replay

// replay is the services' transport: outside a take it calls; in a take
// it plays the take's n-th request back when it was recorded, else calls
// and records it.
type replay struct {
	dir, calls string
	base       http.RoundTripper
	mu         sync.Mutex
	take       string
	n          int
}

type recChunk struct {
	MS   int64  `json:"ms"`
	Data []byte `json:"data"`
}

type recording struct {
	URL      string     `json:"url"`
	Status   int        `json:"status"`
	Type     string     `json:"type"`
	HeaderMS int64      `json:"headerMs"`
	Chunks   []recChunk `json:"chunks"`
}

func (r *replay) setTake(name string) {
	r.mu.Lock()
	r.take, r.n = name, 0
	r.mu.Unlock()
}

func (r *replay) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	take, n := r.take, r.n
	r.n++
	r.mu.Unlock()
	url := req.URL.Host + req.URL.Path
	path := ""
	if take != "" {
		path = filepath.Join(r.dir, take, fmt.Sprintf("%02d.json", n))
		var rec recording
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &rec) == nil && rec.URL == url {
			if req.Body != nil {
				io.Copy(io.Discard, req.Body)
				req.Body.Close()
			}
			return r.play(req, rec)
		}
	}
	start := time.Now()
	resp, err := r.base.RoundTrip(req)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	r.logCall(fmt.Sprintf("%s\t%s\t%s\t%d\t%v", start.Format(time.RFC3339), req.URL.Host, take, status, err))
	if err != nil || path == "" || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	rec := recording{URL: url, Status: resp.StatusCode, Type: resp.Header.Get("Content-Type"), HeaderMS: time.Since(start).Milliseconds()}
	resp.Body = &recBody{rc: resp.Body, rec: rec, t0: time.Now(), path: path}
	return resp, nil
}

func (r *replay) logCall(line string) {
	f, err := os.OpenFile(r.calls, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintln(f, line)
	f.Close()
}

// play answers req with rec, at its pace.
func (r *replay) play(req *http.Request, rec recording) (*http.Response, error) {
	ctx := req.Context()
	select {
	case <-time.After(time.Duration(rec.HeaderMS) * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	pr, pw := io.Pipe()
	go func() {
		t0 := time.Now()
		for _, c := range rec.Chunks {
			if d := time.Duration(c.MS)*time.Millisecond - time.Since(t0); d > 0 {
				select {
				case <-time.After(d):
				case <-ctx.Done():
					pw.CloseWithError(ctx.Err())
					return
				}
			}
			if _, err := pw.Write(c.Data); err != nil {
				return
			}
		}
		pw.Close()
	}()
	h := http.Header{}
	h.Set("Content-Type", rec.Type)
	return &http.Response{StatusCode: rec.Status, Status: http.StatusText(rec.Status), Header: h, Body: pr,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Request: req, ContentLength: -1}, nil
}

// recBody records a response's body as it is read, and saves it at its
// end.
type recBody struct {
	rc   io.ReadCloser
	rec  recording
	t0   time.Time
	path string
	done bool
}

func (b *recBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.rec.Chunks = append(b.rec.Chunks, recChunk{MS: time.Since(b.t0).Milliseconds(), Data: append([]byte(nil), p[:n]...)})
	}
	if err == io.EOF {
		b.save()
	}
	return n, err
}

// Close saves what was read: a stream's reader stops at its last event,
// before the end of the body.
func (b *recBody) Close() error {
	b.save()
	return b.rc.Close()
}

func (b *recBody) save() {
	if b.done || len(b.rec.Chunks) == 0 {
		return
	}
	b.done = true
	if data, e := json.Marshal(b.rec); e == nil {
		os.MkdirAll(filepath.Dir(b.path), 0o755)
		os.WriteFile(b.path, data, 0o644)
	}
}
