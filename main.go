// herdr-github: your pull requests in a herdr popup, one key away from a
// worktree for any of them, and each space labelled with its branch's PR.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const usage = `herdr-github — GitHub pull requests in herdr

  action open      what the herdr action runs: opens the picker popup
  action demo      what the herdr action runs: the picker on a fictional org
  action labels    what the herdr action runs: refresh the sidebar labels now
  action pr        what the herdr action runs: the picker, on the pull request
                   of the agent you're in
  picker [--demo]  the popup itself; --demo (or HERDR_GITHUB_DEMO=1) shows a
                   fictional org: no account, no network, safe to screenshot
  tick [--force]   refresh the sidebar labels ($pr, $pr_badge, …); herdr runs
                   this on its events. --force asks GitHub again right away
  prs [--force] JSON
                   the pull request of each branch in JSON, a list of
                   {"remote", "branch", "head", "owner"}, as a JSON list on
                   stdout, in the same order. An answer younger than
                   label_refresh_seconds (a minute) is reused; --force asks
                   GitHub again right away
  app JSON         what the Herdr app's GitHub mod runs: one request (list,
                   detail, act, worktree, repos, sessions, status, job) in,
                   one JSON answer out; --demo's data with "demo": true
  status           which GitHub hosts gh is signed in to, as the plugin sees it
  handoff JSON     (internal) open the popup this one was opened from, once
                   this one has closed
  kickoff W P      (internal) wait for pane P's agent in workspace W, then
                   send it the prompt read from stdin
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-github:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cfg, cfgErr := loadConfig()
	if cfgErr != nil {
		switch {
		case args[0] == "tick":
			// herdr runs this on every event: say it once per tick in the
			// plugin log, and go on with the defaults.
			fmt.Fprintln(os.Stderr, "herdr-github: ignoring", cfgErr)
		case args[0] == "action":
			notify("GitHub", cfgErr.Error())
			return cfgErr
		default:
			return cfgErr
		}
	}

	switch args[0] {
	case "action":
		if len(args) < 2 {
			return errors.New("action needs a name")
		}
		return runAction(ctx, cfg, args[1])
	case "picker":
		demo := os.Getenv("HERDR_GITHUB_DEMO") == "1" || (len(args) > 1 && args[1] == "--demo")
		if err := runPicker(ctx, cfg, demo); err != nil {
			return err
		}
		return finishSwap()
	case "tick":
		force := len(args) > 1 && args[1] == "--force"
		if err := tick(ctx, cfg, force); err != nil && !isQuietTickErr(err) {
			return err
		}
		return nil
	case "handoff":
		if len(args) != 2 {
			return errors.New("handoff needs a popup")
		}
		return runHandoff(args[1])
	case "prs":
		return runPRs(ctx, cfg, args[1:], os.Stdout)
	case "app":
		return runApp(ctx, cfg, args[1:], os.Stdout)
	case "status":
		return status(ctx, cfg)
	case "kickoff":
		if len(args) != 3 {
			return errors.New("kickoff needs: workspace pane (and the prompt on stdin)")
		}
		text, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
		if err != nil || len(text) == 0 {
			return errors.New("kickoff: no prompt on stdin")
		}
		return kickoff(cfg, args[1], args[2], string(text))
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runAction(ctx context.Context, cfg config, name string) error {
	switch name {
	case "open":
		inv := invocationContext()
		cwd := inv.WorkspaceCwd
		if cwd == "" {
			cwd = inv.FocusedPaneCwd
		}
		if err := openPicker(map[string]string{"HERDR_GITHUB_CWD": cwd}); err != nil {
			notify("GitHub", "Couldn't open the picker: "+err.Error())
			return err
		}
		return nil
	case "pr":
		// Straight to the PR of the agent you're in. The picker works out
		// which, so that it can look again, and say so there, if there's none:
		// a toast can be anywhere but in front of you.
		inv := invocationContext()
		cwd := inv.WorkspaceCwd
		if cwd == "" {
			cwd = inv.FocusedPaneCwd
		}
		env := map[string]string{"HERDR_GITHUB_CWD": cwd, "HERDR_GITHUB_PANE": inv.FocusedPaneID, "HERDR_GITHUB_SPACE": inv.WorkspaceID}
		if err := openPicker(env); err != nil {
			notify("GitHub", "Couldn't open the pull request: "+err.Error())
			return err
		}
		return nil
	case "demo":
		if err := openPicker(map[string]string{"HERDR_GITHUB_DEMO": "1"}); err != nil {
			notify("GitHub", "Couldn't open the demo: "+err.Error())
			return err
		}
		return nil
	case "labels":
		err := tick(ctx, cfg, true)
		switch {
		case !cfg.labelsOn():
			notify("GitHub", "Labels are off (\"labels\": false in config.json), so they were cleared.")
		case errors.Is(err, errSignedOut):
			notify("GitHub", err.Error())
		case err != nil:
			notify("GitHub", "Couldn't refresh every label: "+err.Error())
		default:
			notify("GitHub", "PR labels refreshed.")
		}
		return nil
	}
	return fmt.Errorf("unknown action %q", name)
}

// status reports, per configured host, whether gh gives the plugin a token.
// It never prints the token.
func status(ctx context.Context, cfg config) error {
	for _, h := range cfg.Hosts {
		if _, err := tokenFn(ctx, h); err != nil {
			fmt.Printf("%s: %v\n", h, err)
			continue
		}
		var res struct {
			Viewer struct {
				Login string `json:"login"`
			} `json:"viewer"`
		}
		if err := newClient(h).graphql(ctx, `query { viewer { login } }`, nil, &res); err != nil {
			fmt.Printf("%s: gh has a token, but GitHub refused it: %v\n", h, err)
			continue
		}
		fmt.Printf("%s: signed in as %s (through gh)\n", h, res.Viewer.Login)
	}
	fmt.Println("Labels:", map[bool]string{true: "on", false: "off"}[cfg.labelsOn()], "· config:", strings.TrimSpace(configDir()+"/config.json"))
	return nil
}
