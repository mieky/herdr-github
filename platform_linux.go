package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Linux: the browser through `xdg-open`, the clipboard through whichever of
// wl-copy, xclip and xsel is there.

// helperWait bounds a clipboard helper, which runs while the picker waits.
const helperWait = 5 * time.Second

// browserGrace is how long openBrowser waits for xdg-open to fail.
var browserGrace = 2 * time.Second

// openBrowser runs xdg-open without waiting for it to end: with some
// desktops it lasts as long as the browser does. A failure within
// browserGrace ("no browser", say) is reported; one still running then is
// taken as the browser opening.
func openBrowser(u string) error {
	cmd := exec.Command("xdg-open", u)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("xdg-open: %w", err)
		}
		return nil
	case <-time.After(browserGrace):
		return nil
	}
}

func copyLocal(s string) error {
	var tries [][]string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		tries = append(tries, []string{"wl-copy"})
	}
	tries = append(tries, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	for _, c := range tries {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), helperWait)
		defer cancel()
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		return cmd.Run()
	}
	return errors.New("no clipboard tool: install wl-clipboard (Wayland), xclip or xsel")
}

// remoteSession: a browser can't be opened where you are: over SSH, or with
// no display to open one on (where xdg-open would fall back to a text
// browser, in the terminal the popup is using, or hang). WSL opens the
// Windows browser, display or not.
func remoteSession() bool {
	if overSSH() {
		return true
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return false
	}
	return os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
}

// jobCmd runs an app job as a systemd unit of its own where systemd-run is:
// the app runs this binary through herdr-core, a service that ends whatever
// a command leaves behind when it returns, a new session included. Nil
// elsewhere: started as is.
func jobCmd(self string, args []string) *exec.Cmd {
	run, err := exec.LookPath("systemd-run")
	if err != nil || os.Getenv("HERDR_GITHUB_NO_SYSTEMD") != "" {
		return nil
	}
	argv := []string{"--user", "--collect", "--quiet"}
	for _, kv := range os.Environ() {
		if !strings.ContainsAny(kv, "\n") {
			argv = append(argv, "--setenv="+kv)
		}
	}
	if dir, err := os.Getwd(); err == nil {
		argv = append(argv, "--working-directory="+dir)
	}
	argv = append(argv, "/bin/sh", "-c", `exec "$0" "$@" >>"${HERDR_GITHUB_JOB_LOG:-/dev/null}" 2>&1`, self)
	return exec.Command(run, append(argv, args...)...)
}
