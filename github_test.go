package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func decodeReq(t *testing.T, r *http.Request) gqlRequest {
	t.Helper()
	var req gqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("Authorization"); got != "bearer tok" {
		t.Errorf("Authorization = %q", got)
	}
	return req
}

func reply(w http.ResponseWriter, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func prNode(n int) map[string]any {
	return map[string]any{
		"id": "PR_" + string(rune('a'+n)), "number": n, "title": "t", "url": "https://github.com/o/r/pull/1",
		"state": "OPEN", "repository": map[string]any{"nameWithOwner": "o/r"}, "headRefName": "b",
		"updatedAt": "2026-09-20T10:00:00Z",
		"commits":   map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{"state": "FAILURE"}}}}},
	}
}

func TestSearchPagesAndSkipsNonPRs(t *testing.T) {
	var calls int
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeReq(t, r)
		calls++
		if req.Variables["q"] != "is:pr" {
			t.Errorf("q = %v", req.Variables["q"])
		}
		if calls == 1 {
			if req.Variables["after"] != nil {
				t.Error("first page has a cursor")
			}
			reply(w, map[string]any{"search": map[string]any{
				"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "c1"},
				"nodes":    []any{prNode(1), map[string]any{}}, // an issue comes back as {}
			}})
			return
		}
		if req.Variables["after"] != "c1" {
			t.Errorf("second page after = %v", req.Variables["after"])
		}
		reply(w, map[string]any{"search": map[string]any{"pageInfo": map[string]any{}, "nodes": []any{prNode(2)}}})
	})
	prs, err := newClient("github.com").search(context.Background(), "is:pr")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || prs[0].Host != "github.com" || prs[1].Number != 2 || prs[0].checks() != "FAILURE" {
		t.Fatalf("%+v", prs)
	}
	if prs[0].UpdatedAt.IsZero() {
		t.Fatal("updatedAt not decoded")
	}
}

func TestSearchStopsAtTheCap(t *testing.T) {
	var sizes []float64
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeReq(t, r)
		n := int(req.Variables["first"].(float64))
		sizes = append(sizes, req.Variables["first"].(float64))
		nodes := []any{}
		for i := 0; i < n; i++ {
			nodes = append(nodes, prNode(len(sizes)*100+i))
		}
		reply(w, map[string]any{"search": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "more"},
			"nodes":    nodes,
		}})
	})
	prs, err := newClient("github.com").search(context.Background(), "x")
	if !errors.Is(err, errTruncated) || len(prs) != 220 {
		t.Fatalf("%d PRs, err %v", len(prs), err)
	}
	// A small page first, to show something soon; then bigger ones.
	if sizes[0] != firstPage || sizes[1] != morePage {
		t.Fatalf("page sizes %v", sizes)
	}
}

func TestListPagesAcrossHostsWithOneCursor(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeReq(t, r)
		after, _ := req.Variables["after"].(string)
		switch after {
		case "":
			reply(w, map[string]any{"search": map[string]any{"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "p2"}, "nodes": []any{prNode(1)}}})
		case "p2":
			reply(w, map[string]any{"search": map[string]any{"pageInfo": map[string]any{}, "nodes": []any{prNode(2)}}})
		default:
			t.Errorf("after = %q", after)
		}
	})
	s := newGitHubSource(withDefaults(config{Hosts: []string{"github.com", "ghe.example.com"}}), nil)
	p, err := s.list(context.Background(), tabMine, "")
	if err != nil || len(p.prs) != 2 || p.next == "" {
		t.Fatalf("first page: %d PRs, next %q, err %v", len(p.prs), p.next, err)
	}
	var hosts map[string]string
	if json.Unmarshal([]byte(p.next), &hosts) != nil || hosts["github.com"] != "p2" || hosts["ghe.example.com"] != "p2" {
		t.Fatalf("cursor %q", p.next)
	}
	p, err = s.list(context.Background(), tabMine, p.next)
	if err != nil || len(p.prs) != 2 || p.next != "" {
		t.Fatalf("second page: %d PRs, next %q, err %v", len(p.prs), p.next, err)
	}
	for _, pr := range p.prs {
		if pr.Number != 2 {
			t.Fatalf("second page has #%d", pr.Number)
		}
	}
}

func TestUnauthorizedMeansSignedOut(t *testing.T) {
	var tokens int
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	})
	tokenFn = func(context.Context, string) (string, error) { tokens++; return "tok", nil }
	c := newClient("github.com")
	_, err := c.search(context.Background(), "x")
	var so *signedOutError
	if !errors.As(err, &so) || so.Host != "github.com" || !errors.Is(err, errSignedOut) {
		t.Fatalf("err = %v", err)
	}
	_, _ = c.search(context.Background(), "x")
	if tokens != 2 {
		t.Fatalf("a refused token should be asked for again, got %d asks", tokens)
	}
}

func TestGraphQLErrorsAreShown(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{
			map[string]any{"message": "Pull request is not mergeable\x1b[2J"},
			map[string]any{"message": "Head branch was modified"},
		}})
	})
	err := newClient("github.com").merge(context.Background(), &prDetail{}, "SQUASH", false)
	if err == nil || err.Error() != "GitHub: Pull request is not mergeable; Head branch was modified" {
		t.Fatalf("err = %v", err)
	}
}

func TestDetailDecodesChecksReviewersAndSettings(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeReq(t, r)
		if req.Variables["id"] != "PR_x" || req.Variables["n"] != float64(9) {
			t.Errorf("vars %v", req.Variables)
		}
		n := prNode(9)
		n["body"] = "hello\nworld"
		n["mergeStateStatus"] = "BLOCKED"
		n["headRefOid"] = "abc"
		n["headRef"] = map[string]any{"id": "REF_1"}
		n["repository"] = map[string]any{"nameWithOwner": "o/r", "squashMergeAllowed": true, "rebaseMergeAllowed": true,
			"autoMergeAllowed": true, "viewerDefaultMergeMethod": "REBASE", "viewerPermission": "WRITE"}
		n["last"] = map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{"contexts": map[string]any{"nodes": []any{
			map[string]any{"__typename": "CheckRun", "name": "lint", "status": "COMPLETED", "conclusion": "SUCCESS"},
			map[string]any{"__typename": "StatusContext", "context": "ci/build", "state": "PENDING", "isRequired": true},
			map[string]any{"__typename": "CheckRun", "name": "test", "status": "COMPLETED", "conclusion": "FAILURE", "isRequired": true},
		}}}}}}}
		n["latestReviews"] = map[string]any{"nodes": []any{map[string]any{"author": map[string]any{"login": "ann"}, "state": "APPROVED"}}}
		n["reviewRequests"] = map[string]any{"nodes": []any{
			map[string]any{"requestedReviewer": map[string]any{"name": "core-team"}},
			map[string]any{"requestedReviewer": nil},
		}}
		n["reviews"] = map[string]any{"nodes": []any{map[string]any{"author": map[string]any{"login": "ann"}, "state": "APPROVED", "body": "lgtm", "createdAt": "2026-09-21T10:00:00Z"}}}
		n["comments"] = map[string]any{"nodes": []any{map[string]any{"author": nil, "body": "hi", "createdAt": "2026-09-22T10:00:00Z"}}}
		n["reviewThreads"] = map[string]any{"nodes": []any{map[string]any{"isResolved": false, "path": "a.go", "line": 3,
			"comments": map[string]any{"nodes": []any{map[string]any{"author": map[string]any{"login": "bo"}, "body": "why?"}}}}}}
		reply(w, map[string]any{"node": n})
	})
	d, err := newClient("github.com").detail(context.Background(), pullRequest{ID: "PR_x", Number: 9})
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "hello\nworld" || d.Repository.NameWithOwner != "o/r" || d.HeadRef.ID != "REF_1" || d.Host != "github.com" {
		t.Fatalf("detail: %+v", d)
	}
	if got := d.RepoSettings.methods(); strings.Join(got, ",") != "SQUASH,REBASE" || !d.ViewerCanMerge {
		t.Fatalf("methods %v, can merge %v", got, d.ViewerCanMerge)
	}
	var names []string
	for _, c := range d.Checks {
		names = append(names, c.name()+":"+c.outcome())
	}
	if strings.Join(names, " ") != "test:fail ci/build:pending lint:pass" {
		t.Fatalf("checks: %v", names)
	}
	if len(d.Reviewers) != 2 || d.Reviewers[1] != (reviewer{"core-team", "PENDING"}) {
		t.Fatalf("reviewers: %+v", d.Reviewers)
	}
	if len(d.Reviews) != 1 || d.Reviews[0].CreatedAt.IsZero() || len(d.Comments) != 1 || len(d.Threads) != 1 {
		t.Fatalf("conversation: %+v %+v %+v", d.Reviews, d.Comments, d.Threads)
	}
	if f, p := requiredCounts(d.Checks); f != 1 || p != 1 {
		t.Fatalf("required: %d failing, %d pending", f, p)
	}
}

func TestMergePinsTheHeadCommit(t *testing.T) {
	var got []gqlRequest
	var mu sync.Mutex
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, decodeReq(t, r))
		mu.Unlock()
		reply(w, map[string]any{})
	})
	c := newClient("github.com")
	d := &prDetail{HeadRefOid: "abc123"}
	d.ID = "PR_1"
	if err := c.merge(context.Background(), d, "REBASE", false); err != nil {
		t.Fatal(err)
	}
	if err := c.merge(context.Background(), d, "SQUASH", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[0].Query, "mergePullRequest") || got[0].Variables["oid"] != "abc123" || got[0].Variables["method"] != "REBASE" {
		t.Fatalf("merge: %+v", got[0])
	}
	if !strings.Contains(got[1].Query, "enablePullRequestAutoMerge") || got[1].Variables["method"] != "SQUASH" {
		t.Fatalf("auto-merge: %+v", got[1])
	}
}

func TestDraftMutations(t *testing.T) {
	var queries []string
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, decodeReq(t, r).Query)
		reply(w, map[string]any{})
	})
	c := newClient("github.com")
	_ = c.setDraft(context.Background(), "PR_1", true)
	_ = c.setDraft(context.Background(), "PR_1", false)
	if !strings.Contains(queries[0], "convertPullRequestToDraft") || !strings.Contains(queries[1], "markPullRequestReadyForReview") {
		t.Fatalf("%v", queries)
	}
}

// Branch names go in as variables, never into the query text: a branch can
// be named almost anything.
func TestPRsForBranchesUsesVariables(t *testing.T) {
	evil := `x") { nodes { id } } viewer { login } z: pullRequests(headRefName: "y`
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeReq(t, r)
		if strings.Contains(req.Query, "viewer") || req.Variables["b1"] != evil || req.Variables["owner"] != "o" {
			t.Errorf("query built from input: %s %v", req.Query, req.Variables)
		}
		reply(w, map[string]any{"repository": map[string]any{
			"b0": map[string]any{"nodes": []any{
				map[string]any{"number": 5, "state": "CLOSED", "headRepositoryOwner": map[string]any{"login": "o"}},
				map[string]any{"number": 6, "state": "OPEN", "headRepositoryOwner": map[string]any{"login": "fork"}},
				map[string]any{"number": 7, "state": "OPEN", "headRepositoryOwner": map[string]any{"login": "o"}},
			}},
			"b1": map[string]any{"nodes": []any{}},
		}})
	})
	heads := []branchHead{{"feat", "o"}, {evil, ""}}
	got, err := newClient("github.com").prsForBranches(context.Background(), repoRef{"github.com", "o", "r"}, heads)
	if err != nil {
		t.Fatal(err)
	}
	if got[heads[0]].Number != 7 {
		t.Fatalf("should pick the open PR from the right owner: %+v", got)
	}
	if _, ok := got[heads[1]]; ok {
		t.Fatal("a branch with no PR should be left out")
	}
}

func TestPickPR(t *testing.T) {
	nodes := []prStatus{{Number: 3, State: "MERGED"}, {Number: 2, State: "CLOSED"}}
	if s, _ := pickPR(nodes, ""); s.Number != 3 {
		t.Fatalf("latest when none open: %d", s.Number)
	}
	if _, ok := pickPR(nil, ""); ok {
		t.Fatal("no nodes, no PR")
	}
}

func TestListAcrossHostsSurvivesOneSignedOut(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"search": map[string]any{"pageInfo": map[string]any{}, "nodes": []any{prNode(1)}}})
	})
	tokenFn = func(_ context.Context, host string) (string, error) {
		if host == "ghe.example.com" {
			return "", &signedOutError{host}
		}
		return "tok", nil
	}
	s := newGitHubSource(withDefaults(config{Hosts: []string{"github.com", "ghe.example.com"}}), nil)
	p, err := s.list(context.Background(), tabMine, "")
	if len(p.prs) != 1 || !errors.Is(err, errSignedOut) {
		t.Fatalf("%d PRs, err %v", len(p.prs), err)
	}
	// With every host signed out, the error stands on its own.
	tokenFn = func(_ context.Context, host string) (string, error) { return "", &signedOutError{host} }
	s = newGitHubSource(withDefaults(config{Hosts: []string{"github.com", "ghe.example.com"}}), nil)
	if p, err := s.list(context.Background(), tabMine, ""); len(p.prs) != 0 || !errors.Is(err, errSignedOut) {
		t.Fatalf("%d PRs, err %v", len(p.prs), err)
	}
}

func TestRepoTabWithoutRepoIsEmpty(t *testing.T) {
	s := newGitHubSource(withDefaults(config{}), nil)
	if p, err := s.list(context.Background(), tabRepo, ""); err != nil || len(p.prs) != 0 {
		t.Fatal("no repo: nothing to list, and no error")
	}
}

func TestViewerReposPages(t *testing.T) {
	calls := 0
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeReq(t, r)
		calls++
		if !strings.Contains(req.Query, "ORGANIZATION_MEMBER") || !strings.Contains(req.Query, "isArchived: false") {
			t.Errorf("query: %s", req.Query)
		}
		more := calls == 1
		reply(w, map[string]any{"viewer": map[string]any{"repositories": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": more, "endCursor": "c"},
			"nodes":    []any{map[string]any{"nameWithOwner": "acme/app", "pushedAt": "2026-09-20T10:00:00Z"}, map[string]any{"nameWithOwner": "bad"}},
		}}})
	})
	rs, err := newClient("github.com").viewerRepos(context.Background())
	if err != nil || len(rs) != 2 || rs[0].Repo != (repoRef{"github.com", "acme", "app"}) || rs[0].Pushed.IsZero() {
		t.Fatalf("%+v %v", rs, err)
	}
}

func TestQueueMutations(t *testing.T) {
	var got []gqlRequest
	var mu sync.Mutex
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, decodeReq(t, r))
		mu.Unlock()
		reply(w, map[string]any{})
	})
	c := newClient("github.com")
	d := &prDetail{HeadRefOid: "abc123"}
	d.ID = "PR_1"
	if err := c.enqueue(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if err := c.dequeue(context.Background(), "PR_1"); err != nil {
		t.Fatal(err)
	}
	// Pinned to the head commit shown, as a merge is.
	if !strings.Contains(got[0].Query, "enqueuePullRequest") || got[0].Variables["oid"] != "abc123" || got[0].Variables["id"] != "PR_1" {
		t.Fatalf("enqueue: %+v", got[0])
	}
	if !strings.Contains(got[1].Query, "dequeuePullRequest(input: {id: $id})") || got[1].Variables["id"] != "PR_1" {
		t.Fatalf("dequeue: %+v", got[1])
	}
}
