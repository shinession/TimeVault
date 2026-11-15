# English

## Overview

TimeVault serves a JSON API below `/api/v1` and server-rendered pages. Both are implemented by the same service layer, so they behave identically. This document describes what exists now; it differs from the illustrative API in plan.md §6 in the ways listed in [Differences from plan.md](#differences-from-planmd).

## Conventions

- **Authentication.** Sign in with `POST /login` (form fields `username`, `password`, optional `next`). The response sets the `tv_session` cookie (HttpOnly, SameSite=Lax, Secure when `TIMEVAULT_COOKIE_SECURE=true`). Every `/api/v1` endpoint requires it and answers `401` with a JSON error otherwise.
- **CSRF.** Every `POST` needs the session's token in the `X-CSRF-Token` header (HTML forms use the `csrf_token` field). Read it from `GET /api/v1/session`. A request whose `Origin` or `Sec-Fetch-Site` shows another site is rejected with `403`.
- **JSON.** Request bodies are JSON with `Content-Type: application/json`; unknown fields are rejected with `400`. Times are RFC 3339. Sizes are bytes.
- **Errors.** `{"error": {"code": "...", "message": "..."}}` with these statuses: `400` invalid input (bad path, bad destination, unknown field), `401` not signed in, `403` CSRF, `404` not found, `409` conflict (duplicate source, busy, stale preview), `422` confirmation missing or items blocked, `503` search engine unavailable or health failure, `500` internal error (the message carries only a reference id; details are in the server log).
- **Paths.** Source-relative, using `/` or `\`; leading and trailing separators are ignored; `""` is the source root. `..`, drive letters, reserved device names and invalid characters are rejected with `400`.
- **Paging.** `files` and `search` use `offset`/`limit` and return `next_offset` while more results exist. `history/tree` uses an opaque `cursor` and returns `next_cursor`. `audit` uses `before` (an event id) and `limit`.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Liveness probe, no authentication, returns `ok`. |
| GET | `/api/v1/session` | `user`, `role`, `csrf_token`, `auth_disabled`. |
| GET | `/api/v1/health` | `ok`, `components[]` (`database`, `repository`, `search`, `restore`, `vss`, `usn`, `jobs`) each with `state` (`ok`, `degraded`, `disabled`, `failed`) and `detail`; HTTP `503` when a component is `failed`. |
| GET | `/api/v1/sources` | Registered sources with their newest committed recovery point. |
| POST | `/api/v1/sources` | Register a source: `{"name", "root_path", "excludes": []}` (default excludes when omitted). `201`. The folder must be a local, existing directory that does not overlap the repository, the restore folder or another source. |
| GET | `/api/v1/files` | Current folder: `source_id`, `path`, `offset`, `limit` (≤ 1000). Returns `entries[]` (`name`, `path`, `is_dir`, `size`, `modified_at`, `status` = `protected` / `changed` / `new` / `not_captured`), `total`, `next_offset`, `deleted[]` (items present in the newest recovery point but gone from disk), `latest_point`. |
| GET | `/api/v1/search` | `q`, `source_id`, `offset`, `limit` (≤ 200). Returns `results[]`, `total`, `next_offset`. `503` with code `search_unavailable` when the engine is down or not configured. |
| GET | `/api/v1/recovery-points` | Committed points of `source_id`, newest first (`limit`, `before_seq`). Each has `seq`, `kind` (`full` / `incremental`), `consistency` (`live_scan`), counters, `verification`, `restorable`, `manifest_sha256`. |
| GET | `/api/v1/history/tree` | A folder as it was at `point_id`: `source_id`, `point_id`, `path`, `cursor`, `limit`. Each entry has `status` relative to the newest point: `same`, `modified`, `deleted`. |
| GET | `/api/v1/history/versions` | Every version of `path`: `captured_at`, `until`, `current`, `size`, `sha256`, `state`, `first_point_id`, `object_present`. |
| POST | `/api/v1/backups` | `{"source_id", "rehash": false}`. `202` with `{job, already_active}`; if a backup of that source is already queued or running the existing job is returned with `200` and `already_active: true`. |
| GET | `/api/v1/jobs/{id}` | `{job, events[]}`; `after_event` returns only newer log lines. `job.active` is true while queued or running; `job.progress` is a JSON object whose fields depend on the kind. |
| POST | `/api/v1/restores/preview` | `{"source_id", "point_id", "paths": [], "dest_name", "policy": "skip"|"rename"|"overwrite", "allow_partial": false}`. Writes nothing. Returns `destination`, `totals` (`create`, `overwrite`, `rename`, `skip_same`, `skip_exists`, `blocked`, `missing_objects`, `not_captured`, …), `sample[]`, `problems[]`, `digest` and `confirmation_token`. |
| POST | `/api/v1/restores` | The preview body plus `confirmation_token`, `idempotency_key` and `confirm_overwrite`. `202` with `{job}`. `422` when the token is missing or invalid, when files would be replaced without `confirm_overwrite`, or when items are blocked without `allow_partial`; `409` when the preview is stale. Repeating a request with the same `idempotency_key` returns the original job. |
| GET | `/api/v1/audit` | Audit events newest first: `action`, `actor`, `before`, `limit`. |

## Page routes

| Route | Page |
|---|---|
| `GET /login`, `POST /login`, `POST /logout` | Sign-in and sign-out. |
| `GET /lang?set=en\|zh&next=…` | Sets the language cookie and redirects to a local path. |
| `GET /explorer?source=&path=&q=&offset=` | Current files, deleted items, and search results. |
| `GET /history?source=&point=&path=&cursor=` | Time Machine: timeline of recovery points and the folder at the selected point. |
| `GET /versions?source=&path=` | Versions of one file or folder. |
| `GET /backups`, `POST /backups/sources`, `POST /backups/run`, `POST /backups/verify` | Sources, recovery points, jobs; add a source, start a backup, start an integrity check. |
| `GET /jobs/{id}`, `POST /jobs/{id}/cancel` | A job, its log and (for restores) the per-item results. |
| `POST /restore/preview`, `POST /restore/start` | Restore preview and confirmation. |
| `GET /audit`, `GET /status` | Audit log and component status. |

A request that carries the header `X-TV-Partial: 1` receives only the page content, which the UI script swaps in without a full reload; links remain ordinary deep-linkable URLs.

## Example (PowerShell)

```powershell
$base = 'http://127.0.0.1:8470'
$session = $null
try { Invoke-WebRequest "$base/login" -Method Post -UseBasicParsing -MaximumRedirection 0 -SessionVariable session `
        -Body @{ username = 'admin'; password = $password; next = '/' } } catch { }   # a 303 is expected
$csrf = (Invoke-RestMethod "$base/api/v1/session" -WebSession $session).csrf_token
Invoke-RestMethod "$base/api/v1/backups" -Method Post -WebSession $session `
    -Headers @{ 'X-CSRF-Token' = $csrf } -ContentType 'application/json' -Body '{"source_id": 1}'
```

`scripts/dev-demo.ps1` is a complete working example.

## Differences from plan.md

- Folders and files are addressed by **source-relative `path`**, not by `parent_id` / `node_id`, so every URL stays deep-linkable and no internal id leaks.
- `GET /api/v1/session` and `POST /api/v1/sources` were added so the API can be scripted.
- `GET /api/v1/files` reads the live folder and returns offset paging; `history/tree` uses cursor paging.
- Restore requests name a destination **folder below the configured restore root** instead of a free path.
- Job progress is polled (`/api/v1/jobs/{id}`); server-sent events are not implemented.

---

# 中文

## 概述

TimeVault 在 `/api/v1` 下提供 JSON API，同时提供服务端渲染的页面。两者由同一个服务层实现，行为一致。本文档描述当前已有的内容；与 plan.md §6 中示意性 API 的差异见[与 plan.md 的差异](#与-planmd-的差异)。

## 约定

- **认证。** 通过 `POST /login` 登录（表单字段 `username`、`password`，可选 `next`）。响应会设置 `tv_session` Cookie（HttpOnly、SameSite=Lax；当 `TIMEVAULT_COOKIE_SECURE=true` 时带 Secure）。所有 `/api/v1` 接口都需要它，否则返回带 JSON 错误的 `401`。
- **CSRF。** 所有 `POST` 请求都需要在 `X-CSRF-Token` 请求头中携带会话的令牌（HTML 表单使用 `csrf_token` 字段）。可从 `GET /api/v1/session` 获取。如果 `Origin` 或 `Sec-Fetch-Site` 显示请求来自其他站点，则返回 `403`。
- **JSON。** 请求体为 `Content-Type: application/json` 的 JSON；未知字段会返回 `400`。时间为 RFC 3339 格式，大小单位为字节。
- **错误。** `{"error": {"code": "...", "message": "..."}}`，状态码含义：`400` 输入无效（路径错误、目标错误、未知字段），`401` 未登录，`403` CSRF，`404` 未找到，`409` 冲突（备份源重复、忙碌、预览已过期），`422` 缺少确认或存在被阻止的项目，`503` 搜索引擎不可用或健康检查失败，`500` 内部错误（消息只包含参考编号，细节在服务端日志中）。
- **路径。** 相对于备份源，使用 `/` 或 `\`；忽略开头和结尾的分隔符；`""` 表示备份源根目录。`..`、盘符、保留设备名和非法字符会返回 `400`。
- **分页。** `files` 和 `search` 使用 `offset`/`limit`，还有更多结果时返回 `next_offset`。`history/tree` 使用不透明的 `cursor` 并返回 `next_cursor`。`audit` 使用 `before`（事件 id）和 `limit`。

## 接口

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/healthz` | 存活探针，无需认证，返回 `ok`。 |
| GET | `/api/v1/session` | `user`、`role`、`csrf_token`、`auth_disabled`。 |
| GET | `/api/v1/health` | `ok`、`components[]`（`database`、`repository`、`search`、`restore`、`vss`、`usn`、`jobs`），每项含 `state`（`ok`、`degraded`、`disabled`、`failed`）和 `detail`；有组件为 `failed` 时 HTTP 状态为 `503`。 |
| GET | `/api/v1/sources` | 已登记的备份源及其最新已提交恢复点。 |
| POST | `/api/v1/sources` | 登记备份源：`{"name", "root_path", "excludes": []}`（省略时使用默认排除规则）。返回 `201`。文件夹必须是本机已存在的目录，且不得与仓库、恢复目录或其他备份源重叠。 |
| GET | `/api/v1/files` | 当前文件夹：`source_id`、`path`、`offset`、`limit`（≤ 1000）。返回 `entries[]`（`name`、`path`、`is_dir`、`size`、`modified_at`、`status` = `protected` / `changed` / `new` / `not_captured`）、`total`、`next_offset`、`deleted[]`（存在于最新恢复点、但磁盘上已不存在的项目）、`latest_point`。 |
| GET | `/api/v1/search` | `q`、`source_id`、`offset`、`limit`（≤ 200）。返回 `results[]`、`total`、`next_offset`。搜索引擎不可用或未配置时返回 `503`，错误码为 `search_unavailable`。 |
| GET | `/api/v1/recovery-points` | `source_id` 的已提交恢复点，最新的在前（`limit`、`before_seq`）。每个包含 `seq`、`kind`（`full` / `incremental`）、`consistency`（`live_scan`）、计数器、`verification`、`restorable`、`manifest_sha256`。 |
| GET | `/api/v1/history/tree` | 文件夹在 `point_id` 时的样子：`source_id`、`point_id`、`path`、`cursor`、`limit`。每个条目带有相对最新恢复点的 `status`：`same`、`modified`、`deleted`。 |
| GET | `/api/v1/history/versions` | `path` 的每个版本：`captured_at`、`until`、`current`、`size`、`sha256`、`state`、`first_point_id`、`object_present`。 |
| POST | `/api/v1/backups` | `{"source_id", "rehash": false}`。返回 `202` 及 `{job, already_active}`；如果该备份源已有排队或运行中的备份，则返回已有任务，状态 `200`，且 `already_active: true`。 |
| GET | `/api/v1/jobs/{id}` | `{job, events[]}`；`after_event` 只返回更新的日志行。排队或运行中时 `job.active` 为 true；`job.progress` 是一个 JSON 对象，字段取决于任务类型。 |
| POST | `/api/v1/restores/preview` | `{"source_id", "point_id", "paths": [], "dest_name", "policy": "skip"|"rename"|"overwrite", "allow_partial": false}`。不写入任何内容。返回 `destination`、`totals`（`create`、`overwrite`、`rename`、`skip_same`、`skip_exists`、`blocked`、`missing_objects`、`not_captured` 等）、`sample[]`、`problems[]`、`digest` 和 `confirmation_token`。 |
| POST | `/api/v1/restores` | 预览请求体，加上 `confirmation_token`、`idempotency_key` 和 `confirm_overwrite`。返回 `202` 及 `{job}`。令牌缺失或无效、未设置 `confirm_overwrite` 就要替换文件、或未设置 `allow_partial` 却有被阻止的项目时返回 `422`；预览已过期时返回 `409`。使用相同 `idempotency_key` 重复请求会返回原来的任务。 |
| GET | `/api/v1/audit` | 审计事件，最新的在前：`action`、`actor`、`before`、`limit`。 |

## 页面路由

| 路由 | 页面 |
|---|---|
| `GET /login`、`POST /login`、`POST /logout` | 登录与退出。 |
| `GET /lang?set=en\|zh&next=…` | 设置语言 Cookie，并重定向到本站路径。 |
| `GET /explorer?source=&path=&q=&offset=` | 当前文件、已删除项目和搜索结果。 |
| `GET /history?source=&point=&path=&cursor=` | 时间机器：恢复点时间轴，以及所选恢复点时的文件夹。 |
| `GET /versions?source=&path=` | 单个文件或文件夹的各个版本。 |
| `GET /backups`、`POST /backups/sources`、`POST /backups/run`、`POST /backups/verify` | 备份源、恢复点、任务；添加备份源、启动备份、启动完整性校验。 |
| `GET /jobs/{id}`、`POST /jobs/{id}/cancel` | 任务、任务日志，以及（恢复任务的）逐项结果。 |
| `POST /restore/preview`、`POST /restore/start` | 恢复预览与确认。 |
| `GET /audit`、`GET /status` | 审计日志与组件状态。 |

带有 `X-TV-Partial: 1` 请求头的请求只会得到页面内容，界面脚本会据此无刷新地替换内容；链接仍然是普通的、可直接访问的 URL。

## 示例（PowerShell）

```powershell
$base = 'http://127.0.0.1:8470'
$session = $null
try { Invoke-WebRequest "$base/login" -Method Post -UseBasicParsing -MaximumRedirection 0 -SessionVariable session `
        -Body @{ username = 'admin'; password = $password; next = '/' } } catch { }   # 预期会收到 303
$csrf = (Invoke-RestMethod "$base/api/v1/session" -WebSession $session).csrf_token
Invoke-RestMethod "$base/api/v1/backups" -Method Post -WebSession $session `
    -Headers @{ 'X-CSRF-Token' = $csrf } -ContentType 'application/json' -Body '{"source_id": 1}'
```

`scripts/dev-demo.ps1` 是一个完整可运行的示例。

## 与 plan.md 的差异

- 文件夹和文件通过**相对于备份源的 `path`** 寻址，而不是 `parent_id` / `node_id`，这样每个 URL 都可以直接访问，也不会泄露内部 id。
- 新增了 `GET /api/v1/session` 和 `POST /api/v1/sources`，使 API 可以脚本化调用。
- `GET /api/v1/files` 读取实时文件夹并使用 offset 分页；`history/tree` 使用 cursor 分页。
- 恢复请求指定的是**所配置恢复根目录之下的目标文件夹**，而不是任意路径。
- 任务进度通过轮询（`/api/v1/jobs/{id}`）获取；未实现 Server-Sent Events。
