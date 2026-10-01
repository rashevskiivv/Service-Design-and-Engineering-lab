//go:build unix

package main

import "syscall"

// setUmask makes every file the process creates owner-only.
func setUmask() { syscall.Umask(0o077) }
