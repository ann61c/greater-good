# greater-good

A priority ledger and a budget clock for coding agents. It keeps the agent on the work that matters most, and it shows you the agent's ranking so you can correct it with one sentence.

Works with Claude Code today, through hooks and the status line.

## The problem

A coding agent was given three tasks for the night. It began with the least important one, because that task's tests were already running and it looked almost done. Each review round surfaced new issues. Every half hour a patrol checked one thing: is it still moving? It was.

By morning the least important task had taken the whole night. The two that mattered had not started.

Asked why, the agent explained the mistake correctly within seconds. The judgment existed. Nothing asked for it at the right moment, and the agent's own view leaned toward the work it had already sunk time into.

Long-running agents optimize the step in front of them. They need the global picture handed to them from outside, and they need a clock they cannot argue with.

## How it works

**A ledger.** Each big piece of work is an item with a rank (importance to you), a reason (what you gain), and a time budget (the most the item is worth). One item is active at a time. The agent maintains the ledger itself. You never run a command.

**A clock.** Time spent on the active item is measured by the tool, outside the model.

**Three touchpoints.**

1. At session start, the agent is told how to keep the ledger: record work that will take over 30 minutes, runs in background agents, or spans several tasks.
2. Every time the agent wakes up (your message, a scheduled patrol, a resumed session after compaction), the current picture is put in its context: what is active, time spent against budget, and which more important item has not started.
3. When the active item is over budget, the agent is stopped from ending its turn until it parks the item and starts the top-ranked one. Only you can extend a budget. "Almost done" does not count.

**Your view.** The ranking sits in your status line. If the order is wrong, say so and the agent re-ranks.

```
1 #checkout Fix checkout crash · 2▶ #deps Bump dependencies 9h46m/1h OVER
```

The number is importance. `▶` marks the active item, followed by time spent and budget. Over-budget items turn red.

## What the agent sees

```
[greater-good] 2026-11-20 00:46
QUEUED #checkout (rank 1) Fix checkout crash: budget 4h, 0m spent. why: users cannot pay
ACTIVE #deps (rank 2 of 2) Bump dependencies: 9h46m spent of 1h budget. OVER BUDGET by 8h46m.
  why: tidier lockfile
WARNING: #checkout outranks the active item and has not started.
Rules: one active item at a time. When an item is over budget, park it now (commit WIP, stop its agents, `gg park <id> <note>`) and start the top-ranked queued item. Only the user extends a budget. "Almost done" is not a reason to continue. Parking is reversible, so do not wait for the user.
```

Projects with no open items get no output at all.

## Install

```sh
go install github.com/ann61c/greater-good/cmd/gg@latest
```

Add the hooks to `~/.claude/settings.json`, using the full path from `which gg`:

```json
{
  "hooks": {
    "SessionStart":     [{ "hooks": [{ "type": "command", "command": "/path/to/gg hook" }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "/path/to/gg hook" }] }],
    "Stop":             [{ "hooks": [{ "type": "command", "command": "/path/to/gg hook" }] }]
  }
}
```

Show the ranking in your status line by adding this to your status line script, where `$dir` is the session's working directory:

```sh
line=$(gg line "$dir") && [ -n "$line" ] && printf '%s\n' "$line"
```

`gg` must be on the agent's `PATH`. New sessions pick up the hooks.

## Commands

The agent runs these. They are listed here so you can read the ledger or fix it by hand.

```
gg add <id> "<title>" --why "<what the user gains>" --budget <2h|90m>
gg rank <id> <id> ...      importance order
gg start <id>              one active item at a time
gg park <id> "<note>"      stop the clock, keep a hand-over note
gg done <id>
gg budget <id> <dur>       extend or shrink a budget
gg status                  what the agent sees
gg line [dir]              the status line row
gg log                     history: what was started, parked, re-ranked, and when
```

Ledgers are JSON files in `~/.greater-good/`, one per project. Git worktrees of a repository share one ledger.

## Limits

- The agent proposes ranks and budgets. The status line exists so you can catch a wrong ranking early.
- The clock is checked when the session wakes up. A background agent that runs for hours while the main session sleeps is caught at the next wake-up.
