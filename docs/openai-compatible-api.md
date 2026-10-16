# OpenKitty OpenAI-Compatible API

OpenKitty can expose published Agent roles as an OpenAI-compatible provider.
Open WebUI and enterprise applications can therefore use the same standard
connection instead of implementing the asynchronous AgentOS task protocol.

The compatibility API runs on the dedicated AgentOS listener. It does not
expose the administrative Web UI.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/v1/models` | Lists Agent roles the authenticated service may use |
| `POST` | `/v1/chat/completions` | Runs an Agent role and returns an OpenAI chat completion |

The `model` field is an OpenKitty `service_roles[].id`. If no explicit
`service_roles` are configured, the available Agent names are used.

Both normal JSON responses and `stream: true` Server-Sent Events are supported.
Streaming responses include short `reasoning_content` status updates while the
Agent is working, followed by the final assistant content and `data: [DONE]`.

## Identity and session boundary

Three independent values define a conversation:

1. `Authorization: Bearer ...` selects a configured `service_clients` entry and
   therefore establishes the caller, tenant, scopes, and allowed roles.
2. `X-OpenKitty-User-ID` identifies the natural person inside that tenant.
3. `X-OpenKitty-Session-ID` identifies that person's external conversation.

Open WebUI's default `X-OpenWebUI-User-Id` and `X-OpenWebUI-Chat-Id` headers are
also accepted. Conflicting native and Open WebUI identity values are rejected.

The API key alone never lets a request select another tenant. User and session
IDs are scoped under the tenant established by the key. The same external chat
ID used by two users resolves to two different OpenKitty sessions and
workspaces.

For a multi-user frontend, configure the client with `require_user_id: true`
and only the minimum scopes:

```yaml
service_clients:
  - id: openwebui
    tenant_id: your-company
    token: REPLACE_IN_THE_RUNTIME_CONFIG
    enabled: true
    require_user_id: true
    allowed_roles:
      - research-agent
    scopes:
      - tasks:submit
      - tasks:read:own
      - tasks:cancel:own
      - users:act_as
```

The value above is only a placeholder. Inject a real random token into the
runtime configuration during deployment, keep that file readable only by the
service account, and do not commit it to a template or source repository.

## Open WebUI configuration

In **Admin Settings → Connections → OpenAI**, add:

- URL: `https://YOUR_OPENKITTY_HOST/v1`
- API Key: the token of the corresponding `service_clients` entry
- Model IDs: normally auto-discovered from `/v1/models`

Open WebUI must forward the signed-in user and chat identity. Either enable its
global user-info forwarding:

```text
ENABLE_FORWARD_USER_INFO_HEADERS=true
```

or add these per-connection custom headers:

```json
{
  "X-OpenKitty-User-ID": "{{USER_ID}}",
  "X-OpenKitty-Session-ID": "{{CHAT_ID}}",
  "X-OpenWebUI-Message-Id": "{{MESSAGE_ID}}"
}
```

The per-connection form works independently of the global forwarding toggle.
The message ID is used as an idempotency key so a network retry does not start
the same Agent turn twice.

Use HTTPS between Open WebUI and OpenKitty, keep the API key on the Open WebUI
backend, and expose only the AgentOS listener through the reverse proxy. Do not
publish the administrative listener on port 8080 as the model provider.

If Open WebUI uses a separate model for title generation, search-query
generation, or other background tasks, configure `TASK_MODEL_EXTERNAL`
accordingly. This keeps those utility prompts out of the business Agent's
conversation.

## Enterprise caller example

Model discovery:

```bash
curl https://YOUR_OPENKITTY_HOST/v1/models \
  -H "Authorization: Bearer YOUR_SERVICE_TOKEN"
```

Streaming conversation:

```bash
curl -N https://YOUR_OPENKITTY_HOST/v1/chat/completions \
  -H "Authorization: Bearer YOUR_SERVICE_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-OpenKitty-User-ID: employee-123" \
  -H "X-OpenKitty-Session-ID: conversation-456" \
  -H "Idempotency-Key: message-789" \
  -d '{
    "model": "research-agent",
    "messages": [
      {"role": "user", "content": "分析这个产品及其主要竞争对手"}
    ],
    "stream": true,
    "stream_options": {"include_usage": true}
  }'
```

The response headers include `X-OpenKitty-Task-ID` and the internally scoped
`X-OpenKitty-Session-ID` for tracing.

## Current compatibility boundary

- Text messages are supported.
- OpenAI text content blocks are supported.
- OpenAI function definitions sent by the caller are not executed; the
  published OpenKitty Agent keeps its host-owned tool policy.
- Binary/image content blocks, embeddings, audio, and image-generation
  endpoints are not yet exposed through this adapter.
- The native AgentOS file endpoint remains available for tenant-isolated file
  upload. Open WebUI RAG content works when Open WebUI injects extracted text
  into the chat messages.
