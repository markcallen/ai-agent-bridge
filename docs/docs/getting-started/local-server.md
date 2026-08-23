---
title: Start a Local Server
---

This is the fastest path for one machine. It starts a local bridge server, creates a session from the CLI, and watches the same session from another terminal.

## 1. Start the Server

From the repository root:

```bash
make build
export PATH="$PWD/bin:$PATH"

bridgectl server start
```

Without `--listen`, the server runs in local mode on a Unix socket with no network listener. Keep this terminal open.

## 2. Start a Session with the CLI Example

Open a second terminal:

```bash
cd /path/to/ai-agent-bridge
export PATH="$PWD/bin:$PATH"

go run ./examples/chat \
  --provider codex \
  --project local-demo \
  --timeout 30m \
  "$HOME/repos/bridge-demo"
```

Replace `codex` with `claude`, `opencode`, or `gemini` if that provider is configured on your machine.

The `examples/chat` program starts a session, attaches as the writer, forwards your terminal input, and prints provider output.

## 3. List Sessions

Open a third terminal:

```bash
bridgectl session list --project local-demo
```

You can also use the examples helper:

```bash
go run ./examples/sessions list --project local-demo
```

## 4. Watch a Running Session

Copy the session ID from the list output and attach as a read-only observer:

```bash
bridgectl session watch <session-id>
```

Or with the example:

```bash
go run ./examples/sessions watch <session-id>
```

The watcher replays buffered output and then follows the live stream.

## 5. Take Over the Writer Slot

Only one client can write to a session at a time. To let a human operator type into a running session:

```bash
bridgectl session attach --take-over <session-id>
```

Press `Ctrl-]` to detach and release the writer slot.

## 6. Stop a Session

```bash
bridgectl session stop <session-id>
```

Use `--force` only when the provider process does not exit cleanly.
