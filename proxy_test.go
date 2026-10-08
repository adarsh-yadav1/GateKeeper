package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestProxy starts the proxy on a random local port with the given rules.
func newTestProxy(t *testing.T, rules ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewProxy(NewPolicy(rules)))
	t.Cleanup(srv.Close)
	return srv
}

// clientVia returns an HTTP client that sends all requests through the proxy.
func clientVia(t *testing.T, proxy *httptest.Server) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
}

func newBackend(t *testing.T) *httptest.Server {
	t.Helper()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello from backend")
	}))
	t.Cleanup(b.Close)
	return b
}

// startEchoServer is a tiny TCP server that sends back whatever it receives.
func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

// connectVia sends a raw CONNECT request and returns the connection, a reader
// positioned after the response headers, and the response status line.
func connectVia(t *testing.T, proxy *httptest.Server, target string) (net.Conn, *bufio.Reader, string) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	for { // skip the remaining response headers
		line, err := br.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}
	return conn, br, strings.TrimSpace(statusLine)
}

func TestHTTPAllowed(t *testing.T) {
	backend := newBackend(t)
	proxy := newTestProxy(t) // empty policy: everything allowed

	resp, err := clientVia(t, proxy).Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK || string(body) != "hello from backend" {
		t.Errorf("got %d %q, want 200 %q", resp.StatusCode, body, "hello from backend")
	}
}

func TestHTTPBlocked(t *testing.T) {
	backend := newBackend(t)
	proxy := newTestProxy(t, "127.0.0.1") // block the backend's host

	resp, err := clientVia(t, proxy).Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestHTTPRelativeURLRejected(t *testing.T) {
	proxy := newTestProxy(t)

	// Talking to the proxy like a normal web server (no absolute URL).
	resp, err := http.Get(proxy.URL + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestConnectTunnelAllowed(t *testing.T) {
	echoAddr := startEchoServer(t)
	proxy := newTestProxy(t)

	conn, br, status := connectVia(t, proxy, echoAddr)
	if status != "HTTP/1.1 200 Connection Established" {
		t.Fatalf("status = %q", status)
	}

	// Bytes sent through the tunnel must come back from the echo server.
	fmt.Fprint(conn, "ping\n")
	reply, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if reply != "ping\n" {
		t.Errorf("reply = %q, want %q", reply, "ping\n")
	}
}

func TestConnectBlocked(t *testing.T) {
	echoAddr := startEchoServer(t)
	proxy := newTestProxy(t, "127.0.0.1")

	_, _, status := connectVia(t, proxy, echoAddr)
	if !strings.HasPrefix(status, "HTTP/1.1 403") {
		t.Errorf("status = %q, want 403", status)
	}
}

func TestConnectUnreachableDestination(t *testing.T) {
	proxy := newTestProxy(t)

	// Port 1 on localhost is almost certainly closed.
	_, _, status := connectVia(t, proxy, "127.0.0.1:1")
	if !strings.HasPrefix(status, "HTTP/1.1 502") {
		t.Errorf("status = %q, want 502", status)
	}
}
