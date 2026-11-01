package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// macOS: the browser through `open`, the clipboard through pbcopy.

// helperWait bounds open and pbcopy, which run while the picker waits.
const helperWait = 5 * time.Second

func openBrowser(u string) error {
	ctx, cancel := context.WithTimeout(context.Background(), helperWait)
	defer cancel()
	return exec.CommandContext(ctx, "open", u).Run()
}

func copyLocal(s string) error {
	ctx, cancel := context.WithTimeout(context.Background(), helperWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

// remoteSession: a browser opened here wouldn't be in front of you. On a
// Mac, that's over SSH.
func remoteSession() bool { return overSSH() }

// jobCmd: on macOS an app job starts as is, in a session of its own.
func jobCmd(self string, args []string) *exec.Cmd { return nil }
