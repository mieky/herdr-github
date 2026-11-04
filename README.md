# herdr-github

**Your pull requests, one key away from whatever you're working in.**

See what's waiting on you, read a PR with its checks and reviews, flip it
between draft and ready, merge it (or auto-merge, or through a merge queue),
and land in a worktree for it with an agent already on it, forks included:
in the [Herdr app](https://herdr.dev), from any machine it's connected to,
and in a [herdr](https://herdr.dev) TUI popup. Every PR knows its agents: the
one that opened it and any that worked on it since, a key or a click away.

![The GitHub mod in the Herdr app, on the demo org: your pull requests grouped by whose move it is, each with its branch, checks, review, size and agents](app/preview.png)

*In the Herdr app.*

![Your pull requests in the herdr TUI, on the built-in demo org](docs/images/mine.png)

*In the herdr TUI.*

- **Three tabs**: *Mine* (your open PRs, every repo), *Review requested*,
  and *this repo* (all open PRs in the repo you opened it from), each
  grouped by whose move it is: what needs fixing, what's ready to merge,
  what's waiting, your drafts. Under a PR's title: its branch, its size,
  its review, its checks, auto-merge, its worktree and its agents.
- **PR screen**: merge readiness in one line (and why not), checks that need
  attention, reviewers, the description as Markdown, unresolved review
  threads, and the conversation.
- **Draft ↔ ready** with one key.
- **Merge** with any method the repo allows, your last one first, after a
  confirmation. Not mergeable yet? **Auto-merge** instead, or, behind its
  base, **update the branch** first. Then optionally
  delete the branch and remove the worktree.
- **Worktrees**: one key opens the PR's worktree, or makes it: in the right
  repo (found by its remotes, wherever you open the popup), on the PR's
  branch, tracking it. A fork's PR comes from `refs/pull/N/head` and pushes
  back to the fork.
- **Start**: the worktree, plus a prompt for its new agent.
- **Sidebar labels** (TUI): `$pr`, `$pr_badge` and friends on every space
  and agent, kept current as you work.
- **Whose PR is this?** A PR lists the agents that opened it and worked on
  it; a key or a click goes to one, or resumes a session that's been
  closed. An agent's PRs, its branch's and the others it opened, are in the
  sidebar as `$prs`.
- Uses **gh's sign-in**: no tokens of its own, GitHub Enterprise included.
- Keyboard first, and the mouse works: every key along the bottom is a pill
  you can click. In the **app's or herdr's theme colours**, with
  [one look](#colours) shared by the herdr plugins.

**Contents:** [Install](#install) · [Requirements](#requirements) · [In the Herdr app](#in-the-herdr-app-1) ·
[Try it without an account](#try-it-without-an-account) · [Works with the other plugins](#works-with-the-other-herdr-plugins) ·
[In the herdr TUI](#in-the-herdr-tui-1) · [Merging](#merging) · [Worktrees](#worktrees) · [Sidebar labels](#sidebar-labels) ·
[Configuration](#configuration) · [Privacy and security](#privacy-and-security) ·
[Troubleshooting](#troubleshooting) · [How it works](#how-it-works) ·
[Development](#development)

## Install

### In the Herdr app

Ask the agent in your Herdr window to *add the mod at
github.com/mrolafsson/herdr-github/tree/main/app*: it follows the app's own
steps (it reads the mod, copies it into `mods/`, merges its keys and checks
it). Or from a terminal:

```sh
curl -fsSL https://raw.githubusercontent.com/mrolafsson/herdr-github/main/install.sh | sh -s -- --app
```

That's all: the mod fetches what it needs onto the machines it works on by
itself (see [In the Herdr app](#in-the-herdr-app-1)). ⌘G opens it.

### In the herdr TUI

```sh
herdr plugin install mrolafsson/herdr-github
```

Then give the picker a key in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+alt+g"
type = "plugin_action"
command = "herdr-github.open"
description = "GitHub: pull requests"

# Optional: straight to the pull request of the agent you're in.
[[keys.command]]
key = "prefix+alt+p"
type = "plugin_action"
command = "herdr-github.pr"
description = "GitHub: this agent's pull request"
```

and reload with `herdr server reload-config`.

| key, as bound above | action              | does                                      |
|---------------------|---------------------|-------------------------------------------|
| `prefix+alt+g`      | `herdr-github.open` | the popup, on your lists                  |
| `prefix+alt+p`      | `herdr-github.pr`   | the popup, straight on the PR of the agent you're in (see [A PR's agents](#a-prs-agents)) |

Any action also runs without a key:
`herdr plugin action invoke herdr-github.open`. Attached to another machine
with `herdr --remote`? These bindings go in the config of the machine herdr's
server runs on, where the plugin is.

> On macOS, **alt** is the Option key, and most terminals type a character
> with it (Option+G is `©`) unless it's set to act as Meta: iTerm2 →
> Profiles → Keys → Left Option key → *Esc+*; Terminal.app → Profiles →
> Keyboard → *Use Option as Meta key*; Ghostty → `macos-option-as-alt = true`.

### Both

`sh install.sh` in a clone (or the `curl` line above without `-s -- --app`)
installs whichever of the two is on the computer, and both when both are.

### Requirements

- **[gh](https://cli.github.com)**, signed in (`gh auth login`), on at least
  one machine: herdr-github borrows its token. The app's mod finds the
  machine that's signed in by itself.
- **git**, for worktrees.
- **macOS** or **Linux** (arm64 or x86-64).
- The app's mod: the **Herdr app**, and `curl`, `tar` and `sha256sum` or
  `shasum` on the machines it works on, which macOS and Linux have.
- The TUI plugin: **herdr 0.9.0** or later. On Linux, `xdg-open` opens the
  browser, and copying needs `wl-copy`, `xclip` or `xsel`. **Go 1.26.8+** to
  build from source; without Go, installing downloads a prebuilt binary from
  the matching GitHub release, checked against its SHA-256 checksums.

### From a local checkout

```sh
git clone https://github.com/mrolafsson/herdr-github
sh herdr-github/scripts/build.sh
herdr plugin link "$PWD/herdr-github"
```

After changing the code, run `scripts/build.sh` again; no need to re-link.

## In the Herdr app

⌘G opens a popup with the same three tabs (Mine, Review requested, and the
shown space's repo), grouped the same way, as cards: each PR's branch,
checks, review, size, auto-merge or place in the merge queue, labels, its
worktree and its agents. Type to filter; ← → change tabs, ↑ ↓ and Enter (or
a click) open a PR.

![A pull request's screen in the app, on the demo: ready to merge, its checks, reviewers, labels and the two agents on it, then its description](docs/images/app-pr.png)

A PR's screen has whether it can merge and why not, its checks (the
failing and running ones by name, a click away), reviewers, its agents, and
the description, unresolved threads and conversation as Markdown. Its keys,
along the bottom and clickable:

| key | does |
|-----|------|
| `w` | its worktree, in a space: opened if it has one, made if not |
| `s` | start: the same, with an agent in it asked to look at the PR (a new worktree only) |
| `m` | merge: the methods the repo allows, auto-merge, its merge queue, or updating a branch that's behind; asks first. After a merge, offers to delete the branch and remove the worktree |
| `d` | draft ↔ ready for review |
| `a` | its agents: go to one, or resume a closed Claude session where it ran |
| `l` | its Linear issue (from its branch, title or a "Fixes HAL-212"), in herdr-linear's Linear mod |
| `o` / `y` | open it on GitHub / copy its link |
| `esc` | back to the list (or to Recap, when Recap opened it); out of the popup |

![The merge menu on a PR whose branch merges through a merge queue](docs/images/app-merge.png)

**Worktrees** go where the app's own worktrees do,
`~/.herdr/worktrees/<repo>/<branch>`, made from the PR's head as GitHub has
it (a fork's from `refs/pull/N/head`, pushing back to the fork), on the
machine of the space you're in when it has a checkout of the repo, else on
another machine with one. With none anywhere it offers to clone the repo
(into `clone_root`, `~/code` by default).

**With Recap and Linear.** With [herdr-recap](https://github.com/mrolafsson/herdr-recap)'s
mod installed too, an agent's pull request there opens here, on its screen
(a click, or `ctrl+g`), and `esc` goes back. With
[herdr-linear](https://github.com/mrolafsson/herdr-linear)'s, a PR's Linear
issue is a link on its screen (a click, or `l`) to the Linear mod, and `esc`
there comes back to the PR.

**How it works.** The mod is the plugin's own code: it runs
`herdr-github app` (one JSON request in, one answer out) on the machine
where gh is signed in, and for a worktree, on the machine with the
checkout. The first time it asks a machine, it downloads the herdr-github
release it names (`GITHUB_VERSION` in `main.js`), checks it against the
release's `checksums.txt`, and keeps it in
`~/.local/share/herdr-github/app/<version>/`, apart from any TUI install.
`herdr-app do github.demo`, while it's open, switches to the demo org.

## Try it without an account

The demo is *Halcyon*, a made-up company with three repos and a handful of
PRs. Everything works (drafts toggle, PRs merge, worktrees "open") and
nothing is real: it never contacts GitHub, never runs git, never opens a
browser. The status line says what the real thing would have done. It's
where the screenshots here come from.

- **In the Herdr app:** open it (⌘G) and run `herdr-app do github.demo`, for
  that opening. In Recap's demo, an agent's pull request opens here, in the
  demo too.
- **In the herdr TUI:** **GitHub: demo**, or

  ```sh
  herdr plugin action invoke herdr-github.demo
  # or, in any terminal:
  bin/herdr-github picker --demo
  ```

## Works with the other herdr plugins

This is one of six plugins that are useful alone and better together.
Install [herdr-recap](https://github.com/mrolafsson/herdr-recap), [herdr-linear](https://github.com/mrolafsson/herdr-linear), [herdr-snooze](https://github.com/mrolafsson/herdr-snooze), [herdr-vitals](https://github.com/mrolafsson/herdr-vitals) or [herdr-file-transfer](https://github.com/mrolafsson/herdr-file-transfer) alongside it and they find each other:
there is nothing to configure.

| Plugin | What it adds to the others |
| --- | --- |
| [herdr-recap](https://github.com/mrolafsson/herdr-recap) | Every agent in one list. An agent's pull request and its Linear issue are links there: a click, or `ctrl+g` / `ctrl+l`, opens it in the plugin it belongs to. Snoozed agents have a tab of their own: `ctrl+s` snoozes an agent, in Snooze's popup, and `ctrl+w` wakes one. |
| **[herdr-github](https://github.com/mrolafsson/herdr-github)** (this one) | Tags every space and agent with its branch's pull request (`#482 ✓ approved`), which Recap shows and groups its agents by. Opens straight on an agent's PR when Recap asks. |
| [herdr-linear](https://github.com/mrolafsson/herdr-linear) | Tags every space and agent with the Linear issue its branch is named for (`ENG-142 In Progress`), which Recap shows. Opens straight on that issue when Recap asks. |
| [herdr-snooze](https://github.com/mrolafsson/herdr-snooze) | Hides an agent, or a whole space, from herdr's Agents panel until a time you pick, or until the agent's done or its pull request is merged (from herdr-github's labels). Tags each snoozed agent with when it wakes (`💤 17:30`, `💤 till done`), which Recap shows. Opens straight on an agent when Recap asks, and opens Recap on one with `r`. |
| [herdr-vitals](https://github.com/mrolafsson/herdr-vitals) | How the machine is doing: right of Recap's tabs, a small graph of its CPU and a gauge of its memory, yellow from 80% and red from 90%, when there's room. A click on it opens Vitals on every machine you work on, and `esc` comes back. |
| [herdr-file-transfer](https://github.com/mrolafsson/herdr-file-transfer) | Files between the machine you're at and the server herdr runs on. Chooses the files a new Linear issue attaches (`ctrl+f` in herdr-linear's form): any on the server, or one brought over from your Mac, or from an iPad with Taildrop. |

- **Auto-discovery.** Each plugin asks herdr which others are installed and
  enabled. A link only appears when the plugin behind it is there; without
  it the same text is shown, as text, and its key is left out.
- **Clickable, and there and back.** herdr shows one popup at a time, so
  following a link turns the open popup into the other plugin's, in place and
  without a flicker, and `esc` there brings you back to where you were.
- **Shared look.** A title, a branch, a pull request and a key are
  drawn the same in all six, in herdr's theme colours.

See [Sidebar labels](#sidebar-labels) for the tags and [Opened from another popup](#opened-from-another-popup) for the convention behind the links.

## In the herdr TUI

### The lists

| tab                  | shows                                                         |
|----------------------|---------------------------------------------------------------|
| **Mine**             | your open PRs across every repo                               |
| **Review requested** | open PRs asking for your review (or your team's)              |
| **this repo**        | every open PR in the repo you opened the popup from; **ctrl+t** picks another |

Every tab is **grouped by whose move it is**, yours first, each group under
a heading with its count, newest first within it:

| group              | is                                                             |
|--------------------|----------------------------------------------------------------|
| **To review**      | (*Review requested* only) asked of you, and nothing wrong with it |
| **Needs fixing**   | checks failed, in conflict, or changes requested               |
| **Ready to merge** | approved; or no review asked of it and its checks passed       |
| **Waiting**        | on its reviewers, or on checks still running                   |
| **Drafts**         | not out for review yet, whatever its checks say                |

A PR is two lines. The first has its state (`●` open, `◌` draft), number
and title, and at the right edge whose it is, its repo and when it last
changed (`priya · notes-app · 3h`), each only where the tab doesn't already
say. Under it, each in its own colour:

```
sam/offline-conflicts · +318 −74 · approved · ✓ checks · worktree · ✓ Offline sync conflicts +1
```

its branch, lines added and removed, `conflicts`, `changes requested` or
`approved`, its checks (`✓ checks` passed, `✗ checks` failed, `● checks`
running), `auto-merge` if it's on, `worktree` if one exists, and last the
first of the agents that worked on it, counting the rest, the mark being
the agent's state (see [A PR's agents](#a-prs-agents)). The line above the
keys has the selected PR's labels, and the branch it's to be merged into.
The popup reopens on the tab you left.

**Another repo.** The third tab starts on the repo of the space you opened
the popup from, named on its first row: `⇄ owner/repo  change repo ›`.
Enter (or a click) on that row, **ctrl+t** from anywhere in the tab, or a
click on the tab itself opens the list of repos right there, **grouped by
organisation** (opened outside a repo, the tab starts on it):

- this space's org first, with its repo on top;
- then the orgs of your recent picks, your herdr spaces and your PRs;
- then every other repo you can reach on GitHub (your own, your orgs',
  ones you collaborate on; archived ones left out), most recently pushed
  first. They're fetched as the popup opens, and last time's list shows
  until they arrive.

Type to filter: an org's name keeps its whole group. Any `owner/repo` (or
`host/owner/repo`) that isn't listed is offered as typed. The pick lasts for
the popup; next time it opens on the space's repo again.

**Type to filter**: every word must match the number, title, repo, author,
branch, a label or one of its agents, so `sync bug` finds *#482
Offline edits…*.

GitHub takes a couple of seconds per page of PRs, so the popup shows the
lists from last time at once, marked *updating*, and swaps in the fresh ones
as they arrive; long lists fill in page by page (`Mine 20+`).

![Review requested](docs/images/review.png)

| key                | does                                     |
|--------------------|------------------------------------------|
| typing             | filter                                   |
| ↑ ↓, ctrl+p ctrl+n | move                                     |
| enter              | open the PR                              |
| ctrl+w             | open or create its worktree              |
| ctrl+s             | start: worktree, and a prompt for its agent |
| ctrl+o             | open on GitHub                           |
| ctrl+a             | go to its agent (a menu, if several)     |
| ctrl+t             | pick the repo tab's repo                 |
| ctrl+r             | refresh                                  |
| ← →, tab           | switch tabs (with text in the filter, ← → move within it) |
| pgup pgdn          | a page up or down                        |
| esc                | clear the filter, then close             |

### A pull request

![A pull request](docs/images/pr.png)

At the top: state, title, author, branch → base (and whether its worktree
exists), its agents, size, labels, reviewers (`✓` approved, `✗` changes requested, `○`
requested), a checks summary, and **Merge**: whether it can merge now, and
if not, the reason GitHub is holding it: a required check failing, changes
requested, a missing approval, conflicts, a branch that's behind.

Below, scrolling: the checks that failed or are still running (required
ones marked), the description, unresolved review threads with their file
and line, and the conversation, oldest first.

![A blocked pull request](docs/images/blocked.png)

| key            | does                                             |
|----------------|--------------------------------------------------|
| `w` or enter   | open or create the worktree                      |
| `s`            | start (see [Worktrees](#worktrees))              |
| `a`            | go to its agent, or pick one of several (see [A PR's agents](#a-prs-agents)) |
| `d`            | draft → ready for review, or back to draft       |
| `m`            | merge (see [Merging](#merging))                  |
| `o`            | open on GitHub                                   |
| `y` / `b`      | copy the URL / the local branch name             |
| ctrl+r         | reload the PR                                    |
| ↑ ↓, PgUp PgDn, space | scroll                                    |
| esc, ←         | back                                             |

### Mouse

Hover highlights a row, a click opens it, the wheel scrolls. A click on
an agent, in a row or on the PR screen, goes to the agent instead. The tabs, the
keys along the bottom (pills: the one under the pointer underlines) and menu
items are all buttons. The popup captures the mouse, so
to select text hold **⌥** while dragging.

## Merging

`m` lists what can happen now:

- **Merge now**, with each method the repo allows (merge commit, squash,
  rebase), when GitHub says it can merge. The method you used last in this
  repo comes first; before that, the one GitHub remembers for you.
- **Auto-merge when ready**, when it can't merge yet (checks running, a
  review missing) and the repo has auto-merge turned on.
- **Turn off auto-merge**, when it's on.
- **Update the branch with main**, when the repo requires branches to be up
  to date and this one is behind: it merges the base into the PR's branch,
  as GitHub's *Update branch* button does. Its checks run again, so it's
  blocked on them for a while rather than ready straight away. A fork's
  branch only when it lets maintainers push to it.

![Merging](docs/images/merge.png)

Picking one asks once more, naming exactly what will happen: *Squash-merge
#482 into main now?* The merge is pinned to the commit you were looking at:
if someone pushed since the screen loaded, GitHub refuses rather than merge
code you haven't seen (press ctrl+r and look again). Updating the branch is
pinned the same way.

A draft, a PR with conflicts, or a repo where you can't merge gets a line
saying so instead of a menu.

### Through a merge queue

Where the base branch has a **merge queue**, the queue merges the PR, by its
own method, when its turn comes, so `m` offers no methods:

- **Add it to the merge queue**, when it's ready (or only waiting on checks
  the queue runs anyway). It asks once more, and is pinned to the commit you
  were looking at, as a merge is.
- **Take it out of the merge queue**, once it's in.

The PR screen says where it is: *In the merge queue, 2nd in line: its checks
are running · about 9 min*, and before it joins, *Ready to merge: through the
merge queue*. In the lists it's under **Waiting**, marked `queued 2nd`, and
its sidebar badge ends in `queued`. The demo's `halcyon/sync-server` has a
queue: try #121.

After a merge, the popup offers to **delete the branch** on GitHub (unless
the repo does that itself, or the branch is in someone's fork) and to
**remove the worktree**, which closes its space. Neither is forced: a
worktree with uncommitted changes stays, and the popup says why.

## Worktrees

`w` takes you to the PR's worktree as its own herdr space:

- **Already there?** It's opened and focused.
- **Not yet?** It's created with `herdr worktree create`, so your worktree
  template (say from [herdr-plus](https://github.com/cloudmanic/herdr-plus))
  runs as usual.

**Which repo.** The plugin looks for a checkout of the PR's repo:

1. `"repos"` in the config (`"owner/repo": "~/code/app"`), if you mapped it;
2. the repo you opened the popup from, and every repo herdr has a space for,
   by their git remotes: any remote pointing at the repo counts, so a fork's
   clone with an `upstream` remote does too;
3. `<clone_root>/<owner>/<repo>` (`~/code/owner/repo` by default).

With none found, it offers to clone the repo there with `gh repo clone`.

**Which branch.**

- A PR from the repo itself: its own branch, fetched fresh from the remote
  and tracking it, so `git pull` and `git push` just work.
- A PR from a fork: `<fork owner>/<branch>` (say `alice/fix-typo`), so it
  can't collide with a branch of your own, fetched from `refs/pull/N/head`,
  and set to pull from and push to the fork (as `gh pr checkout` does; pushing
  works when the author lets maintainers edit).
- A local branch of that name already there (from a worktree you removed)
  is checked out as it is, never reset, so commits on it aren't lost.

### Start

`s` (ctrl+s in the list) opens the worktree and then types a prompt into the
new worktree's agent once it's ready (your worktree template starts it). By
default:

> Pull request {url} is checked out here. Read it with `gh pr view {number}
> --comments`, look at any failing checks, and tell me what's left to do.

Set your own with `start_prompt` (`{number}`, `{url}`, `{repo}`, `{branch}`
and `{title}` are filled in), for example `"/review-pr {url}"` for a Claude
Code skill of that name. Think twice before `{title}`: anyone who can open a
PR writes it, and the agent will read it as instructions.

Only a **new** worktree's own pane is prompted: an existing worktree's agent
may be mid-task. It waits up to `agent_wait_seconds` (90) for the agent, and
waits out a trust or approval dialog for you to answer. This runs in the
background after the popup closes; `background.log` in
`~/.local/state/herdr/plugins/herdr-github/` records what happened, not the
prompt.

## Sidebar labels

The plugin puts the PR of each space's branch on the space, and on each agent
pane in it, as herdr tokens. herdr only shows tokens you ask for, so add them
to your sidebar rows in `~/.config/herdr/config.toml`:

```toml
[ui.sidebar.spaces]
rows = [
  ["state_icon", "workspace", "$pr_badge"],
  ["branch", "git_status"],
]

[ui.sidebar.agents]
rows = [
  ["state_icon", "terminal_title_stripped"],       # what the agent is doing
  [{ token = "$prs", dim = true }, "workspace"],   # its PRs, then its space
]
```

and reload with `herdr server reload-config`. A space then reads

```
  ● HAL-212 Offline sync conflicts  #482 ✓ approved
    hal-212-offline-sync-conflicts
```

and an agent

```
  ○ Offline sync conflicts
    #482 ✓ approved #476 draft ● · notes-app
```

For an agent, `$prs` is the one token to use: its branch's PR and the others
it opened, as one list (see [A PR's agents](#a-prs-agents)). Give it a line
with little else on it: herdr shortens every token on a crowded line, and
the status is the first thing to go.

> **Attached to another machine?** With `herdr --remote`, the sidebar is
> drawn from the config of the machine you're sitting at, so the rows go in
> *its* `config.toml` (reload with the UI's *reload config* action), while
> the tokens come from the plugin on the machine the agents run on, so
> that's where the plugin has to be installed. Rows changed on the server
> change nothing you can see. (Key bindings for the plugin's actions are the
> other way round: they go in the server's config.)

| token        | example                        |
|--------------|--------------------------------|
| `$pr`        | `#482`                         |
| `$pr_badge`  | `#482 ✓ approved`, `#476 draft ●`, `#64 ✓ conflicts`, `#118 merged`, `… auto` |
| `$pr_state`  | `open`, `draft`, `merged`, `closed` |
| `$pr_checks` | `✓`, `✗`, `●` (open PRs)       |
| `$pr_review` | `approved`, `changes requested`, `needs review` (open PRs) |
| `$prs`       | `#482 ✓ approved #1103 ● #1101 ✓`: every PR that's this agent's, in one list: its branch's, then the open ones it opened (see [below](#a-prs-agents)) |
| `$prs_past`  | `#1155 #1146`: the PRs it opened that are since merged or closed, for two weeks after. Not for a row: it's what [herdr-recap](https://github.com/mrolafsson/herdr-recap)'s filter finds an agent by once its PR is done |

Style them like any token, e.g. `{ token = "$pr_badge", dim = true }`.

**How it stays current.** herdr runs `herdr-github tick` at startup and on
its events: switching space or pane, an agent changing state (often a push),
a space or worktree being created or opened. A tick within 3 seconds of the
last does nothing; otherwise it reads each space's branch from git and asks
GitHub, in one query per repo, only about branches whose answer is older than
`label_refresh_seconds` (60). The popup refreshes them straight after you
change a PR. **GitHub: refresh PR labels** does it on demand.

**Which PR.** The one for the branch the space is on, or the branch it pushes
to (its upstream), from the repo's `upstream` remote, else `origin`. An open
PR wins over closed ones; a fork's PR is matched to its fork's owner. The
default branch and `main`/`master` are skipped. When GitHub can't be reached,
labels keep what they last showed; they expire after two hours without a
tick.

Turn them off with `"labels": false` (the next tick clears them).

### A PR's agents

A space's branch has one PR, but an agent can open several, from branches
its space was never on, and more than one agent can work on a PR. Then
it's anyone's guess which agent to go to about it. So the plugin keeps
track of every agent that has to do with an open PR:

| | |
|---|---|
| **opened it**     | the Claude Code session that created the PR |
| **on its branch** | an agent, of any kind, in a space on the PR's branch |
| **worked on it**  | another Claude Code session that looked at, pushed to or commented on it |

- **In the popup**, the line under a PR's title ends with its first agent
  and counts the rest (`✓ Offline sync conflicts +1`), and the PR screen
  lists them, each with
  its state. **ctrl+a** (**a** on the PR screen) goes to the agent's pane,
  asking which when there are several; so does a click on an agent. Typing
  an agent's name filters to its PRs.
- **From an agent to its PR**, the other way round: **GitHub: this agent's
  pull request** (`herdr-github.pr`, `prefix+alt+p` in the
  [Install](#install) example) opens the popup straight on the PR of the
  agent you're in, without the lists: its branch's PR, or the one it
  opened. With several it asks which. With none it refreshes and looks
  again (a PR opened a moment ago isn't known yet), and failing that
  lands on this repo's list, saying so. From a PR opened this way **esc**
  closes the popup, and **←** goes to the lists.
- **A session that isn't in a pane** (you closed it, or it never ran in
  herdr) is listed too, dimmed, under the name Claude gave it. There's
  nowhere to go, so the key copies the command that resumes it:
  `cd … && claude --resume …`.
- **`$prs`** on an agent in the sidebar is every PR that's its own, as one
  list: its branch's PR first, as `$pr_badge` has it, then the open PRs it
  opened from elsewhere, newest first, each with its checks (`#1112 draft ✓
  #1103 ● #1101 ✓`). A PR that's both is there once; past three, the rest
  are counted (`+2`). A space has its agents' together. It's the one token
  an agent's row needs; `$pr` and `$pr_badge` are the branch's PR alone.
- **A PR that's merged or closed** leaves `$prs` (unless the agent is still
  on its branch) for **`$prs_past`**: numbers alone, newest first, kept for
  two weeks. It isn't meant for a sidebar row. It's there so an agent that
  has moved on to the next thing can still be found by the PR it finished:
  Recap's filter matches it without showing it. For those two weeks the
  popup still lists the PR's agents too.

There's nothing to set up. Claude Code notes in each session's transcript
the PRs it works on and the ones it created (its own commands or a
subagent's, typically `gh pr create`), and herdr knows which session each
pane runs. Each tick reads what's new in the transcripts: the first time,
those of the last 30 days. A PR stays with its pane through `/clear`, and
follows its session into another pane when you resume it there. Once a PR
is merged or closed it's forgotten.

![A pull request's agents, to choose between](docs/images/agents.png)

It can't know about a PR opened in the browser or on another machine, and
of another kind of agent it knows only the branch it's on. Turn it off with
`"agent_prs": false`.

### Opened from another popup

[herdr-recap](https://github.com/mrolafsson/herdr-recap) opens this popup on
an agent's pull request (a click on the PR in its list). herdr shows one
popup at a time, so Recap's popup becomes this one in place, and says where it was in
`HERDR_POPUP_RETURN`: a popup as JSON, in the shape of herdr's
`plugin.pane.open` (`{"plugin_id", "entrypoint", "width", "height", "env"}`).
Opened that way, `esc` reads *back* and opens that popup again; `ctrl+c`, or
going to a worktree or an agent, just closes.

Any plugin can open this one the same way: `HERDR_GITHUB_PANE` and
`HERDR_GITHUB_SPACE` are the agent whose pull request to open on, as the
`pr` action sets them.

## Configuration

Everything is optional. Put `config.json` in the plugin's config directory
(`herdr plugin config-dir herdr-github`, usually
`~/.config/herdr/plugins/config/herdr-github`):

```json
{
  "hosts": ["github.com", "github.example.com"],
  "repos": { "acme/app": "~/work/app" },
  "clone_root": "~/code",
  "start_prompt": "/review-pr {url}",
  "agent_wait_seconds": 90,
  "labels": true,
  "label_refresh_seconds": 60,
  "agent_prs": true,
  "theme": "dark"
}
```

| key                     | default          | what it does |
|-------------------------|------------------|--------------|
| `hosts`                 | `["github.com"]` | Hosts the Mine and Review tabs search, e.g. add your GitHub Enterprise host. Sign in to each with `gh auth login --hostname …`. |
| `repos`                 | none             | `"owner/repo"` (or `"host/owner/repo"`) → checkout, for one the plugin can't find by its remotes (an SSH host alias, say). |
| `clone_root`            | `~/code`         | Where a repo with no checkout is cloned, as `<clone_root>/<owner>/<repo>`, after asking. |
| `start_prompt`          | see [Start](#start) | What **start** types into the new worktree's agent. |
| `agent_wait_seconds`    | `90`             | How long **start** waits for that agent. |
| `labels`                | `true`           | The sidebar tokens. |
| `label_refresh_seconds` | `60`             | How old a branch's PR status may get before a herdr event asks GitHub again. |
| `agent_prs`             | `true`           | Keeping track of each PR's agents, by reading Claude Code's transcripts (see [A PR's agents](#a-prs-agents)). |
| `theme`                 | asks the terminal | `dark` or `light`, if the automatic choice is wrong. |

A copy is in [`config.example.json`](config.example.json).

### Colours

Colours follow herdr's own `[theme]` (how it's read is described in
[herdr-linear](https://github.com/mrolafsson/herdr-linear#colours)).
The herdr plugins (herdr-github, herdr-linear, herdr-recap, herdr-snooze)
draw the same thing the same way, from one shared file (`look.go`):

| what                 | how it's drawn |
|----------------------|----------------|
| a title              | the theme's brightest text; bold where it heads a card or a screen |
| prose                | a step quieter |
| where, when, who     | dim |
| a branch             | mauve |
| a pull request       | its state's colour: draft peach (work in progress), open green, merged mauve, closed red |
| an agent             | its state's mark and colour, as in herdr's sidebar: `◉` needs you, `◔` working, `●` done, `✓` idle |
| counts               | yellow; added green, removed red; tasks teal |
| keys                 | pills along the bottom, coloured by what they do: green goes somewhere, peach changes something, blue changes what you see, grey leaves |
| a screen's subject   | wears its state as a pill (`Open`, `Draft`, `In Review`) |

**The `terminal` theme.** herdr's `terminal` theme draws with your terminal's
16 colours, and gives several of these the same one (branches and dim text
the body's grey). The plugins spread them over the terminal's other colours
(branches on magenta, dim on bright black), so the list reads the same as on
a named theme. A colour you set yourself in `[theme.custom]` wins.

**A narrow popup.** When the keys don't fit, their labels shorten first (`^o
open in Linear` becomes `^o open`), then the keys you'd miss least are left
out. A key that isn't shown still works.

## Privacy and security

**What leaves your machine.** GraphQL requests to GitHub (`api.github.com`,
or `https://<host>/api/graphql` for Enterprise), and `git fetch` / `gh repo
clone` for worktrees. Nothing else: no analytics, no telemetry.

**Sign-in.** The plugin asks `gh auth token` for each host when it needs one
(`GH_TOKEN`/`GITHUB_TOKEN`, or `GH_ENTERPRISE_TOKEN` for Enterprise, win if
set, as they do for gh). It keeps no token: nothing is written to disk or
passed on a command line. Signing out of gh signs it out too.

**What's written to GitHub**, and only when you press the key and, for
merges, confirm: draft/ready, merge, auto-merge on/off, deleting a merged
PR's branch.

**What's kept locally**, in `~/.local/state/herdr/plugins/herdr-github/`,
readable only by you: `lists.json` (the last lists, shown while fresh ones
load: numbers, titles, branches, states; and the repos you can reach, for
the repo list), `labels.json` (each branch's PR status, for the labels;
and for each open PR your Claude sessions worked on, its number and status
and those sessions: their IDs, titles, directories and panes),
`prefs.json` (your last tab, your merge method per repo, repos you picked
lately) and the background log.

**What's read locally.** Claude Code's session transcripts
(`~/.claude/projects`, or under `CLAUDE_CONFIG_DIR`) written to in the last
30 days, to find the PRs each session worked on. Only Claude Code's own
notes are looked at (a PR linked to the session, a command's result marked
as having created one, the session's title), and only the PR's URL, the
time, the title and the session's directory are kept. Nothing from them
leaves your machine except the PR's number, when GitHub is asked for its
status.
`"agent_prs": false` stops it.

**Text from GitHub.** Titles, bodies, comments, names and labels are written
by other people, so every string has terminal escape sequences and control
characters removed on arrival, and every frame is checked again before it's
drawn. A PR title can't rewrite your clipboard, retitle your terminal or fake
what's on screen. Branch names go to GitHub as GraphQL variables, never
spliced into a query. "Open on GitHub" only opens `https` links on your
configured hosts.

## Troubleshooting

**Over SSH, "open" copies the link instead.** A browser opened on the remote
machine wouldn't be in front of you (on Linux with no display there's none to
open), so `o` puts the link on your clipboard to open yourself. Over SSH,
copying (`o`, `y`, `b`) asks your terminal to set its clipboard (OSC 52)
rather than the remote machine's. If the popup says it sent something to your
terminal's clipboard but it isn't there, your terminal, or a multiplexer
between you and it, doesn't allow that.

**The key does nothing.** `herdr plugin action invoke herdr-github.open`
tells you whether the plugin works. If it does, it's the key: with
`prefix+alt+g`, turn on your terminal's Option-as-Meta setting (see
[Install](#install)); otherwise check herdr doesn't already use it.

**"GitHub isn't connected".** gh has no working sign-in for that host. Press
enter in the popup to run `gh auth login` there, or run
`gh auth status` / `gh auth refresh` yourself. `bin/herdr-github status`
shows what the plugin sees.

**No labels in the sidebar.** Add the tokens to your rows (see
[Sidebar labels](#sidebar-labels)) and reload the config: on the machine
you're sitting at, if you're attached to another with `herdr --remote`. Then
`herdr plugin action invoke herdr-github.labels` refreshes them and toasts
what went wrong, if anything. A space on `main`, or on a branch with no PR,
has none.

**An agent's PR isn't in `$prs`, or a PR has no agent.** `$prs` shows PRs
that are open and that the agent opened itself (merged and closed ones are
in `$prs_past`). A session is matched to
its pane by the session ID herdr knows (`herdr agent list` shows an
`agent_session`; `herdr integration install claude` if not); without one
it's listed as having no pane. Either appears at the next tick: when the
agent stops, or you switch pane.

**"no checkout of owner/repo found".** Open the popup from inside the repo,
let it clone, or map it under `"repos"`. A remote using an SSH host alias
(`git@work-github:owner/repo`) isn't recognised as GitHub; map it.

**Merging says the head branch was modified.** Someone pushed after you
opened the PR. ctrl+r, look, and merge again.

**The agent didn't get the prompt.** See `background.log` (under
[Start](#start)). Usually: the worktree already existed, no agent starts in
new worktrees, or it took longer than `agent_wait_seconds`.

**The popup opens and closes at once.** Look in
`herdr plugin log list --plugin herdr-github`, and rebuild with
`scripts/build.sh`. `HERDR_GITHUB_DEBUG=/tmp/gh.log` logs the list loads.

## How it works

herdr plugins are commands plus popups. **GitHub: pull requests** runs
`herdr-github action open`, which opens the plugin's `picker` pane as a popup
with the invoking space's directory; the popup is the same binary running a
[Bubble Tea](https://github.com/charmbracelet/bubbletea) interface.

- **GitHub**: GraphQL over HTTPS with gh's token. One search per tab and
  page, one query per PR screen, one per repo for the labels.
- **herdr**: its local socket: `worktree.list/create/open/remove`,
  `workspace.list` and `pane.list` to find checkouts and spaces,
  `workspace.report_metadata` and `pane.report_metadata` for labels,
  `plugin.pane.open` for the popup, `agent.prompt` for start, `agent.focus`
  to go to an agent.
- **Start** and the post-change label refresh run detached, so the popup can
  close.

| file           | what's in it                                           |
|----------------|--------------------------------------------------------|
| `main.go`      | commands and actions                                   |
| `github.go`    | gh token, GraphQL queries and mutations                |
| `source.go`    | the tabs' searches across hosts, paging                |
| `repos.go`     | remotes, finding a repo's checkout, cloning            |
| `worktree.go`  | PR worktrees (forks too), start, removal               |
| `labels.go`    | the sidebar tokens (`tick`)                            |
| `agents.go`    | each PR's agents, from Claude Code's transcripts and herdr's panes |
| `tui.go`       | lists, rows, filter, menus                             |
| `groups.go`    | the groups a list is in, their headings, the tab bar   |
| `detail.go`    | the PR screen, draft, merge and clean-up               |
| `repopicker.go` | the repo tab's repo picker                            |
| `cache.go`, `prefs.go` | the lists from last time, remembered choices   |
| `look.go`      | the look shared with herdr-linear and herdr-recap: colours, pills, keys (kept identical in all three) |
| `markdown.go`, `sanitize.go`, `theme.go`, `mouse.go` | rendering, safety, herdr's theme, mouse |
| `clipboard.go`, `platform_*.go` | the clipboard and the browser, on macOS and Linux |
| `demo.go`      | the fictional demo org                                 |

## Development

```sh
sh scripts/build.sh                 # build bin/herdr-github
go test ./...                       # the suite
bin/herdr-github picker --demo      # the picker in any terminal
sh scripts/screenshots.sh           # docs/images from the demo (needs freeze; sips or ImageMagick to size them)
```

The tests cover the GraphQL client against a fake GitHub (paging, errors,
sign-in, merge pinning, variables not splicing), every screen and key on the
demo, the mouse, the list cache, and, on real temporary git repositories
with a stand-in herdr, finding checkouts, worktrees for same-repo and fork
PRs, and the labels tick from branch to token.

Releasing works as in herdr-linear: bump `version` in `herdr-plugin.toml`,
move the `CHANGELOG.md` entry, commit, and push a matching `v` tag.

## License

MIT. See [LICENSE](LICENSE).
