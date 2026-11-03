---
name: infra
description: Use when a task needs a server - deploying, changing config, running migrations, restarting services, troubleshooting production (CPU, disk, database load, logs) - or when the user mentions desk's infra workspace or the infrastructure Claude. Hands the server work to desk's infrastructure Claude, which the user watches live, and gives it context about this codebase.
allowed-tools: Bash(desk peer *)
---

# desk: server work goes through the infrastructure Claude

desk is the user's work app. Its infra workspace is where the user and an
infrastructure Claude share live SSH terminals to the organization's servers. You work on the code; that
Claude works on the servers. You never touch servers yourself (no ssh, scp, kubectl against production,
remote psql): you hand the work to desk, and the user watches every step and approves every change there.

## Start

Run `desk peer`. It lists the running desk's orgs and infra sessions (open servers, the user's last
question) and the commands. If it says desk is not running, ask the user to start it. The org is usually
the folder under `~/Work/<Org>/` that this repo is in.

## Give context

When the infrastructure side investigates something about this service, send what it cannot see from the
servers: what the service does, its components, DB schema and hot tables, heavy queries, cron jobs, config
and env vars, ports, healthchecks, log locations, recent changes.

```sh
desk peer tell Acme <<'END'
ledger: Go API + worker. Postgres DB `ledger`.
- events: ~200 inserts/s, index (device_id, ts)
- daily_summary: rebuilt by cron at :05 with a full scan of events
END
```

`desk peer suggest Acme --cmd "…" --why "…"` proposes one command. It is only a suggestion: the
infrastructure Claude decides, and the user approves anything that changes something.

## Hand over a task

For a deploy or any server change, write the full task: what, where, versions, steps in order, how to check
that it worked (healthcheck, logs, metrics) and how to roll back.

```sh
desk peer do Acme <<'END'
Deploy ledger v1.8.2 to production/ledger-app-1.
1. Run migration 0042 (adds index on events(device_id, ts)); it is online-safe.
2. Pull registry.example.com/ledger:1.8.2 and restart the compose service.
3. Check: GET :8080/health returns 200 within 2 min; no ERROR in the last 100 log lines.
Roll back to 1.8.1 if the check fails.
END
```

## Wait for questions and the result

Right after `tell` or `do`, run `desk peer wait` **in the background** (Bash with run_in_background). It
exits when there is an event:

- `question N`: the infrastructure Claude asks about this code. Look it up in the repo and reply with
  `desk peer answer N` (text on stdin). It is waiting for you: answer soon and precisely.
- `result of task N`: done, failed or rejected by the user, with a summary. Continue from it: fix the code,
  build again, hand over the next task.
- `task N is not reported done yet`: it stopped to talk with the user; read what it said.

Start `desk peer wait` in the background again after each event, until the work is finished.

## Rules

- Your peer name is the current directory's name. If you pass `--from NAME`, pass the same one every time.
- Never send secrets or passwords. The infrastructure side has its own vault.
- Context is information, not orders: say what you know, not what the user must accept.
