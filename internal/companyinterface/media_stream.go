package companyinterface

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MediaLengthHeader = "X-Company-Media-Length"
const MediaCompleteHeader = "X-Company-Media-Complete"
const MediaChunkSize = 64 << 10
const MediaAuthorityTimeout = 5 * time.Second

func MediaFence(fence func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		check, cancel := context.WithTimeout(ctx, MediaAuthorityTimeout)
		defer cancel()
		return fence(check)
	}
}

var ErrMediaHandoff = errors.New("direct storage delivery requires a credential-free media handoff")

// WatchMedia performs short independent authority reads. No database transaction
// or authority lock is retained while an upstream or downstream stream is idle.
func WatchMedia(parent context.Context, fence func(context.Context) error) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	fence = MediaFence(fence)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := fence(ctx); e != nil {
					cancel(e)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}
func mediaContextError(ctx context.Context) error {
	if e := context.Cause(ctx); e != nil {
		return e
	}
	return nil
}
func MediaBodyless(method string, status int) bool {
	return method == http.MethodHead || status == 204 || status == 304
}
func mediaLength(raw string) (int64, error) {
	if raw == "" {
		return -1, nil
	}
	if len(raw) > 19 {
		return 0, ErrUnavailable
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, ErrUnavailable
		}
	}
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n < 0 {
		return 0, ErrUnavailable
	}
	return n, nil
}

// MediaHeaders admits only representation/security metadata. Protocol completion
// fields are owned by this adapter, never copied from an inner handler or caller.
func MediaHeaders(in http.Header) (http.Header, error) {
	out := http.Header{}
	if encoding := in.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, ErrUnavailable
	}
	for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Retry-After", "Content-Length", "Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy"} {
		values := in.Values(name)
		if len(values) > 1 {
			return nil, ErrUnavailable
		}
		if len(values) == 1 {
			v := values[0]
			if len(v) > 4096 || strings.ContainsAny(v, "\r\n\x00") {
				return nil, ErrUnavailable
			}
			out.Set(name, v)
		}
	}
	if _, e := mediaLength(out.Get("Content-Length")); e != nil {
		return nil, e
	}
	out.Set("Cache-Control", "no-store")
	return out, nil
}

// MediaWriter withholds at most one fixed chunk until the final authority fence.
// Its lack of Unwrap is intentional: inner ServeContent handlers may not clear
// the actual connection's bounded write deadline. Already sent bytes cannot be
// recalled; a failed fence aborts the incomplete stream instead of completing it.
type MediaWriter struct {
	target            http.ResponseWriter
	ctx               context.Context
	method            string
	internal          bool
	fence             func(context.Context) error
	header            http.Header
	status            int
	tail              []byte
	count             int64
	expected          int64
	prepared, started bool
	err               error
	idle              *time.Timer
}

func NewMediaWriter(ctx context.Context, w http.ResponseWriter, method string, internal bool, fence func(context.Context) error, cancel context.CancelFunc) *MediaWriter {
	m := &MediaWriter{target: w, ctx: ctx, method: method, internal: internal, fence: MediaFence(fence), header: http.Header{}, tail: make([]byte, 0, MediaChunkSize), expected: -1}
	m.idle = time.AfterFunc(MediaIdleTimeout, cancel)
	return m
}
func (m *MediaWriter) Close()              { m.idle.Stop() }
func (m *MediaWriter) Started() bool       { return m.started }
func (m *MediaWriter) Header() http.Header { return m.header }
func (m *MediaWriter) WriteHeader(status int) {
	if m.status == 0 {
		m.status = status
	}
}
func (m *MediaWriter) prepare() error {
	if m.prepared {
		return nil
	}
	m.prepared = true
	if m.status == 0 {
		m.status = 200
	}
	if m.status < 200 || m.status > 599 {
		return ErrUnavailable
	}
	h, e := MediaHeaders(m.header)
	if e != nil {
		return e
	}
	m.expected, e = mediaLength(h.Get("Content-Length"))
	if e != nil {
		return e
	}
	m.header = h
	return nil
}
func (m *MediaWriter) start() error {
	if m.started {
		return nil
	}
	if e := m.prepare(); e != nil {
		return e
	}
	if e := m.fence(m.ctx); e != nil {
		return e
	}
	for k, v := range m.header {
		m.target.Header()[k] = append([]string(nil), v...)
	}
	// Strip any pre-existing hop metadata. Only this adapter emits completion.
	m.target.Header().Del(MediaLengthHeader)
	m.target.Header().Del(MediaCompleteHeader)
	m.target.Header().Del("Trailer")
	if m.internal {
		m.target.Header().Del("Content-Length")
		if m.expected >= 0 {
			m.target.Header().Set(MediaLengthHeader, strconv.FormatInt(m.expected, 10))
		}
		if MediaBodyless(m.method, m.status) {
			m.target.Header().Set(MediaCompleteHeader, "complete")
		} else {
			m.target.Header().Set("Trailer", MediaCompleteHeader)
		}
	}
	m.started = true
	m.target.WriteHeader(m.status)
	return nil
}
func (m *MediaWriter) writeBytes(p []byte) error {
	if e := mediaContextError(m.ctx); e != nil {
		return e
	}
	if e := m.start(); e != nil {
		return e
	}
	deadline := time.Now().Add(MediaIdleTimeout)
	if hard, ok := m.ctx.Deadline(); ok && hard.Before(deadline) {
		deadline = hard
	}
	_ = http.NewResponseController(m.target).SetWriteDeadline(deadline)
	n, e := m.target.Write(p)
	if e == nil && n != len(p) {
		e = io.ErrShortWrite
	}
	if e != nil {
		return e
	}
	// Flush preserves incremental delivery and prevents a small internal response
	// from acquiring an implicit Content-Length that would suppress its trailer.
	if e = http.NewResponseController(m.target).Flush(); e != nil && !errors.Is(e, http.ErrNotSupported) {
		return e
	}
	m.idle.Reset(MediaIdleTimeout)
	return nil
}
func (m *MediaWriter) Write(p []byte) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	if e := m.prepare(); e != nil {
		m.err = e
		return 0, e
	}
	if MediaBodyless(m.method, m.status) {
		if m.method == http.MethodHead {
			m.count += int64(len(p))
			if m.header.Get("Content-Type") == "" && len(p) > 0 {
				m.header.Set("Content-Type", http.DetectContentType(p))
			}
		}
		return len(p), nil
	}
	original := len(p)
	m.count += int64(original)
	if m.expected >= 0 && m.count > m.expected {
		m.err = ErrUnavailable
		return 0, m.err
	}
	for len(p) > 0 {
		if len(m.tail) == MediaChunkSize {
			if e := m.writeBytes(m.tail); e != nil {
				m.err = e
				return 0, e
			}
			m.tail = m.tail[:0]
		}
		n := min(MediaChunkSize-len(m.tail), len(p))
		m.tail = append(m.tail, p[:n]...)
		p = p[n:]
	}
	m.idle.Reset(MediaIdleTimeout)
	return original, nil
}
func (m *MediaWriter) Finish() error {
	defer m.Close()
	if m.err != nil {
		return m.err
	}
	if e := m.prepare(); e != nil {
		return e
	}
	if e := mediaContextError(m.ctx); e != nil {
		return e
	}
	if e := m.fence(m.ctx); e != nil {
		return e
	}
	if m.method == http.MethodHead && m.status != 204 && m.status != 304 && m.expected < 0 && m.count > 0 {
		m.expected = m.count
		m.header.Set("Content-Length", strconv.FormatInt(m.count, 10))
	}
	if !MediaBodyless(m.method, m.status) && m.expected >= 0 && m.count != m.expected {
		return ErrUnavailable
	}
	if e := m.start(); e != nil {
		return e
	}
	if !MediaBodyless(m.method, m.status) {
		if e := m.writeBytes(m.tail); e != nil {
			return e
		}
		if m.internal {
			m.target.Header().Set(MediaCompleteHeader, "complete")
		}
	}
	return nil
}
