### `gateway` repository

#### Project Overview

This repository contains the `gateway`, the central nervous system of the Slidebolt ecosystem. It is a standalone service that acts as an API gateway, a plugin registry, an event hub, and a command broker for the entire system.

#### Architecture

The gateway is a multi-faceted Go application that serves as the single entry point for all interactions with the Slidebolt environment.

-   **REST API**: It exposes a comprehensive RESTful API built with the [Gin](https://gin-gonic.com/) web framework and the [Huma](https://huma.rocks/) OpenAPI-based framework. This API provides endpoints for managing every aspect of the system, from listing plugins to sending commands to entities.

-   **Plugin Registry & RPC Gateway**: The gateway connects to the NATS message bus and listens for registration messages from all active plugins. It maintains a dynamic registry of these plugins and their RPC subjects. When an API call for a specific plugin is received, the gateway acts as an RPC client, forwarding the request to the correct plugin over NATS.

-   **Event Hub**: It subscribes to the global event bus on NATS (`slidebolt.entity.events`). This allows it to observe every state change from every entity in the system, maintaining a short-term event journal.

-   **Virtual Entity Management**: A key feature of the gateway is its ability to create and manage "virtual entities". These are proxy entities that can mirror a real "source" entity from any plugin. The gateway handles the command forwarding (from the virtual entity to the source) and state synchronization (from the source to the virtual entity), enabling powerful automations and abstractions.

-   **MCP Bridge**: The gateway includes a bridge to the "Master Control Program" (MCP). It automatically generates MCP tools from its own OpenAPI specification, allowing AI agents to programmatically interact with the entire Slidebolt system via the gateway's API.

#### Key Files

| File | Description |
| :--- | :--- |
| `bootstrap.go` | Initializes the gateway, connects to NATS, and starts the API server and MCP bridge. |
| `routes.go` | Defines the entire REST API using the Huma framework, providing endpoints for plugins, devices, entities, commands, and more. |
| `rpc.go` | Contains the logic for routing incoming API requests as RPC calls to the appropriate plugins over NATS. |
| `events.go` | Handles the subscription to the NATS event bus and processes all incoming entity state changes. |
| `virtual_store.go`| Manages the persistence and state of all created virtual entities. |
| `mcp.go` | Implements the bridge that exposes the gateway's REST API as a set of tools for the Master Control Program (MCP). |
| `domains.go` | Imports the `sdk-entities` domain packages (e.g., `light`, `switch`) to ensure their schemas are registered with the system. |

#### API Endpoints

The gateway exposes a rich REST API. The full, detailed specification can be viewed by running the gateway and accessing its Swagger UI. Key capabilities include:

-   Listing registered plugins.
-   Listing devices and entities for a specific plugin.
-   Creating, updating, and deleting devices and entities.
-   Sending commands to entities.
-   Creating and managing virtual entities.
-   Managing automation scripts on entities.
-   Searching for devices and entities across all plugins.
-   Querying the event journal.
