# OpenAI-compatible endpoint

`yakos serve` can expose the router as an OpenAI-compatible API, so Open WebUI,
Continue, an OpenAI SDK or `curl` can talk to it. Code:
`cli-go/internal/gateway/openai`. Card: K-150.

Every request goes through the same dispatch Service as the console and the other
transports: the router, the sensitive-class scan, the agent budget, supervision
and the ledger all apply. Ledger rows carry `surface=openai-compat`.

## Turn it on

It is off by default. Either:

```
yakos serve --openai-endpoint
```

or, in the trusted `~/.yakos-state/router-policy.yml` (the same owner-only file that
holds router rules; a project `.yakos.yml` cannot enable it):

```yaml
openai_endpoint: true
```

Only the YAML boolean `true` counts. The endpoint listens on `127.0.0.1:7898` and
refuses any non-loopback address. A taken port is a loud warning, and the daemon
carries on without the endpoint.

## Authenticate

The bearer is the endpoint's own token, `~/.yakos-state/openai-endpoint-token`: 32
random bytes, mode 0600, owner only. `yakos serve` mints it on the first start with
the endpoint on and keeps it across restarts. A symlink, a file owned by someone
else or a file others can read is never trusted, and the endpoint answers 401 until
the file is replaced. The REST write token is **not** accepted here, and this token
does not open the REST or MCP write surfaces, so a leak from a chat client is
revoked by rotating this file alone. The token is never logged, printed or written
to the ledger; the start-up banner names the file only.

It is still a powerful credential: **the bearer grants runs with
`--permission-mode bypassPermissions` as the lead agent**, so whoever holds it can
have an agent read and write the workspace and run commands without any prompt.
Treat it like a shell on the host, and never put it in a client that is reachable
from another machine. The OpenAI `tools` field is refused because the agent brings
its own.

```
TOKEN=$(cat ~/.yakos-state/openai-endpoint-token)
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7898/v1/models
```

### Rotate the token

```
yakos serve --rotate-openai-token
```

This writes a new token to the same file, prints the file path (not the token) and
exits. A running endpoint reads the file on every request, so the old token is
rejected from that moment with no restart. Update each client with the new value.
Deleting the file also locks every client out until the next start mints a new one.

The `Host` header must be `127.0.0.1`, `localhost` or `[::1]` with port 7898, and a
request with an `Origin` header must carry this server's own loopback origin
(DNS-rebinding defence, as in the console). Server-side clients (Open WebUI's
backend, SDKs, curl) send no `Origin`. A browser page on another origin cannot call
the endpoint directly; there is no CORS.

## Models

`GET /v1/models` lists:

| Id | Meaning |
|---|---|
| `yakos/auto` | the router picks runtime and model; default agent `lead` |
| `yakos/agent/<id>` | a roster agent; the router still picks where it runs |
| `<runtime>/<model>` | a registry entry (`claude/sonnet`, `codex/...`) whose harness is installed and signed in right now; the runtime is pinned for the turn |

An unknown or unusable id is `404 model_not_found`. A sensitive request (a secret
in the task or the history) is still placed on claude or a local runtime, whatever
model was asked for; the response says so in `yakos.route`.

## Chat completions

`POST /v1/chat/completions`, `stream: true` for SSE (`data:` chunks ending with
`data: [DONE]`), otherwise one JSON object. Body cap 1 MiB.

- The last message must be a `user` message. It is the turn.
- A `system` or `developer` message is carried at the head of the turn, in a
  labelled block, after the yakOS persona. It never replaces the persona, and the
  persona bytes (`--append-system-prompt`, `--agents`) are unchanged by it, so the
  prompt cache prefix stays stable.
- Earlier messages are not replayed as a conversation: a runtime keeps its own
  session. On a new conversation they ride at the tail of the turn as the console's
  bounded (6 KiB), secret-scanned handoff digest, labelled as data. The raw text of
  all of them also goes to the sensitive-class scan, so a key anywhere in the
  history keeps the turn on claude.
- `tools`, `functions`, `tool_choice`, `function_call`, tool messages and
  `tool_calls` are `400`. `n` must be 1. Content parts must be `text`. Sampling
  fields (`temperature`, `max_tokens`, ...) are accepted and ignored.
- `usage` is in tokens: `prompt_tokens` (fresh plus cached), `completion_tokens`,
  `total_tokens`, `prompt_tokens_details.cached_tokens`. No dollars. A stream sends
  the usage chunk only when `stream_options.include_usage` is true.
- `yakos` extension object: `{conversation, route: {runtime, model, rule, reason,
  class, policy_sha}}`. In a stream it is on the first chunk and on the finish
  chunk. Route metadata is response data only; it never enters a prompt.

### Resuming a conversation

Every response carries `X-Yakos-Conversation: <id>`. Send it back on the next
request to resume: the stored native session of each runtime is resumed, the
client's earlier messages are ignored (the transcript is the truth), and a move to
another explicit `<runtime>/<model>` carries the console's handoff digest of the
earlier turns. The conversation belongs to this endpoint's operator
(`openai-compat`): resuming a conversation made by the console or anything else is
`403`, and a bad id is `400`. An id nobody has used yet starts a new conversation.
A second request on a conversation that is still running is `409`.

Transcripts are the console's (`work/current/chats`), so a conversation made here
is visible there under the operator `openai-compat`.

## Errors

OpenAI envelope: `{"error": {"message", "type", "code"}}`. Messages are fixed text
and name no path.

| Status | Code | When |
|---|---|---|
| 400 | `invalid_request`, `invalid_json`, `invalid_conversation` | bad body, tools, bad id |
| 401 | `invalid_api_key` | missing or wrong bearer |
| 403 | `forbidden_origin`, `conversation_forbidden`; Host mismatch | rebinding defence, not your conversation |
| 404 | `model_not_found` | unknown or unusable model |
| 409 | `conversation_busy` | a turn is running on that conversation |
| 413 | `request_too_large` | body over 1 MiB, or the turn over the task limit |
| 415 | `unsupported_media_type` | not `application/json` |
| 429 | `budget_exhausted` | the agent is in budget hard stop |
| 502 | `model_run_failed` | the harness exited non-zero |
| 503 | `route_refused`, `runtime_unavailable`, `agent_unavailable` | nothing permitted can take it |

An error after a stream has started is an `error` frame followed by `[DONE]`.

## Open WebUI

Admin settings, Connections, OpenAI API: URL `http://127.0.0.1:7898/v1`, key = the
endpoint token (`cat ~/.yakos-state/openai-endpoint-token`). Open WebUI's backend makes the calls, so Host and Origin checks pass.
This setup has not been verified against a live Open WebUI.

Open WebUI stores the token in its database. Anyone who can log in to Open WebUI can
spend that token's power, so keep Open WebUI
itself on loopback:

- Docker: publish only on loopback, `-p 127.0.0.1:8080:8080`. Do not use host
  networking and do not publish on `0.0.0.0`. Inside a container `127.0.0.1` is the
  container, not the host, so the loopback-only endpoint is not reachable from a
  default Docker network; use the pip install instead, or a deliberate forward you
  control that listens on 127.0.0.1 only. A forwarder that binds a Docker bridge or
  LAN address re-exposes the endpoint.
- pip: `open-webui serve --host 127.0.0.1`.
- Create the admin account first, then set `ENABLE_SIGNUP=false` and restart, so no
  one else can register.
- No LAN exposure without the ADR-0005 mTLS path. There is no non-loopback mode.

## Not in this version

Function calling, images and audio, embeddings, `n > 1`, per-request sampling, a
`GET /v1/models/<id>` route, and a non-loopback listener (the mTLS path of
ADR-0005 would carry that).

## Related

- [routing.md](routing.md): how the router picks the runtime and model behind
  `yakos/auto` and `yakos/agent/<id>`, and the sensitive class.
- [ADR-0011](adr/ADR-0011.md): the Anthropic pass-through gateway on 7897, a
  different listener with a different token and a different trust boundary. This
  endpoint (7898) is OpenAI-shaped and drives the yakOS harnesses; it never talks
  to a vendor API itself and never sees a subscription login.
- [ADR-0010](adr/ADR-0010.md): why requests are served by spawning the vendor
  binaries.
- Local model servers are not a provider yet; the documented slot is in
  [routing.md](routing.md#local-providers-a-documented-slot). An external client
  that wants a local model today uses that server directly.
