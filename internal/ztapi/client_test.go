package ztapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testToken = "abcdefghijklmnopqrstuvwx"

// recorded is what the fake server saw of one request.
type recorded struct {
	method, path, token, contentType, body string
}

// newRecordingServer starts an httptest server that answers every request
// with code and reply, and records the request. It returns a client for it.
//
// httptest.NewServer listens on a random free port on 127.0.0.1;
// t.Cleanup closes it when the test ends.
func newRecordingServer(t *testing.T, code int, reply string) (*Client, *recorded) {
	t.Helper()
	rec := &recorded{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body) // a test server: a read error just leaves body empty
		*rec = recorded{
			method:      r.Method,
			path:        r.URL.Path,
			token:       r.Header.Get("X-ZT1-Auth"),
			contentType: r.Header.Get("Content-Type"),
			body:        string(body),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(ts.Close)
	return New(ts.URL, testToken), rec
}

// closedAddr returns a 127.0.0.1 address where nothing listens, so
// connecting to it is refused.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestClientEndpoints(t *testing.T) {
	ctx := context.Background()
	id := NetworkID("c0ffee1234000001")
	networkJSON := `{"id":"c0ffee1234000001","name":"home-lab","status":"OK","allowDNS":true,"routes":[{"target":"10.147.17.0/24","via":null}]}`

	// Each case calls one method. check looks at the decoded result.
	tests := []struct {
		name      string
		reply     string
		call      func(c *Client) (any, error)
		method    string
		path      string
		body      string // expected request body ("" = none)
		wantToken bool
		check     func(t *testing.T, result any)
	}{
		{
			name:  "Health",
			reply: `{"online":true,"version":"1.16.2","versionMajor":1,"versionMinor":16,"versionRev":2,"clock":1}`,
			call:  func(c *Client) (any, error) { return c.Health(ctx) },
			// The token is sent when we have one, even though /health
			// doesn't need it.
			method: "GET", path: "/health", wantToken: true,
			check: func(t *testing.T, result any) {
				h := result.(Health)
				if !h.Online || h.Version != "1.16.2" || h.VersionMinor != 16 {
					t.Errorf("Health = %+v", h)
				}
			},
		},
		{
			name:   "Status",
			reply:  `{"address":"a1b2c3d4e5","online":true,"version":"1.16.2","config":{"settings":{"primaryPort":9993}}}`,
			call:   func(c *Client) (any, error) { return c.Status(ctx) },
			method: "GET", path: "/status", wantToken: true,
			check: func(t *testing.T, result any) {
				s := result.(NodeStatus)
				if s.Address != "a1b2c3d4e5" || !s.Online || s.Config.Settings.PrimaryPort != 9993 {
					t.Errorf("Status = %+v", s)
				}
			},
		},
		{
			name:   "Networks sorted by ID",
			reply:  `[{"id":"c0ffee1234000002","status":"OK"},{"id":"c0ffee1234000001","status":"ACCESS_DENIED"}]`,
			call:   func(c *Client) (any, error) { return c.Networks(ctx) },
			method: "GET", path: "/network", wantToken: true,
			check: func(t *testing.T, result any) {
				nets := result.([]Network)
				if len(nets) != 2 || nets[0].ID != "c0ffee1234000001" || nets[1].ID != "c0ffee1234000002" {
					t.Errorf("Networks = %+v", nets)
				}
				if nets[0].Status != StatusAccessDenied {
					t.Errorf("status = %q", nets[0].Status)
				}
			},
		},
		{
			name:   "Network",
			reply:  networkJSON,
			call:   func(c *Client) (any, error) { return c.Network(ctx, id) },
			method: "GET", path: "/network/c0ffee1234000001", wantToken: true,
			check: func(t *testing.T, result any) {
				n := result.(Network)
				if n.Name != "home-lab" || !n.AllowDNS || n.Routes[0].Via != "" {
					t.Errorf("Network = %+v", n)
				}
			},
		},
		{
			name:   "Join",
			reply:  `{"id":"c0ffee1234000001","status":"REQUESTING_CONFIGURATION"}`,
			call:   func(c *Client) (any, error) { return c.Join(ctx, id) },
			method: "POST", path: "/network/c0ffee1234000001", body: `{}`, wantToken: true,
			check: func(t *testing.T, result any) {
				if n := result.(Network); n.Status != StatusRequestingConfiguration {
					t.Errorf("status = %q", n.Status)
				}
			},
		},
		{
			name:  "Leave",
			reply: `{"result":true}`,
			call: func(c *Client) (any, error) {
				return nil, c.Leave(ctx, id)
			},
			method: "DELETE", path: "/network/c0ffee1234000001", wantToken: true,
		},
		{
			name:  "SetFlags",
			reply: networkJSON,
			call: func(c *Client) (any, error) {
				return c.SetFlags(ctx, id, map[Flag]bool{FlagAllowDNS: true, FlagAllowGlobal: false})
			},
			// encoding/json writes map keys in sorted order.
			method: "POST", path: "/network/c0ffee1234000001", body: `{"allowDNS":true,"allowGlobal":false}`, wantToken: true,
			check: func(t *testing.T, result any) {
				if n := result.(Network); !n.AllowDNS {
					t.Errorf("SetFlags returned %+v", n)
				}
			},
		},
		{
			name:   "Peers",
			reply:  `[{"address":"1111111111","role":"PLANET","latency":42,"paths":[{"address":"192.0.2.1/9993","active":true,"preferred":true}]}]`,
			call:   func(c *Client) (any, error) { return c.Peers(ctx) },
			method: "GET", path: "/peer", wantToken: true,
			check: func(t *testing.T, result any) {
				peers := result.([]Peer)
				if len(peers) != 1 || peers[0].Role != "PLANET" || !peers[0].Paths[0].Preferred {
					t.Errorf("Peers = %+v", peers)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newRecordingServer(t, http.StatusOK, tc.reply)
			result, err := tc.call(c)
			if err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if rec.method != tc.method || rec.path != tc.path {
				t.Errorf("request = %s %s, want %s %s", rec.method, rec.path, tc.method, tc.path)
			}
			if tc.wantToken && rec.token != testToken {
				t.Errorf("X-ZT1-Auth = %q, want the token", rec.token)
			}
			if rec.body != tc.body {
				t.Errorf("request body = %q, want %q", rec.body, tc.body)
			}
			if tc.body != "" && rec.contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", rec.contentType)
			}
			if tc.check != nil {
				tc.check(t, result)
			}
		})
	}
}

// TestClientWithRealFixtures serves the captured 1.16.2 replies and reads
// them through the client.
func TestClientWithRealFixtures(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"/status":                   "1.16.2/status.json",
		"/network":                  "1.16.2/network.json",
		"/network/a1b2c3d4e5dead01": "1.16.2/network-requesting-configuration.json",
		"/peer":                     "1.16.2/peer.json",
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readFixture(t, name))
	}))
	defer ts.Close()
	c := New(ts.URL, testToken)

	if s, err := c.Status(ctx); err != nil || s.Address != "a1b2c3d4e5" {
		t.Errorf("Status = %+v, %v", s, err)
	}
	if nets, err := c.Networks(ctx); err != nil || len(nets) != 1 || nets[0].Status != StatusNotFound {
		t.Errorf("Networks = %+v, %v", nets, err)
	}
	if n, err := c.Network(ctx, "a1b2c3d4e5dead01"); err != nil || n.Status != StatusRequestingConfiguration {
		t.Errorf("Network = %+v, %v", n, err)
	}
	if peers, err := c.Peers(ctx); err != nil || len(peers) != 4 {
		t.Errorf("Peers = %d peers, %v", len(peers), err)
	}
}

func TestClientHTTPErrors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		code  int
		reply string
		// check gets the error the call returned.
		check func(t *testing.T, err error)
	}{
		{
			name: "401 is ErrUnauthorized", code: 401, reply: `{}`,
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrUnauthorized) {
					t.Errorf("err = %v, want ErrUnauthorized", err)
				}
			},
		},
		{
			name: "404 is an HTTPError and IsNotFound", code: 404, reply: `{}`,
			check: func(t *testing.T, err error) {
				var httpErr *HTTPError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 || httpErr.Path != "/status" {
					t.Errorf("err = %v, want an *HTTPError with code 404", err)
				}
				if !IsNotFound(err) {
					t.Error("IsNotFound = false, want true")
				}
			},
		},
		{
			name: "500 is an HTTPError with the body", code: 500, reply: "boom\nsecond line",
			check: func(t *testing.T, err error) {
				var httpErr *HTTPError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != 500 || httpErr.Body != "boom" {
					t.Errorf("err = %#v, want an *HTTPError with code 500 and body \"boom\"", err)
				}
				if IsNotFound(err) {
					t.Error("IsNotFound = true for a 500")
				}
			},
		},
		{
			name: "bad JSON is a ProtocolError", code: 200, reply: `{"address": `,
			check: func(t *testing.T, err error) {
				var protoErr *ProtocolError
				if !errors.As(err, &protoErr) {
					t.Fatalf("err = %v, want a *ProtocolError", err)
				}
				// Unwrap gives the JSON error underneath.
				var syntaxErr *json.SyntaxError
				if !errors.As(err, &syntaxErr) && !strings.Contains(err.Error(), "unexpected end") {
					t.Errorf("err = %v, want the JSON error inside", err)
				}
			},
		},
		{
			name: "wrong JSON type is a ProtocolError", code: 200, reply: `["not", "an", "object"]`,
			check: func(t *testing.T, err error) {
				var protoErr *ProtocolError
				if !errors.As(err, &protoErr) {
					t.Errorf("err = %v, want a *ProtocolError", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newRecordingServer(t, tc.code, tc.reply)
			_, err := c.Status(ctx)
			if err == nil {
				t.Fatal("Status succeeded, want an error")
			}
			tc.check(t, err)
		})
	}
}

func TestClientServiceDown(t *testing.T) {
	addr := closedAddr(t)

	t.Run("connection refused is ErrServiceDown", func(t *testing.T) {
		c := New("http://"+addr, testToken)
		_, err := c.Health(context.Background())
		if !errors.Is(err, ErrServiceDown) {
			t.Fatalf("err = %v, want ErrServiceDown", err)
		}
		// The cause is kept too.
		if !errors.Is(err, syscall.ECONNREFUSED) {
			t.Errorf("err = %v, want it to wrap ECONNREFUSED", err)
		}
	})

	t.Run("not installed", func(t *testing.T) {
		setZeroTierPrograms(t, filepath.Join(t.TempDir(), "zerotier-one")) // does not exist
		c := newClient([]string{"http://" + addr}, testToken)
		c.checkInstalled = true
		_, err := c.Status(context.Background())
		if !errors.Is(err, ErrNotInstalled) {
			t.Fatalf("err = %v, want ErrNotInstalled", err)
		}
	})

	t.Run("installed but stopped", func(t *testing.T) {
		program := filepath.Join(t.TempDir(), "zerotier-one")
		if err := os.WriteFile(program, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		setZeroTierPrograms(t, program)
		c := newClient([]string{"http://" + addr}, testToken)
		c.checkInstalled = true
		_, err := c.Status(context.Background())
		if !errors.Is(err, ErrServiceDown) {
			t.Fatalf("err = %v, want ErrServiceDown", err)
		}
	})

	t.Run("connection closed without a reply is ErrServiceDown", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}))
		defer ts.Close()
		_, err := New(ts.URL, testToken).Status(context.Background())
		if !errors.Is(err, ErrServiceDown) {
			t.Fatalf("err = %v, want ErrServiceDown", err)
		}
	})
}

// setZeroTierPrograms replaces the list of places where zerotier-one may
// be installed, for one test.
func setZeroTierPrograms(t *testing.T, paths ...string) {
	t.Helper()
	old := zeroTierPrograms
	zeroTierPrograms = paths
	t.Cleanup(func() { zeroTierPrograms = old })
}

func TestClientTimeout(t *testing.T) {
	// release unblocks the handler when the test ends, so ts.Close doesn't
	// wait for it.
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer ts.Close()
	defer close(release) // deferred calls run last-in, first-out: this runs before ts.Close

	c := New(ts.URL, testToken)
	c.http.Timeout = 50 * time.Millisecond // don't make the test wait 2 seconds

	start := time.Now()
	_, err := c.Status(context.Background())
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("timed out after %v, want about 50ms", elapsed)
	}

	t.Run("context deadline is ErrTimeout too", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		c := New(ts.URL, testToken)
		if _, err := c.Status(ctx); !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
	})

	t.Run("cancelled context is passed on", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := New(ts.URL, testToken)
		_, err := c.Status(ctx)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrServiceDown) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestClientDefaultTimeout(t *testing.T) {
	c := New("http://127.0.0.1:9993", testToken)
	if c.http.Timeout != 2*time.Second {
		t.Errorf("timeout = %v, want 2s", c.http.Timeout)
	}
	transport, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.http.Transport)
	}
	if transport.Proxy != nil {
		t.Error("transport uses a proxy, want Proxy: nil")
	}
}

func TestClientWithoutToken(t *testing.T) {
	ctx := context.Background()
	c, rec := newRecordingServer(t, 200, `{"online":true}`)
	c.token = ""

	if c.HasToken() {
		t.Error("HasToken = true, want false")
	}
	// Health works without a token, and sends no auth header.
	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if rec.token != "" {
		t.Errorf("sent X-ZT1-Auth %q, want none", rec.token)
	}

	// Everything else fails at once, without a request.
	*rec = recorded{}
	if _, err := c.Status(ctx); !errors.Is(err, ErrTokenMissing) {
		t.Errorf("Status err = %v, want ErrTokenMissing", err)
	}
	if _, err := c.Join(ctx, "c0ffee1234000001"); !errors.Is(err, ErrTokenMissing) {
		t.Errorf("Join err = %v, want ErrTokenMissing", err)
	}
	if rec.method != "" {
		t.Errorf("a request was sent (%s %s), want none", rec.method, rec.path)
	}
}

func TestClientRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	c, rec := newRecordingServer(t, 200, `{}`)
	bad := NetworkID("../status")

	calls := map[string]func() error{
		"Network": func() error { _, err := c.Network(ctx, bad); return err },
		"Join":    func() error { _, err := c.Join(ctx, bad); return err },
		"Leave":   func() error { return c.Leave(ctx, bad) },
		"SetFlags": func() error {
			_, err := c.SetFlags(ctx, bad, map[Flag]bool{FlagAllowDNS: true})
			return err
		},
		"SetFlags with no flags": func() error {
			_, err := c.SetFlags(ctx, "c0ffee1234000001", map[Flag]bool{})
			return err
		},
		"SetFlags with an unknown flag": func() error {
			_, err := c.SetFlags(ctx, "c0ffee1234000001", map[Flag]bool{"allowEverything": true})
			return err
		},
		"SetFlags with a wrongly cased flag": func() error {
			_, err := c.SetFlags(ctx, "c0ffee1234000001", map[Flag]bool{"allowdns": true})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if rec.method != "" {
		t.Errorf("a request was sent (%s %s), want none", rec.method, rec.path)
	}
}

func TestClientFallsBackToIPv6(t *testing.T) {
	// Listen on [::1] only. Skip if this machine has no IPv6 loopback.
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"address":"a1b2c3d4e5"}`)
	}))
	_ = ts.Listener.Close()
	ts.Listener = l
	ts.Start()
	defer ts.Close()

	// Same port on both addresses, like APIURLs builds them: 127.0.0.1
	// refuses (nothing listens there), so the client tries [::1].
	_, port, _ := net.SplitHostPort(l.Addr().String())
	c := newClient([]string{
		"http://" + net.JoinHostPort("127.0.0.1", port),
		"http://" + net.JoinHostPort("::1", port),
	}, testToken)

	s, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if s.Address != "a1b2c3d4e5" {
		t.Errorf("address = %q", s.Address)
	}
}

func TestClientNoFallbackAfterReply(t *testing.T) {
	// The first address answers with an error; the second must not be
	// asked, because the service already got the request.
	first, _ := newRecordingServer(t, 500, `{}`)
	second, secondRec := newRecordingServer(t, 200, `{}`)
	c := newClient([]string{first.baseURLs[0], second.baseURLs[0]}, testToken)

	if _, err := c.Join(context.Background(), "c0ffee1234000001"); err == nil {
		t.Fatal("Join succeeded, want the first server's 500")
	}
	if secondRec.method != "" {
		t.Error("the second address got the request too")
	}
}

func TestDiscover(t *testing.T) {
	ctx := context.Background()
	setTestHome(t)

	t.Run("with token", func(t *testing.T) {
		c, rec := newRecordingServer(t, 200, `{"address":"a1b2c3d4e5"}`)
		t.Setenv(EnvAPIURL, c.BaseURL()+"/") // a trailing slash is fine
		t.Setenv(EnvTokenFile, writeTokenFile(t, t.TempDir(), "token", testToken+"\n", 0o600))

		d, err := Discover()
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if d.checkInstalled {
			t.Error("checkInstalled = true with an overridden URL")
		}
		if _, err := d.Status(ctx); err != nil {
			t.Fatalf("Status: %v", err)
		}
		if rec.path != "/status" || rec.token != testToken {
			t.Errorf("request = %s with token %q", rec.path, rec.token)
		}
	})

	t.Run("without token", func(t *testing.T) {
		c, _ := newRecordingServer(t, 200, `{"online":true}`)
		t.Setenv(EnvAPIURL, c.BaseURL())
		d, err := Discover()
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if d.HasToken() {
			t.Error("HasToken = true, want false")
		}
		if _, err := d.Health(ctx); err != nil {
			t.Errorf("Health: %v", err)
		}
		if _, err := d.Status(ctx); !errors.Is(err, ErrTokenMissing) {
			t.Errorf("Status err = %v, want ErrTokenMissing", err)
		}
	})

	t.Run("unsafe token file is an error", func(t *testing.T) {
		t.Setenv(EnvTokenFile, writeTokenFile(t, t.TempDir(), "token", testToken, 0o644))
		_, err := Discover()
		var insecure *InsecureTokenFileError
		if !errors.As(err, &insecure) {
			t.Fatalf("err = %v, want *InsecureTokenFileError", err)
		}
	})

	t.Run("default URLs", func(t *testing.T) {
		t.Setenv(EnvAPIURL, "")
		d, err := Discover()
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if !d.checkInstalled || len(d.baseURLs) != 2 {
			t.Errorf("checkInstalled = %v, baseURLs = %v; want true and two URLs", d.checkInstalled, d.baseURLs)
		}
	})
}
