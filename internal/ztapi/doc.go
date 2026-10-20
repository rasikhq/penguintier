// Package ztapi talks to the ZeroTier One service's local HTTP API
// (http://127.0.0.1:9993 by default).
//
// What is in here:
//
//   - models.go: the JSON shapes the service sends (NodeStatus, Network,
//     Peer, ...), the NetworkID type and the network Status type.
//   - errors.go: the errors callers can check with errors.Is / errors.As.
//   - discovery.go: where the API listens (port file, 127.0.0.1 then [::1])
//     and where the API token is (see "ZeroTier API token" in
//     docs/architecture.md).
//   - token.go: the token format, and saving a copy to
//     ~/.zeroTierOneAuthToken.
//   - client.go: the HTTP client (Health, Status, Networks, Join, ...).
//   - saved.go: the saved-networks store, and the one-time import from
//     ZeroTier's official desktop app.
//
// The ztmock subpackage is a fake service for tests and for cmd/zt-mock.
//
// A quick example:
//
//	c, err := ztapi.Discover()
//	if err != nil {
//		return err
//	}
//	nets, err := c.Networks(ctx)
//	if errors.Is(err, ztapi.ErrServiceDown) {
//		// show "Waiting for ZeroTier system service…"
//	}
package ztapi
