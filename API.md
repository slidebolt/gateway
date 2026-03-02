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
