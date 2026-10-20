// Package ztmock is a fake ZeroTier One local API, for tests and for the
// zt-mock dev command.
//
// A Server is an http.Handler (anything with a ServeHTTP method), so tests
// can run it with net/http/httptest:
//
//	srv, _ := ztmock.New(ztmock.Online)
//	ts := httptest.NewServer(srv)
//	defer ts.Close()
//	client := ztapi.New(ts.URL, ztmock.Token)
//
// Each scenario starts from a fixed set of networks. Join, leave and flag
// changes are kept in memory. A newly joined network first shows as
// REQUESTING_CONFIGURATION and gets its configuration a moment later, like
// the real service.
package ztmock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// Token is the only API token the fake service accepts (24 characters,
// a-z and 0-9, like a real one).
const Token = "ztmockztmockztmockztmock" //nolint:gosec // a fake token for the fake service

// NodeAddress is the fake node's ZeroTier address ("My Address").
const NodeAddress = "a1b2c3d4e5"

// The scenario names, as `zt-mock --scenario` takes them.
const (
	// Online: the service runs, two healthy networks.
	Online = "online"
	// ServiceDown: every connection is closed without a reply, as if the
	// service had stopped. The client reports ztapi.ErrServiceDown.
	ServiceDown = "service-down"
	// Unauthorized: every token is rejected with HTTP 401.
	Unauthorized = "unauthorized"
	// SSORequired: one network waits for an SSO login, one has a login that
	// expires in 8 hours.
	SSORequired = "sso-required"
	// AccessDenied: one network the controller has not authorized us for.
	AccessDenied = "access-denied"
	// ManyNetworks: twelve networks with every status, long and odd names,
	// IPv6 and many routes, to stress the menu.
	ManyNetworks = "many-networks"
	// Joining: one network still waiting for its configuration. A network
	// joined in this scenario stays out of the network list for 5 seconds
	// (so the tray's "Joining…" row can be seen), then waits 10 seconds for
	// its configuration.
	Joining = "joining"
)

// Scenarios lists every scenario name.
var Scenarios = []string{Online, ServiceDown, Unauthorized, SSORequired, AccessDenied, ManyNetworks, Joining}

// Server is the fake service. Create it with New.
type Server struct {
	// Now returns the current time. It is time.Now unless a test replaces it
	// (before serving any request) to get a fixed clock.
	Now func() time.Time

	mux *http.ServeMux

	// mu guards every field below it. HTTP handlers run on many goroutines
	// at once, so they must take the lock before touching this state. It is
	// held only briefly, never while writing a reply.
	mu       sync.Mutex
	scenario string
	networks map[ztapi.NetworkID]*mockNetwork
}

// mockNetwork is a network plus the bookkeeping for a join in progress.
type mockNetwork struct {
	ztapi.Network
	// joinedAt is when it was joined through the API; zero for the
	// scenario's starting networks, which never change on their own.
	joinedAt   time.Time
	configured bool
}

// joinTiming says how a network joined through the API develops: hidden
// from the list for hiddenFor, then REQUESTING_CONFIGURATION until
// configuredAfter, then configured.
type joinTiming struct {
	hiddenFor       time.Duration
	configuredAfter time.Duration
}

// New returns a fake service running the given scenario.
func New(scenario string) (*Server, error) {
	s := &Server{Now: time.Now}

	// Go 1.22+ ServeMux patterns can name the method and capture parts of
	// the path: in "GET /network/{id}", r.PathValue("id") gives the ID.
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /status", s.handleStatus)
	s.mux.HandleFunc("GET /network", s.handleNetworks)
	s.mux.HandleFunc("GET /network/{id}", s.handleNetwork)
	s.mux.HandleFunc("POST /network/{id}", s.handleJoinOrSet)
	s.mux.HandleFunc("DELETE /network/{id}", s.handleLeave)

	if err := s.SetScenario(scenario); err != nil {
		return nil, err
	}
	return s, nil
}

// SetScenario switches to another scenario and resets the networks to its
// starting set.
func (s *Server) SetScenario(scenario string) error {
	nets, err := scenarioNetworks(scenario)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock() // runs when SetScenario returns
	s.scenario = scenario
	s.networks = make(map[ztapi.NetworkID]*mockNetwork, len(nets))
	for _, n := range nets {
		s.networks[n.ID] = &mockNetwork{Network: n, configured: true}
	}
	return nil
}

// Scenario returns the current scenario's name.
func (s *Server) Scenario() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scenario
}

// ServeHTTP answers one request. It handles the scenarios that affect every
// endpoint, then passes the request on to the route handlers.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	scenario := s.Scenario()

	if scenario == ServiceDown {
		// Take over the raw connection and close it without a reply: the
		// client sees the connection drop, as when the service stops.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close() // nothing useful to do if closing fails
				return
			}
		}
		// The ResponseWriter can't be hijacked (e.g. HTTP/2): best effort.
		http.Error(w, "service down", http.StatusServiceUnavailable)
		return
	}

	// Like the real service, /health needs no token; everything else does.
	if r.URL.Path != "/health" {
		if scenario == Unauthorized || r.Header.Get("X-ZT1-Auth") != Token {
			writeJSON(w, http.StatusUnauthorized, struct{}{})
			return
		}
	}

	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, ztapi.Health{
		Online: true, Version: "1.16.2", VersionMajor: 1, VersionMinor: 16, VersionRev: 2,
		Clock: s.Now().UnixMilli(),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, ztapi.NodeStatus{
		Address:        NodeAddress,
		PublicIdentity: NodeAddress + ":0:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Online:         true,
		Version:        "1.16.2", VersionMajor: 1, VersionMinor: 16, VersionRev: 2,
		Clock:                s.Now().UnixMilli(),
		PlanetWorldID:        149604618,
		PlanetWorldTimestamp: 1738848951118,
		Config: ztapi.NodeConfig{Settings: ztapi.NodeSettings{
			PrimaryPort:           9993,
			SecondaryPort:         40001,
			TertiaryPort:          40002,
			PortMappingEnabled:    true,
			AllowTCPFallbackRelay: true,
			ListeningOn:           []string{"192.0.2.10/9993", "2001:db8::10/9993"},
			SurfaceAddresses:      []string{"198.51.100.7/9993"},
			SoftwareUpdate:        "disable",
			HomeDir:               "/var/lib/zerotier-one",
		}},
	})
}

func (s *Server) handleNetworks(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.advanceJoins()
	list := make([]ztapi.Network, 0, len(s.networks))
	for _, n := range s.networks {
		if !s.hidden(n) {
			list = append(list, s.withExpiry(n.Network))
		}
	}
	s.mu.Unlock()

	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	id := ztapi.NetworkID(r.PathValue("id"))
	s.mu.Lock()
	s.advanceJoins()
	n, ok := s.networks[id]
	var reply ztapi.Network
	if ok && !s.hidden(n) {
		reply = s.withExpiry(n.Network)
	} else {
		ok = false
	}
	s.mu.Unlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, struct{}{})
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// handleJoinOrSet is POST /network/{id}. Like the real service it joins the
// network if needed, then applies any allow* settings in the body.
func (s *Server) handleJoinOrSet(w http.ResponseWriter, r *http.Request) {
	id, err := ztapi.ParseNetworkID(r.PathValue("id"))
	if err != nil || string(id) != r.PathValue("id") {
		writeJSON(w, http.StatusBadRequest, struct{}{})
		return
	}

	// The real service ignores a body that is not valid JSON, so a decode
	// error is ignored here too. Values are "any" because the body may hold
	// other keys with other types.
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	s.mu.Lock()
	n, ok := s.networks[id]
	if !ok {
		n = newJoinedNetwork(id, s.Now())
		s.networks[id] = n
	}
	for key, value := range body {
		b, isBool := value.(bool) // type assertion: is this value a bool?
		if !isBool {
			continue
		}
		switch ztapi.Flag(key) {
		case ztapi.FlagAllowManaged:
			n.AllowManaged = b
		case ztapi.FlagAllowGlobal:
			n.AllowGlobal = b
		case ztapi.FlagAllowDefault:
			n.AllowDefault = b
		case ztapi.FlagAllowDNS:
			n.AllowDNS = b
		}
	}
	reply := s.withExpiry(n.Network)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	id := ztapi.NetworkID(r.PathValue("id"))
	s.mu.Lock()
	_, ok := s.networks[id]
	delete(s.networks, id)
	s.mu.Unlock()

	// Like ZeroTier 1.16: 200 either way, but only a real leave says
	// "result": true.
	if !ok {
		writeJSON(w, http.StatusOK, struct{}{})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"result": true})
}

// timing returns how joins develop in the current scenario. Call it with
// s.mu held.
func (s *Server) timing() joinTiming {
	if s.scenario == Joining {
		return joinTiming{hiddenFor: 5 * time.Second, configuredAfter: 15 * time.Second}
	}
	return joinTiming{hiddenFor: 0, configuredAfter: 2 * time.Second}
}

// hidden reports whether a just-joined network should still be missing
// from the list. Call it with s.mu held.
func (s *Server) hidden(n *mockNetwork) bool {
	if n.joinedAt.IsZero() {
		return false
	}
	return s.Now().Sub(n.joinedAt) < s.timing().hiddenFor
}

// advanceJoins gives networks joined through the API their configuration
// once their time has come. Call it with s.mu held.
func (s *Server) advanceJoins() {
	for _, n := range s.networks {
		if n.configured || s.Now().Sub(n.joinedAt) < s.timing().configuredAfter {
			continue
		}
		n.configured = true
		configure(&n.Network, s.scenario)
	}
}

// withExpiry fills in the SSO expiry time, which is always 8 hours from
// now, so it never runs out while the mock runs. Call it with s.mu held.
func (s *Server) withExpiry(n ztapi.Network) ztapi.Network {
	if n.SSOEnabled && n.Status == ztapi.StatusOK {
		n.AuthenticationExpiryTime = s.Now().Add(8 * time.Hour).UnixMilli()
	}
	return n
}

// writeJSON sends v as a JSON reply with the given HTTP status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// The status line is already sent, so an encoding error can't be
	// reported to the client any more; the client sees a broken body.
	_ = json.NewEncoder(w).Encode(v)
}

// errUnknownScenario builds the error for a scenario name we don't know.
func errUnknownScenario(name string) error {
	return fmt.Errorf("unknown scenario %q (known: %v)", name, Scenarios)
}
