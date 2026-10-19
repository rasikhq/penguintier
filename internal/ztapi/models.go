package ztapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// About the structs in this file:
//
// The `json:"..."` text after each field is a "struct tag". encoding/json
// reads it to know which JSON key fills the field. Keys that have no field
// are skipped, so a newer ZeroTier that adds fields still decodes fine.
// A key that is missing from the JSON leaves the field at its "zero value":
// "" for strings, 0 for numbers, false for bools, nil for slices.
//
// Only the fields Penguintier uses (or that help with debugging) are listed.

// NetworkID is a ZeroTier network ID: exactly 16 lowercase hex characters,
// for example "8056c2e21c000001". The first 10 characters are the address of
// the network's controller.
//
// It is a separate type (not a plain string) so the compiler stops us from
// passing, say, a network name where an ID is expected. Make one from user
// input with ParseNetworkID.
type NetworkID string

// ParseNetworkID checks and normalizes a network ID typed or pasted by a
// user. Surrounding spaces are removed and upper-case hex is accepted, so
// " 8056C2E21C000001\n" becomes "8056c2e21c000001".
func ParseNetworkID(s string) (NetworkID, error) {
	id := NetworkID(strings.ToLower(strings.TrimSpace(s)))
	if !id.Valid() {
		return "", fmt.Errorf("invalid network ID %q: want 16 hex characters (0-9, a-f)", s)
	}
	return id, nil
}

// Valid reports whether id is exactly 16 lowercase hex characters.
//
// The client checks this before putting an ID into a URL, so a bad value
// such as "../status" can never change which endpoint is called.
func (id NetworkID) Valid() bool {
	if len(id) != 16 {
		return false
	}
	// Ranging over a string gives one character (a "rune") at a time.
	for _, r := range id {
		isDigit := r >= '0' && r <= '9'
		isHexLetter := r >= 'a' && r <= 'f'
		if !isDigit && !isHexLetter {
			return false
		}
	}
	return true
}

// String returns the ID as a plain string. Types with a String method print
// nicely with fmt.Println and %s / %v.
func (id NetworkID) String() string {
	return string(id)
}

// Status is a network's state as the service reports it.
type Status string

// The statuses ZeroTier One 1.16 sends. Anything else (a status added in a
// later version, or an empty string) is decoded as StatusUnknown.
const (
	StatusOK                      Status = "OK"
	StatusRequestingConfiguration Status = "REQUESTING_CONFIGURATION"
	StatusAccessDenied            Status = "ACCESS_DENIED"
	StatusNotFound                Status = "NOT_FOUND"
	StatusPortError               Status = "PORT_ERROR"
	StatusClientTooOld            Status = "CLIENT_TOO_OLD"
	StatusAuthenticationRequired  Status = "AUTHENTICATION_REQUIRED"
	StatusUnknown                 Status = "UNKNOWN"
)

// UnmarshalJSON is called by encoding/json whenever it decodes a Status.
// It turns any status we don't know into StatusUnknown, so code that uses
// a `switch` on the status can rely on seeing only the constants above.
//
// The receiver is a pointer (*Status) because the method has to change the
// value it is called on.
func (s *Status) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("network status: %w", err)
	}
	*s = parseStatus(raw)
	return nil
}

// parseStatus maps the service's status text to a Status.
func parseStatus(raw string) Status {
	switch Status(raw) {
	case StatusOK, StatusRequestingConfiguration, StatusAccessDenied, StatusNotFound,
		StatusPortError, StatusClientTooOld, StatusAuthenticationRequired:
		return Status(raw)
	default:
		return StatusUnknown
	}
}

// Health is the reply of GET /health. It is the only endpoint that needs no
// API token, so it tells "the service is running" apart from "we have no
// token yet".
type Health struct {
	Online       bool   `json:"online"`
	Version      string `json:"version"` // e.g. "1.16.2"
	VersionMajor int    `json:"versionMajor"`
	VersionMinor int    `json:"versionMinor"`
	VersionRev   int    `json:"versionRev"`
	VersionBuild int    `json:"versionBuild"`
	Clock        int64  `json:"clock"` // the service's clock, Unix milliseconds
}

// NodeStatus is the reply of GET /status: this machine's ZeroTier node.
type NodeStatus struct {
	// Address is this node's 10-hex-character ZeroTier address (shown as
	// "My Address" in the tray).
	Address           string `json:"address"`
	PublicIdentity    string `json:"publicIdentity"`
	Online            bool   `json:"online"`
	TCPFallbackActive bool   `json:"tcpFallbackActive"`

	Version      string `json:"version"`
	VersionMajor int    `json:"versionMajor"`
	VersionMinor int    `json:"versionMinor"`
	VersionRev   int    `json:"versionRev"`
	VersionBuild int    `json:"versionBuild"`

	Clock                int64  `json:"clock"` // Unix milliseconds
	PlanetWorldID        uint64 `json:"planetWorldId"`
	PlanetWorldTimestamp int64  `json:"planetWorldTimestamp"`

	Config NodeConfig `json:"config"`
}

// NodeConfig is the "config" object inside NodeStatus.
type NodeConfig struct {
	Settings NodeSettings `json:"settings"`
}

// NodeSettings is the service's local.conf settings, as /status reports
// them.
type NodeSettings struct {
	PrimaryPort           int      `json:"primaryPort"`
	SecondaryPort         int      `json:"secondaryPort"`
	TertiaryPort          int      `json:"tertiaryPort"`
	PortMappingEnabled    bool     `json:"portMappingEnabled"`
	AllowTCPFallbackRelay bool     `json:"allowTcpFallbackRelay"`
	ForceTCPRelay         bool     `json:"forceTcpRelay"`
	ListeningOn           []string `json:"listeningOn"`
	SurfaceAddresses      []string `json:"surfaceAddresses"`
	SoftwareUpdate        string   `json:"softwareUpdate"`
	HomeDir               string   `json:"homeDir"`
}

// Network is one joined network, from GET /network or GET /network/{id}.
type Network struct {
	ID     NetworkID `json:"id"`
	Name   string    `json:"name"` // "" until the controller has sent the config
	Status Status    `json:"status"`
	Type   string    `json:"type"` // "PRIVATE" or "PUBLIC" ("" while joining)

	MAC              string `json:"mac"` // e.g. "aa:bb:cc:dd:ee:ff"
	MTU              int    `json:"mtu"`
	PortDeviceName   string `json:"portDeviceName"` // the Linux interface, e.g. "ztabcdefgh"
	PortError        int    `json:"portError"`
	NetconfRevision  int    `json:"netconfRevision"`
	Bridge           bool   `json:"bridge"`
	BroadcastEnabled bool   `json:"broadcastEnabled"`
	DHCP             bool   `json:"dhcp"`

	// The four per-network settings the tray shows as check boxes.
	AllowManaged bool `json:"allowManaged"` // "Allow Managed Addresses"
	AllowGlobal  bool `json:"allowGlobal"`  // "Allow Global IPs"
	AllowDefault bool `json:"allowDefault"` // "Allow Default Route Override"
	AllowDNS     bool `json:"allowDNS"`     // "Allow DNS Configuration"

	// AssignedAddresses are this node's addresses on the network, with the
	// prefix length: "10.147.17.5/24", "fd80:56c2:e21c:...:1/88".
	AssignedAddresses []string `json:"assignedAddresses"`
	Routes            []Route  `json:"routes"`
	DNS               DNS      `json:"dns"`

	// Single sign-on. The service only sends these keys for networks that
	// use SSO, so for other networks they keep their zero values.
	SSOEnabled        bool   `json:"ssoEnabled"`
	AuthenticationURL string `json:"authenticationURL"`
	// AuthenticationExpiryTime is Unix milliseconds; use AuthExpiry.
	AuthenticationExpiryTime int64 `json:"authenticationExpiryTime"`
}

// Flag returns the current value of one of the four "Allow ..." settings.
// An unknown flag returns false.
func (n Network) Flag(f Flag) bool {
	switch f {
	case FlagAllowManaged:
		return n.AllowManaged
	case FlagAllowGlobal:
		return n.AllowGlobal
	case FlagAllowDefault:
		return n.AllowDefault
	case FlagAllowDNS:
		return n.AllowDNS
	default:
		return false
	}
}

// AuthExpiry returns when the network's SSO login expires. ok is false when
// the network has no expiry time.
//
// Go functions can return several values; "ok" as the last one is the usual
// way to say "there was nothing to return".
func (n Network) AuthExpiry() (expiry time.Time, ok bool) {
	if n.AuthenticationExpiryTime <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(n.AuthenticationExpiryTime), true
}

// Route is one entry of a network's routing table.
type Route struct {
	Target string `json:"target"` // e.g. "10.147.17.0/24"
	// Via is the gateway IP. The service sends `"via": null` for routes
	// without a gateway; decoding null into a Go string leaves it "", so
	// "" means "no gateway".
	Via    string `json:"via"`
	Flags  int    `json:"flags"`
	Metric int    `json:"metric"`
}

// DNS is the DNS configuration a network's controller pushes.
type DNS struct {
	Domain  string   `json:"domain"`  // e.g. "home.arpa"; "" if none
	Servers []string `json:"servers"` // IP addresses
}

// Peer is one entry of GET /peer: another ZeroTier node this one talks to.
type Peer struct {
	Address      string     `json:"address"` // 10 hex characters
	Role         string     `json:"role"`    // "LEAF", "MOON" or "PLANET"
	Latency      int        `json:"latency"` // milliseconds, -1 if unknown
	Version      string     `json:"version"` // "-1.-1.-1" if unknown
	VersionMajor int        `json:"versionMajor"`
	VersionMinor int        `json:"versionMinor"`
	VersionRev   int        `json:"versionRev"`
	IsBonded     bool       `json:"isBonded"`
	Tunneled     bool       `json:"tunneled"`
	Paths        []PeerPath `json:"paths"`
}

// PeerPath is one physical path (IP address and port) to a peer.
type PeerPath struct {
	Address       string `json:"address"` // "ip/port", e.g. "192.0.2.10/9993"
	Active        bool   `json:"active"`
	Expired       bool   `json:"expired"`
	Preferred     bool   `json:"preferred"`
	LastSend      int64  `json:"lastSend"`    // Unix milliseconds
	LastReceive   int64  `json:"lastReceive"` // Unix milliseconds
	TrustedPathID uint64 `json:"trustedPathId"`
}

// Flag names one of the four per-network "Allow ..." settings. The values
// are the service's JSON keys, so a map[Flag]bool encodes straight into a
// request body such as {"allowDNS":true}.
type Flag string

// The four per-network settings.
const (
	FlagAllowManaged Flag = "allowManaged"
	FlagAllowGlobal  Flag = "allowGlobal"
	FlagAllowDefault Flag = "allowDefault"
	FlagAllowDNS     Flag = "allowDNS"
)

// AllFlags lists the four settings in the order the tray menu shows them.
var AllFlags = []Flag{FlagAllowManaged, FlagAllowGlobal, FlagAllowDefault, FlagAllowDNS}

// Valid reports whether f is exactly one of the four flag names. The
// service ignores keys it doesn't know (such as "allowdns"), so the client
// checks this before sending.
func (f Flag) Valid() bool {
	for _, known := range AllFlags {
		if f == known {
			return true
		}
	}
	return false
}

// ParseFlag turns a flag name such as "allowDNS" into a Flag. The match
// ignores case, so "allowdns" works too.
func ParseFlag(s string) (Flag, error) {
	for _, f := range AllFlags {
		if strings.EqualFold(s, string(f)) {
			return f, nil
		}
	}
	return "", fmt.Errorf("unknown flag %q: want one of allowManaged, allowGlobal, allowDefault, allowDNS", s)
}
