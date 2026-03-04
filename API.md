# SlideBolt Core API

The Core API is the central service that communicates with all plugins and manages the system state. It is a Go application using the Huma/Gin framework and exposes a NATS-backed RPC system.

## Connection Details

- **Base URL**: `http://<host>:39011`
- **Protocol**: HTTP/1.1
- **Format**: JSON / JSON-RPC 2.0 (internally)

## Documentation

The API is self-documenting via OpenAPI 3.1:

- **Swagger UI**: `http://<host>:39011/docs`
- **OpenAPI Spec**: `http://<host>:39011/openapi.json`

## Key Capabilities

1.  **Service Discovery**: `/api/plugins` lists all active plugins and their capabilities.
2.  **Universal Device/Entity Management**: Standardized endpoints for every device in the system, regardless of the underlying protocol.
3.  **Command Dispatch**: Send commands to any physical or virtual device.
4.  **Event Journal**: Access a history of system-wide state changes.
5.  **MCP Server**: The gateway natively supports the **Model Context Protocol (MCP)**, allowing AI agents to use the entire REST API as a set of tools.

## Example Request

```bash
# List all plugins
curl http://localhost:39011/api/plugins
```

## Labels and Naming (Important)

Most day-to-day mutations are `local_name` and `labels`.

### Labels JSON Shape

Labels are an object map of `string -> string[]`:

```json
{
  "labels": {
    "room": ["kitchen", "dining"],
    "zone": ["downstairs"]
  }
}
```

Notes:
- This is **not** `string[][]`.
- Multiple values for one key are represented as an array under that key.
- Legacy scalar labels are still accepted in some decode paths, but clients should send `string[]` values.

### Search Filter Syntax

Search endpoints accept repeated `label` query parameters in `key:value` form:

```bash
curl "http://localhost:39011/api/search/devices?label=room:kitchen&label=room:dining&label=zone:downstairs"
curl "http://localhost:39011/api/search/entities?label=room:kitchen"
```

### Current Update Behavior

Device/entity updates use `PUT` and accept full resource payloads. Internally, update handlers merge common fields, so partial payloads often work in practice.

For reliable behavior today:
- Always include `id` (and `device_id` for entities).
- Send only fields you intend to update plus required identifiers.
- Use `labels` with `map[string][]string` shape.

### Simplified Endpoints for Common Changes

The API now provides narrow PATCH-style endpoints for the most common mutations:

- `PATCH /api/plugins/{plugin_id}/devices/{device_id}/name`
  - body: `{ "local_name": "Kitchen Lamp" }`
- `PATCH /api/plugins/{plugin_id}/devices/{device_id}/labels`
  - body: `{ "labels": { "room": ["kitchen"] } }`
- `PATCH /api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/name`
  - body: `{ "local_name": "Main Light" }`
- `PATCH /api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/labels`
  - body: `{ "labels": { "room": ["kitchen"], "group": ["ceiling"] } }`

These keep common UI/API mutations small, explicit, and easy to validate while preserving existing `PUT` routes for full updates.
