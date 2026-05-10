//go:build !linux

package compose

import "os/exec"

// detachLinuxCmd is a no-op on non-Linux platforms. infra-mngmt only ships
// for Linux/WSL, but keeping the build green on darwin/macOS for local
// developer iteration costs nothing.
func detachLinuxCmd(_ *exec.Cmd) {}
