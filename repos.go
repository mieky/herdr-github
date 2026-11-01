package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// repoRef names a GitHub repository.
type repoRef struct {
	Host, Owner, Name string
}

func (r repoRef) String() string { return r.Owner + "/" + r.Name }
func (r repoRef) key() string    { return strings.ToLower(r.Host + "/" + r.Owner + "/" + r.Name) }
func (r repoRef) valid() bool    { return r.Host != "" && r.Owner != "" && r.Name != "" }

func (r repoRef) same(o repoRef) bool { return r.valid() && r.key() == o.key() }

// parseRemote reads a git remote URL: https://host/owner/repo(.git),
// git@host:owner/repo(.git), ssh://git@host(:port)/owner/repo(.git), git://…
// An SSH host alias from ~/.ssh/config isn't resolved; map such a repo under
// "repos" in the config instead.
func parseRemote(raw string) (repoRef, bool) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return repoRef{}, false
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(raw, ":"):
		// scp-like: [user@]host:owner/repo
		h, p, _ := strings.Cut(raw, ":")
		if i := strings.LastIndex(h, "@"); i >= 0 {
			h = h[i+1:]
		}
		host, path = h, p
	default:
		return repoRef{}, false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return repoRef{}, false
	}
	return repoRef{Host: strings.ToLower(host), Owner: owner, Name: name}, true
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// remote is one git remote and the GitHub repo it points at.
type remote struct {
	Name string
	URL  string
	Repo repoRef
}

// remoteCache remembers each directory's remotes for the life of the process.
var remoteCache sync.Map // dir → []remote

func remotesOf(ctx context.Context, dir string) []remote {
	if v, ok := remoteCache.Load(dir); ok {
		return v.([]remote)
	}
	// The URLs as configured, before any url.*.insteadOf rewriting: that's
	// the name you gave the repo, and what a rewrite points elsewhere.
	out, err := git(ctx, dir, "config", "--get-regexp", `^remote\..*\.url$`)
	var rs []remote
	if err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(out, "\n") {
			key, u, ok := strings.Cut(line, " ")
			name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
			if !ok || seen[name] {
				continue
			}
			seen[name] = true
			r, _ := parseRemote(u)
			rs = append(rs, remote{Name: name, URL: u, Repo: r})
		}
	}
	remoteCache.Store(dir, rs)
	return rs
}

// repoOf is the GitHub repo a checkout is for: its upstream remote's (the
// usual name for the original of a fork), else origin's, else its only one.
func repoOf(ctx context.Context, dir string) (repoRef, bool) {
	rs := remotesOf(ctx, dir)
	for _, want := range []string{"upstream", "origin"} {
		for _, r := range rs {
			if r.Name == want && r.Repo.valid() {
				return r.Repo, true
			}
		}
	}
	if len(rs) == 1 && rs[0].Repo.valid() {
		return rs[0].Repo, true
	}
	return repoRef{}, false
}

// remoteFor is the remote in dir that points at repo r, if any.
func remoteFor(ctx context.Context, dir string, r repoRef) (remote, bool) {
	var found []remote
	for _, x := range remotesOf(ctx, dir) {
		if x.Repo.same(r) {
			found = append(found, x)
		}
	}
	for _, x := range found {
		if x.Name == "upstream" || x.Name == "origin" {
			return x, true
		}
	}
	if len(found) > 0 {
		return found[0], true
	}
	return remote{}, false
}

// remoteForCheckout is remoteFor for a checkout already chosen (mapped in
// the config, say), where a remote may name the host by an SSH alias
// ("git@work-github:o/r"): then owner and name are enough. Not for a remote
// on another GitHub you use: o/r there is a different repo.
func remoteForCheckout(ctx context.Context, cfg config, dir string, r repoRef) (remote, bool) {
	if x, ok := remoteFor(ctx, dir, r); ok {
		return x, true
	}
	var found []remote
	for _, x := range remotesOf(ctx, dir) {
		if cfg.knownHost(x.Repo.Host) || x.Repo.Host == "github.com" {
			continue // a real GitHub host, and not r's
		}
		if strings.EqualFold(x.Repo.Owner, r.Owner) && strings.EqualFold(x.Repo.Name, r.Name) {
			found = append(found, x)
		}
	}
	for _, x := range found {
		if x.Name == "upstream" || x.Name == "origin" {
			return x, true
		}
	}
	if len(found) > 0 {
		return found[0], true
	}
	return remote{}, false
}

// noCheckoutError: no local clone of a repo was found.
type noCheckoutError struct {
	Repo repoRef
	Dest string // where it would be cloned
}

func (e *noCheckoutError) Error() string {
	return "no checkout of " + e.Repo.String() + " found: clone it into " + e.Dest + ", or map it under \"repos\" in " + filepath.Join(configDir(), "config.json")
}

// listWorkspacesFn is herdr's space list, a seam for tests.
var listWorkspacesFn = listWorkspaces

// findCheckout finds a local clone of r:
//
//  1. the config's "repos" map, by host/owner/repo or owner/repo;
//  2. any checkout herdr has a space for, or the ones the picker (or the
//     app) was opened from, whose remotes include r (so a fork's clone counts, by its
//     upstream remote);
//  3. <clone_root>/<owner>/<repo>, if it's there.
func findCheckout(ctx context.Context, cfg config, r repoRef, invoked ...string) (string, error) {
	for _, k := range []string{r.key(), strings.ToLower(r.String())} {
		if d, ok := cfg.Repos[k]; ok && d != "" {
			return d, nil
		}
	}
	var dirs []string
	for _, d := range invoked {
		if d == "" {
			continue
		}
		if top, err := git(ctx, expandHome(d), "rev-parse", "--show-toplevel"); err == nil {
			dirs = append(dirs, top)
		}
	}
	if wss, err := listWorkspacesFn(); err == nil {
		for _, w := range wss {
			if w.Worktree != nil && w.Worktree.RepoRoot != "" {
				dirs = append(dirs, w.Worktree.RepoRoot)
			}
		}
	}
	dest := cloneDest(cfg, r)
	dirs = append(dirs, dest)
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] {
			continue
		}
		seen[d] = true
		if _, ok := remoteFor(ctx, d, r); ok {
			return d, nil
		}
	}
	return "", &noCheckoutError{Repo: r, Dest: dest}
}

func cloneDest(cfg config, r repoRef) string {
	return filepath.Join(cfg.CloneRoot, r.Owner, r.Name)
}

// clone clones r into its place under clone_root with gh, which knows how
// to authenticate for private repos and GitHub Enterprise.
func clone(ctx context.Context, cfg config, r repoRef) (string, error) {
	dest := cloneDest(cfg, r)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("%s already exists but isn't a clone of %s", dest, r)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "repo", "clone", r.String(), dest)
	cmd.Env = append(os.Environ(), "GH_HOST="+r.Host, "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("couldn't clone " + r.String() + ": " + clean(lastLine(msg), false))
	}
	remoteCache.Delete(dest)
	return dest, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
