# mindskein

One command for the question you ask every morning: **what are my priorities, what's running elsewhere, and where did we leave off?**

```
PRIORITIES
  !1  Installer rewrite       traffic lands Wednesday, needs the signed build
  !2  Onboarding email        draft exists, unsent
      Billing reconciliation  blocked on the export format

  4 more in the backlog (--all)

LIVE SESSIONS
  a3b12c9f  Installer rewrite    acme-cli    running          12m  (Edit)
  e0cc146a  Onboarding email     notes       waiting        1h04m  (idle_prompt)

  2 sessions · 1 running · 3 ended hidden (--all)

WHERE WE LEFT OFF
  acme-cli · signing   2026-08-21 23:40  the notarization step still fails on…
  notes                2026-08-21 18:12  rewrite the second paragraph, it's flat
  acme-api             2026-08-20 21:05  (no prompt recorded)
```

Three sections, three sources: a markdown file you already keep, a registry of live Claude Code sessions, and the last handoff from each piece of work.

## What it is

A single Go binary and four Claude Code hooks. No daemon, no database, no server, no account. The hooks record what your sessions are doing into `~/.mindskein/`; the CLI reads it back.

It exists because the alternative is asking Claude the same four questions every morning and stitching the answers together by hand.

## Install

**With Go** (needs Go 1.23+):

```sh
go install github.com/swilgosz/mindskein/cmd/mindskein@latest
```

**From a release:** [Releases](https://github.com/swilgosz/mindskein/releases)
carries archives for macOS, Linux and Windows on amd64 and arm64, with a
`checksums.txt` to verify them against:

```sh
tar xzf mindskein_0.1.0_darwin_arm64.tar.gz
shasum -a 256 -c checksums.txt --ignore-missing
mv mindskein /usr/local/bin/
```

macOS will quarantine a binary downloaded through a browser; `xattr -d
com.apple.quarantine mindskein` clears it, or download with `curl` instead.
The archives are not notarised.

**Homebrew:** not yet. It is worth a tap once there is any evidence someone
other than the author wants this.

## Set up

```
mindskein init
```

That registers all four hooks in `~/.claude/settings.json` and writes a starter
config. It backs the file up first, only touches its own entries, and leaves
the rest of your settings — including hooks from other tools — exactly as it
found them. Run it again any time; it is idempotent, and it repairs a
registration written by an older version instead of adding a second one.

`mindskein init --dry-run` shows what it would change without changing it, and
`mindskein init --uninstall` removes the hooks again.

Then restart Claude Code, and point it at your priorities file.

### 1. Point it at your priorities file

`mindskein` does not guess where your notes live, because a wrong guess prints
an empty list, which looks exactly like a quiet day. Write
`~/.mindskein/config.toml`:

```toml
[priorities]
file = "~/notes/PRIORITIES.md"  # absolute, or a leading ~

[status]
hide_after = "7d"         # drop sessions quiet longer than this; "0" keeps all
```

A priority is a checkbox **and** a level token:

```markdown
- [ ] !1 Installer rewrite - traffic lands Wednesday
- [ ] !2 Onboarding email
- [x] !1 Fix the login bug        ← ticked, so it stops showing
```

The full contract — what splits a label from its note, how wikilinks resolve,
what is deliberately not supported — is in [`docs/priorities-format.md`](docs/priorities-format.md).

### 2. Check it is recording

Open a session, run a tool, then:

```
mindskein status
```

The session should be listed. If it is empty, check what was registered with
`mindskein init --dry-run` — it prints the current state without touching
anything.

<details>
<summary>What <code>init</code> writes, if you would rather do it by hand</summary>

```json
{
  "hooks": {
    "PreToolUse": [
      { "hooks": [{ "type": "command", "command": "/absolute/path/to/mindskein hook pre-tool-use", "timeout": 5, "async": true }] }
    ],
    "Notification": [
      { "matcher": "idle_prompt|permission_prompt|agent_needs_input",
        "hooks": [{ "type": "command", "command": "/absolute/path/to/mindskein hook notification", "timeout": 5, "async": true }] }
    ],
    "Stop": [
      { "hooks": [{ "type": "command", "command": "/absolute/path/to/mindskein hook stop", "timeout": 5, "async": true }] }
    ],
    "SessionEnd": [
      { "matcher": "clear|resume|logout|prompt_input_exit|other",
        "hooks": [{ "type": "command", "command": "/absolute/path/to/mindskein hook session-end", "timeout": 5, "async": true }] }
    ]
  }
}
```

The path is absolute because a hook runs with no `PATH` guarantee. `async`
keeps the hook out of the critical path of the event. Keep the `timeout` —
leaving it out does not mean "no timeout", it means the Claude Code default,
which is far longer than anything this should ever need.

</details>

## Use

```sh
mindskein brief         # all three sections — the morning read
mindskein status        # live sessions only — the mid-day check
mindskein priorities    # the priorities file only
mindskein prs           # pull requests waiting on you, from every org
mindskein prs decline   # hide a PR for 7 days; prs done hides it until it changes
mindskein usage         # token spend and what to improve in your setup
mindskein resolve       # mark a finding fixed; unresolve undoes it
mindskein serve         # the dashboard in a browser, on 127.0.0.1
mindskein prune         # delete state past its retention horizon
mindskein init          # register the hooks; --uninstall removes them
mindskein upgrade       # build the newest version with Go and swap it in
```

`--all` widens every section: the `!3` backlog, sessions that have ended or
aged out, and every workstream rather than the newest few.

Set `MINDSKEIN_PROJECT` before launching Claude to name a workstream that spans
several folders and sessions. It is the only grouping key that does, and
`usage` reports a session's cost under it too.

### Usage

`mindskein usage` reports the last 30 days: API-equivalent cost by model and
workstream, cache hit rate, friction (interrupts, denials, compactions, hook
errors), then **FIX FIRST** — failing hooks, skills and MCP servers unused in
90 days and what they cost every session, context bloat, oversized tool
results, subagents on top-tier models, heavy instruction files.

The `Stop` hook keeps it current, reading only what each turn appended. For
sessions from before the hooks were installed, run once:

```sh
mindskein usage --backfill            # ~/.claude and $CLAUDE_CONFIG_DIR
mindskein usage --backfill ~/.claude-personal
```

Every Claude config directory lands in one store and keeps its origin: the
report covers all of them with a per-account line, and `--root
~/.claude-personal` reads one alone.

`--days N` changes the range (`0` is all time), `--all` prints every finding,
and `--json` prints the report as the machine-readable contract other surfaces
read.

Each finding shows an id that stays the same from run to run, such as
`[hook-failures]` or `[unused-skills]`. Findings that name things (hooks,
skills, agents, MCP servers, subagent rows, instruction files) carry items:
`--all` lists every one with its address, `<id>:<key>`, and `--json` gives
`id` and `items` (`key`, `text`) beside the capped `evidence`.

Once you have fixed a finding, resolve it so it stops topping the list:

```sh
mindskein resolve hook-failures --note "removed the plugin"
mindskein resolve 'hook-failures:PostToolUse:Bash :: node "…/fix.mjs"'   # one item
mindskein resolve --list        # what is resolved, when and why
mindskein unresolve hook-failures
```

`resolve` takes an id, or an item as `<id>:<key>` exactly as `--all` prints it,
from the report `usage` would print with the same `--days`, `--since`,
`--until` and `--root` (30 days by default); anything else is an error listing
the valid ids. `big-results`, `context-bloat`, `cache-writes` and
`skill-listing` list no items and resolve only as a whole. Resolving it again
replaces the time, note and recorded size. Resolutions hold for every config
root. Resolving a finding that lists things covers only what it lists now: a
skill or file that shows up later is reported as new.

When a resolved finding comes back depends on its kind:

- **Events** (`hook-failures`, `big-results`, `top-tier-subagents`): a newer
  occurrence, and the finding then counts only the new ones.
- **Rates** (`context-bloat`, `cache-writes`, `skill-listing`): the rate over
  the sessions started after the resolve time crosses the threshold again; the
  title shows that rate.
- **Unused** (`unused-skills`, `unused-agents`, `unused-mcp`): the item is used
  again. That use clears the resolution, which `usage` then drops from
  `resolved.json`, so a later unused stretch is reported as new.
- **File size** (`heavy-instructions`, `long-memory-index`): the file grows more
  than 10% past its size (tokens, or lines for a memory index) when resolved.

A finding with some items resolved shows the rest, its title, severity and
saving counted from them. The report ends with how many findings and items are hidden (`3 resolved
· mindskein resolve --list`), `--json` lists each resolution under `resolved`
with how much it hides, and `--include-resolved` reports as if nothing were
resolved, for before/after comparisons.

To measure a change, compare fixed windows rather than "the last 30 days":

```sh
mindskein usage --since 2026-10-05 --until 2026-11-01   # before
mindskein usage --since 2026-11-02 --until 2026-11-29   # after
```

Both days are included; `--until` alone ends the `--days` window on that day.
The `per run` line shows what one run of each costly subagent type cost, the
number a total hides when a cheap agent runs far more often than an expensive
one. Unused-item findings always look back from today, since they describe the
setup as installed now. Costs use API list prices: on a subscription they are the value consumed,
not the bill.

### Pull requests

`mindskein prs` lists the pull requests that wait on you, from every org and
repository your GitHub account reaches, in these groups:

- **Waiting on you**: your review is requested.
- **Yours**: your open PRs, only when they are blocked on you: failing CI
  (`fix`), changes requested or new comments (`reply`), approved (`merge it`),
  or a draft an agent opened (`review it`). One with green CI and nothing new
  waits on someone else and is left out.
- **Watching**: open PRs where a notification says you were mentioned or
  commented, and your review is not requested.
- **Declined**: PRs you declined, until they come back; shown only while it
  holds one.
- **Done this week**: your PRs merged or closed, PRs you reviewed, and PRs you
  marked done, in the last 7 days.

```
PULL REQUESTS

Waiting on you (1)
  acme/api#12  Add a rate limiter  120 lines  2d  review requested  https://github.com/acme/api/pull/12
```

`--json` prints the same groups for agents: each PR with its `ref`, `url`,
owner, repo, number, title, author, changed lines, update time, draft flag,
group, reason, `space` and `rank`, plus whether its review is asked of you by
name, whether it is a security fix and whether a notification is on it.
`--space <name>` narrows the list, or the JSON, to one Life Space.

It reads GitHub through the [`gh` CLI](https://cli.github.com) you are
signed in to, and only reads: it never reviews, comments or merges. Each call
has a timeout. The last good result is kept in `~/.mindskein/prs.json` and
reused while it is younger than `cache_age`; when a refresh fails, the cached
list prints with its age and the reason. With no cache, one line says what is
wrong (gh not installed, not signed in, offline, rate-limited) and how to fix
it.

"New comments" means the latest comment or review is someone else's and came
after your last commit. A draft counts as opened by an agent when its body or
head branch contains `agent_marker`; without one, every draft of yours does.

To take a PR off the list, decline it or mark it done. Either takes
`owner/repo#N` or the PR's URL, and only writes `~/.mindskein/prs-marks.json`:
nothing is sent to GitHub.

```sh
mindskein prs decline acme/api#12
mindskein prs done https://github.com/acme/web/pull/88
```

- **Declined** PRs stay out for 7 days, listed under Declined, then come
  back if still open. Declining asks GitHub nothing.
- **Done** PRs stay out until they change: new commits, or your review newly
  requested. `done` reads the PR's head commit to tell. When GitHub cannot be
  reached and no list is cached, the PR is still marked, and comes back
  once GitHub shows it updated after you marked it.

Marking a PR again replaces its earlier mark. `prune` drops the marks that no
longer hide anything once they are a week old: every decline, and a done mark
whose PR the last fetched list shows changed, merged or closed.

Inside each group, PRs are ranked, first to last:

1. Your review is asked of you by name, not through a team.
2. Yours, blocked on you.
3. Security fixes under 200 changed lines: `CVE-` or `GHSA-` in the title,
   or in the body of a dependency bump.
4. Fresh PRs, updated in the last 5 days, from your team's authors or repos.
5. Everything else.

Equal ranks print oldest first; Declined and Done this week are not ranked and
print newest first. A notification on a PR (you were mentioned, or a comment
you take part in) bumps it one tier and marks it 🔔.

With Life Spaces configured, every PR goes to the first space one of whose
patterns matches its repository, or to **Other**; spaces print in the order
written, Other last. Without any, the list is not split. Skip rules drop the
PRs they match before anything is ranked; a rule with several patterns needs
all of them to match.

Repo patterns are an owner (`acme`, every repository it has) or
`owner/repo`; author and title patterns match the whole login or title. In
all of them `*` stands for any run of characters, and case is ignored. Every
key is optional.

```toml
[reviews]
cache_age = "10m"          # reuse the last result this long; "0" always asks GitHub
agent_marker = "## Spec"   # text in an agent's draft body or branch name

[reviews.team]
authors = ["ann", "bob"]   # whose fresh PRs rank above the rest
repos = ["acme/api"]

[[reviews.spaces]]
name = "Work"
repos = ["acme", "globex/web"]

[[reviews.spaces]]
name = "Side projects"
repos = ["me"]

[[reviews.skip]]
repo = "acme/legacy-*"

[[reviews.skip]]
title = "wip*"
unless_review_requested = true   # keep it when your review is asked of you by name
```

### Dashboard

`mindskein serve` opens the same answers in a browser at
`http://127.0.0.1:8787`: usage, the brief, setup (are the hooks registered,
what failed) and the health of each Claude config. It listens on the loopback
interface only, and the page loads nothing from the internet.
It only reads: recommendations show each finding's id and how many are
resolved, and resolving stays on the command line.

Anyone who reaches the port sees session titles, prompts and tool inputs. Set
a password before you tunnel it to another device:

```toml
[serve]
user = "admin"
password = "something long"
```

`--port` picks another port.

To keep it running after the terminal closes:

```sh
mindskein serve --background   # prints the URL and pid, then returns
mindskein serve --stop         # SIGTERM, then SIGKILL after 5 s
```

`--background` takes the other flags (`--port`) and remembers them in
`~/.mindskein/serve.json`; the server's output goes to `~/.mindskein/serve.log`.
Starting a second one says where the first is running instead. `--stop` signals
only the process it started: a recorded pid now held by something else is left
alone.

### Upgrade

```sh
mindskein upgrade                  # the newest commit on main
mindskein upgrade --ref v0.2.0     # any branch, tag or commit
mindskein upgrade --no-restart     # leave the background server stopped
```

`upgrade` needs Go on `PATH`. It stops a background server, builds the module
with `go install` and `GOPROXY=direct`, so a commit pushed a minute ago is the
one you get, and renames the new binary over the one you ran. A hook firing
meanwhile runs the old binary or the new one, never half of either. It prints
`old -> new`, or `already up to date` and leaves the binary alone, then starts
the server again with the flags it had.

A failed build changes nothing: the old binary stays and the server comes back.
The hooks point at the same path, so there is no need to run `init` again.

## What it stores, and where

```
~/.mindskein/
  config.toml       you write this; the app never rewrites it
  sessions/         one JSON file per session — id, folder, status, last tool
  handoffs/         one markdown file per session — title, times, last prompt
  usage/            one JSON file per session — token counts, tool and skill use
  resolved.json     findings you marked fixed with `resolve`; prune keeps it
  prs.json          the last pull request list `prs` fetched, with its time
  prs-marks.json    PRs you declined or marked done, with when and their head
  hooks.log         appended to only when a hook fails
  serve.json        the background server, while one runs: pid, port, flags
  serve.log         the background server's output
```

**Nothing leaves your machine.** No uploads, no telemetry, no account, no third-party service. The store above is all of it, and it is yours to delete.

Today the binary makes no network calls of its own: everything it reads is a local file, and two commands reach the network through tools you already have. `mindskein prs` runs `gh`, which reads your pull requests and notifications from GitHub with the account gh is signed in to. `mindskein upgrade` runs `go install`: Go fetches the module from GitHub and checks it against the Go checksum database, `sum.golang.org`. That is a fact about this version, not the boundary. **Pulling your work in from the tools it already lives in — a tracker, a code host, a to-do app — is the point of this thing**, and when those adapters land they are outbound reads only, to hosts you name, with credentials that stay on disk here. Fetching your own data in is not the same as sending it out, and sending it out is what this promises never to do.

Be aware of what a handoff holds: **the last message you typed**, verbatim, up to 1500 characters. That is the point — it is what answers "where did we leave off" — but it means anything you paste into a session can land in `~/.mindskein/handoffs/`. The directory is created `0700` and the files `0600`.

To remove everything: `rm -rf ~/.mindskein` (and un-register the hooks).

## How it works

| Hook | When | What it records |
| --- | --- | --- |
| `PreToolUse` | every tool call | session is `running`, and the tool it just used |
| `Notification` | idle / permission prompts | session is `waiting` on you |
| `Stop` | every turn completion | session is between turns, **and** writes the handoff |
| `SessionEnd` | `/clear`, `/resume`, logout, exit | session `ended`, with the reason |

`PreToolUse` runs on *every* tool call, so it is kept deliberately cheap —
about 8.7 ms, and it never opens a transcript. `Stop` is the only hook that
reads the transcript, because it is the only one that fires where "where did we
leave off" has an answer.

`Stop` fires **per turn**, not once per session, so a handoff is rewritten
throughout a session and always describes the latest state.

## Status

`brief` runs on real data and is in daily use as of 2026-08-22. Two separate
things are worth knowing, and they are easy to confuse.

**Its own v0.1 is one item short, and that item is not code.** The definition of
done asks for five consecutive mornings of actually reaching for this before the
first work block — a tool nobody reaches for is not finished, however green the
tests are. That count starts now. The rest of it — the command answering from
real captured data, a public repo, green CI — is met.

**The sharp edges below are a separate list.** None of them block that
definition. They are what to weigh before registering four global hooks:

- **A runtime fatal error still exits 2.** A panic is recovered, logged to
  `~/.mindskein/hooks.log`, and the hook exits `0` — `PreToolUse` reads an exit
  of `2` as *block this tool call*, so that path is closed. But a Go runtime
  fatal (a concurrent map write, a stack overflow) bypasses `recover` and still
  exits `2`. Those are bugs to prevent rather than absorb; there is no guard
  that can catch them.
- **Old state is deleted on a timer, and deletion is permanent.** `prune` drops
  session records after 30 days, handoffs after 90 and usage records after
  180, and PR marks a week old that no longer hide anything; a sweep runs
  once a day from the `Stop` hook. Set
  `sessions`/`handoffs`/`usage` to `0` under `[retention]` to keep everything
  forever. `mindskein prune --dry-run` shows what would go.
- **A session that dies hard keeps its last status for a while.** No hook fires
  when a terminal is killed, so the record simply stops changing. After 72
  hours the row is marked `running (stale)` and stops counting toward the
  running total; it disappears at `hide_after` (7 days by default). Between
  the kill and the 72-hour mark, the status is stated with more confidence
  than it deserves.

## Contributing

`go build ./... && go vet ./... && go test ./...`, plus `gofmt -l .`,
`scripts/scenarios.sh` and `scripts/cover.sh 85`. CI runs all of those against
the Go version in `go.mod`, and builds the release archives without publishing
them.

Behaviour is specified as failing scenarios before it is implemented — see [`AGENTS.md`](AGENTS.md) for why, and for the rest of the repo conventions.

## License

[AGPL-3.0](LICENSE).
