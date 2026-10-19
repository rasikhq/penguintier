// Package doctor collects the diagnostic report printed by
// `penguintier doctor [--json]`: the ZeroTier service and token, the tray
// host, notifications, clipboard, polkit and DNS, for bug reports and
// for users fixing their own setup.
//
// It has three parts:
//
//   - facts.go: Gather reads everything (files, HTTP, D-Bus) into Facts,
//     plain data.
//   - checks.go: Evaluate turns Facts into a Report. It does no I/O, so
//     table tests cover every verdict.
//   - report.go: the Report, written as text or as JSON.
//
// The JSON form is meant for scripts and bug reports, so its shape is
// stable: the check IDs and fields only ever get added to.
package doctor
