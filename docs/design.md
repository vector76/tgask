# tgask — Design Document

## Overview

**tgask** is a lightweight client/server system that allows automated processes to
interactively query a human via Telegram. A process submits a text prompt; the
server delivers it to the user's Telegram chat and waits for a reply; the reply
is returned to the calling process.

The system is designed for the common scenario where a non-interactive background
process (a build system, an AI agent, a script) needs a human decision before
it can proceed — without requiring that process to know anything about Telegram.

---

## Problem Statement

A calling process needs to:

1. Send a prompt to a human and block until a response arrives.
2. Receive the response as plain text on stdout (or via HTTP), suitable for
   scripting.

Complications:

- Multiple independent processes may issue queries concurrently, possibly from
  different machines.
- The human uses a single Telegram chat, so simultaneous pending questions would
  create ambiguity about which answer belongs to which query.
- The calling processes are already blocked waiting for the human, so waiting
  their turn in a queue is acceptable.

---

## Architecture

### Components

```
┌───────────────────────────────────────────────────────────────┐
│  Caller A  (machine 1)  ──POST /ask──┐                        │
│  Caller B  (machine 2)  ──POST /ask──┤──► tgask server ◄────► Telegram
│  Caller C  (machine 1)  ──POST /ask──┘        │               Bot API
│                                               │
│  Caller A  ──GET /result/{id}──► (blocks until reply)         │
└───────────────────────────────────────────────────────────────┘
```

**tgask server** — a single long-running Go binary that:
- Exposes an HTTP REST API for clients
- Maintains a serial queue of pending queries
- Owns the connection to the Telegram Bot API
- Routes replies back to waiting callers

**tgask client** — a compiled Go CLI (or any HTTP client) that:
- Posts a query to the server
- Receives a job ID
- Polls (or long-polls) for the result
- Prints the response to stdout and exits

Because the server is the sole consumer of `getUpdates`, there are no race
conditions on the Telegram side.

### Why a serial queue

The simplest and most user-friendly design is to present one question at a time.
When a second query arrives while one is already pending, it waits in the queue.
From the caller's perspective this is indistinguishable from waiting for the
human to respond — which it would have to do anyway.

Benefits over a parallel/nonce approach:
- No friction for the human (no nonce to type or remember)
- No ambiguity about which answer belongs to which question
- No risk of one caller consuming another's reply
- Simpler server logic

`send` (fire-and-forget notifications) is **not** subject to the queue. Send
requests are dispatched to Telegram immediately, even while a query is awaiting
a reply. Only `ask` operations are serialized.

### Reply attribution via Telegram threads

When the server sends a query to Telegram it uses `ForceReply`, which causes
the Telegram client to pre-fill the reply field pointing at that specific
message. The server records `{job_id → sent_message_id}`. When an incoming
update contains a reply whose `reply_to_message.message_id` matches a known
job, the response is routed correctly.

This works even if the user sends unrequested messages in the chat: only
messages that are explicit replies to a server-sent prompt are considered
responses to a query. Other messages are ignored (or optionally forwarded to
an operator log).

---

## Server Design

### Language and framework

Go. Single statically-linked binary. No runtime dependencies, trivial to deploy
on Linux, macOS, or Windows. HTTP routing via
[chi](https://github.com/go-chi/chi).

### Internal structure

```
cmd/tgask/          — entry point, cobra command tree
internal/
  server/           — HTTP handlers, middleware, routing
  queue/            — serial job queue and in-flight state
  telegram/         — Telegram Bot API polling loop, send helpers
  model/            — Job struct and status enum
```

### Job lifecycle

```
POST /ask
  → create Job{id, prompt, status=queued}
  → enqueue
  → return {id}

Queue worker (goroutine):
  → dequeue Job
  → compute expiry time (now + timeout)
  → send Telegram message (ForceReply) including "⏱ Reply by HH:MM"
  → record {job_id → sent_message_id}
  → set status=awaiting_reply
  → block on channel (or expiry timer)
  → on expiry:
      → delete original Telegram message
      → send replacement: "This query expired — no response needed."
      → resolve job with error

Telegram poller (goroutine):
  → getUpdates loop
  → on reply matching an in-flight message_id:
      → send reply text on job's channel
      → advance update offset

HTTP handler (GET /result/{id}):
  → look up job
  → long-poll on job's channel (up to client-specified timeout)
  → on receive: return {reply}
  → on timeout: return 202 Accepted (client should retry)
```

### Concurrency model

- One goroutine runs the Telegram polling loop.
- One goroutine runs the queue worker (serializes sends).
- Each `GET /result/{id}` handler blocks on a per-job `chan string`.
- A `sync.Mutex` protects the job map.
- No external dependencies (Redis, database) — state is in-memory.

Because the queue worker and Telegram poller are the only goroutines that
touch Telegram, there are no concurrent `getUpdates` callers and no offset
races.

### State persistence (optional)

For the initial version, job state is in-memory and lost on restart. A pending
reply that was already delivered to Telegram before a crash will be re-matched
if the server restarts before the human replies (because `getUpdates` with the
last known offset will re-deliver it). A simple journal file (append-only JSON
lines, atomic rename on checkpoint) can be added later to survive restarts
cleanly.

---

## HTTP API

All endpoints except `/health` and `/version` require a Bearer token
(`Authorization: Bearer <token>`), configured via environment variable.

### Submit a query

```
POST /api/v1/ask
Content-Type: application/json

{
  "prompt": "Which migration should I apply? (a) v12 (b) v13",
  "timeout": 300
}
```

Response `201 Created`:

```json
{ "id": "a1b2c3d4" }
```

`timeout` (seconds) is a hint: if the human has not replied within this window
the job is expired and waiting callers receive an error. Default: 300.

### Poll for result

```
GET /api/v1/result/{id}?wait=30
```

`wait` (seconds) controls the long-poll duration (max 60). If the reply has
arrived, returns immediately regardless.

Response `200 OK` (reply received):

```json
{ "id": "a1b2c3d4", "status": "done", "reply": "Apply v13" }
```

Response `202 Accepted` (still waiting — client should retry):

```json
{ "id": "a1b2c3d4", "status": "queued" }
```

Response `410 Gone` (job expired or not found):

```json
{ "id": "a1b2c3d4", "status": "expired" }
```

### Send a notification (no reply expected)

```
POST /api/v1/send
Content-Type: application/json

{ "message": "Build succeeded. Deploying to staging." }
```

Response `200 OK`.

### Health / Version

```
GET /health   →  200 OK  {"status": "ok"}
GET /version  →  200 OK  {"version": "0.1.0"}
```

---

## CLI Client

The `tgask` binary doubles as a client when not invoked with `serve`.

### Commands

```
tgask serve               Start the server
tgask ask  "prompt text"  Submit a query and wait for the reply (blocking)
tgask send "message"      Fire-and-forget notification
```

`tgask ask` is the primary client-side command. It:
1. POSTs to `/api/v1/ask`
2. Long-polls `/api/v1/result/{id}` in a loop
3. Prints the reply text to stdout (and optionally writes it to a file)
4. Exits 0 on success, 1 on error, 2 on timeout

### File I/O flags

Both `ask` and `send` accept `-f`/`--file` to read the prompt or message from
a file instead of a command-line argument:

```
tgask ask -f HUMAN_PROMPT.md
tgask send -f notification.md
```

`tgask ask` additionally accepts `-o`/`--output` to write the reply text to a
file. When `-o` is given, stdout remains clean for other signaling (exit codes,
`<goto>` tags, etc.) and only the reply content goes to the output file:

```
tgask ask -f HUMAN_PROMPT.md -o HUMAN_REPLY.md
```

If neither `-f` nor a positional argument is given, the prompt is read from
stdin, enabling pipes:

```
cat HUMAN_PROMPT.md | tgask ask -o HUMAN_REPLY.md
```

This makes `tgask ask` a direct replacement for the shell-script pattern: any
script can capture the reply via stdout, a file, or a pipe.

### Configuration

All configuration is via environment variables (`.env` file supported):

| Variable              | Description                              | Default              |
|-----------------------|------------------------------------------|----------------------|
| `TGASK_BOT_TOKEN`     | Telegram Bot API token                   | required             |
| `TGASK_CHAT_ID`       | Telegram chat ID to use                  | required             |
| `TGASK_TOKEN`         | Bearer token for the HTTP API            | required             |
| `TGASK_URL`           | Server base URL (client-side)            | `http://localhost:9100` |
| `TGASK_PORT`          | Port the server listens on               | `9100`               |
| `TGASK_DEFAULT_TIMEOUT` | Default query timeout (seconds)        | `300`                |

---

## Build and Deployment

```
make build          # build for host platform → bin/tgask
make build-linux    # cross-compile for Linux/amd64
make build-all      # Linux amd64 + arm64, Windows amd64, macOS amd64 + arm64
```

The server is a single process with no external dependencies. Run it with:

```
TGASK_BOT_TOKEN=... TGASK_CHAT_ID=... TGASK_TOKEN=... tgask serve
```

Or via a systemd unit / Docker container. No database, no message broker.

### Typical deployment topologies

**Docker callers → host server**
The server runs on the Docker host. Containers reach it via the host's internal
network address (e.g. `172.17.0.1:9100` or the host gateway). Set
`TGASK_URL=http://172.17.0.1:9100` in each container's environment.

**VPS callers → server via tunnel**
The server runs on a local machine exposed through an ngrok (or similar) tunnel.
Set `TGASK_URL` to the tunnel URL. The Bearer token prevents unauthorized access
through the public endpoint.

In all cases, clients only need `TGASK_URL` and `TGASK_TOKEN`; the bot token and
chat ID are server-side only.

---

## Security Considerations

- The HTTP API requires a Bearer token on every call; use a strong random
  secret.
- Run the server on localhost or a private network; do not expose it to the
  public internet without TLS termination (nginx / caddy in front).
- The Telegram bot should be a private bot restricted to the owner's chat.
  Enable `allowed_updates: ["message"]` to limit the update surface.
- Job prompts and replies are held in memory only; they are not written to disk
  in the base design.

---

## Future Considerations

- **Webhook mode** — replace `getUpdates` polling with a Telegram webhook for
  lower latency. Requires a publicly reachable HTTPS endpoint.
- **Persistent job log** — append-only journal so in-flight jobs survive a
  server restart.
- **Multiple chats** — route different callers to different Telegram chats,
  authenticated by separate API tokens (same multi-project pattern used in
  beads_server).
- **Inline keyboard buttons** — for yes/no or multiple-choice questions, send
  Telegram inline keyboard buttons instead of free-text prompts, and resolve
  the job when the user taps a button.
- **Parallel mode (opt-in)** — allow multiple simultaneous queries by
  displaying all pending questions and using `reply_to_message_id` for
  disambiguation, enabled per-client via a flag.
