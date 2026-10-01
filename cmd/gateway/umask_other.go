//go:build !unix

package main

// setUmask is a no-op where there is no umask; store.Open still creates the
// database file with mode 0600.
func setUmask() {}
