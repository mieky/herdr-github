# Changelog

## 0.10.1 - 2026-11-01

(0.10.0 was tagged but never released: its tests failed on macOS.)

- **In the Herdr app.** A GitHub mod (in `app/`) brings the popup to the
  Herdr app: ⌘G, the same three tabs and groups as cards, a PR's screen
  with whether it can merge and why not, its checks, reviewers, agents and
  the conversation; merge, auto-merge, the merge queue and draft ↔ ready;
  `w` for its worktree in a space and `s` for one with Claude Code already
  asked to look at it. It finds the machine where gh is signed in, makes
  worktrees on the machine with the checkout, and fetches the herdr-github
  it needs onto each by itself. Recap's pull requests open in it.
  `sh install.sh --app` installs it (`--tui` the plugin; no flag, both).

- **`herdr-github app JSON`**: what the mod runs. One request in (list,
  detail, act, worktree, repos, sessions, status, or a background job's
  answer), one JSON answer out, from the plugin's own code: the merge menu
  the popup offers is the one the app offers.

## 0.9.0 - 2026-11-01

- **Merge queues.** Where a PR's base branch has a merge queue, `m` adds it
  to the queue (after a confirmation, pinned to the commit on screen) or
  takes it out, instead of offering merge methods the queue would ignore.
  The PR screen says its place, what's happening to it and GitHub's estimate
  (*In the merge queue, 1st in line: its checks are running · about 9
  min*); the lists mark it `queued 1st` under Waiting, and its sidebar badge
  says `queued`. The demo's sync-server has a queue, with #121 ready to join.

- **A branch's pull request, for programs that can't see the labels.**
  `herdr-github prs '[{"remote": …, "branch": …, "head": …, "owner": …}]'`
  prints, as JSON in the same order, the PR each branch has: its number,
  link, state, checks, review and badge (`#482 ✓ approved`), the same PR
  its label would show. A branch is named by its repo's remote URL, not a
  checkout, so the machine where gh is signed in can answer for agents on
  any machine; it's what the Herdr app's Recap uses. An answer under a
  minute old is reused, so asking every few seconds is fine, and
  `--force` asks GitHub again. A branch that can't be looked up says why
  in its own entry; the others are answered as usual.

## 0.8.5 - 2026-10-20

- **herdr-vitals joins the plugins that work together.** The README's
  table has it: how your machines are doing, and in Recap's header, the
  machine's CPU and memory, a click from Vitals.

## 0.8.4 — 2026-10-13

- **herdr-file-transfer joins the plugins that work together.** The README's
  table has it: files between the machine you're at and the server, and the
  files a new Linear issue attaches.

## 0.8.3 — 2026-10-13

- **The README says what herdr-snooze can now wait for**: until an agent's
  done, or until its pull request is merged, from this plugin's labels.

## 0.8.2 — 2026-10-12

- **herdr-snooze joins the plugins that work together.** The README's table
  has it, with Recap's Snoozed tab and its `ctrl+s` / `ctrl+w`; the shared
  look (`look.go`) is now kept the same in all four.
- **The screenshots script stops, rather than drawing an emoji as a box**,
  when the machine has no `rsvg-convert`, JetBrains Mono or emoji font, and
  says what to install.

## 0.8.1 — 2026-10-12

- The tabs no longer say **GitHub** before them: the popup's frame already
  does. The tab you're on lines up with the keys along the bottom.

## 0.8.0 — 2026-10-12

- **An agent can still be found by a PR it's done with.** A PR an agent
  opened used to be forgotten the moment it was merged or closed, so once
  the agent moved to another branch nothing tied the two together. It's now
  remembered for two weeks, in a new token, **`$prs_past`** (`#1155 #1146`:
  numbers alone, newest first). The token isn't for a sidebar row:
  herdr-recap 0.5.4 searches it without showing it, so filtering Recap by a
  merged PR's number finds the agent that opened it. `$prs` is unchanged.
- For those two weeks the popup lists a merged or closed PR's agents too.
- The first tick after upgrading reads the last month's transcripts again,
  for the PRs earlier versions let go; it takes a few seconds, once.

## 0.7.0 — 2026-10-12

- **The lists are grouped by whose move it is**, yours first, as Recap's
  agents are: *Needs fixing* (checks failed, in conflict, changes
  requested), *Ready to merge*, *Waiting* (on reviewers, or checks still
  running) and *Drafts*; *Review requested* leads with *To review*. Each
  group has a heading in its colour with its count, and the heading of
  whatever you've scrolled to stays at the top. Within a group, newest
  first. *Mine* used to be grouped by repo; a PR's repo is now on its row.
- **A PR is two lines.** Its title has the line to itself, with whose it
  is, where and when at the right edge (`priya · notes-app · 3h`). Under
  it, each in its own colour: its branch, `+318 −74`, its review, its
  checks, `auto-merge`, `worktree`, and its agent, which a click still goes
  to.
- **Said, not marked.** Checks are `✓ checks`, `✗ checks`, `● checks`, a
  worktree is `worktree` (it was `⌥`), and `changes requested` and
  `auto-merge` are spelled out. A PR in conflict shows its review too.
- **The line above the keys is about the selected PR**: its labels, and the
  branch it's to be merged into.
- **A header like Recap's**: the popup's name, the tab you're on as a pill,
  and room between the tabs, the filter and the list. Too narrow for the
  tabs, the name goes, then the labels shorten.
- **Fewer keys along the bottom.** The filter says how to change tabs
  (`← →`; `tab` still works), and `esc` says when it will clear the filter
  rather than close.
- **The cursor stays on the first PR** when a fresher list changes the
  order, until you've moved it; after that it stays with the PR you chose.
  Coming back from a PR's screen, it's on that PR wherever it's now listed.
- The spinner is `◔ ◕`: braille could come from a fallback font, at the
  wrong width.
- **pgup / pgdn** move by what fits, whatever the rows' heights.

## 0.6.0 — 2026-10-11

- **`esc` goes back to the popup you came from.** herdr-recap opens this
  popup on an agent's pull request; closing it with `esc` now opens Recap
  again, on that agent. Any plugin can ask for the same with
  `HERDR_POPUP_RETURN` (see *Opened from another popup* in the README).

## 0.5.0 — 2026-10-06

- **One look, shared with herdr-linear and herdr-recap.** A title, a branch,
  a pull request, an agent and a key are drawn the same in all three (see
  *Colours* in the README). Here: a PR's number and mark wear its state's
  colour (draft peach, open green, merged mauve, closed red), its screen
  shows the state as a pill, branches are mauve, and an agent shows its
  state's mark (`◉ ◔ ● ✓`) in place of `▸`.
- **Keys are pills.** The keys along the bottom are solid chips, coloured by
  what they do and grouped by colour; the one under the pointer underlines.
  Too many for the popup's width, and labels shorten, then the least missed
  keys are left out.
- **The `terminal` theme keeps its hierarchy.** Branches and dim text no
  longer share the body's grey, and a title is the brightest text.
- **ctrl+a** goes to a PR's agent from the list, matching **a** on the PR
  screen. ctrl+g still works.
- Fixed: a click on the agents key, or on `← lists`, typed the key's name
  into the filter instead of pressing it.

## 0.4.0 — 2026-10-06

- **Straight to an agent's PR.** A new action, **GitHub: this agent's pull
  request** (`herdr-github.pr`; bind it to `prefix+alt+p`, say), opens the
  popup on the PR screen of the agent you're in: its branch's PR, or one it
  opened. With several it asks which. With none it refreshes and looks
  again, then lands on this repo's list, saying so.
- Opened that way, the popup never shows the lists on the way to the PR,
  the PR's keys work while they load behind it, and **esc** closes the
  popup (**←** goes to the lists).
- The agent marker is `▸`: `◆` is a merged PR's.
- A long title no longer wraps a menu's line.

## 0.3.0 — 2026-10-06

- **Whose PR is this?** Every open PR knows its agents: the Claude Code
  session that opened it, others that worked on it, and any agent in a
  space on its branch. In the popup a PR's row names the first and counts
  the rest, the PR screen lists them with their state, and **ctrl+g**
  (**a** on the PR screen) or a click goes to one. A session that isn't in
  a pane any more is listed too, and the key copies the command that
  resumes it. An agent's name filters to its PRs. Nothing to set up;
  `"agent_prs": false` turns it off.
- **`$prs`**, a new sidebar token: every PR that's an agent's, in one list.
  Its branch's PR with its status, then the others it opened, whatever
  branch its space is on (`#1112 draft ✓ #1103 ● #1101 ✓`). It's the one
  token an agent's row needs.
- The README says where sidebar rows go when you're attached to another
  machine with `herdr --remote`: in the config of the machine you're
  sitting at.

## 0.2.0 — 2026-09-26

- Linux. The browser opens with `xdg-open`; copying uses `wl-copy`, `xclip`
  or `xsel`. Releases include Linux binaries, and `scripts/build.sh`
  downloads them on machines without Go.
- Over SSH, or on Linux with no display, `o` copies the link instead of
  opening a browser you couldn't see, and every copy asks your terminal to
  set its clipboard (OSC 52) instead of the remote machine's.
- Opening the browser and copying have time limits, so neither can hang the
  picker.

## 0.1.1 — 2026-09-25

- Opening the picker while an earlier one is still up (left open on another
  herdr client, where you can't see it) closes that one and opens it where
  you are, instead of failing with *a popup pane is already open*. Another
  plugin's popup is left alone, with a toast saying so.
- The popup's size is in the manifest, so it's the same however it's
  opened.

## 0.1.0 — 2026-09-24

First version.

- **Pull requests in a popup**: Mine, Review requested, and a repo tab that
  starts on the repo of the space you opened it from. Last time's lists show
  at once while fresh ones load, page by page.
- **Any repo**: the repo tab's first row (`⇄ owner/repo  change repo ›`, or
  **ctrl+t**) opens the repos you can reach, grouped by organisation, or
  takes a typed `owner/repo`.
- **The PR screen**: merge readiness and why not, checks needing attention,
  reviewers, the description, unresolved review threads and the
  conversation.
- **Draft ↔ ready**, and **merge** with any method the repo allows (pinned to
  the head commit you saw, confirmed before it happens), or auto-merge; then
  delete the branch and remove the worktree.
- **Worktrees** for any PR, forks through `refs/pull/N/head`, in the repo's
  checkout found by its remotes, or cloned; **start** also prompts the new
  worktree's agent.
- **Sidebar labels**: `$pr`, `$pr_badge`, `$pr_state`, `$pr_checks` and
  `$pr_review` on each space and its agents, kept current on herdr's events.
- A fictional **demo** org. Signs in through gh.
