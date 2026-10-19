package ztapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// DefaultTimeout is how long one request may take. The service is on this
// machine, so a healthy one replies in milliseconds.
const DefaultTimeout = 2 * time.Second

// maxBodySize caps how much of a reply we read (4 MiB). A /peer reply on a
// busy node is a few hundred KiB.
const maxBodySize = 4 << 20

// Client talks to one ZeroTier One service. Create it with Discover or New.
//
// A Client has no state that changes after it is created, so it is safe to
// use from several goroutines at once (net/http's client is too).
type Client struct {
	// baseURLs are tried in order, but only when connecting fails: a
	// service that answered (even with an error) is never asked twice.
	baseURLs []string
	token    string // "" means "no token": only Health works
	http     *http.Client
	// checkInstalled turns on the ErrNotInstalled check. It is off when the
	// URL was given explicitly (zt-mock, tests), where "is the zerotier-one
	// program installed?" has nothing to do with the server we talk to.
	checkInstalled bool
}

// Discover returns a Client set up the standard way: the API address from
// APIURLs (127.0.0.1 then [::1], or $PENGUINTIER_API_URL) and the token
// from FindToken.
//
// A missing token is not an error here. The client still works for Health,
// and every other call returns ErrTokenMissing, so the tray can tell "not
// authorized yet" from "service down". Other token problems (an unsafe or
// malformed file) are returned as errors.
func Discover() (*Client, error) {
	urls, err := APIURLs()
	if err != nil {
		return nil, err
	}
	token, _, err := FindToken()
	if err != nil && !errors.Is(err, ErrTokenMissing) {
		return nil, err
	}
	c := newClient(urls, token)
	// Only check for an installed zerotier-one when talking to the real
	// service, not to a URL from $PENGUINTIER_API_URL.
	c.checkInstalled = os.Getenv(EnvAPIURL) == ""
	return c, nil
}

// New returns a Client for the API at baseURL (e.g.
// "http://127.0.0.1:9993") using token. Pass "" as the token to call only
// Health. Use it in tests, with zt-mock, or when the token comes from
// somewhere else (penguintier-dnsd gets it from systemd).
func New(baseURL, token string) *Client {
	return newClient([]string{strings.TrimRight(baseURL, "/")}, token)
}

func newClient(baseURLs []string, token string) *Client {
	transport := &http.Transport{
		// Never send requests to the local service through an HTTP proxy
		// from $http_proxy and friends. (nil means "no proxy".)
		Proxy:       nil,
		DialContext: (&net.Dialer{Timeout: DefaultTimeout}).DialContext,
		// The service closes idle connections after 5 seconds. Closing ours
		// a bit earlier avoids reusing a connection the service is just
		// closing, which would fail the request.
		IdleConnTimeout: 4 * time.Second,
		MaxIdleConns:    2,
	}
	return &Client{
		baseURLs: baseURLs,
		token:    token,
		http:     &http.Client{Transport: transport, Timeout: DefaultTimeout},
	}
}

// HasToken reports whether the client has an API token. Without one, every
// method except Health returns ErrTokenMissing.
func (c *Client) HasToken() bool {
	return c.token != ""
}

// BaseURL returns the first address the client tries, for logs and
// "penguintier doctor".
func (c *Client) BaseURL() string {
	return c.baseURLs[0]
}

// About context.Context, the first argument of every method below: it lets
// the caller cancel a request or give it a deadline. Pass
// context.Background() when you have nothing better. Requests also time
// out on their own after DefaultTimeout.

// Health calls GET /health. It needs no token, so use it to check that the
// service is running.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.do(ctx, http.MethodGet, "/health", nil, &h, false)
	return h, err
}

// Status calls GET /status: this node's address, version and settings.
func (c *Client) Status(ctx context.Context) (NodeStatus, error) {
	var s NodeStatus
	err := c.do(ctx, http.MethodGet, "/status", nil, &s, true)
	return s, err
}

// Networks calls GET /network: every joined network, sorted by ID.
func (c *Client) Networks(ctx context.Context) ([]Network, error) {
	var nets []Network
	if err := c.do(ctx, http.MethodGet, "/network", nil, &nets, true); err != nil {
		return nil, err
	}
	// sort.Slice sorts in place; the function says whether element i
	// belongs before element j.
	sort.Slice(nets, func(i, j int) bool { return nets[i].ID < nets[j].ID })
	return nets, nil
}

// Network calls GET /network/{id}. If the network is not joined the error
// matches IsNotFound.
func (c *Client) Network(ctx context.Context, id NetworkID) (Network, error) {
	var n Network
	if !id.Valid() {
		return n, fmt.Errorf("get network: invalid network ID %q", id)
	}
	err := c.do(ctx, http.MethodGet, "/network/"+id.String(), nil, &n, true)
	return n, err
}

// Join calls POST /network/{id} to join a network. Joining a network that
// is already joined does nothing. The returned network usually has status
// REQUESTING_CONFIGURATION and no name yet.
func (c *Client) Join(ctx context.Context, id NetworkID) (Network, error) {
	var n Network
	if !id.Valid() {
		return n, fmt.Errorf("join network: invalid network ID %q", id)
	}
	// The service wants a JSON body; an empty object means "no settings".
	err := c.do(ctx, http.MethodPost, "/network/"+id.String(), map[string]any{}, &n, true)
	return n, err
}

// Leave calls DELETE /network/{id} to leave a network. Leaving a network
// that is not joined is not an error: ZeroTier 1.16 answers 200 with {}
// then (and {"result":true} after a real leave).
func (c *Client) Leave(ctx context.Context, id NetworkID) error {
	if !id.Valid() {
		return fmt.Errorf("leave network: invalid network ID %q", id)
	}
	// A 2xx status already says it worked, so the body is not decoded
	// (out is nil).
	return c.do(ctx, http.MethodDelete, "/network/"+id.String(), nil, nil, true)
}

// SetFlags changes some of a network's four "Allow ..." settings and
// returns the updated network. Settings not in the map stay as they are:
//
//	c.SetFlags(ctx, id, map[ztapi.Flag]bool{ztapi.FlagAllowDNS: true})
//
// Watch out: this is a POST /network/{id}, the same call as Join. If the
// network is not joined, the service joins it with these settings. That is
// how "Reconnect" for a saved network works.
func (c *Client) SetFlags(ctx context.Context, id NetworkID, flags map[Flag]bool) (Network, error) {
	var n Network
	if !id.Valid() {
		return n, fmt.Errorf("set flags: invalid network ID %q", id)
	}
	// An empty map would be sent as {}, which is a plain Join. Refuse it so
	// a bug can't join a network by accident.
	if len(flags) == 0 {
		return n, errors.New("set flags: no flags given")
	}
	for f := range flags {
		if !f.Valid() {
			return n, fmt.Errorf("set flags: unknown flag %q (use the Flag constants or ParseFlag)", f)
		}
	}
	err := c.do(ctx, http.MethodPost, "/network/"+id.String(), flags, &n, true)
	return n, err
}

// Peers calls GET /peer: the other ZeroTier nodes this node talks to.
func (c *Client) Peers(ctx context.Context) ([]Peer, error) {
	var peers []Peer
	err := c.do(ctx, http.MethodGet, "/peer", nil, &peers, true)
	return peers, err
}

// do sends one request and decodes the JSON reply into out (a pointer), or
// ignores the reply body when out is nil. body, if not nil, is sent as
// JSON. needAuth says whether the endpoint needs the token.
//
// It turns every failure into one of the errors in errors.go.
func (c *Client) do(ctx context.Context, method, path string, body, out any, needAuth bool) error {
	if needAuth && c.token == "" {
		return fmt.Errorf("%s %s: %w", method, path, ErrTokenMissing)
	}

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s %s: encode request: %w", method, path, err)
		}
	}

	// Try each base URL until one accepts the connection.
	var resp *http.Response
	var lastErr error
	for _, base := range c.baseURLs {
		// The body reader is made fresh for every try: a reader can only
		// be read once.
		var bodyReader io.Reader
		if payload != nil {
			bodyReader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, bodyReader)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.token != "" {
			req.Header.Set("X-ZT1-Auth", c.token)
		}

		resp, err = c.http.Do(req)
		if err == nil {
			break // connected and got a reply
		}
		lastErr = err
		if !isDialError(err) {
			// It connected but then failed (timeout, connection reset,
			// ...). The request may have reached the service, so don't
			// send it to another address.
			break
		}
	}
	if resp == nil {
		return c.transportError(method, path, lastErr)
	}
	// Always close the body, or the connection can't be reused.
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return c.transportError(method, path, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s %s: %w", method, path, ErrUnauthorized)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &HTTPError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: firstLine(data)}
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &ProtocolError{Method: method, Path: path, Err: err}
	}
	return nil
}

// transportError maps an error from sending a request (no HTTP reply) to
// one of our errors.
func (c *Client) transportError(method, path string, err error) error {
	// The caller cancelled: pass that on unchanged (still wrapped, so
	// errors.Is(err, context.Canceled) works).
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}

	// A net.Error is an interface; errors.As finds one anywhere in the
	// chain of wrapped errors and stores it in netErr.
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%s %s: %w", method, path, ErrTimeout)
	}

	// Nothing answered. Two %w verbs keep both errors, so errors.Is works
	// for ErrServiceDown and for the underlying cause (e.g.
	// syscall.ECONNREFUSED).
	if c.checkInstalled && !zeroTierInstalled() {
		return fmt.Errorf("%s %s: %w (%w)", method, path, ErrNotInstalled, err)
	}
	return fmt.Errorf("%s %s: %w (%w)", method, path, ErrServiceDown, err)
}

// isDialError reports whether err happened while connecting, before
// anything was sent. Only then is it safe to try the next address.
func isDialError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// firstLine returns the first line of a reply body, at most 200 bytes, for
// error messages.
func firstLine(data []byte) string {
	s := string(data)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
