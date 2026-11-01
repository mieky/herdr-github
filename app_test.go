package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The app mod pins the version it downloads; this build says it's that one.
func TestAppVersionIsThePlugins(t *testing.T) {
	toml, err := os.ReadFile("herdr-plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version = "([^"]+)"`).FindSubmatch(toml)
	if m == nil || string(m[1]) != appVersion {
		t.Fatalf("appVersion %q, herdr-plugin.toml %q", appVersion, m)
	}
	js, err := os.ReadFile("app/mods/github/main.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `const GITHUB_VERSION = "`+appVersion+`"`) {
		t.Fatalf("app/mods/github/main.js doesn't pin %s", appVersion)
	}
}

func appRun(t *testing.T, req string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := runApp(context.Background(), withDefaults(config{}), []string{req}, &out); err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	return res
}

// The demo's lists come grouped, and what's done to a PR stays done from
// one request to the next.
func TestAppDemoKeepsWhatWasDone(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Cleanup(func() { labelStateFile = "labels.json" })
	list := appRun(t, `{"op":"list","tab":"mine","demo":true}`)
	prs := list["prs"].([]any)
	if len(prs) != 5 {
		t.Fatalf("Mine has %d", len(prs))
	}
	if first := prs[0].(map[string]any); first["group"] != "fixing" || first["groupName"] != "Needs fixing" {
		t.Fatalf("first is %v", first)
	}
	pr := `"pr":{"host":"github.com","repo":"halcyon/notes-app","number":476}`
	d := appRun(t, `{"op":"detail","demo":true,`+pr+`}`)
	if d["draft"] != true || d["mergeWhy"] == "" || len(d["merge"].([]any)) != 0 {
		t.Fatalf("a draft offers no merge: %v %v", d["draft"], d["merge"])
	}
	if res := appRun(t, `{"op":"act","do":"ready","demo":true,`+pr+`}`); res["ok"] != true {
		t.Fatal(res)
	}
	d = appRun(t, `{"op":"detail","demo":true,`+pr+`}`)
	if d["draft"] != false {
		t.Fatal("marking it ready didn't stay")
	}
	// The queue: in, then the menu offers out.
	q := `"pr":{"host":"github.com","repo":"halcyon/sync-server","number":121}`
	d = appRun(t, `{"op":"detail","demo":true,`+q+`}`)
	if m := d["merge"].([]any); len(m) != 1 || m[0].(map[string]any)["do"] != "enqueue" || m[0].(map[string]any)["confirm"] == "" {
		t.Fatalf("merge menu: %v", d["merge"])
	}
	appRun(t, `{"op":"act","do":"enqueue","demo":true,`+q+`}`)
	d = appRun(t, `{"op":"detail","demo":true,`+q+`}`)
	if m := d["merge"].([]any); len(m) != 1 || m[0].(map[string]any)["do"] != "dequeue" || d["tone"] != "queue" {
		t.Fatalf("after enqueue: %v %v", d["merge"], d["tone"])
	}
	if res := appRun(t, `{"op":"act","do":"explode","demo":true,`+q+`}`); res["error"] == nil {
		t.Fatal("an unknown action should say so")
	}
}

// A job runs in the background, and its answer is there once, when it's done.
func TestAppJobAnswersOnce(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Cleanup(func() { labelStateFile = "labels.json" })
	id := "0123456789abcdef"
	if err := os.MkdirAll(filepath.Dir(jobPath(id)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jobPath(id), []byte(`{"done":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := appRun(t, `{"op":"job","job":"`+id+`"}`); res["done"] != false {
		t.Fatal(res)
	}
	if err := finishJob(id, map[string]any{"path": "/x"}); err != nil {
		t.Fatal(err)
	}
	if res := appRun(t, `{"op":"job","job":"`+id+`"}`); res["done"] != true || res["result"].(map[string]any)["path"] != "/x" {
		t.Fatal(res)
	}
	if res := appRun(t, `{"op":"job","job":"`+id+`"}`); res["error"] == nil {
		t.Fatal("a job's answer is given once")
	}
	if res := appRun(t, `{"op":"job","job":"../../etc/passwd"}`); res["error"] == nil {
		t.Fatal("a job id is only ever a job id")
	}
}

// The app's worktrees go where its own worktree mod puts them, from the
// PR's head; a second ask finds the first; a fork's pushes go to the fork.
func TestAppWorktree(t *testing.T) {
	f := gitFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctx := context.Background()
	cfg := withDefaults(config{CloneRoot: filepath.Join(f.root, "code")})
	req := appRequest{Dirs: []string{filepath.Join(f.clone)}}

	res, err := appWorktree(ctx, cfg, samePR("github.com"), req)
	if err != nil {
		t.Fatal(err)
	}
	got := res.(map[string]any)
	path := got["path"].(string)
	if want := filepath.Join(home, ".herdr", "worktrees", "clone", "feat"); path != want || got["created"] != true {
		t.Fatalf("made %v, want %s", got, want)
	}
	if sha := sh(t, path, "git", "rev-parse", "HEAD"); sha != f.featSHA {
		t.Fatal("not at the PR's head")
	}
	if up := sh(t, path, "git", "rev-parse", "--abbrev-ref", "feat@{upstream}"); up != "origin/feat" {
		t.Fatalf("upstream %q", up)
	}
	if !strings.Contains(got["prompt"].(string), "gh pr view 12") {
		t.Fatalf("prompt %q", got["prompt"])
	}
	again, err := appWorktree(ctx, cfg, samePR("github.com"), req)
	if err != nil || again.(map[string]any)["created"] != false || again.(map[string]any)["path"] != path {
		t.Fatalf("again: %v %v", again, err)
	}

	fork, err := appWorktree(ctx, cfg, forkPR(), req)
	if err != nil {
		t.Fatal(err)
	}
	fp := fork.(map[string]any)["path"].(string)
	if filepath.Base(fp) != "alice-fix" || sh(t, fp, "git", "rev-parse", "HEAD") != f.forkSHA {
		t.Fatalf("fork worktree %s", fp)
	}
	if r := sh(t, fp, "git", "config", "branch.alice/fix.remote"); r != "https://github.com/alice/r.git" {
		t.Fatalf("fork pushes to %q", r)
	}

	// Removing it: never the checkout itself, and never by force.
	if err := removeAppWorktree(ctx, f.clone); err == nil {
		t.Fatal("removed the repo's own checkout")
	}
	if err := os.WriteFile(filepath.Join(fp, "wip.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeAppWorktree(ctx, fp); err == nil {
		t.Fatal("removed a worktree with work in it")
	}
	if err := removeAppWorktree(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("still there")
	}

	// No checkout anywhere: it says where it would clone.
	other := samePR("github.com")
	other.Repository.NameWithOwner = "o/other"
	_, err = appWorktree(ctx, cfg, other, appRequest{})
	e := appError(err)
	if e["noCheckout"] == nil {
		t.Fatalf("no checkout: %v", e)
	}
}
