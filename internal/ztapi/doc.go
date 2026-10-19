// Package ztapi talks to the ZeroTier One service's local HTTP API
// (http://127.0.0.1:9993 by default).
//
// It will hold the API models (Status, Network, NetworkID, ...), token and
// port discovery, a small net/http client with typed errors, and the
// saved-networks store.
//
// This is a P0 stub with no code yet. Phase P1 owns this package.
// See docs/architecture.md.
package ztapi
