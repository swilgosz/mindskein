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

Then restart Claude Code, and point it at your plan.

### 1. Point it at your plan

`mindskein` does not guess where your notes live, because a wrong guess prints
an empty list, which looks exactly like a quiet day. Write
`~/.mindskein/config.toml`:

```toml
[vault]
path = "~/notes"          # optional base; ~ expands
plan = "plan.md"          # relative to path, or absolute

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
what is deliberately not supported — is in [`docs/plan-format.md`](docs/plan-format.md).

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
mindskein priorities    # the plan only
mindskein prune         # delete state past the retention horizon
mindskein init          # register the hooks; --uninstall removes them
```

`--all` widens every section: the `!3` backlog, sessions that have ended or
aged out, and every workstream rather than the newest few.

Set `MINDSKEIN_PROJECT` before launching Claude to name a workstream that spans
several folders and sessions. It is the only grouping key that does.

## What it stores, and where

```
~/.mindskein/
  config.toml       you write this; the app never rewrites it
  sessions/         one JSON file per session — id, folder, status, last tool
  handoffs/         one markdown file per session — title, times, last prompt
  hooks.log         appended to only when a hook fails
```

**Everything stays on your machine.** Nothing is uploaded, and the tool makes no network calls at all.

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
  session records after 30 days and handoffs after 90, and a sweep runs once a
  day from the `Stop` hook. Set `sessions`/`handoffs` to `0` under
  `[retention]` to keep everything forever. `mindskein prune --dry-run` shows
  what would go.
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
