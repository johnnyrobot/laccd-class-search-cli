// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/johnnyrobot/laccd-class-search-cli/internal/cliutil"
)

const testResponseBodyLimit = 8 << 20

func TestScheduleAPIDiscoveryPropagatesBodyReadError(t *testing.T) {
	httpClient := &http.Client{Transport: staticResponseTransport{
		status: http.StatusOK,
		body:   &unexpectedEOFBody{payload: []byte(`{"termname":"Fall 2026"`)},
	}}
	discovery := NewScheduleAPIDiscovery("https://schedule.test", httpClient, cliutil.NewAdaptiveLimiter(0))

	_, err := discovery.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD")
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Catalogs error = %T %[1]v, want wrapped io.ErrUnexpectedEOF", err)
	}
}

func TestScheduleAPIDiscoveryRejectsOversizedBody(t *testing.T) {
	httpClient := &http.Client{Transport: staticResponseTransport{
		status: http.StatusOK,
		body:   io.NopCloser(strings.NewReader(strings.Repeat("x", testResponseBodyLimit+1))),
	}}
	discovery := NewScheduleAPIDiscovery("https://schedule.test", httpClient, cliutil.NewAdaptiveLimiter(0))

	_, err := discovery.Catalogs(context.Background(), "LAMC", "2268", "MULTIMD")
	if err == nil || !strings.Contains(err.Error(), "exceeds 8388608-byte limit") {
		t.Fatalf("Catalogs error = %v, want oversized-response error", err)
	}
}

func TestPeopleSoftClientPropagatesBodyReadError(t *testing.T) {
	client := New("https://peoplesoft.test", 0, 0)
	client.http = &http.Client{Transport: staticResponseTransport{
		status: http.StatusOK,
		body:   &unexpectedEOFBody{payload: []byte("short HTML")},
	}}

	_, err := client.do(context.Background(), http.MethodGet, "https://peoplesoft.test/search", nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("PeopleSoft error = %T %[1]v, want wrapped io.ErrUnexpectedEOF", err)
	}
}

func TestPeopleSoftClientRejectsOversizedBody(t *testing.T) {
	client := New("https://peoplesoft.test", 0, 0)
	client.http = &http.Client{Transport: staticResponseTransport{
		status: http.StatusOK,
		body:   io.NopCloser(strings.NewReader(strings.Repeat("x", testResponseBodyLimit+1))),
	}}

	_, err := client.do(context.Background(), http.MethodGet, "https://peoplesoft.test/search", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds 8388608-byte limit") {
		t.Fatalf("PeopleSoft error = %v, want oversized-response error", err)
	}
}

type staticResponseTransport struct {
	status int
	body   io.ReadCloser
}

func (t staticResponseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.status,
		Header:     make(http.Header),
		Body:       t.body,
	}, nil
}

type unexpectedEOFBody struct {
	payload []byte
	read    bool
}

func (b *unexpectedEOFBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	n := copy(p, b.payload)
	return n, io.ErrUnexpectedEOF
}

func (*unexpectedEOFBody) Close() error { return nil }
