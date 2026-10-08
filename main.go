// Gatekeeper is a small forward proxy that blocks destinations listed in a
// blocklist file. It handles plain HTTP requests and HTTPS via CONNECT tunnels.
package main

import (
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

// Hop-by-hop headers describe a single connection, so a proxy must not
// forward them to the next hop.
var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, name := range hopHeaders {
		h.Del(name)
	}
}

// Proxy implements http.Handler, so it can be used directly by http.Server.
type Proxy struct {
	policy    *Policy
	transport *http.Transport // used to forward plain HTTP requests
}

func NewProxy(policy *Policy) *Proxy {
	return &Proxy{
		policy: policy,
		transport: &http.Transport{
			Proxy:                 nil, // never send our outgoing requests through another proxy
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       60 * time.Second,
			MaxIdleConnsPerHost:   10,
		},
	}
}

// ServeHTTP is called by the http.Server in its own goroutine for every request.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r, start)
		return
	}
	p.handleHTTP(w, r, start)
}

// handleHTTP forwards a plain HTTP request and relays the response.
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request, start time.Time) {
	// A proxy receives the full URL ("GET http://host/path"). A relative URL
	// means the client talked to us as if we were a normal web server.
	if !r.URL.IsAbs() {
		http.Error(w, "this is a proxy: send an absolute URL", http.StatusBadRequest)
		return
	}

	host := r.URL.Host
	if p.policy.IsBlocked(host) {
		http.Error(w, "blocked by policy", http.StatusForbidden)
		logRequest(r, host, "BLOCKED", start)
		return
	}

	out := r.Clone(r.Context())
	out.RequestURI = "" // must be empty for client-side requests
	removeHopHeaders(out.Header)

	resp, err := p.transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		logRequest(r, host, "ERROR", start)
		return
	}
	defer resp.Body.Close()

	removeHopHeaders(resp.Header)
	for name, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	logRequest(r, host, "ALLOWED", start)
}

// handleConnect builds an HTTPS tunnel. The client asks "CONNECT host:443";
// after the policy check we open a TCP connection to the host, answer 200, and
// then just copy bytes both ways. The bytes are TLS-encrypted, so the proxy
// can see the domain name but not the URL path or the content.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request, start time.Time) {
	host := r.Host // "example.com:443"
	if p.policy.IsBlocked(host) {
		http.Error(w, "blocked by policy", http.StatusForbidden)
		logRequest(r, host, "BLOCKED", start)
		return
	}

	dest, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		http.Error(w, "cannot reach destination: "+err.Error(), http.StatusBadGateway)
		logRequest(r, host, "ERROR", start)
		return
	}

	// Hijack takes over the raw TCP connection from the http.Server.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		dest.Close()
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		dest.Close()
		return
	}

	client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Two goroutines copy in opposite directions. When either direction ends
	// (a side closed the connection), we close both sockets, which also makes
	// the other io.Copy return.
	done := make(chan struct{}, 2)
	go func() { io.Copy(dest, client); done <- struct{}{} }()
	go func() { io.Copy(client, dest); done <- struct{}{} }()
	<-done
	client.Close()
	dest.Close()

	logRequest(r, host, "TUNNEL", start)
}

func logRequest(r *http.Request, host, action string, start time.Time) {
	log.Printf("client=%s method=%s host=%s action=%s duration=%s",
		r.RemoteAddr, r.Method, host, action, time.Since(start).Round(time.Millisecond))
}

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	blocklist := flag.String("blocklist", "blocklist.txt", "path to the blocklist file")
	flag.Parse()

	policy, err := LoadPolicy(*blocklist)
	if err != nil {
		log.Fatalf("cannot load blocklist: %v", err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           NewProxy(policy),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("Gatekeeper listening on %s with %d rules", *addr, policy.Count())
	log.Fatal(srv.ListenAndServe())
}
