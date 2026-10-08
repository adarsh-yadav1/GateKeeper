# Gatekeeper

Gatekeeper is a small forward proxy written in Go. Clients send their HTTP and HTTPS traffic through it, and it allows or blocks each request based on the destination domain, using a plain-text blocklist. Every decision is written to a log.

It uses only the Go standard library and is intended as a compact, readable example of how a web gateway works: request routing, `CONNECT` tunnelling, concurrent connection handling and policy enforcement.

## Features

- Forwards plain HTTP requests and relays the responses.
- Tunnels HTTPS traffic through `CONNECT`, copying encrypted bytes in both directions.
- Blocks destinations using a text-file blocklist with exact rules and `*.domain.com` wildcard rules.
- Handles each connection concurrently, with one goroutine per request.
- Logs one line per request: client, method, destination, action and duration.
- Returns clear status codes: `403` for blocked destinations, `400` for requests that are not proxy requests, and `502` when the destination cannot be reached.
- Includes unit and integration tests, which pass with Go's race detector enabled.

## How it works

```
                    +--------------------------------------+
  browser / curl -->|            Gatekeeper :8080          |--> internet
                    |                                      |
                    |  1. read request                     |
                    |  2. extract destination host         |
                    |  3. check blocklist  --blocked-->  403 Forbidden
                    |  4. allowed:                         |
                    |       HTTP    -> forward, relay reply|
                    |       CONNECT -> open TCP, copy bytes|
                    |  5. log one line                     |
                    +--------------------------------------+
```

**Plain HTTP.** A client using a proxy sends the full URL (`GET http://host/path`). Gatekeeper checks the host against the blocklist, forwards the request without the hop-by-hop headers, and relays the response.

**HTTPS.** The client first sends `CONNECT host:443`. After the blocklist check, Gatekeeper opens a TCP connection to the destination, replies `200 Connection Established`, and then copies bytes between the two sockets using two goroutines. The traffic is TLS-encrypted end to end, so Gatekeeper sees the destination domain but not the URL path or the content. Filtering by path would require TLS inspection, which this project does not implement.

## Requirements

- Go 1.22 or newer
- `curl` (optional, for manual testing)

## Getting started

```bash
git clone <your-repository-url>
cd gatekeeper

go test -race ./...        # run the tests
go build -o gatekeeper .   # build the binary
./gatekeeper               # start the proxy on :8080
```

Command-line flags:

| Flag | Default | Description |
|---|---|---|
| `-addr` | `:8080` | Address and port to listen on |
| `-blocklist` | `blocklist.txt` | Path to the blocklist file |

When running on a machine that other people can reach, bind to the local interface only, because the proxy has no authentication:

```bash
./gatekeeper -addr 127.0.0.1:8080
```

## Blocklist format

One rule per line. Blank lines and lines starting with `#` are ignored. Matching is case-insensitive and ignores the port and any trailing dot.

| Rule | Blocks | Does not block |
|---|---|---|
| `example.com` | `example.com` | `www.example.com` |
| `*.example.com` | `www.example.com`, `a.b.example.com` | `example.com`, `notexample.com` |

To block a domain and all of its subdomains, list both rules. The blocklist is read once at startup, so restart the proxy after editing it.

## Usage examples

Allowed requests:

```bash
curl -x http://127.0.0.1:8080 http://example.com
curl -x http://127.0.0.1:8080 https://example.com
```

Blocked requests (with the sample blocklist, which contains `facebook.com` and `*.facebook.com`):

```bash
curl -i -x http://127.0.0.1:8080 http://facebook.com
curl -i -x http://127.0.0.1:8080 https://www.facebook.com
```

The first returns `403 Forbidden`. For the second, `curl` reports that the CONNECT tunnel failed with status 403.

A request that is not a proxy request (a relative URL) is rejected:

```bash
curl -i http://127.0.0.1:8080/        # 400 Bad Request
```

To use Gatekeeper from a browser, set the HTTP and HTTPS proxy to `127.0.0.1` and port `8080` in the browser or system settings.

**Note on local testing.** If you test against local addresses such as `127.0.0.1`, `curl` may bypass the proxy when `NO_PROXY` or `no_proxy` includes that address. Add `--noproxy ''` to force the request through the proxy.

## Log format

```
2026/10/08 12:00:09 client=127.0.0.1:56786 method=GET host=127.0.0.1:9000 action=ALLOWED duration=7ms
2026/10/08 12:00:16 client=127.0.0.1:56806 method=CONNECT host=127.0.0.1:9000 action=TUNNEL duration=1ms
2026/10/08 12:00:16 client=127.0.0.1:42998 method=GET host=127.0.0.1:9000 action=BLOCKED duration=0s
```

| Action | Meaning |
|---|---|
| `ALLOWED` | A plain HTTP request was forwarded |
| `TUNNEL` | A `CONNECT` tunnel was established and has now closed |
| `BLOCKED` | The destination matched a blocklist rule |
| `ERROR` | The destination could not be reached |

For tunnels, the duration is the total time the tunnel was open.

## Project structure

| File | Purpose |
|---|---|
| `main.go` | Proxy handler (HTTP forwarding and `CONNECT` tunnels), logging and program entry point |
| `policy.go` | Blocklist loading and domain matching |
| `policy_test.go` | Unit tests for the domain matcher |
| `proxy_test.go` | Integration tests that start the proxy with real local servers |
| `blocklist.txt` | Sample blocklist |

## Testing

```bash
go vet ./...
go test -race -v ./...
```

The tests cover:

- Domain matching: case, ports, trailing dots, exact rules, wildcards and dot boundaries.
- Plain HTTP forwarding (allowed and blocked).
- Rejection of non-proxy requests.
- `CONNECT` tunnelling, verified by sending bytes through the tunnel to a local echo server.
- Blocked tunnels and unreachable destinations.

## Limitations

- No authentication. Anyone who can reach the port can use the proxy.
- No idle timeout or connection limit on tunnels.
- The blocklist is loaded once at startup.
- HTTPS traffic can be filtered by domain only, not by URL path or content.
- HTTP/1.1 only. `Upgrade` is not forwarded on the plain HTTP path.
- An exact rule does not block subdomains. Add a `*.` rule as well.

## Possible improvements

- Idle timeouts and a maximum number of concurrent connections.
- Reloading the blocklist on `SIGHUP` using an atomic pointer swap.
- Per-client rate limiting.
- Byte counters in the log and a metrics endpoint.
- Optional proxy authentication.
