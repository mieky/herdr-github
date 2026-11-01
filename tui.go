package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type mode int

const (
	modeLoading mode = iota
	modeSignedOut
	modeList
	modeBusy
)

// ── messages ──────────────────────────────────────────────────────────────────

// A load's reply carries the gen it was asked for under, so one that arrives
// after a refresh or sign-in is dropped rather than shown over newer data.
type listMsg struct {
	tab    tab
	repo   string // the repo tab's repo it's for (key)
	pick   int    // …and which pick of it (see model.picks)
	cursor string // the page asked for: "" is the first
	prs    []pullRequest
	next   string // the page after, "" at the end
	err    error
	gen    int
}

type worktreesMsg struct {
	marks map[string]bool   // pr.key() → its worktree exists
	dirs  map[string]string // repo key → local checkout
	gen   int
}

// agentsMsg is each PR's agents, by openedKey.
type agentsMsg map[string][]prAgent

// noteMsg puts a line in the status line without stopping anything.
type noteMsg string

// actionDoneMsg ends a worktree action: success closes the popup (you're in
// the worktree now); a note or error keeps it open to be read.
type actionDoneMsg struct {
	err  error
	note string
}

// needCloneMsg: the PR's repo has no checkout here yet.
type needCloneMsg struct {
	pr    pullRequest
	start bool
	err   *noCheckoutError
}

type loginDoneMsg struct{ err error }

// prIcon is the PR's state as a mark, in the state's colour (prStyle). Every
// glyph is one common monospace fonts include, so the columns stay straight.
func prIcon(pr pullRequest) string {
	glyph := "●"
	switch {
	case pr.State == "MERGED":
		glyph = "◆"
	case pr.State == "CLOSED":
		glyph = "⊘"
	case pr.IsDraft:
		glyph = "◌"
	}
	return prStyle(pr.State, pr.IsDraft).Render(glyph)
}

// prStateWord is the PR's state in a word.
func prStateWord(pr pullRequest) string {
	switch {
	case pr.State == "MERGED":
		return "Merged"
	case pr.State == "CLOSED":
		return "Closed"
	case pr.IsDraft:
		return "Draft"
	}
	return "Open"
}

// checksBadge is the head commit's checks, said: "✓ checks" passed, "✗
// checks" failed, "● checks" running. The mark alone would read as an
// agent's, which the same line may end with.
func checksBadge(state string) string {
	g := checkGlyph(state)
	switch g {
	case "✓":
		return styleOK.Render(g + " checks")
	case "✗":
		return styleErr.Render(g + " checks")
	case "●":
		return styleWarn.Render(g + " checks")
	}
	return ""
}

func colored(glyph, hex string) string {
	if hex == "" {
		return glyph
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(glyph)
}

// ── model ─────────────────────────────────────────────────────────────────────

// row is something in a list to select: a pull request, or the repo tab's
// "change repo" row. The groups' headings are drawn between them (groups.go).
type row struct {
	pr   *pullRequest
	repo bool
}

type model struct {
	ctx     context.Context
	cfg     config
	client  source
	demo    bool
	invoked string   // cwd of the space the picker was opened from
	home    *repoRef // its GitHub repo, if it is one
	repo    *repoRef // the repo tab's repo: home, unless another was picked
	gen     int
	width   int
	height  int
	mode    mode
	tab     tab
	filter  textinput.Model
	spin    spinner.Model
	status  string // what's in progress
	err     string // last error, shown above the footer
	flash   string // a one-line confirmation
	host    string // the host to sign in to, on the signed-out screen

	prs       map[tab][]pullRequest
	loaded    map[tab]bool         // a list is there to show (fresh or cached)
	stale     map[tab]bool         // it's the cached one; the fresh one is on its way
	paging    map[tab]bool         // more pages are loading
	tabErr    map[tab]string       // why a tab's list couldn't load
	worktrees map[string]bool      // pr.key() → its worktree exists
	agents    map[string][]prAgent // openedKey → the PR's agents
	// Opened on an agent's PR ("pr" action): opening is what's being fetched
	// for it, shown in place of the lists; direct, that its screen is where
	// you came in, so esc there closes the popup.
	opening string
	direct  bool
	// back is the popup this one was opened from (another plugin's): esc
	// closes this one for it.
	back      *popupRef
	checkouts map[string]string // repo key → local checkout
	cursor    int
	offset    int
	// chose: you put the cursor where it is. Until then it stays on the
	// first PR, whatever a fresher list puts there.
	chose bool
	// wantRepoRow: you moved onto the "change repo" row, so the cursor
	// stays there when the list under it changes.
	wantRepoRow bool
	picks       int     // bumped on every repo switch: A→B→A mustn't take A's old pages
	menuOffset  int     // the menu's first shown item, kept so hovering doesn't scroll it
	startCmd    tea.Cmd // extra work for Init: opening the repo picker

	// The PR screen (detail.go).
	screen    screen
	cur       *pullRequest
	curDetail *prDetail
	md        *markdown
	scroll    int

	// A choice over the current screen: merge method, confirmations.
	menu       *menu
	menuCursor int
	menuOpened time.Time // clicks right after this belong to the click that opened it

	// The repo picker's choices (repopicker.go): what's here, and what you
	// can reach on GitHub, fetched once per popup.
	repoLocal     []repoChoice
	repoRemote    []repoInfo // what the list shows: fresh pages, gaps filled from last time
	repoFresh     []repoInfo // the pages fetched so far this time
	remoteLoaded  bool
	remoteLoading bool

	mouseX, mouseY int // last pointer position; -1 until the mouse moves
}

func newModel(ctx context.Context, cfg config, client source, invoked string, repo *repoRef) model {
	ti := textinput.New()
	// Its text lines up with the PRs' numbers.
	ti.Prompt = " › "
	ti.Placeholder = "Type to filter… ← → to change tabs"
	ti.PromptStyle, ti.PlaceholderStyle = styleDim, styleDim
	ti.Focus()
	sp := spinner.New()
	// A pie filling and emptying: in progress, and unlike braille it's in
	// common monospace fonts such as JetBrains Mono (a glyph from a fallback
	// font can come out the wrong width).
	sp.Spinner = spinner.Spinner{Frames: []string{"◔", "◕"}, FPS: time.Second / 3}
	sp.Style = lipgloss.NewStyle()
	_, demo := client.(*demoSource)
	return model{
		ctx: ctx, cfg: cfg, client: client, demo: demo, invoked: invoked, home: repo, repo: repo,
		mode: modeLoading, filter: ti, spin: sp,
		prs: map[tab][]pullRequest{}, loaded: map[tab]bool{}, stale: map[tab]bool{},
		paging: map[tab]bool{}, tabErr: map[tab]string{},
		worktrees: map[string]bool{}, checkouts: map[string]string{},
		md: newMarkdown(cfg.Theme != "light"), mouseX: -1, mouseY: -1,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.loadAll(), m.startCmd)
}

// useCache shows the lists the last popup loaded until fresh ones arrive.
func (m model) useCache(lists map[tab][]pullRequest) model {
	for t, prs := range lists {
		sortPRs(prs, t) // as a fresh list is, however it was kept
		m.prs[t], m.loaded[t], m.stale[t] = prs, true, true
	}
	if m.loaded[m.tab] {
		m.mode = modeList
		m.clampCursor()
	}
	return m
}

// settle puts the current tab in the right mode when nothing is coming for
// it: the repo tab, opened outside a GitHub repo, has nothing to load.
func (m model) settle() model {
	if m.tab == tabRepo && m.repo == nil {
		m.loaded[tabRepo] = true
		if m.mode == modeLoading {
			m.mode = modeList
		}
	}
	return m
}

// loadAll asks for every tab at once, so switching tabs doesn't wait.
func (m model) loadAll() tea.Cmd {
	cmds := []tea.Cmd{m.loadTab(tabMine, ""), m.loadTab(tabReview, ""), m.loadAgents()}
	if m.repo != nil {
		cmds = append(cmds, m.loadTab(tabRepo, ""))
	}
	return tea.Batch(cmds...)
}

func (m model) loadTab(t tab, cursor string) tea.Cmd {
	client, ctx, gen := m.client, m.ctx, m.gen
	repo, pick := "", m.picks
	if t == tabRepo && m.repo != nil {
		repo = m.repo.key()
	}
	return func() tea.Msg {
		debugf("loadTab start tab=%d cursor=%q", t, cursor)
		p, err := client.list(ctx, t, cursor)
		return listMsg{tab: t, repo: repo, pick: pick, cursor: cursor, prs: p.prs, next: p.next, err: err, gen: gen}
	}
}

// mergePRs adds a page to a list, without doubles (a PR updated between two
// pages can move from one to the next).
func mergePRs(list, more []pullRequest) []pullRequest {
	seen := map[string]bool{}
	for _, pr := range list {
		seen[pr.key()] = true
	}
	for _, pr := range more {
		if !seen[pr.key()] {
			seen[pr.key()] = true
			list = append(list, pr)
		}
	}
	return list
}

// selectedKey and reselect keep the cursor on the same PR while the list
// under it changes.
func (m model) selectedKey() string {
	if r := m.selected(); r != nil {
		return r.pr.key()
	}
	return ""
}

func (m *model) reselect(key string) {
	if key != "" {
		for i, r := range m.rows() {
			if r.pr != nil && r.pr.key() == key {
				m.cursor = i
				m.scrollTo()
				return
			}
		}
	}
	m.clampCursor()
}

// loadWorktrees finds which PRs already have a worktree, by looking for each
// repo's checkout and listing its worktrees.
func (m model) loadWorktrees(prs []pullRequest) tea.Cmd {
	if len(prs) == 0 {
		return nil
	}
	if d, ok := m.client.(*demoSource); ok {
		gen := m.gen
		return func() tea.Msg { return worktreesMsg{d.worktreeMarks(), nil, gen} }
	}
	ctx, cfg, invoked, gen := m.ctx, m.cfg, m.invoked, m.gen
	return func() tea.Msg {
		marks, dirs := map[string]bool{}, map[string]string{}
		byRepo := map[string][]pullRequest{}
		for _, pr := range prs {
			byRepo[pr.repo().key()] = append(byRepo[pr.repo().key()], pr)
		}
		for key, list := range byRepo {
			dir, err := findCheckout(ctx, cfg, list[0].repo(), invoked)
			if err != nil {
				continue
			}
			dirs[key] = dir
			wts, err := listWorktrees(dir)
			if err != nil {
				continue
			}
			have := map[string]bool{}
			for _, w := range wts {
				if w.IsLinkedWorktree { // the main checkout is the repo, not a PR's worktree
					have[strings.TrimPrefix(w.Branch, "refs/heads/")] = true
				}
			}
			for _, pr := range list {
				marks[pr.key()] = have[localBranch(pr)]
			}
		}
		return worktreesMsg{marks, dirs, gen}
	}
}

// ── update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.updateDetail(msg); handled {
		return next, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.Width = max(10, msg.Width-6)
		// Whatever was laid out before the size was known (last time's
		// lists), lay out again from the top.
		m.offset = 0
		m.scrollTo()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case listMsg:
		debugf("listMsg tab=%d cursor=%q n=%d next=%q err=%v gen=%d/%d", msg.tab, msg.cursor, len(msg.prs), msg.next, msg.err, msg.gen, m.gen)
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.tab == tabRepo && (m.repo == nil || msg.repo != m.repo.key() || msg.pick != m.picks) {
			return m, nil // for a repo that's no longer the one picked
		}
		t := msg.tab
		if msg.tab == m.tab && m.mode == modeLoading {
			m.mode = modeList
		}
		if msg.err != nil && len(msg.prs) == 0 {
			var so *signedOutError
			if errors.As(msg.err, &so) {
				m.mode, m.host = modeSignedOut, so.Host
				return m, nil
			}
			// A cached list stays up, with the reason it's not fresh.
			m.loaded[t], m.stale[t], m.paging[t], m.tabErr[t] = true, false, false, msg.err.Error()
			return m, nil
		}
		keep := ""
		if m.chose {
			keep = m.selectedKey()
		}
		if msg.cursor == "" {
			m.prs[t] = append([]pullRequest(nil), msg.prs...)
		} else {
			m.prs[t] = mergePRs(m.prs[t], msg.prs)
		}
		sortPRs(m.prs[t], t) // GitHub sorts by update; the tab groups too
		m.loaded[t], m.stale[t], m.tabErr[t] = true, false, ""
		if msg.err != nil {
			m.tabErr[t] = msg.err.Error() // usable, but not complete
		}
		if t == m.tab && m.screen == screenList {
			m.reselect(keep)
		}
		cmds := []tea.Cmd{m.loadWorktrees(msg.prs)}
		switch {
		case msg.next != "" && len(m.prs[t]) < listCap:
			m.paging[t] = true
			cmds = append(cmds, m.loadTab(t, msg.next))
		case msg.next != "":
			m.paging[t], m.tabErr[t] = false, errTruncated.Error()
		default:
			m.paging[t] = false
			if !m.demo && msg.err == nil {
				prs, repo := append([]pullRequest(nil), m.prs[t]...), m.repo
				cmds = append(cmds, func() tea.Msg { saveListCache(t, prs, repo); return nil })
			}
		}
		return m, tea.Batch(cmds...)

	case worktreesMsg:
		if msg.gen == m.gen {
			for k, v := range msg.marks {
				m.worktrees[k] = v
			}
			for k, v := range msg.dirs {
				m.checkouts[k] = v
			}
		}
		return m, nil

	case paneRefsMsg:
		if len(msg) == 0 {
			return m.noPRForAgent(), nil
		}
		return m.openOn(msg)

	case prsResolvedMsg:
		m.opening = ""
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		return m.showPRs(msg.prs)

	case agentsMsg:
		m.agents = msg
		m.clampCursor() // a filter on an agent's name now matches
		return m, nil

	case noteMsg:
		m.err = string(msg)
		return m, nil

	case repoChoicesMsg:
		m.repoLocal = msg
		// Asked for from the list; if you've moved on (into a PR), let it go.
		if m.mode == modeSignedOut || m.mode == modeBusy || m.menu != nil || m.screen != screenList {
			return m, nil
		}
		return m.openMenu(m.repoMenu()), nil

	case remoteReposMsg:
		return m.takeRemoteRepos(msg)

	case loginDoneMsg:
		if msg.err != nil {
			m.err = "gh auth login: " + msg.err.Error()
		}
		return m.refreshAll()

	case needCloneMsg:
		m.mode = modeList
		return m.openMenu(cloneMenu(msg)), nil

	case demoDoneMsg:
		// The demo stays open after an action so you can keep exploring.
		m.mode = modeList
		m.flash = msg.note
		if msg.key != "" {
			m.worktrees[msg.key] = true
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.mode, m.err = modeList, msg.err.Error()
			return m, nil
		}
		if msg.note != "" {
			// Something was left undone: say it here, not in a toast that
			// might never show, and let you close the popup once you've read it.
			m.mode, m.err = modeList, msg.note
			return m, nil
		}
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// refreshAll forgets every tab and loads the current one again.
func (m model) refreshAll() (model, tea.Cmd) {
	m.gen++
	m.prs, m.loaded, m.stale, m.paging, m.tabErr = map[tab][]pullRequest{}, map[tab]bool{}, map[tab]bool{}, map[tab]bool{}, map[tab]string{}
	m.mode, m.err = modeLoading, ""
	if gs, ok := m.client.(*githubSource); ok {
		// A new sign-in means new tokens: start the clients afresh.
		m.client = newGitHubSource(m.cfg, gs.repo)
	}
	m = m.settle()
	return m, tea.Batch(m.spin.Tick, m.loadAll())
}

// leave closes the popup (esc): for the one it was opened from, if it was.
// Going to a worktree or an agent, or ctrl+c, just closes it.
func (m model) leave() (tea.Model, tea.Cmd) {
	if m.back != nil {
		if err := handOver(*m.back); err != nil {
			debugf("back to %s: %v", m.back.PluginID, err)
		}
	}
	return m, tea.Quit
}

// escLabel is what esc is said to do where it closes the popup.
func (m model) escLabel() string {
	if m.back != nil {
		return "esc back"
	}
	return "esc close"
}

func (m model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.opening != "" && m.mode != modeSignedOut {
		if k.String() == "esc" || k.String() == "q" {
			return m.leave()
		}
		return m, nil
	}
	// An open menu takes the keys, loading or not: the repo list opened
	// while a repo loads must work (and esc must close it, not the popup).
	if m.menu != nil && m.mode != modeSignedOut && m.mode != modeBusy {
		return m.handleMenuKey(k)
	}
	switch m.mode {
	case modeSignedOut:
		switch k.String() {
		case "enter":
			return m, m.login()
		case "esc", "q":
			return m.leave()
		}
		return m, nil
	case modeBusy:
		return m, nil
	case modeLoading:
		if m.screen != screenList {
			break // a PR opened straight away doesn't wait for the lists behind it
		}
		switch k.String() {
		case "esc":
			return m.leave()
		case "ctrl+t":
			// A slow repo doesn't hold you there: pick another meanwhile.
			if m.screen == screenList && m.menu == nil {
				return m.openRepoPicker()
			}
			return m, nil
		case "tab", "shift+tab", "left", "right":
			// A load in flight doesn't lock the tabs; its reply still lands.
			if m.screen != screenList || m.menu != nil {
				return m, nil
			}
		default:
			return m, nil
		}
	}
	if m.menu != nil {
		return m.handleMenuKey(k)
	}
	if m.screen != screenList {
		return m.handleDetailKey(k)
	}
	m.flash = ""

	switch k.String() {
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.cursor, m.offset, m.chose = 0, 0, false
			m.clampCursor()
			return m, nil
		}
		return m.leave()
	case "tab", "shift+tab":
		next := (m.tab + 1) % 3
		if k.String() == "shift+tab" {
			next = (m.tab + 2) % 3
		}
		return m.switchTab(next)
	case "left", "right":
		// With text in the filter, arrows edit it; otherwise they switch tabs.
		if m.filter.Value() != "" {
			break
		}
		if k.String() == "right" {
			return m.switchTab((m.tab + 1) % 3)
		}
		return m.switchTab((m.tab + 2) % 3)
	case "up", "ctrl+p", "ctrl+k":
		m.move(-1)
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.move(1)
		return m, nil
	case "pgup":
		m.move(-m.visibleRows())
		return m, nil
	case "pgdown":
		m.move(m.visibleRows())
		return m, nil
	case "ctrl+r":
		return m.reloadLists()
	case "ctrl+o":
		if r := m.selected(); r != nil {
			m.openURL(r.pr.URL)
		}
		return m, nil
	case "ctrl+t":
		return m.openRepoPicker()
	case "ctrl+a", "ctrl+g": // a for agent, as on the PR screen; ctrl+g was its key before
		if r := m.selected(); r != nil {
			return m.goToAgent(*r.pr)
		}
		return m, nil
	case "enter":
		if m.onRepoRow() {
			return m.openRepoPicker()
		}
		if r := m.selected(); r != nil {
			return m.openPR(*r.pr)
		}
		return m, nil
	case "ctrl+w":
		if r := m.selected(); r != nil {
			return m.runWorktree(*r.pr, false)
		}
		return m, nil
	case "ctrl+s":
		if r := m.selected(); r != nil {
			return m.runWorktree(*r.pr, true)
		}
		return m, nil
	}

	// Anything else edits the filter.
	prev := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(k)
	if m.filter.Value() != prev {
		m.cursor, m.offset, m.chose = 0, 0, false
		m.clampCursor()
	}
	return m, cmd
}

func (m model) switchTab(t tab) (tea.Model, tea.Cmd) {
	m.tab, m.err, m.flash = t, "", ""
	if !m.demo {
		rememberTab(t)
	}
	m.cursor, m.offset, m.wantRepoRow, m.chose = 0, 0, false, false
	switch {
	case t == tabRepo && m.repo == nil:
		// No repo here to show: show the repos to choose from.
		m.loaded[t] = true
		m.mode = modeList
		m.clampCursor()
		return m.openRepoPicker()
	case !m.loaded[t]:
		m.mode = modeLoading // it was asked for with the others
	default:
		m.mode = modeList
	}
	m.clampCursor()
	return m, nil
}

// reloadLists asks GitHub for every tab again; what's shown stays up,
// marked stale, until the new lists arrive.
func (m model) reloadLists() (model, tea.Cmd) {
	m.gen++
	m.err, m.tabErr = "", map[tab]string{}
	for t := range m.loaded {
		m.stale[t] = true
	}
	return m, tea.Batch(m.spin.Tick, m.loadAll())
}

// login runs `gh auth login` in the popup itself: it's a terminal, and gh's
// prompts and browser hand-off work as they would anywhere else.
func (m model) login() tea.Cmd {
	host := m.host
	if host == "" {
		host = "github.com"
	}
	return tea.ExecProcess(exec.Command("gh", "auth", "login", "--hostname", host, "--web"), func(err error) tea.Msg {
		return loginDoneMsg{err}
	})
}

// runWorktree opens or creates the PR's worktree (start: and prompts its new
// agent). It finds the repo's checkout first; with none, it offers to clone.
func (m model) runWorktree(pr pullRequest, start bool) (tea.Model, tea.Cmd) {
	verb := "Opening"
	if !m.worktrees[pr.key()] {
		verb = "Creating a worktree for"
	}
	m.mode, m.err, m.flash, m.status = modeBusy, "", "", fmt.Sprintf("%s #%d…", verb, pr.Number)
	if d, ok := m.client.(*demoSource); ok {
		return m, tea.Batch(m.spin.Tick, func() tea.Msg { return d.worktree(pr, start) })
	}
	ctx, cfg, invoked := m.ctx, m.cfg, m.invoked
	known := m.checkouts[pr.repo().key()]
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		dir := known
		if dir == "" {
			var err error
			dir, err = findCheckout(ctx, cfg, pr.repo(), invoked)
			var nc *noCheckoutError
			if errors.As(err, &nc) {
				return needCloneMsg{pr, start, nc}
			} else if err != nil {
				return actionDoneMsg{err: err}
			}
		}
		return doWorktree(ctx, cfg, pr, dir, start)
	})
}

func cloneMenu(msg needCloneMsg) *menu {
	pr, start, r := msg.pr, msg.start, msg.err.Repo
	return &menu{
		title: "There's no checkout of " + r.String() + " here yet.",
		items: []menuItem{
			{label: "Clone it into " + tildePath(msg.err.Dest), run: func(m model) (tea.Model, tea.Cmd) {
				m.mode, m.status = modeBusy, "Cloning "+r.String()+"…"
				ctx, cfg := m.ctx, m.cfg
				return m, tea.Batch(m.spin.Tick, func() tea.Msg {
					dir, err := clone(ctx, cfg, r)
					if err != nil {
						return actionDoneMsg{err: err}
					}
					return doWorktree(ctx, cfg, pr, dir, start)
				})
			}},
			{label: "Cancel", run: func(m model) (tea.Model, tea.Cmd) {
				m.err = "To use a checkout that's elsewhere, map it under \"repos\" in the plugin's config.json."
				return m, nil
			}},
		},
	}
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// ── rows + cursor ─────────────────────────────────────────────────────────────

func (m model) rows() []row {
	q := m.filter.Value()
	var rows []row
	if m.tab == tabRepo {
		// The repo tab says which repo it's showing, and switching is one
		// enter away: no key to remember.
		rows = append(rows, row{repo: true})
	}
	list := m.prs[m.tab]
	for i := range list {
		if pr := &list[i]; pr.matches(q, agentNames(m.agentsOf(*pr))) {
			rows = append(rows, row{pr: pr})
		}
	}
	return rows
}

// selected is the PR row under the cursor, if that's where it is.
func (m model) selected() *row {
	rows := m.rows()
	if m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].pr != nil {
		return &rows[m.cursor]
	}
	return nil
}

// onRepoRow says whether the cursor is on the "change repo" row.
func (m model) onRepoRow() bool {
	rows := m.rows()
	return m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].repo
}

func (m *model) move(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		return
	}
	m.cursor, m.chose = min(max(m.cursor+delta, 0), len(rows)-1), true
	m.wantRepoRow = rows[m.cursor].repo
	m.scrollTo()
}

func (m *model) clampCursor() {
	rows := m.rows()
	m.cursor = max(0, min(m.cursor, len(rows)-1))
	// The cursor starts on the first PR, not the "change repo" row above
	// it, unless you went there.
	if len(rows) > 1 && rows[m.cursor].repo && !m.wantRepoRow {
		m.cursor++
	}
	m.scrollTo()
}

// rowHeight is a row's lines on screen, as viewRow draws them, counted
// without drawing: scrolling and the mouse ask for it a lot. (A test holds
// the two to each other.)
func (m model) rowHeight(r row) int {
	if r.pr != nil && m.detailed(*r.pr) {
		return 3 // title, details, a blank after
	}
	return 2
}

// scrollTo keeps the selected row on screen, heading and all.
func (m *model) scrollTo() {
	rows := m.rows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	// Back at the first PR, the "change repo" row above it shows again,
	// room allowing.
	if m.offset == 1 && rows[0].repo {
		m.offset = 0
	}
	for m.offset < m.cursor && m.cursor < len(rows) {
		used := 0
		for i := m.offset; i <= m.cursor; i++ {
			used += m.headingHeight(rows, i) + m.rowHeight(rows[i])
		}
		if used <= m.listHeight()+1 { // the last row's trailing blank may not fit
			break
		}
		m.offset++
	}
	m.offset = max(0, min(m.offset, len(rows)-1))
}

// visibleRows is how many whole rows fit from the offset: a page.
func (m model) visibleRows() int {
	rows := m.rows()
	n, used := 0, 0
	for i := m.offset; i < len(rows); i++ {
		used += m.headingHeight(rows, i) + m.rowHeight(rows[i])
		if used > m.listHeight()+1 {
			break
		}
		n++
	}
	return max(1, n)
}

// rowLineAt is the row drawn on screen line y, and which of its lines that
// is (0 its title).
func (m model) rowLineAt(y int) (i, line int, ok bool) {
	if m.screen != screenList || m.menu != nil {
		return 0, 0, false
	}
	rows := m.rows()
	top, end := listTop, listTop+m.listHeight()
	for i := m.offset; i < len(rows) && top < end; i++ {
		top += m.headingHeight(rows, i) // a group's heading isn't a row
		h := m.rowHeight(rows[i])
		if y >= top && y < min(top+h-1, end) { // not its trailing blank
			return i, y - top, true
		}
		top += h
	}
	return 0, 0, false
}

// rowAt is the row drawn on screen line y, if any.
func (m model) rowAt(y int) (int, bool) {
	i, _, ok := m.rowLineAt(y)
	return i, ok
}

func (m model) listHeight() int {
	// tabs + blank + filter + blank above; status line + footer below, the
	// footer on the popup's last line (mouse.go counts on that).
	return max(3, m.height-listTop-2)
}

// ── view ──────────────────────────────────────────────────────────────────────

// View is what Bubble Tea writes to the terminal. Every frame passes through
// screenSafe, whatever path its text took to get there.
func (m model) View() string {
	return screenSafe(m.view())
}

func (m model) view() string {
	if m.width == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.viewTabs() + "\n")

	if m.mode == modeSignedOut {
		host := m.host
		if host == "" {
			host = "github.com"
		}
		b.WriteString("\n  " + styleHeader.Render("GitHub isn't connected") + ": gh has no working sign-in for " + host + ".\n\n")
		b.WriteString("  Press enter to run " + styleTabOn.Render("gh auth login") + " here; it opens your browser.\n")
		b.WriteString(styleDim.Render("  The plugin uses gh's token and keeps none of its own.") + "\n")
		if m.err != "" {
			b.WriteString("\n  " + styleErr.Render(shorten(clean(m.err, false), max(10, m.width-4))) + "\n")
		}
		return b.String()
	}

	if m.opening != "" {
		// On the way to a PR: no lists in between.
		b.WriteString("\n " + m.spin.View() + " " + m.opening + "\n")
		for n := strings.Count(b.String(), "\n"); n < m.height-2; n++ {
			b.WriteString("\n")
		}
		b.WriteString(m.statusLine())
		b.WriteString(m.renderFooter(m.currentFooter()))
		return b.String()
	}

	if m.screen != screenList {
		b.WriteString("\n")
		b.WriteString(m.viewPR())
		// Pin the status line and footer to the bottom.
		for n := strings.Count(b.String(), "\n"); n < m.height-2; n++ {
			b.WriteString("\n")
		}
		b.WriteString(m.statusLine())
		b.WriteString(m.renderFooter(m.currentFooter()))
		return b.String()
	}

	b.WriteString("\n" + m.filter.View() + "\n\n")
	h := m.listHeight()
	if m.menu != nil {
		menu := m.viewMenu(h)
		b.WriteString(menu)
		for n := strings.Count(menu, "\n"); n < h; n++ {
			b.WriteString("\n")
		}
	} else {
		rows := m.rows()
		used := 0
		write := func(lines ...string) {
			for _, l := range lines {
				if used < h {
					b.WriteString(l + "\n")
					used++
				}
			}
		}
		for i := m.offset; i < len(rows) && used < h; i++ {
			if m.headed(rows, i) {
				if h-used < 3 {
					break // a heading with nothing under it says nothing
				}
				write(m.viewHeading(rows, groupOf(*rows[i].pr, m.tab)), "")
			}
			write(m.viewRow(rows[i], i == m.cursor)...)
		}
		// With no PRs to list (under the repo row, in its tab), say why.
		switch prs := len(rows) > 0 && rows[len(rows)-1].pr != nil; {
		case prs:
		case !m.loaded[m.tab] || m.mode == modeLoading:
			write("  " + m.spin.View() + " Loading…")
		default:
			write(styleDim.Render("  " + m.emptyText()))
		}
		for ; used < h; used++ {
			b.WriteString("\n")
		}
	}

	b.WriteString(m.statusLine())
	b.WriteString(m.renderFooter(m.currentFooter()))
	return b.String()
}

func (m model) emptyText() string {
	switch {
	case m.filter.Value() != "":
		return "Nothing matches."
	case m.tab == tabMine:
		return "You have no open pull requests."
	case m.tab == tabReview:
		return "Nobody is waiting on your review."
	case m.repo == nil:
		return "Press ctrl+t to pick a repo, or open this from a space inside one."
	}
	return "No open pull requests in " + m.repo.String() + "."
}

// statusLine is the one line above the footer: work in progress, the last
// error, or a confirmation.
func (m model) statusLine() string {
	switch {
	case m.mode == modeBusy:
		return " " + m.spin.View() + " " + m.status + "\n"
	case m.err != "":
		return " " + styleErr.Render(shorten(clean(m.err, false), max(10, m.width-2))) + "\n"
	case m.flash != "":
		return " " + styleOK.Render("✓ ") + shorten(clean(m.flash, false), max(10, m.width-4)) + "\n"
	case m.screen == screenList && m.tabErr[m.tab] != "":
		return " " + styleErr.Render(shorten(clean(m.tabErr[m.tab], false), max(10, m.width-2))) + "\n"
	case m.screen == screenList && m.menu == nil && m.opening == "":
		return m.about() + "\n"
	}
	return "\n"
}

// about is the selected PR's labels, with the branch it's to be merged into
// on the right, for the status line: selecting a row doesn't change its
// height.
func (m model) about() string {
	r := m.selected()
	if r == nil {
		return ""
	}
	var labels []string
	for _, l := range r.pr.Labels.Nodes {
		labels = append(labels, colored("●", "#"+l.Color)+" "+styleDim.Render(l.Name))
	}
	right := ""
	if r.pr.BaseRefName != "" {
		right = styleDim.Render("into ") + styleBranch.Render(shorten(r.pr.BaseRefName, 30))
	}
	left := shorten(strings.Join(labels, "  "), max(1, m.width-lipgloss.Width(right)-4))
	return " " + left + strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2)) + right
}

// viewTabs is the tab bar, with where you are on a PR's screen, and on the
// right what's going on behind it.
func (m model) viewTabs() string {
	line, _ := m.tabBar()
	if m.screen != screenList && m.cur != nil {
		line += styleDim.Render(" › ") + styleHeader.Render(fmt.Sprintf("%s#%d", m.cur.Repository.NameWithOwner, m.cur.Number))
	}
	updating := false
	for t := range m.stale {
		updating = updating || m.stale[t] || m.paging[t]
	}
	tag := ""
	if updating && m.mode != modeBusy {
		tag = m.spin.View() + styleDim.Render(" updating ")
	} else if m.demo {
		tag = styleDim.Render("demo ")
	}
	if pad := m.width - lipgloss.Width(line) - lipgloss.Width(tag); tag != "" && pad > 1 {
		line += strings.Repeat(" ", pad) + tag
	}
	return ansi.Truncate(line, m.width, "") // narrower than the tabs at their shortest
}

// viewRow is a row's lines: a PR's title, what there is to say of it under
// that, and a blank after.
func (m model) viewRow(r row, selected bool) []string {
	w := m.width
	var lines []string
	if r.repo {
		left := " " + styleTree.Render("⇄") + " "
		name := "Pick a repo"
		if m.repo != nil {
			name = m.repo.String()
			if m.repo.Host != "github.com" {
				name = m.repo.Host + "/" + name
			}
			left += styleHeader.Render(name)
			name = ""
		}
		hint := "change repo ›"
		if m.repo == nil {
			hint = "›"
		}
		lines = []string{fitRow(left, name, styleDim.Render(hint), w)}
	} else {
		left := m.rowLeft(r.pr)
		lines = []string{fitRow(left, r.pr.Title, styleDim.Render(strings.Join(m.rowMeta(*r.pr), " · ")), w)}
		if info, _, _ := m.detailLine(r.pr, selected && m.pointerOnAgent(r.pr)); len(info) > 0 {
			indent := strings.Repeat(" ", lipgloss.Width(left))
			lines = append(lines, indent+shortenStyled(strings.Join(info, styleDim.Render(" · ")), max(1, w-lipgloss.Width(left)-1)))
		}
	}
	if selected {
		for i, l := range lines {
			lines[i] = highlight(l, w)
		}
	}
	return append(lines, "")
}

// rowLeft is what's left of a PR's title: its mark and its number, which
// wears the PR's colour like the mark (they read as one). Padded after, so
// the titles line up.
func (m model) rowLeft(pr *pullRequest) string {
	return " " + prIcon(*pr) + " " + prNumber(pr.Number, pr.State, pr.IsDraft) + strings.Repeat(" ", m.numWidth()-len(strconv.Itoa(pr.Number))) + " "
}

// rowMeta is what's right of a PR's title, read last: whose it is, where,
// and when it last changed. Each only where the tab doesn't already say.
func (m model) rowMeta(pr pullRequest) []string {
	var meta []string
	if m.tab != tabMine {
		meta = append(meta, shorten(pr.author(), 20))
	}
	if m.tab != tabRepo {
		meta = append(meta, shorten(m.repoName(pr), 30))
	}
	if a := age(pr.UpdatedAt); a != "" {
		meta = append(meta, a)
	}
	return meta
}

// repoName is a PR's repo as its row names it: by its name alone, unless
// the tab lists another repo of that name (a fork's, another host's).
func (m model) repoName(pr pullRequest) string {
	r := pr.repo()
	for _, other := range m.prs[m.tab] {
		if o := other.repo(); o.Name == r.Name && !o.same(r) {
			if pr.Host != "github.com" && pr.Host != "" {
				return pr.Host + "/" + pr.Repository.NameWithOwner
			}
			return pr.Repository.NameWithOwner
		}
	}
	return r.Name
}

// detailLine is the line under a PR's title, each piece in its own colour:
// its branch, its size, its review, its checks, auto-merge, its worktree and
// last its agent, with the cells the agent is on (a click there goes to it).
// hot: the pointer is on the agent.
func (m model) detailLine(pr *pullRequest, hot bool) (info []string, from, to int) {
	if pr.HeadRefName != "" {
		// No branch glyph: ⎇ isn't in common monospace fonts. First, in its
		// own colour, it reads as the branch anyway.
		info = append(info, styleBranch.Render(shorten(pr.HeadRefName, 32)))
	}
	if pr.Additions > 0 || pr.Deletions > 0 {
		info = append(info, styleOK.Render("+"+strconv.Itoa(pr.Additions))+" "+styleErr.Render("−"+strconv.Itoa(pr.Deletions)))
	}
	if pr.Mergeable == "CONFLICTING" {
		info = append(info, styleErr.Render("conflicts"))
	}
	switch pr.ReviewDecision {
	case "CHANGES_REQUESTED":
		info = append(info, styleErr.Render("changes requested"))
	case "APPROVED":
		info = append(info, styleOK.Render("approved"))
	}
	if c := checksBadge(pr.checks()); c != "" {
		info = append(info, c)
	}
	if pr.AutoMergeRequest != nil {
		info = append(info, styleTree.Render("auto-merge"))
	}
	if q := pr.MergeQueueEntry; q != nil {
		info = append(info, styleTree.Render("queued "+ordinal(q.Position)))
	}
	if m.worktrees[pr.key()] {
		info = append(info, styleTree.Render("worktree"))
	}
	if a := m.rowAgent(*pr, hot); a != "" {
		from = lipgloss.Width(m.rowLeft(pr))
		for _, piece := range info {
			from += lipgloss.Width(piece) + 3
		}
		// Only as much of it as the popup is wide enough to show.
		if to = min(from+lipgloss.Width(a), m.width-1); to <= from {
			from, to = 0, 0
		}
		info = append(info, a)
	}
	return info, from, to
}

// detailed is whether detailLine has anything to say, cheaply.
func (m model) detailed(pr pullRequest) bool {
	return pr.HeadRefName != "" || pr.Additions > 0 || pr.Deletions > 0 || pr.Mergeable == "CONFLICTING" ||
		pr.ReviewDecision == "CHANGES_REQUESTED" || pr.ReviewDecision == "APPROVED" ||
		checkGlyph(pr.checks()) != "" || pr.AutoMergeRequest != nil || m.worktrees[pr.key()] || len(m.agentsOf(pr)) > 0
}

// agentSpan is where under its title a PR's agent is drawn: the columns a
// click on goes to the agent rather than the PR.
func (m model) agentSpan(pr *pullRequest) (from, to int, ok bool) {
	_, from, to = m.detailLine(pr, false)
	return from, to, to > from
}

// pointerOnAgent says whether the mouse is over pr's agent in the list.
func (m model) pointerOnAgent(pr *pullRequest) bool {
	i, line, ok := m.rowLineAt(m.mouseY)
	if !ok || line != 1 || m.rows()[i].pr != pr {
		return false
	}
	from, to, ok := m.agentSpan(pr)
	return ok && m.mouseX >= from && m.mouseX < to
}

// numWidth is the width of the longest PR number in the tab, so titles line up.
func (m model) numWidth() int {
	n := 1
	for _, pr := range m.prs[m.tab] {
		n = max(n, len(fmt.Sprint(pr.Number)))
	}
	return n
}

// fitRow lays out left + title + right-aligned meta in exactly w cells,
// truncating the title (never the number) when space runs out.
func fitRow(left, title, right string, w int) string {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	room := w - lw - rw - 2
	if room < 8 {
		right, rw = "", 0
		room = w - lw - 1
	}
	title = shorten(title, max(1, room))
	pad := max(1, w-lw-lipgloss.Width(title)-rw-1)
	return left + styleLead.Render(title) + strings.Repeat(" ", pad) + right
}

// shortenStyled is shorten for text that's already coloured.
func shortenStyled(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	return ansi.Truncate(s, max(0, n-1), "") + styleDim.Render("…")
}

// age is how long ago t was, as briefly as a row's edge wants it: "now",
// "4m", "2h", "3d", "5w", "7mo", "2y".
func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := nowFn().Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 9*7*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/24/365))
}

// highlight gives a whole line the selection background, w cells wide. The
// line's own coloured pieces each end in a style reset, which would also end
// the background, so it's switched back on after every one.
func highlight(line string, w int) string {
	on := styleSelected.Render("x")
	on = on[:strings.Index(on, "x")] // the escape that turns the background on
	if on != "" {
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+on)
	}
	return styleSelected.Width(w).Render(line)
}

func (m model) currentFooter() []hint {
	switch {
	case m.opening != "":
		return []hint{{m.escLabel(), "esc", hintQuiet}}
	case m.menu != nil:
		return []hint{{"↑↓ choose", "", hintView}, {"enter select", "enter", hintGo}, {"esc cancel", "esc", hintQuiet}}
	case m.screen != screenList:
		return m.detailFooter()
	}
	// By kind, so the colours sit together: go, act, view, leave.
	hs := []hint{{"enter details", "enter", hintGo}}
	if r := m.selected(); r != nil && len(m.agentsOf(*r.pr)) > 0 {
		hs = append(hs, hint{"^a " + plural(len(m.agentsOf(*r.pr)), "agent", "agents"), "ctrl+a", hintGo})
	}
	hs = append(hs, hint{"^w worktree", "ctrl+w", hintAct}, hint{"^s start", "ctrl+s", hintAct}, hint{"^o open", "ctrl+o", hintView})
	if m.tab == tabRepo {
		hs = append(hs, hint{"^t repo", "ctrl+t", hintView})
	}
	// The tabs' keys are in the filter's placeholder. esc says what it'll do.
	esc := m.escLabel()
	if m.filter.Value() != "" {
		esc = "esc clear"
	}
	return append(hs, hint{"^r refresh", "ctrl+r", hintView}, hint{esc, "esc", hintQuiet})
}

// ── menus ─────────────────────────────────────────────────────────────────────

// menu is a short list of choices shown over the current screen: how to
// merge, what to clean up afterwards, whether to clone, which repo.
type menu struct {
	id     string // which menu this is, for replacing it while it's open
	title  string
	items  []menuItem
	cursor int // where the cursor starts: a confirmation starts on Cancel

	// A filterable menu takes typing as a filter over its items (and then
	// has no number shortcuts); typed may add an item for what was typed.
	filterable bool
	query      string
	typed      func(q string) *menuItem

	// noDigits turns off the number shortcuts: a confirmation mustn't be
	// answered by the same key that asked for it (m, 1, 1 merging).
	noDigits bool
}

type menuItem struct {
	label  string
	detail string
	run    func(m model) (tea.Model, tea.Cmd)
	header bool   // a group heading: shown, never chosen
	search string // what the filter matches, if not label and detail
}

func (it menuItem) haystack() string {
	if it.search != "" {
		return strings.ToLower(it.search)
	}
	return strings.ToLower(it.label + " " + it.detail)
}

func (m model) openMenu(mn *menu) model {
	m.menu, m.menuCursor, m.flash, m.menuOpened, m.menuOffset = mn, mn.cursor, "", time.Now(), 0
	m.menuCursor = m.selectable(m.menuCursor, 1)
	return m
}

// selectable is the first item from i on, going by step, that isn't a
// heading; i itself when there's none that way.
func (m model) selectable(i, step int) int {
	items := m.menuItems()
	for j := i; j >= 0 && j < len(items); j += step {
		if !items[j].header {
			return j
		}
	}
	if step > 0 {
		return m.selectable(min(i, len(items)-1), -1)
	}
	return max(0, i)
}

func (m *model) moveMenu(delta int) {
	n := len(m.menuItems())
	if n == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	next := m.selectable(min(max(0, m.menuCursor+delta), n-1), step)
	if items := m.menuItems(); next >= 0 && next < n && !items[next].header {
		m.menuCursor = next
	}
	m.menuOffset = m.menuStart(m.menuRoom())
}

// menuClickGuard is how long a new menu ignores clicks: the second click of a
// double-click must not land on what appeared under it (a merge's "yes").
var menuClickGuard = 400 * time.Millisecond

// menuItems are the items shown: all of them, or those matching the filter.
func (m model) menuItems() []menuItem {
	mn := m.menu
	if mn == nil {
		return nil
	}
	if !mn.filterable || mn.query == "" {
		return mn.items
	}
	var out []menuItem
	if mn.typed != nil {
		if it := mn.typed(mn.query); it != nil {
			out = append(out, *it)
		}
	}
	// A heading stays while anything under it matches; matching the
	// heading (an org's name) keeps everything under it.
	var head *menuItem
	headShown, headMatch := false, false
	matches := func(hay string) bool {
		for _, w := range strings.Fields(strings.ToLower(mn.query)) {
			if !strings.Contains(hay, w) {
				return false
			}
		}
		return true
	}
	for i := range mn.items {
		it := mn.items[i]
		if it.header {
			head, headShown, headMatch = &mn.items[i], false, matches(it.haystack())
			continue
		}
		hay := it.haystack()
		if head != nil {
			hay += " " + head.haystack()
		}
		if !headMatch && !matches(hay) {
			continue
		}
		if head != nil && !headShown {
			out, headShown = append(out, *head), true
		}
		out = append(out, it)
	}
	return out
}

func (m model) handleMenuKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.menuItems())
	switch s := k.String(); s {
	case "esc":
		if m.menu.filterable && m.menu.query != "" {
			m.setMenuQuery("")
		} else {
			m.menu = nil
		}
	case "up", "ctrl+p":
		m.moveMenu(-1)
	case "down", "ctrl+n":
		m.moveMenu(1)
	case "pgup":
		m.moveMenu(-max(1, m.listHeight()-menuTop))
	case "pgdown":
		m.moveMenu(max(1, m.listHeight()-menuTop))
	case "enter":
		return m.chooseMenu(m.menuCursor)
	case "backspace":
		if m.menu.filterable && m.menu.query != "" {
			q := []rune(m.menu.query)
			m.setMenuQuery(string(q[:len(q)-1]))
		}
	default:
		switch {
		case m.menu.filterable:
			if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
				m.setMenuQuery(m.menu.query + string(k.Runes))
			}
		case s == "q":
			m.menu = nil
		case s == "k":
			m.moveMenu(-1)
		case s == "j":
			m.moveMenu(1)
		case !m.menu.noDigits && len(s) == 1 && s[0] >= '1' && s[0] <= '9' && int(s[0]-'1') < n:
			return m.chooseMenu(int(s[0] - '1'))
		}
	}
	return m, nil
}

// setMenuQuery changes a filterable menu's filter. The menu is copied, not
// changed in place: models are values, and an older copy may still hold it.
func (m *model) setMenuQuery(q string) {
	mn := *m.menu
	mn.query = q
	m.menu, m.menuCursor, m.menuOffset = &mn, 0, 0
	m.menuCursor = m.selectable(0, 1)
}

func (m model) chooseMenu(i int) (tea.Model, tea.Cmd) {
	items := m.menuItems()
	if i < 0 || i >= len(items) || items[i].header {
		return m, nil
	}
	it := items[i]
	m.menu = nil
	return it.run(m)
}

// menuTop is how many lines come before the first menu item within the
// menu's own block: the title and a blank line.
const menuTop = 2

// menuStart is the first item shown: where the menu was scrolled to, moved
// only as far as it takes to keep the cursor in view. Hovering moves the
// cursor within what's shown, so it never scrolls under the pointer.
func (m model) menuStart(room int) int {
	vis := max(1, room-menuTop)
	start := m.menuOffset
	if m.menuCursor < start {
		start = m.menuCursor
	}
	if m.menuCursor >= start+vis {
		start = m.menuCursor - vis + 1
	}
	return max(0, start)
}

// menuRoom is how many lines the open menu has, on the screen it's over.
func (m model) menuRoom() int {
	if m.screen != screenList {
		return m.bodyRoomFor(m.prHeader())
	}
	return m.listHeight()
}

func (m model) viewMenu(room int) string {
	var b strings.Builder
	title := m.menu.title
	if m.menu.filterable {
		title += " › " + m.menu.query + "▏"
	}
	b.WriteString(" " + styleHeader.Render(shorten(title, max(10, m.width-2))) + "\n\n")
	items := m.menuItems()
	if len(items) == 0 {
		b.WriteString(styleDim.Render("   Nothing matches.") + "\n")
	}
	start := m.menuStart(room)
	for i := start; i < len(items) && i-start < room-menuTop; i++ {
		it := items[i]
		if it.header {
			b.WriteString(" " + styleDim.Render(shorten(it.label, max(10, m.width-2))) + "\n")
			continue
		}
		num := fmt.Sprint(i + 1)
		if m.menu.filterable || m.menu.noDigits {
			num = "·"
		}
		// A long label (a PR's title) gives way to its detail, not the line.
		label := shorten(it.label, max(20, m.width-ansi.StringWidth(it.detail)-10))
		line := fmt.Sprintf("   %s %s", styleDim.Render(num), label)
		if it.detail != "" {
			line += styleDim.Render("  " + it.detail)
		}
		if i == m.menuCursor {
			line = highlight(" ›"+line[2:], max(20, m.width-2))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// ── running it ────────────────────────────────────────────────────────────────

// program lets background work post progress to the UI.
var program *tea.Program

func runPicker(ctx context.Context, cfg config, demo bool) error {
	invoked := os.Getenv("HERDR_GITHUB_CWD")
	// Ask the terminal for its background now: once the program owns stdin,
	// the reply would arrive as stray input.
	if cfg.Theme != "dark" && cfg.Theme != "light" {
		cfg.Theme = "light"
		if lipgloss.HasDarkBackground() {
			cfg.Theme = "dark"
		}
	}
	darkTerminal = cfg.Theme == "dark"
	useTheme(pickerTheme(cfg.Theme == "dark"))

	var client source
	var repo *repoRef
	if demo {
		d := newDemoSource()
		home := d.homeRepo()
		client, repo = d, &home
		spawnTickFn = func() {}
	} else {
		if invoked != "" {
			if top, err := git(ctx, invoked, "rev-parse", "--show-toplevel"); err == nil {
				if r, ok := repoOf(ctx, top); ok && cfg.knownHost(r.Host) {
					repo = &r
				}
			}
		}
		client = newGitHubSource(cfg, repo)
	}
	m := newModel(ctx, cfg, client, invoked, repo)
	if !demo {
		// Open on the tab you were last on, showing last time's lists.
		if t, ok := lastTab(); ok {
			m.tab = t
		}
		m = m.useCache(readListCache(repo))
	}
	m = m.settle()
	if !demo {
		// Your repos, for the repo list: last time's at once, and fresh ones
		// asked for now, so they're there by the time you open it.
		m.repoRemote = readRepoCache(cfg)
		m.remoteLoading = true
		m.startCmd = m.loadRemoteRepos("")
	}
	if !demo {
		m.back = cameFrom()
	}
	if pane, space := os.Getenv("HERDR_GITHUB_PANE"), os.Getenv("HERDR_GITHUB_SPACE"); !demo && pane+space != "" {
		// Opened on an agent's PR ("pr" action): its screen, not the lists.
		var cmd tea.Cmd
		m, cmd = m.openForPane(pane, space)
		m.startCmd = tea.Batch(m.startCmd, cmd)
	} else if m.tab == tabRepo && m.repo == nil {
		next, cmd := m.openRepoPicker()
		m = next.(model)
		m.startCmd = tea.Batch(m.startCmd, cmd)
	}
	if !demo {
		defer notePopup()()
	}
	// WithContext: a SIGTERM (a newer open replacing this popup) ends it
	// cleanly, restoring the terminal.
	program = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithContext(ctx))
	_, err := program.Run()
	if errors.Is(err, context.Canceled) {
		return nil // replaced by a newer open, or interrupted: not an error
	}
	return err
}

// sortedKeys is for stable output in tests and menus.
func sortedKeys[V any](mp map[string]V) []string {
	ks := make([]string, 0, len(mp))
	for k := range mp {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func debugf(format string, args ...any) {
	p := os.Getenv("HERDR_GITHUB_DEBUG")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s "+format+"\n", append([]any{nowFn().Format("15:04:05.000")}, args...)...)
}
