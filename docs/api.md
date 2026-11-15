# English

## Overview

TimeVault (version 0.1.1) serves a JSON API below `/api/v1` and server-rendered pages. Both are implemented by the same service layer, so they behave identically. This document describes what exists now; it differs from the illustrative API in plan.md §6 in the ways listed in [Differences from plan.md](#differences-from-planmd).

## Conventions

- **Authentication.** Reading is open to everyone; everything that changes something, and everything that shows the inside of the service, needs an account (see [Who may call what](#who-may-call-what)). Sign in with `POST /login` (form fields `username`, `password`, optional `next`). `username` is the employee ID, the name or the e-mail address of a company account from the Redis user directory, or the name of the local administrator. The response sets the `tv_session` cookie (HttpOnly, SameSite=Lax, Secure when `TIMEVAULT_COOKIE_SECURE=true`). A protected endpoint answers `401` with a JSON error when there is no valid session, and `403` when the account may not do it. The sign-in itself answers `401` for a wrong or unknown account (the same text for every reason), `429` with `Retry-After` when too many attempts were made, and `503` when the user directory cannot be reached.
- **CSRF.** Every `POST`, `PATCH` and other state-changing request of a signed-in account needs the session's token in the `X-CSRF-Token` header (HTML forms use the `csrf_token` field). Read it from `GET /api/v1/session`. A request whose `Origin` or `Sec-Fetch-Site` shows another site is rejected with `403`.
- **JSON.** Request bodies are JSON with `Content-Type: application/json`; unknown fields are rejected with `400`. Times are RFC 3339. Sizes are bytes.
- **Errors.** `{"error": {"code": "...", "message": "..."}}` with these statuses: `400` invalid input (bad path, bad destination, unknown field, a schedule outside 1 to 43200), `401` not signed in, `403` CSRF (`csrf`) or not allowed (`forbidden`), `404` not found, `409` conflict (duplicate monitored folder, a backup of the folder is running (`source_busy`), the folder is switched off (`source_disabled`), stale preview), `422` confirmation missing or items blocked, `503` search engine unavailable, folder unreadable or health failure, `500` internal error (the message carries only a reference id; details are in the server log).
- **Paths.** Source-relative, using `/` or `\`; leading and trailing separators are ignored; `""` is the source root. `..`, drive letters, reserved device names and invalid characters are rejected with `400`.
- **Paging.** `files` and `search` use `offset`/`limit` and return `next_offset` while more results exist. `history/tree` uses an opaque `cursor` and returns `next_cursor`. `audit` uses `before` (an event id) and `limit`.

## Who may call what

| Caller | May |
|---|---|
| Anyone | `GET /healthz`, `GET /api/v1/session`, `GET /api/v1/sources`, `/files`, `/search`, `/recovery-points`, `/history/tree`, `/history/versions`, and `GET /api/v1/jobs/{id}` of a backup or verify job. |
| Any signed-in account | Everything above with more detail (see below), and `POST /api/v1/backups`, `POST /api/v1/restores/preview`, `POST /api/v1/restores`, `GET /api/v1/jobs/{id}` of a restore job, `GET /api/v1/audit`, `GET /api/v1/health`, `GET /api/v1/settings`. |
| `admin` account (or the local administrator) | Everything above, and `POST /api/v1/sources` and `PATCH /api/v1/sources/{id}`. |

What a visitor without an account does not get: `root_path` in `GET /api/v1/sources` (the folder on the server's disk is left out), and in `GET /api/v1/jobs/{id}` the `requested_by`, the `error` text and the `events` (the log). A restore job is `401` for a visitor.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Liveness probe, no authentication, returns `ok`. |
| GET | `/api/v1/session` | `{"authenticated": false}` for a visitor; for an account `authenticated`, `user` (employee ID), `name`, `role` (`user` or `admin`), `csrf_token`, `auth_disabled`. |
| GET | `/api/v1/health` | Accounts only. `ok`, `components[]` (`database`, `repository`, `search`, `directory`, `restore`, `vss`, `usn`, `jobs`) each with `state` (`ok`, `degraded`, `disabled`, `failed`) and `detail`; HTTP `503` when a component is `failed`. The user directory is `degraded` when Redis does not answer, which is not a failure. |
| GET | `/api/v1/settings` | Accounts only. The running configuration without secrets: `version`, `listen`, `database` and `user_directory` (`host:port/name`), `repository`, `restore_root`, `env_file`, `search`, `local_admin` (boolean), `sign_in_disabled`, `session_ttl`, `cookie_secure`. |
| GET | `/api/v1/sources` | Monitored folders with their newest committed recovery point: `id`, `name`, `root_path` (accounts only), `excludes`, `enabled`, `schedule_minutes` (`null` = manual only), `last_run_at`, `created_at`, `latest_point`. |
| POST | `/api/v1/sources` | Administrators only. Add a monitored folder: `{"name", "root_path", "excludes": [], "schedule_minutes": null}` (default excludes when omitted; `schedule_minutes` is 1 to 43200 or omitted). `201`. The folder must be a local, existing directory that does not overlap the repository, the restore folder or another monitored folder. |
| PATCH | `/api/v1/sources/{id}` | Administrators only. Change the fields that are present and leave the others: `name`, `root_path`, `excludes`, `enabled`, `schedule_minutes` (a number, or `null` to remove the schedule). `200` with the source. `400` for an invalid value, `409` when a backup of the folder is running (`source_busy`) or the name or folder is taken. The folder of a source that has any recovery point cannot be changed (`400`). A change of folder forgets the last run. |
| GET | `/api/v1/files` | Current folder: `source_id`, `path`, `offset`, `limit` (≤ 1000). Returns `entries[]` (`name`, `path`, `is_dir`, `size`, `modified_at`, `status` = `protected` / `changed` / `new` / `not_captured`), `total`, `next_offset`, `deleted[]` (items present in the newest recovery point but gone from disk), `latest_point`. |
| GET | `/api/v1/search` | `q`, `source_id`, `offset`, `limit` (≤ 200). Returns `results[]`, `total`, `next_offset`. `503` with code `search_unavailable` when the engine is down or not configured. |
| GET | `/api/v1/recovery-points` | Committed points of `source_id`, newest first (`limit`, `before_seq`). Each has `seq`, `kind` (`full` / `incremental`), `consistency` (`live_scan`), counters, `verification`, `restorable`, `manifest_sha256`. |
| GET | `/api/v1/history/tree` | A folder as it was at `point_id`: `source_id`, `point_id`, `path`, `cursor`, `limit`. Each entry has `status` relative to the newest point: `same`, `modified`, `deleted`. |
| GET | `/api/v1/history/versions` | Every version of `path`: `captured_at`, `until`, `current`, `size`, `sha256`, `state`, `first_point_id`, `object_present`. |
| POST | `/api/v1/backups` | Accounts only. `{"source_id", "rehash": false}`. `202` with `{job, already_active}`; if a backup of that source is already queued or running (it may be an automatic one) the existing job is returned with `200` and `already_active: true`. `409` (`source_disabled`) when the folder is switched off. |
| GET | `/api/v1/jobs/{id}` | `{job, events[]}`; `after_event` returns only newer log lines. `job.active` is true while queued or running; `job.progress` is a JSON object whose fields depend on the kind. A visitor gets no `requested_by`, no `error` and no events; a restore job needs an account. An automatic backup that finds nothing changed leaves no job, so its id answers `404`. |
| POST | `/api/v1/restores/preview` | Accounts only. `{"source_id", "point_id", "paths": [], "dest_name", "policy": "skip"|"rename"|"overwrite", "allow_partial": false}`. Writes nothing. Returns `destination`, `totals` (`create`, `overwrite`, `rename`, `skip_same`, `skip_exists`, `blocked`, `missing_objects`, `not_captured`, …), `sample[]`, `problems[]`, `digest` and `confirmation_token`. |
| POST | `/api/v1/restores` | Accounts only. The preview body plus `confirmation_token`, `idempotency_key` and `confirm_overwrite`. `202` with `{job}`. `422` when the token is missing or invalid, when files would be replaced without `confirm_overwrite`, or when items are blocked without `allow_partial`; `409` when the preview is stale. Repeating a request with the same `idempotency_key` returns the original job. |
| GET | `/api/v1/audit` | Accounts only. Audit events newest first: `action`, `actor`, `before`, `limit`. |

## Page routes

| Route | Page |
|---|---|
| `GET /login`, `POST /login`, `POST /logout` | Sign-in and sign-out. Anyone may open the sign-in page; signing out needs a session. |
| `GET /lang?set=en\|zh&next=…` | Sets the language cookie and redirects to a local path. |
| `GET /explorer?source=&path=&q=&offset=` | Current files, deleted items, and search results. Open to everyone. |
| `GET /history?source=&point=&path=&cursor=` | Time Machine: timeline of recovery points and the folder at the selected point. Open to everyone; the restore form needs an account. |
| `GET /versions?source=&path=` | Versions of one file or folder. Open to everyone. |
| `GET /backups`, `POST /backups/run`, `POST /backups/verify` | Monitored folders, recovery points, jobs, schedules; start a backup, start an integrity check (accounts). Viewing is open to everyone; visitors do not see the folders' paths, restore jobs, who asked for a job or why one failed. |
| `GET /jobs/{id}`, `POST /jobs/{id}/cancel` | A job, its log and (for restores) the per-item results. Viewing a backup job is open to everyone without the requester, the error and the log; a restore job and cancelling need an account. |
| `POST /restore/preview`, `POST /restore/start` | Restore preview and confirmation (accounts). |
| `GET /audit`, `GET /status`, `GET /settings` | Audit log, component status, and the settings page (accounts). |
| `POST /settings/sources`, `POST /settings/sources/{id}` | Add and edit a monitored folder (administrators). |

A request that carries the header `X-TV-Partial: 1` receives only the page content, which the UI script swaps in without a full reload; links remain ordinary deep-linkable URLs.

## Example (PowerShell)

```powershell
$base = 'http://127.0.0.1:8660'
$session = $null
try { Invoke-WebRequest "$base/login" -Method Post -UseBasicParsing -MaximumRedirection 0 -SessionVariable session `
        -Body @{ username = 'admin'; password = $password; next = '/' } } catch { }   # a 303 is expected
$csrf = (Invoke-RestMethod "$base/api/v1/session" -WebSession $session).csrf_token
Invoke-RestMethod "$base/api/v1/backups" -Method Post -WebSession $session `
    -Headers @{ 'X-CSRF-Token' = $csrf } -ContentType 'application/json' -Body '{"source_id": 1}'
```

`scripts/dev-demo.ps1` is a complete working example (it signs in as the local administrator). A company account signs in the same way with its employee ID, name or e-mail address as `username`.

## Differences from plan.md

- Folders and files are addressed by **source-relative `path`**, not by `parent_id` / `node_id`, so every URL stays deep-linkable and no internal id leaks.
- `GET /api/v1/session`, `GET /api/v1/settings`, `POST /api/v1/sources` and `PATCH /api/v1/sources/{id}` were added so the API can be scripted.
- Reading is open to everyone and changes need an account, with two roles (`user`, `admin`); plan.md describes a richer role model that is not implemented.
- `GET /api/v1/files` reads the live folder and returns offset paging; `history/tree` uses cursor paging.
- Restore requests name a destination **folder below the configured restore root** instead of a free path.
- Job progress is polled (`/api/v1/jobs/{id}`); server-sent events are not implemented.

---

# 中文

## 概述

TimeVault（0.1.1 版）在 `/api/v1` 下提供 JSON API，同时提供服务端渲染的页面。两者由同一个服务层实现，行为一致。本文档描述当前已有的内容；与 plan.md §6 中示意性 API 的差异见[与 plan.md 的差异](#与-planmd-的差异)。

## 约定

- **认证。** 读取对所有人开放；任何会改变内容的操作，以及任何展示服务内部情况的内容，都需要账号（见[谁可以调用什么](#谁可以调用什么)）。通过 `POST /login` 登录（表单字段 `username`、`password`，可选 `next`）。`username` 是 Redis 用户目录中公司账号的工号、姓名或邮箱，或者是本地管理员的名字。响应会设置 `tv_session` Cookie（HttpOnly、SameSite=Lax；当 `TIMEVAULT_COOKIE_SECURE=true` 时带 Secure）。受保护的接口在没有有效会话时返回带 JSON 错误的 `401`，在账号无权执行时返回 `403`。登录本身对错误或未知的账号返回 `401`（无论原因，文本都相同），尝试次数过多时返回 `429` 和 `Retry-After`，无法连接用户目录时返回 `503`。
- **CSRF。** 已登录账号的所有 `POST`、`PATCH` 以及其他会改变状态的请求，都需要在 `X-CSRF-Token` 请求头中携带会话的令牌（HTML 表单使用 `csrf_token` 字段）。可从 `GET /api/v1/session` 获取。如果 `Origin` 或 `Sec-Fetch-Site` 显示请求来自其他站点，则返回 `403`。
- **JSON。** 请求体为 `Content-Type: application/json` 的 JSON；未知字段会返回 `400`。时间为 RFC 3339 格式，大小单位为字节。
- **错误。** `{"error": {"code": "...", "message": "..."}}`，状态码含义：`400` 输入无效（路径错误、目标错误、未知字段、1 到 43200 之外的计划），`401` 未登录，`403` CSRF（`csrf`）或无权执行（`forbidden`），`404` 未找到，`409` 冲突（监控文件夹重复、该文件夹的备份正在运行（`source_busy`）、文件夹已停用（`source_disabled`）、预览已过期），`422` 缺少确认或存在被阻止的项目，`503` 搜索引擎不可用、文件夹无法读取或健康检查失败，`500` 内部错误（消息只包含参考编号，细节在服务端日志中）。
- **路径。** 相对于备份源，使用 `/` 或 `\`；忽略开头和结尾的分隔符；`""` 表示备份源根目录。`..`、盘符、保留设备名和非法字符会返回 `400`。
- **分页。** `files` 和 `search` 使用 `offset`/`limit`，还有更多结果时返回 `next_offset`。`history/tree` 使用不透明的 `cursor` 并返回 `next_cursor`。`audit` 使用 `before`（事件 id）和 `limit`。

## 谁可以调用什么

| 调用者 | 可以 |
|---|---|
| 任何人 | `GET /healthz`、`GET /api/v1/session`、`GET /api/v1/sources`、`/files`、`/search`、`/recovery-points`、`/history/tree`、`/history/versions`，以及备份或校验任务的 `GET /api/v1/jobs/{id}`。 |
| 任何已登录的账号 | 上述全部（内容更详细，见下文），以及 `POST /api/v1/backups`、`POST /api/v1/restores/preview`、`POST /api/v1/restores`、恢复任务的 `GET /api/v1/jobs/{id}`、`GET /api/v1/audit`、`GET /api/v1/health`、`GET /api/v1/settings`。 |
| `admin` 账号（或本地管理员） | 上述全部，以及 `POST /api/v1/sources` 和 `PATCH /api/v1/sources/{id}`。 |

没有账号的访问者得不到的内容：`GET /api/v1/sources` 中的 `root_path`（文件夹在服务器磁盘上的路径被省略），以及 `GET /api/v1/jobs/{id}` 中的 `requested_by`、`error` 文本和 `events`（日志）。恢复任务对访问者返回 `401`。

## 接口

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/healthz` | 存活探针，无需认证，返回 `ok`。 |
| GET | `/api/v1/session` | 访问者得到 `{"authenticated": false}`；账号得到 `authenticated`、`user`（工号）、`name`、`role`（`user` 或 `admin`）、`csrf_token`、`auth_disabled`。 |
| GET | `/api/v1/health` | 仅限账号。`ok`、`components[]`（`database`、`repository`、`search`、`directory`、`restore`、`vss`、`usn`、`jobs`），每项含 `state`（`ok`、`degraded`、`disabled`、`failed`）和 `detail`；有组件为 `failed` 时 HTTP 状态为 `503`。Redis 没有应答时用户目录为 `degraded`，这不算故障。 |
| GET | `/api/v1/settings` | 仅限账号。当前运行的配置（不含机密信息）：`version`、`listen`、`database` 和 `user_directory`（`host:port/name`）、`repository`、`restore_root`、`env_file`、`search`、`local_admin`（布尔值）、`sign_in_disabled`、`session_ttl`、`cookie_secure`。 |
| GET | `/api/v1/sources` | 监控文件夹及其最新已提交恢复点：`id`、`name`、`root_path`（仅限账号）、`excludes`、`enabled`、`schedule_minutes`（`null` 表示仅手动）、`last_run_at`、`created_at`、`latest_point`。 |
| POST | `/api/v1/sources` | 仅限管理员。添加监控文件夹：`{"name", "root_path", "excludes": [], "schedule_minutes": null}`（省略时使用默认排除规则；`schedule_minutes` 为 1 到 43200，或省略）。返回 `201`。文件夹必须是本机已存在的目录，且不得与仓库、恢复目录或其他监控文件夹重叠。 |
| PATCH | `/api/v1/sources/{id}` | 仅限管理员。修改请求中出现的字段，其余保持不变：`name`、`root_path`、`excludes`、`enabled`、`schedule_minutes`（数字，或用 `null` 取消计划）。返回 `200` 及该备份源。值无效返回 `400`；该文件夹的备份正在运行（`source_busy`）或名称、文件夹已被占用时返回 `409`。已有任何恢复点的备份源不能更改文件夹（`400`）。更改文件夹会忘记上次运行。 |
| GET | `/api/v1/files` | 当前文件夹：`source_id`、`path`、`offset`、`limit`（≤ 1000）。返回 `entries[]`（`name`、`path`、`is_dir`、`size`、`modified_at`、`status` = `protected` / `changed` / `new` / `not_captured`）、`total`、`next_offset`、`deleted[]`（存在于最新恢复点、但磁盘上已不存在的项目）、`latest_point`。 |
| GET | `/api/v1/search` | `q`、`source_id`、`offset`、`limit`（≤ 200）。返回 `results[]`、`total`、`next_offset`。搜索引擎不可用或未配置时返回 `503`，错误码为 `search_unavailable`。 |
| GET | `/api/v1/recovery-points` | `source_id` 的已提交恢复点，最新的在前（`limit`、`before_seq`）。每个包含 `seq`、`kind`（`full` / `incremental`）、`consistency`（`live_scan`）、计数器、`verification`、`restorable`、`manifest_sha256`。 |
| GET | `/api/v1/history/tree` | 文件夹在 `point_id` 时的样子：`source_id`、`point_id`、`path`、`cursor`、`limit`。每个条目带有相对最新恢复点的 `status`：`same`、`modified`、`deleted`。 |
| GET | `/api/v1/history/versions` | `path` 的每个版本：`captured_at`、`until`、`current`、`size`、`sha256`、`state`、`first_point_id`、`object_present`。 |
| POST | `/api/v1/backups` | 仅限账号。`{"source_id", "rehash": false}`。返回 `202` 及 `{job, already_active}`；如果该备份源已有排队或运行中的备份（可能是自动备份），则返回已有任务，状态 `200`，且 `already_active: true`。文件夹已停用时返回 `409`（`source_disabled`）。 |
| GET | `/api/v1/jobs/{id}` | `{job, events[]}`；`after_event` 只返回更新的日志行。排队或运行中时 `job.active` 为 true；`job.progress` 是一个 JSON 对象，字段取决于任务类型。访问者得不到 `requested_by`、`error` 和事件；恢复任务需要账号。没有发现变化的自动备份不会留下任务，所以它的 id 会返回 `404`。 |
| POST | `/api/v1/restores/preview` | 仅限账号。`{"source_id", "point_id", "paths": [], "dest_name", "policy": "skip"|"rename"|"overwrite", "allow_partial": false}`。不写入任何内容。返回 `destination`、`totals`（`create`、`overwrite`、`rename`、`skip_same`、`skip_exists`、`blocked`、`missing_objects`、`not_captured` 等）、`sample[]`、`problems[]`、`digest` 和 `confirmation_token`。 |
| POST | `/api/v1/restores` | 仅限账号。预览请求体，加上 `confirmation_token`、`idempotency_key` 和 `confirm_overwrite`。返回 `202` 及 `{job}`。令牌缺失或无效、未设置 `confirm_overwrite` 就要替换文件、或未设置 `allow_partial` 却有被阻止的项目时返回 `422`；预览已过期时返回 `409`。使用相同 `idempotency_key` 重复请求会返回原来的任务。 |
| GET | `/api/v1/audit` | 仅限账号。审计事件，最新的在前：`action`、`actor`、`before`、`limit`。 |

## 页面路由

| 路由 | 页面 |
|---|---|
| `GET /login`、`POST /login`、`POST /logout` | 登录与退出。任何人都可以打开登录页；退出需要会话。 |
| `GET /lang?set=en\|zh&next=…` | 设置语言 Cookie，并重定向到本站路径。 |
| `GET /explorer?source=&path=&q=&offset=` | 当前文件、已删除项目和搜索结果。对所有人开放。 |
| `GET /history?source=&point=&path=&cursor=` | 时间机器：恢复点时间轴，以及所选恢复点时的文件夹。对所有人开放；恢复表单需要账号。 |
| `GET /versions?source=&path=` | 单个文件或文件夹的各个版本。对所有人开放。 |
| `GET /backups`、`POST /backups/run`、`POST /backups/verify` | 监控文件夹、恢复点、任务、计划；启动备份、启动完整性校验（账号）。查看对所有人开放；访问者看不到文件夹的路径、恢复任务、任务是谁发起的或为什么失败。 |
| `GET /jobs/{id}`、`POST /jobs/{id}/cancel` | 任务、任务日志，以及（恢复任务的）逐项结果。查看备份任务对所有人开放，但看不到发起人、错误和日志；恢复任务和取消操作需要账号。 |
| `POST /restore/preview`、`POST /restore/start` | 恢复预览与确认（账号）。 |
| `GET /audit`、`GET /status`、`GET /settings` | 审计日志、组件状态和设置页（账号）。 |
| `POST /settings/sources`、`POST /settings/sources/{id}` | 添加和编辑监控文件夹（管理员）。 |

带有 `X-TV-Partial: 1` 请求头的请求只会得到页面内容，界面脚本会据此无刷新地替换内容；链接仍然是普通的、可直接访问的 URL。

## 示例（PowerShell）

```powershell
$base = 'http://127.0.0.1:8660'
$session = $null
try { Invoke-WebRequest "$base/login" -Method Post -UseBasicParsing -MaximumRedirection 0 -SessionVariable session `
        -Body @{ username = 'admin'; password = $password; next = '/' } } catch { }   # 预期会收到 303
$csrf = (Invoke-RestMethod "$base/api/v1/session" -WebSession $session).csrf_token
Invoke-RestMethod "$base/api/v1/backups" -Method Post -WebSession $session `
    -Headers @{ 'X-CSRF-Token' = $csrf } -ContentType 'application/json' -Body '{"source_id": 1}'
```

`scripts/dev-demo.ps1` 是一个完整可运行的示例（它以本地管理员身份登录）。公司账号用同样的方式登录，`username` 填工号、姓名或邮箱。

## 与 plan.md 的差异

- 文件夹和文件通过**相对于备份源的 `path`** 寻址，而不是 `parent_id` / `node_id`，这样每个 URL 都可以直接访问，也不会泄露内部 id。
- 新增了 `GET /api/v1/session`、`GET /api/v1/settings`、`POST /api/v1/sources` 和 `PATCH /api/v1/sources/{id}`，使 API 可以脚本化调用。
- 读取对所有人开放，修改需要账号，有两种角色（`user`、`admin`）；plan.md 描述了更丰富的角色模型，目前没有实现。
- `GET /api/v1/files` 读取实时文件夹并使用 offset 分页；`history/tree` 使用 cursor 分页。
- 恢复请求指定的是**所配置恢复根目录之下的目标文件夹**，而不是任意路径。
- 任务进度通过轮询（`/api/v1/jobs/{id}`）获取；未实现 Server-Sent Events。
