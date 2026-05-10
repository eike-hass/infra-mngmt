//go:build linux

package compose

import (
	"os/exec"
	"syscall"
)

// detachLinuxCmd configures cmd so the child runs in a new session, detached
// from the parent's process group. Crucial for the bootstrap path where the
// caller is an HTTP handler — if the child shared our session, it would
// receive any SIGTERM/SIGINT delivered to infra-mngmt and die alongside us
// (or with the request context, depending on how the cmd was constructed).
//
// Setsid implies Setpgid + a fresh session ID + dropping the controlling
// terminal — the strongest decoupling available without going full daemon.
func detachLinuxCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
