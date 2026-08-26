---
title: Start a Local Server
---

This is the fastest path for one machine. It starts a local bridge server, creates a session from the CLI, and watches the same session from another terminal.

## 1. Start the Server

From the repository root:

```bash
make build-cli
bin/bridgectl server start
```

Without `--listen`, the server runs in local mode on a Unix socket with no network listener. Keep this terminal open.

## 2. Start a Session

Open a second terminal:

```bash
bin/bridgectl run --provider claude ~/repos/bridge-demo
```

This creates a session with the chosen provider in the given directory and attaches your terminal as the writer.

Replace `claude` with `codex`, `opencode`, or `gemini` if that provider is configured on your machine. The directory argument defaults to `.` if omitted.

Press **Ctrl-]** to detach without stopping the session. You can reattach later with `bin/bridgectl session attach <session-id>`.

## 3. List Sessions

Open a third terminal:

```bash
bin/bridgectl session list
```

## 4. Watch a Running Session

Copy the session ID from the list output and attach as a read-only observer:

```bash
bin/bridgectl session watch <session-id>
```

The watcher replays buffered output and then follows the live stream.

## 5. Take Over the Writer Slot

Only one client can write to a session at a time. If the provider (or another attached client) currently holds the writer slot, you can forcibly claim it:

```bash
bin/bridgectl session attach --take-over <session-id>
```

The `--take-over` flag evicts the current writer and gives your terminal exclusive write access. This is useful when you need to type into a session that an AI provider is driving, for example to answer a prompt or correct course.

Press **Ctrl-]** to detach and release the writer slot.

## 6. Stop a Session

```bash
bin/bridgectl session stop <session-id>
```

Use `--force` only when the provider process does not exit cleanly.
