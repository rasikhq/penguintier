// Package instance keeps Penguintier to a single running copy by claiming
// the D-Bus name io.github.rasikhq.Penguintier on the session bus (with
// DoNotQueue).
//
// A D-Bus name has at most one owner. The first copy takes it; a second
// copy asks for it, is told that someone else has it, and quits. When the
// first copy exits (or crashes) the bus frees the name by itself, so there
// is no stale lock file to clean up.
package instance
