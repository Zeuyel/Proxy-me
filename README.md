# CLI Proxy API

English | [中文](README_CN.md)

CLI Proxy API is a gateway that exposes OpenAI/Gemini/Claude-compatible endpoints for CLI coding tools and SDK clients.

## Feature Overview

- Unified OpenAI/Gemini/Claude/Codex-compatible API endpoints.
- OAuth-based access for Codex and Claude Code flows.
- Streaming and non-streaming response support.
- Tool/function-calling pass-through support.
- Multimodal input pass-through (text and image where upstream supports it).
- Multi-account routing/rotation for supported providers.
- Config-driven upstream routing to OpenAI-compatible providers.

## Auth File API and Authentication

Auth file APIs are served on the same HTTP port as the main API server. There is no separate upload or management port; use the configured `port` value and the route prefixes below.

### Management auth file endpoints

These endpoints are under `/v0/management` and require the management key:

- `Authorization: Bearer <MANAGEMENT_KEY>`
- or `X-Management-Key: <MANAGEMENT_KEY>`

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/v0/management/auth-files` | List auth files and runtime auth entries. |
| `GET` | `/v0/management/auth-files/models` | Get models available to a selected auth file. |
| `GET` | `/v0/management/auth-files/download` | Download an auth file. |
| `POST` | `/v0/management/auth-files` | Upload an auth file. |
| `DELETE` | `/v0/management/auth-files` | Delete one or all auth files. |
| `PATCH` | `/v0/management/auth-files/metadata` | Update display metadata such as display name and tags. |
| `PATCH` | `/v0/management/auth-files/rename` | Rename an auth file and move related config references. |
| `PATCH` | `/v0/management/auth-files/status` | Enable or disable an auth file. |
| `POST` | `/v0/management/auth-files/reset-cooldown` | Clear runtime cooldown state for auth files. |

### Upload-only key

`remote-management.upload-key` can be configured as a limited credential for auth file uploads only. Like `remote-management.secret-key`, a plaintext value is automatically bcrypt-hashed on startup and persisted back to the config file.

```yaml
remote-management:
  secret-key: ""
  upload-key: "upload-only-secret"
```

The upload-only key can only access:

- `POST /v0/management/auth-files`

Accepted headers:

- `Authorization: Bearer <UPLOAD_KEY>`
- or `X-Auth-Upload-Key: <UPLOAD_KEY>`

It cannot list, download, rename, edit metadata, change status, reset cooldowns, or access any other management endpoint. A full management key still works for uploads and all other management routes.

### Client-scoped auth file usage

Clients that only have a normal API key can query scoped usage data without a management key:

- `GET /v0/client/usage/auth-files`
- Header: `Authorization: Bearer <API_KEY>`

This endpoint returns usage information for auth files accessible to that API key. It does not expose full auth file management operations.

## Current Gaps

- Compatibility is still not fully uniform across all providers and all client edge-cases.
- Quota handling and cooldown visibility are still being refined for some model/provider combinations.
- Production deployment guidance and operational playbooks are still incomplete.
