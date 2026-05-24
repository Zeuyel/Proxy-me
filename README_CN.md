# CLI 代理 API

[English](README.md) | 中文

CLI 代理 API 是一个为 CLI 编码工具与 SDK 客户端提供 OpenAI/Gemini/Claude/Codex 兼容接口的网关。

## 功能概述（给使用者）

- 提供统一的 OpenAI/Gemini/Claude/Codex 兼容 API 端点。
- 支持 Codex 与 Claude Code 的 OAuth 鉴权接入流程。
- 支持流式与非流式响应。
- 支持工具调用 / 函数调用透传。
- 支持多模态输入透传（文本、图片，取决于上游能力）。
- 支持多账户路由与轮询。
- 支持通过配置接入 OpenAI 兼容上游。

## Auth File 接口与鉴权

Auth file 相关接口和主 API 服务使用同一个 HTTP 端口，不存在单独的上传端口或管理端口。实际访问时使用配置里的 `port`，再拼接下面的路由前缀。

### 管理端 Auth File 接口

这些接口位于 `/v0/management` 下，需要管理密钥鉴权：

- `Authorization: Bearer <MANAGEMENT_KEY>`
- 或 `X-Management-Key: <MANAGEMENT_KEY>`

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/v0/management/auth-files` | 获取 auth file 与运行时 auth 条目列表。 |
| `GET` | `/v0/management/auth-files/models` | 获取指定 auth file 可用模型。 |
| `GET` | `/v0/management/auth-files/download` | 下载 auth file。 |
| `POST` | `/v0/management/auth-files` | 上传 auth file。 |
| `DELETE` | `/v0/management/auth-files` | 删除一个或全部 auth file。 |
| `PATCH` | `/v0/management/auth-files/metadata` | 修改显示名称、标签等管理元数据。 |
| `PATCH` | `/v0/management/auth-files/rename` | 重命名 auth file，并迁移相关配置引用。 |
| `PATCH` | `/v0/management/auth-files/status` | 启用或禁用 auth file。 |
| `POST` | `/v0/management/auth-files/reset-cooldown` | 清理 auth file 的运行时冷却状态。 |

### 上传专用密钥

可以配置 `remote-management.upload-key` 作为只允许上传 auth file 的低权限密钥。它和 `remote-management.secret-key` 一样，如果配置的是明文，启动时会自动 bcrypt 哈希并写回配置文件。

```yaml
remote-management:
  secret-key: ""
  upload-key: "upload-only-secret"
```

上传专用密钥只能访问：

- `POST /v0/management/auth-files`

支持的请求头：

- `Authorization: Bearer <UPLOAD_KEY>`
- 或 `X-Auth-Upload-Key: <UPLOAD_KEY>`

它不能获取列表、下载、重命名、修改元数据、切换状态、重置冷却，也不能访问其他管理接口。完整管理密钥仍然可以访问上传接口和所有管理接口。

### Client 侧 Auth File 使用情况

只有普通 API key 的客户端可以查询自己可访问的 auth file 使用情况，不需要管理密钥：

- `GET /v0/client/usage/auth-files`
- 请求头：`Authorization: Bearer <API_KEY>`

这个接口只返回该 API key 可访问范围内的使用情况，不提供 auth file 管理能力。

## 当前欠缺（持续完善中）

- 不同提供商与不同客户端的兼容性仍未完全统一，仍有边缘场景差异。
- 部分模型/提供商的配额处理与冷却可视化还在持续优化。
- 面向生产环境的部署与运维指南仍不完整。
