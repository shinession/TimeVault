# English

## Overview

TimeVault (version 0.1.6) serves a JSON API below `/api/v1` and server-rendered pages. Both are implemented by the same service layer, so they behave identically. This document describes what exists now; it differs from the illustrative API in plan.md §6 in the ways listed in [Differences from plan.md](#differences-from-planmd).

## Conventions

- **Authentication.** Reading is open to everyone; everything that changes something, and everything that shows the inside of the service, needs an account (see [Who may call what](#who-may-call-what)). Sign in with `POST /login` (form fields `username`, `password`, optional `next`). `username` is the employee ID, the name or the e-mail address of a company account from the Redis user directory, or the name of the local administrator. The response sets the `tv_session` cookie (HttpOnly, SameSite=Lax, Secure when `TIMEVAULT_COOKIE_SECURE=true`). A protected endpoint answers `401` with a JSON error when there is no valid session, and `403` when the account may not do it. The sign-in itself answers `401` for a wrong or unknown account (the same text for every reason), `429` with `Retry-After` when too many attempts were made, and `503` when the user directory cannot be reached. **Single sign-on** (when `TIMEVAULT_SSO_HOST` is set) is the other way in: the login page calls `POST /auth/sso/start` and then `POST /auth/sso/poll` (see [Page routes](#page-routes)).
- **Base path.** With `TIMEVAULT_BASE_PATH=/tv` (see [runbook.md](./runbook.md#behind-a-reverse-proxy-under-a-path)) every path in this document is under `/tv` as the browser sees it (`/tv/api/v1/sources`, `/tv/login`). The service also accepts a request without the prefix, for a proxy that takes it off. The `Location` headers of the API and the `next` of `POST /auth/sso/poll` carry the prefix, and the cookies are limited to it. Without the setting every path is as written here.
- **CSRF.** Every `POST`, `PATCH` and other state-changing request of a signed-in account needs the session's token in the `X-CSRF-Token` header (HTML forms use the `csrf_token` field). Read it from `GET /api/v1/session`. A request whose `Origin` or `Sec-Fetch-Site` shows another site is rejected with `403`.
- **JSON.** Request bodies are JSON with `Content-Type: application/json`; unknown fields are rejected with `400`. Times are RFC 3339. Sizes are bytes.
- **Errors.** `{"error": {"code": "...", "message": "..."}}` with these statuses: `400` invalid input (bad path, bad destination, unknown field, a schedule outside 1 to 43200), `401` not signed in, `403` CSRF (`csrf`) or not allowed (`forbidden`), `404` not found, `409` conflict (duplicate monitored folder, a backup of the folder is running (`source_busy`), the folder is switched off (`source_disabled`), stale preview, old versions of the folder are being deleted, so a restore, a verification or a change of the version limit is refused (`prune_running`), lowering the version limit would delete versions and `confirm_prune` was not sent (`prune_not_confirmed`)), `422` confirmation missing or items blocked, `503` search engine unavailable, folder unreadable or health failure, `500` internal error (the message carries only a reference id; details are in the server log).
- **Paths.** Source-relative, using `/` or `\`; leading and trailing separators are ignored; `""` is the source root. `..`, drive letters, reserved device names and invalid characters are rejected with `400`.
- **Paging, sorting and filtering.** `files` and `search` use `offset`/`limit` and return `next_offset` while more results exist. `files` and `history/tree` also take `sort` (`name`, `type`, `size` or `mtime`; default `name`), `order` (`asc` or `desc`), `type` (a filter: `dir`, `file` for everything that is not a folder, `link`, `noext`, or `ext:<extension>` in lower case) and `dirs_first=true` (folders before everything else; the pages always do this, the API only when asked). A value that is not understood means the default: by name, no filter. `history/tree` keeps its opaque `cursor` and `next_cursor` as long as none of `sort`, `order`, `type`, `dirs_first` and `offset` is given; with one of them it takes `offset`/`limit` like `files`. These list answers have `total` (the items that match the filter), `unfiltered` (the items of the folder) and `types[]` (`type` and `count` of every kind of item in the folder, whatever the filter), and every entry has a `type`: `dir`, `link` (a junction or symbolic link), `noext` (a file without an extension) or `ext:<extension>`. `audit` uses `before` (an event id) and `limit`.

## Who may call what

| Caller | May |
|---|---|
| Anyone | `GET /healthz`, `GET /api/v1/session`, `GET /api/v1/sources`, `/files`, `/search`, `/recovery-points`, `/history/tree`, `/history/versions`, and `GET /api/v1/jobs/{id}` of a backup or verify job. Also `POST /auth/sso/start` and `POST /auth/sso/poll`, which exist to sign in (only when single sign-on is configured, and only from the site itself). |
| Any signed-in account | Everything above with more detail (see below), and `POST /api/v1/backups`, `POST /api/v1/restores/preview`, `POST /api/v1/restores`, `GET /api/v1/jobs/{id}` of a restore job, `GET /api/v1/audit`, `GET /api/v1/health`, `GET /api/v1/settings`. |
| `admin` account (or the local administrator) | Everything above, and `POST /api/v1/sources` and `PATCH /api/v1/sources/{id}`. |

What a visitor without an account does not get: in `GET /api/v1/jobs/{id}` the `requested_by`, the `error` text and the `events` (the log). A restore job is `401` for a visitor.

What only administrators get is the folders of the server's disk: `root_path` in `GET /api/v1/sources`, `repository`, `restore_root` and `env_file` in `GET /api/v1/settings`, and `destination_real` in a restore preview. Everybody gets the display folders instead (`display_path`, `destination`, `restore_display_root`). In the answers to anybody else, the real folders in texts (job errors and logs, restore messages and problems, audit details, health details, the reasons a file was not captured) are replaced as described in [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md): by the display folder, by `[<name of the monitored folder>]`, `[backup folder]` or `[restore folder]`.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Liveness probe, no authentication, returns `ok`. |
| GET | `/api/v1/session` | `{"authenticated": false}` for a visitor; for an account `authenticated`, `user` (employee ID), `name`, `role` (`user` or `admin`), `csrf_token`, `auth_disabled`. |
| GET | `/api/v1/health` | Accounts only. `ok`, `components[]` (`database`, `repository`, `search`, `directory`, `restore`, `vss`, `usn`, `jobs`) each with `state` (`ok`, `degraded`, `disabled`, `failed`) and `detail`; HTTP `503` when a component is `failed`. The user directory is `degraded` when Redis does not answer, which is not a failure. For accounts that are not administrators the `detail` of the repository is only how full it is, and real folders are replaced in the other details. |
| GET | `/api/v1/settings` | Accounts only. The running configuration without secrets: `version`, `listen`, `database` and `user_directory` (`host:port/name`), `sso_host` (the single sign-on service, empty when it is off), `base_path` (the path prefix, empty when there is none), `server_host`, `restore_display_root`, `search`, `local_admin` (boolean), `sign_in_disabled`, `session_ttl`, `cookie_secure`. Administrators also get `repository`, `restore_root` and `env_file`. |
| GET | `/api/v1/sources` | Monitored folders with their newest committed recovery point: `id`, `name`, `display_path` (when there is one), `root_path` (administrators only), `excludes`, `enabled`, `schedule_minutes` (`null` = manual only), `last_run_at`, `last_run_ms` (how long the last backup run took, when known), `max_versions` (the most versions kept of one file; `null` = every version is kept), `created_at`, `latest_point`. |
| POST | `/api/v1/sources` | Administrators only. Add a monitored folder: `{"name", "display_path", "root_path", "excludes": [], "schedule_minutes": null, "max_versions": null}` (default excludes when omitted; `schedule_minutes` is 1 to 43200 or omitted; `max_versions` is 1 to 10000, and `0`, `null` or omitted keeps every version; `400` for any other value). `201`. The folder must be a local, existing directory that does not overlap the repository, the restore folder or another monitored folder. `display_path` is the network path people open the folder with (`\\server\share\folder`, either slash, no hidden share ending in `$`). It is required, and its host must be `TIMEVAULT_SERVER_HOST` (case does not matter, it is stored as the setting spells it), when that setting is set; otherwise it is optional. `400` when it is not a network path or is on another host. |
| PATCH | `/api/v1/sources/{id}` | Administrators only. Change the fields that are present and leave the others: `name`, `display_path` (an empty string removes it, which `TIMEVAULT_SERVER_HOST` forbids), `root_path`, `excludes`, `enabled`, `schedule_minutes` (a number, or `null` to remove the schedule), `max_versions` (1 to 10000, or `0` or `null` to keep every version) and `confirm_prune`. `200` with the source. `400` for an invalid value, `409` when a backup of the folder is running (`source_busy`) or the name or folder is taken. The folder of a source that has any recovery point cannot be changed (`400`). A change of folder forgets the last run. While `TIMEVAULT_SERVER_HOST` is set, a source that has no display folder cannot be changed (`400`) until the request gives it one. A `max_versions` that is set, or set lower than before, on a source that has history queues a version cleanup job for the whole folder (see [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md)). When that would delete versions, the request is refused with `409` `prune_not_confirmed` (the message says how many versions of how many files) unless it carries `"confirm_prune": true`. While a cleanup of the folder runs, a request that changes `max_versions` is refused with `409` `prune_running`; the other fields can still be changed. |
| GET | `/api/v1/files` | Current folder: `source_id`, `path`, `sort`, `order`, `type`, `dirs_first`, `offset`, `limit` (≤ 1000, default 200). Returns `entries[]` (`name`, `path`, `is_dir`, `type`, `size`, `modified_at`, `status` = `protected` / `changed` / `new` / `not_captured`), `total`, `unfiltered`, `types[]`, `next_offset`, `deleted[]` (items present in the newest recovery point but gone from disk, following the type filter), `latest_point`. The `size` of a folder is the size of everything inside it according to the index of Everything, or `0` when Everything does not know it; sorting by `size` uses it. |
| GET | `/api/v1/search` | `q`, `source_id`, `offset`, `limit` (≤ 200). Returns `results[]` (with a `type`), `total`, `next_offset`. `503` with code `search_unavailable` when the engine is down or not configured. |
| GET | `/api/v1/recovery-points` | Committed points of `source_id`, newest first (`limit`, `before_seq`). Each has `seq`, `kind` (`full` / `incremental`), `consistency` (`live_scan`), counters, `verification`, `restorable`, `partial` (true when older versions of some files were deleted to stay within the folder's version limit, so the point no longer shows every file as it was), `manifest_sha256`. |
| GET | `/api/v1/history/tree` | A folder as it was at `point_id`: `source_id`, `point_id`, `path`, and either `cursor`/`limit` or the list parameters of `files`. Each entry has a `type` and a `status` relative to the newest point: `same`, `modified`, `deleted`. |
| GET | `/api/v1/history/versions` | Every version of `path`: `type`, `captured_at`, `until`, `current`, `size`, `sha256`, `state`, `first_point_id`, `object_present`. |
| POST | `/api/v1/backups` | Accounts only. `{"source_id", "rehash": false}`. `202` with `{job, already_active}`; if a backup of that source is already queued or running (it may be an automatic one) the existing job is returned with `200` and `already_active: true`. `409` (`source_disabled`) when the folder is switched off. |
| GET | `/api/v1/jobs/{id}` | `{job, events[]}`; `after_event` returns only newer log lines. `job.active` is true while queued or running; `job.progress` is a JSON object whose fields depend on the kind. A visitor gets no `requested_by`, no `error` and no events; a restore job needs an account. An automatic backup that finds nothing changed leaves no job, so its id answers `404`. |
| POST | `/api/v1/restores/preview` | Accounts only. `{"source_id", "point_id", "paths": [], "dest_name", "policy": "skip"|"rename"|"overwrite", "allow_partial": false}` (at most 500 paths). Writes nothing. Returns `destination` (the display path of the destination folder, or `[restore folder]\<dest_name>` when the restore folder has no display root; administrators also get `destination_real`), `totals` (`create`, `overwrite`, `rename`, `skip_same`, `skip_exists`, `blocked`, `missing_objects`, `not_captured`, …), `sample[]`, `problems[]`, `digest` and `confirmation_token`. |
| POST | `/api/v1/restores` | Accounts only. The preview body plus `confirmation_token`, `idempotency_key` and `confirm_overwrite`. `202` with `{job}`. `422` when the token is missing or invalid, when files would be replaced without `confirm_overwrite`, or when items are blocked without `allow_partial`; `409` when the preview is stale, or (`prune_running`) while old versions of the folder are being deleted. Repeating a request with the same `idempotency_key` returns the original job. |
| GET | `/api/v1/audit` | Accounts only. Audit events newest first: `action`, `actor`, `before`, `limit`. Each event has `duration_ms` when the audited operation has a duration (backups, verifications, restores and the deletion of old versions; not sign-ins or requests). The `backup.finish` detail has `bytes`, `new_bytes` and `new_objects`, and the action `retention.prune` is the deletion of old versions. |

## Page routes

| Route | Page |
|---|---|
| `GET /login`, `POST /login`, `POST /logout` | Sign-in and sign-out. Anyone may open the sign-in page; signing out needs a session. |
| `POST /auth/sso/start`, `POST /auth/sso/poll` | Single sign-on, only when `TIMEVAULT_SSO_HOST` is set (otherwise `404`) and only from the site itself (`403` otherwise). `start` takes the form field `next` (a local path) and answers `{"token", "url", "expires_in"}`: `url` is the page of the company sign-on service to open in a window. `poll` takes the form field `token` and answers `200` with `{"status": ...}`: `pending` (ask again; at most once a second), `ok` (with `next`, the page to go to, with the base path when there is one; the session cookie is set), `expired`, `mismatch` (the address that the sign-on service saw is not the browser's), `no_user` (Windows gave no user), `denied` (no active account), `unavailable` (Redis does not answer). A token works once. `429` `busy` when too many sign-ins are waiting (5 per address, 2000 in all). See [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md). |
| `GET /lang?set=en\|zh&next=…` | Sets the language cookie and redirects to a local path. |
| `GET /explorer?source=&path=&q=&sort=&order=&type=&page=&per_page=` | Current files, deleted items, and search results, as pages of 100 by default (`per_page` 50, 100, 200 or 500, 50, 100 or 200 for search results). A page past the end answers `303` to the last page. Open to everyone. |
| `GET /history?source=&point=&path=&sort=&order=&type=&page=&per_page=` | Time Machine: timeline of recovery points and the folder at the selected point, in pages like the Explorer. Open to everyone; the restore form needs an account. |
| `GET /versions?source=&path=` | Versions of one file or folder. Open to everyone. |
| `GET /backups`, `POST /backups/run`, `POST /backups/verify` | Monitored folders, recovery points, jobs, schedules; start a backup, start an integrity check (accounts). Viewing is open to everyone; visitors do not see restore jobs, who asked for a job or why one failed. Nobody but administrators sees the folders of the server's disk, only display folders. |
| `GET /jobs/{id}`, `POST /jobs/{id}/cancel` | A job, its log and (for restores) the per-item results. Viewing a backup job is open to everyone without the requester, the error and the log; a restore job and cancelling need an account. |
| `POST /restore/preview`, `POST /restore/start` | Restore preview and confirmation (accounts). |
| `GET /audit`, `GET /status`, `GET /settings` | Audit log, component status, and the settings page (accounts). Administrators see the real folders on them, everybody else the display folders. |
| `POST /settings/sources`, `POST /settings/sources/{id}` | Add and edit a monitored folder (administrators); the form has the fields `name`, `display_path`, `root_path`, `excludes`, `enabled`, `schedule`, `schedule_minutes`, `max_versions` and (when lowering a limit would delete versions) `confirm_prune`. |

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
- `GET /api/v1/files` reads the live folder and `history/tree` the folder of a recovery point; both can be sorted, filtered by type and paged with `offset`/`limit` (`history/tree` also still pages with a `cursor`).
- Restore requests name a destination **folder below the configured restore root** instead of a free path.
- Job progress is polled (`/api/v1/jobs/{id}`); server-sent events are not implemented.

---

# 中文

## 概述

TimeVault（0.1.6 版）在 `/api/v1` 下提供 JSON API，同时提供服务端渲染的页面。两者由同一个服务层实现，行为一致。本文档描述当前已有的内容；与 plan.md §6 中示意性 API 的差异见[与 plan.md 的差异](#与-planmd-的差异)。

## 约定

- **认证。** 读取对所有人开放；任何会改变内容的操作，以及任何展示服务内部情况的内容，都需要账号（见[谁可以调用什么](#谁可以调用什么)）。通过 `POST /login` 登录（表单字段 `username`、`password`，可选 `next`）。`username` 是 Redis 用户目录中公司账号的工号、姓名或邮箱，或者是本地管理员的名字。响应会设置 `tv_session` Cookie（HttpOnly、SameSite=Lax；当 `TIMEVAULT_COOKIE_SECURE=true` 时带 Secure）。受保护的接口在没有有效会话时返回带 JSON 错误的 `401`，在账号无权执行时返回 `403`。登录本身对错误或未知的账号返回 `401`（无论原因，文本都相同），尝试次数过多时返回 `429` 和 `Retry-After`，无法连接用户目录时返回 `503`。**单点登录**（设置了 `TIMEVAULT_SSO_HOST` 时）是另一种登录方式：登录页调用 `POST /auth/sso/start`，然后调用 `POST /auth/sso/poll`（见[页面路由](#页面路由)）。
- **基础路径。** 设置 `TIMEVAULT_BASE_PATH=/tv`（见 [runbook.md](./runbook.md#放在带路径前缀的反向代理之后)）后，本文档中的每个路径，在浏览器看来都在 `/tv` 之下（`/tv/api/v1/sources`、`/tv/login`）。服务也接受不带前缀的请求，供去掉前缀的代理使用。API 的 `Location` 头和 `POST /auth/sso/poll` 的 `next` 都带有前缀，Cookie 也限制在该前缀之内。不设置时，每个路径都和这里写的一样。
- **CSRF。** 已登录账号的所有 `POST`、`PATCH` 以及其他会改变状态的请求，都需要在 `X-CSRF-Token` 请求头中携带会话的令牌（HTML 表单使用 `csrf_token` 字段）。可从 `GET /api/v1/session` 获取。如果 `Origin` 或 `Sec-Fetch-Site` 显示请求来自其他站点，则返回 `403`。
- **JSON。** 请求体为 `Content-Type: application/json` 的 JSON；未知字段会返回 `400`。时间为 RFC 3339 格式，大小单位为字节。
- **错误。** `{"error": {"code": "...", "message": "..."}}`，状态码含义：`400` 输入无效（路径错误、目标错误、未知字段、1 到 43200 之外的计划），`401` 未登录，`403` CSRF（`csrf`）或无权执行（`forbidden`），`404` 未找到，`409` 冲突（监控文件夹重复、该文件夹的备份正在运行（`source_busy`）、文件夹已停用（`source_disabled`）、预览已过期、正在删除该文件夹的旧版本因而拒绝恢复、校验或更改版本数上限（`prune_running`）、调低版本数上限会删除版本而请求没有带上 `confirm_prune`（`prune_not_confirmed`）），`422` 缺少确认或存在被阻止的项目，`503` 搜索引擎不可用、文件夹无法读取或健康检查失败，`500` 内部错误（消息只包含参考编号，细节在服务端日志中）。
- **路径。** 相对于备份源，使用 `/` 或 `\`；忽略开头和结尾的分隔符；`""` 表示备份源根目录。`..`、盘符、保留设备名和非法字符会返回 `400`。
- **分页、排序和筛选。** `files` 和 `search` 使用 `offset`/`limit`，还有更多结果时返回 `next_offset`。`files` 和 `history/tree` 还接受 `sort`（`name`、`type`、`size` 或 `mtime`；默认 `name`）、`order`（`asc` 或 `desc`）、`type`（筛选：`dir`；`file` 表示所有不是文件夹的项目；`link`；`noext`；或小写的 `ext:<扩展名>`）和 `dirs_first=true`（文件夹排在其他所有项目之前；页面总是这样做，API 只有在要求时才这样）。无法识别的值表示默认：按名称排序，不筛选。只要没有给出 `sort`、`order`、`type`、`dirs_first` 和 `offset` 中的任何一个，`history/tree` 就保持它不透明的 `cursor` 和 `next_cursor`；给出其中任何一个时，它像 `files` 一样使用 `offset`/`limit`。这些列表的响应包含 `total`（符合筛选条件的项目数）、`unfiltered`（文件夹中的项目数）和 `types[]`（文件夹中每种项目的 `type` 和 `count`，与筛选无关），并且每个条目都有 `type`：`dir`、`link`（联接点或符号链接）、`noext`（没有扩展名的文件）或 `ext:<扩展名>`。`audit` 使用 `before`（事件 id）和 `limit`。

## 谁可以调用什么

| 调用者 | 可以 |
|---|---|
| 任何人 | `GET /healthz`、`GET /api/v1/session`、`GET /api/v1/sources`、`/files`、`/search`、`/recovery-points`、`/history/tree`、`/history/versions`，以及备份或校验任务的 `GET /api/v1/jobs/{id}`。还有 `POST /auth/sso/start` 和 `POST /auth/sso/poll`，它们的用途就是登录（仅在配置了单点登录时，并且只接受来自站点本身的请求）。 |
| 任何已登录的账号 | 上述全部（内容更详细，见下文），以及 `POST /api/v1/backups`、`POST /api/v1/restores/preview`、`POST /api/v1/restores`、恢复任务的 `GET /api/v1/jobs/{id}`、`GET /api/v1/audit`、`GET /api/v1/health`、`GET /api/v1/settings`。 |
| `admin` 账号（或本地管理员） | 上述全部，以及 `POST /api/v1/sources` 和 `PATCH /api/v1/sources/{id}`。 |

没有账号的访问者得不到的内容：`GET /api/v1/jobs/{id}` 中的 `requested_by`、`error` 文本和 `events`（日志）。恢复任务对访问者返回 `401`。

只有管理员能得到的是服务器磁盘上的文件夹：`GET /api/v1/sources` 中的 `root_path`，`GET /api/v1/settings` 中的 `repository`、`restore_root` 和 `env_file`，以及恢复预览中的 `destination_real`。所有人得到的则是显示文件夹（`display_path`、`destination`、`restore_display_root`）。给其他人的响应中，文本里的真实文件夹（任务错误和日志、恢复消息和问题、审计详情、健康详情、文件未能备份的原因）会按 [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md) 的说明被替换：换成显示文件夹、`[<监控文件夹名称>]`、`[backup folder]` 或 `[restore folder]`。

## 接口

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/healthz` | 存活探针，无需认证，返回 `ok`。 |
| GET | `/api/v1/session` | 访问者得到 `{"authenticated": false}`；账号得到 `authenticated`、`user`（工号）、`name`、`role`（`user` 或 `admin`）、`csrf_token`、`auth_disabled`。 |
| GET | `/api/v1/health` | 仅限账号。`ok`、`components[]`（`database`、`repository`、`search`、`directory`、`restore`、`vss`、`usn`、`jobs`），每项含 `state`（`ok`、`degraded`、`disabled`、`failed`）和 `detail`；有组件为 `failed` 时 HTTP 状态为 `503`。Redis 没有应答时用户目录为 `degraded`，这不算故障。对不是管理员的账号，仓库的 `detail` 只说明它用了多少，其他详情中的真实文件夹会被替换。 |
| GET | `/api/v1/settings` | 仅限账号。当前运行的配置（不含机密信息）：`version`、`listen`、`database` 和 `user_directory`（`host:port/name`）、`sso_host`（单点登录服务，关闭时为空）、`base_path`（路径前缀，没有时为空）、`server_host`、`restore_display_root`、`search`、`local_admin`（布尔值）、`sign_in_disabled`、`session_ttl`、`cookie_secure`。管理员还会得到 `repository`、`restore_root` 和 `env_file`。 |
| GET | `/api/v1/sources` | 监控文件夹及其最新已提交恢复点：`id`、`name`、`display_path`（有的话）、`root_path`（仅限管理员）、`excludes`、`enabled`、`schedule_minutes`（`null` 表示仅手动）、`last_run_at`、`last_run_ms`（上次备份运行花了多久，已知时才有）、`max_versions`（一个文件最多保留的版本数；`null` 表示保留每个版本）、`created_at`、`latest_point`。 |
| POST | `/api/v1/sources` | 仅限管理员。添加监控文件夹：`{"name", "display_path", "root_path", "excludes": [], "schedule_minutes": null, "max_versions": null}`（省略时使用默认排除规则；`schedule_minutes` 为 1 到 43200，或省略；`max_versions` 为 1 到 10000，`0`、`null` 或省略表示保留每个版本；其他值返回 `400`）。返回 `201`。文件夹必须是本机已存在的目录，且不得与仓库、恢复目录或其他监控文件夹重叠。`display_path` 是人们打开该文件夹时使用的网络路径（`\\server\share\folder`，两种斜杠都可以，不能是以 `$` 结尾的隐藏共享）。设置了 `TIMEVAULT_SERVER_HOST` 时它是必填的，并且它的主机必须是该设置的值（不区分大小写，保存时使用设置中的写法）；否则是可选的。不是网络路径或位于另一台主机上时返回 `400`。 |
| PATCH | `/api/v1/sources/{id}` | 仅限管理员。修改请求中出现的字段，其余保持不变：`name`、`display_path`（空字符串表示删除，设置了 `TIMEVAULT_SERVER_HOST` 时不允许）、`root_path`、`excludes`、`enabled`、`schedule_minutes`（数字，或用 `null` 取消计划）、`max_versions`（1 到 10000，或用 `0` 或 `null` 保留每个版本）和 `confirm_prune`。返回 `200` 及该备份源。值无效返回 `400`；该文件夹的备份正在运行（`source_busy`）或名称、文件夹已被占用时返回 `409`。已有任何恢复点的备份源不能更改文件夹（`400`）。更改文件夹会忘记上次运行。设置了 `TIMEVAULT_SERVER_HOST` 时，没有显示文件夹的备份源在请求给它填上之前不能被修改（`400`）。对已有历史的备份源设置 `max_versions`，或把它调得比以前更低，会为整个文件夹排入一个版本清理任务（见 [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md)）。当这会删除版本时，除非请求带有 `"confirm_prune": true`，否则请求会被拒绝并返回 `409` `prune_not_confirmed`（消息会说明涉及多少文件的多少个版本）。该文件夹的清理正在运行时，更改 `max_versions` 的请求会被拒绝并返回 `409` `prune_running`；其他字段仍然可以更改。 |
| GET | `/api/v1/files` | 当前文件夹：`source_id`、`path`、`sort`、`order`、`type`、`dirs_first`、`offset`、`limit`（≤ 1000，默认 200）。返回 `entries[]`（`name`、`path`、`is_dir`、`type`、`size`、`modified_at`、`status` = `protected` / `changed` / `new` / `not_captured`）、`total`、`unfiltered`、`types[]`、`next_offset`、`deleted[]`（存在于最新恢复点、但磁盘上已不存在的项目，遵循类型筛选）、`latest_point`。文件夹的 `size` 是 Everything 的索引所知道的其中所有内容的大小，Everything 不知道时为 `0`；按 `size` 排序会用到它。 |
| GET | `/api/v1/search` | `q`、`source_id`、`offset`、`limit`（≤ 200）。返回 `results[]`（带 `type`）、`total`、`next_offset`。搜索引擎不可用或未配置时返回 `503`，错误码为 `search_unavailable`。 |
| GET | `/api/v1/recovery-points` | `source_id` 的已提交恢复点，最新的在前（`limit`、`before_seq`）。每个包含 `seq`、`kind`（`full` / `incremental`）、`consistency`（`live_scan`）、计数器、`verification`、`restorable`、`partial`（当为保持在该文件夹的版本数上限之内而删除了某些文件的较旧版本、因而该恢复点不再显示每个文件当时的样子时为 true）、`manifest_sha256`。 |
| GET | `/api/v1/history/tree` | 文件夹在 `point_id` 时的样子：`source_id`、`point_id`、`path`，以及 `cursor`/`limit`，或与 `files` 相同的列表参数。每个条目带有 `type`，以及相对最新恢复点的 `status`：`same`、`modified`、`deleted`。 |
| GET | `/api/v1/history/versions` | `path` 的每个版本：`type`、`captured_at`、`until`、`current`、`size`、`sha256`、`state`、`first_point_id`、`object_present`。 |
| POST | `/api/v1/backups` | 仅限账号。`{"source_id", "rehash": false}`。返回 `202` 及 `{job, already_active}`；如果该备份源已有排队或运行中的备份（可能是自动备份），则返回已有任务，状态 `200`，且 `already_active: true`。文件夹已停用时返回 `409`（`source_disabled`）。 |
| GET | `/api/v1/jobs/{id}` | `{job, events[]}`；`after_event` 只返回更新的日志行。排队或运行中时 `job.active` 为 true；`job.progress` 是一个 JSON 对象，字段取决于任务类型。访问者得不到 `requested_by`、`error` 和事件；恢复任务需要账号。没有发现变化的自动备份不会留下任务，所以它的 id 会返回 `404`。 |
| POST | `/api/v1/restores/preview` | 仅限账号。`{"source_id", "point_id", "paths": [], "dest_name", "policy": "skip"|"rename"|"overwrite", "allow_partial": false}`（最多 500 个路径）。不写入任何内容。返回 `destination`（目标文件夹的显示路径；恢复文件夹没有显示根路径时为 `[restore folder]\<dest_name>`；管理员还会得到 `destination_real`）、`totals`（`create`、`overwrite`、`rename`、`skip_same`、`skip_exists`、`blocked`、`missing_objects`、`not_captured` 等）、`sample[]`、`problems[]`、`digest` 和 `confirmation_token`。 |
| POST | `/api/v1/restores` | 仅限账号。预览请求体，加上 `confirmation_token`、`idempotency_key` 和 `confirm_overwrite`。返回 `202` 及 `{job}`。令牌缺失或无效、未设置 `confirm_overwrite` 就要替换文件、或未设置 `allow_partial` 却有被阻止的项目时返回 `422`；预览已过期，或（`prune_running`）正在删除该文件夹的旧版本时返回 `409`。使用相同 `idempotency_key` 重复请求会返回原来的任务。 |
| GET | `/api/v1/audit` | 仅限账号。审计事件，最新的在前：`action`、`actor`、`before`、`limit`。当被审计的操作有耗时（备份、校验、恢复和删除旧版本；登录和请求没有）时，每个事件带有 `duration_ms`。`backup.finish` 的详情包含 `bytes`、`new_bytes` 和 `new_objects`，操作 `retention.prune` 是删除旧版本。 |

## 页面路由

| 路由 | 页面 |
|---|---|
| `GET /login`、`POST /login`、`POST /logout` | 登录与退出。任何人都可以打开登录页；退出需要会话。 |
| `POST /auth/sso/start`、`POST /auth/sso/poll` | 单点登录，仅在设置了 `TIMEVAULT_SSO_HOST` 时可用（否则返回 `404`），并且只接受来自站点本身的请求（否则返回 `403`）。`start` 接受表单字段 `next`（本站路径），返回 `{"token", "url", "expires_in"}`：`url` 是要在窗口中打开的公司单点登录服务的页面。`poll` 接受表单字段 `token`，返回 `200` 和 `{"status": ...}`：`pending`（再问一次；每秒最多一次）、`ok`（带有 `next`，即要去的页面，设置了基础路径时带有该前缀；会话 Cookie 已设置）、`expired`、`mismatch`（单点登录服务看到的地址不是浏览器的）、`no_user`（Windows 没有给出用户）、`denied`（没有可用的账号）、`unavailable`（Redis 没有响应）。令牌只能使用一次。等待中的登录过多时（每个地址 5 个，总共 2000 个）返回 `429` `busy`。见 [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md)。 |
| `GET /lang?set=en\|zh&next=…` | 设置语言 Cookie，并重定向到本站路径。 |
| `GET /explorer?source=&path=&q=&sort=&order=&type=&page=&per_page=` | 当前文件、已删除项目和搜索结果，默认每页 100 项（`per_page` 可选 50、100、200 或 500，搜索结果为 50、100 或 200）。超出末尾的页码会返回 `303` 到最后一页。对所有人开放。 |
| `GET /history?source=&point=&path=&sort=&order=&type=&page=&per_page=` | 时间机器：恢复点时间轴，以及所选恢复点时的文件夹，与文件浏览一样分页。对所有人开放；恢复表单需要账号。 |
| `GET /versions?source=&path=` | 单个文件或文件夹的各个版本。对所有人开放。 |
| `GET /backups`、`POST /backups/run`、`POST /backups/verify` | 监控文件夹、恢复点、任务、计划；启动备份、启动完整性校验（账号）。查看对所有人开放；访问者看不到恢复任务、任务是谁发起的或为什么失败。除管理员之外，任何人都看不到服务器磁盘上的文件夹，只能看到显示文件夹。 |
| `GET /jobs/{id}`、`POST /jobs/{id}/cancel` | 任务、任务日志，以及（恢复任务的）逐项结果。查看备份任务对所有人开放，但看不到发起人、错误和日志；恢复任务和取消操作需要账号。 |
| `POST /restore/preview`、`POST /restore/start` | 恢复预览与确认（账号）。 |
| `GET /audit`、`GET /status`、`GET /settings` | 审计日志、组件状态和设置页（账号）。管理员在这些页面上看到真实文件夹，其他人看到显示文件夹。 |
| `POST /settings/sources`、`POST /settings/sources/{id}` | 添加和编辑监控文件夹（管理员）；表单字段有 `name`、`display_path`、`root_path`、`excludes`、`enabled`、`schedule`、`schedule_minutes`、`max_versions`，以及（当调低上限会删除版本时的）`confirm_prune`。 |

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
- `GET /api/v1/files` 读取实时文件夹，`history/tree` 读取某个恢复点的文件夹；两者都可以排序、按类型筛选，并用 `offset`/`limit` 分页（`history/tree` 仍然可以用 `cursor` 分页）。
- 恢复请求指定的是**所配置恢复根目录之下的目标文件夹**，而不是任意路径。
- 任务进度通过轮询（`/api/v1/jobs/{id}`）获取；未实现 Server-Sent Events。
