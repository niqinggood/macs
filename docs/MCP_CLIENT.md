# OpenKitty MCP Client

OpenKitty uses the official MCP Go SDK and exposes every connected server tool
through the normal `ToolRegistry`. No special agent-side integration is needed:
an MCP tool appears as `mcp_<tool-name>`.

## Supported transports

- `stdio`: starts a local MCP server process and communicates over stdin/stdout.
- `streamable_http`: MCP Streamable HTTP, including JSON and SSE responses,
  protocol/session headers, server notifications, and graceful session close.
- `sse`: legacy HTTP+SSE transport from MCP 2024-11-05.
- `auto`: for a URL, tries Streamable HTTP first and falls back to legacy SSE
  when the endpoint reports a compatible 4xx response.

The client currently negotiates up to MCP `2025-11-25`. Paginated tool and
resource catalogs are read automatically. A `notifications/tools/list_changed`
event refreshes that server's registered tools.

## Configuration

```yaml
mcp_servers:
  - name: filesystem
    enabled: true
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
    env:
      - LOG_LEVEL=info
    cwd: /path/to/workspace
    timeout_seconds: 30

  - name: company-tools
    enabled: true
    transport: auto
    url: https://mcp.example.com/mcp
    token: ${MCP_TOKEN}
    headers:
      X-Tenant-ID: tenant-42
    tool_prefix: company
    timeout_seconds: 30
```

`token` is sent as `Authorization: Bearer <token>`. Values in `headers` are
attached to every HTTP and SSE request. Use normal configuration secret
substitution rather than committing tokens.

`tool_prefix` is optional. Without it, `search` is registered as `mcp_search`.
With `tool_prefix: company`, it becomes `mcp_company_search`. If two servers
would otherwise register the same name, OpenKitty automatically adds the server
name to the later tool instead of silently overwriting the first.

Servers that expose resources but no tools remain connected. Agents can use
`mcp_list_resources` and `mcp_read_resource` to discover and read them.
