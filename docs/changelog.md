# English

## Overview

What changed in each version of TimeVault, and what to do when upgrading. The newest comes first. 0.1.1 is the latest version that has a number; the section above it holds what changed since.

## Unreleased (after 0.1.1)

One settings file for the development setup, a warning when the environment overrides it, and a clearer Settings page. It has no version number yet.

### Changed

- **`.env` in the repository folder is the only settings file.** The dev scripts used to keep settings in four more files: `data\local.env` (`TIMEVAULT_GO_ROOT`), `data\pgdev\db.env` (the database URLs), `data\dev\server.env` (backup and restore folder, session key, local administrator) and `data\everything\everything.env` (search). They now read and write `.env`. The first time a script runs, it moves the settings of those files into `.env` (a key that `.env` already has keeps its value) and deletes the old files, but only after it has checked that every setting is in `.env`; a file with lines that are not `KEY=VALUE` is left alone with a warning.
- `scripts\dev-run.ps1` removes the `TIMEVAULT_*` variables that are left in your terminal before it starts the service, and says which, because they would override `.env`. `scripts\dev-env.ps1` does the same for your session, sets `TIMEVAULT_ENV_FILE`, and loads only what tests and tools read (`TIMEVAULT_TEST_*`, `TIMEVAULT_GO_ROOT`, `TIMEVAULT_PG_BIN`, `TIMEVAULT_EVERYTHING_EXE`); the settings of the service are no longer copied into the environment. `dev-run.ps1` no longer leaves `TIMEVAULT_LISTEN_ADDR` or `TIMEVAULT_ENV_FILE` in the terminal when it ends.
- `scripts\dev-demo.ps1` takes the address of the server from `TIMEVAULT_LISTEN_ADDR` in `.env` (it was a fixed default).
- The Settings page explains the backup folder and the restore folder, and says that a `TIMEVAULT_*` environment variable overrides the file. The comments in `.env` and in `configs/timevault.example.env` do the same.

### Added

- **The service names the settings that the environment overrides.** When a `TIMEVAULT_*` environment variable has another value than the same key in the `.env` file, the start-up log has a warning with the names (never the values), and `check-redis-users` prints them too.

### Fixed

- **The service listened on the old port 8470 although `.env` said 8660.** The 0.1.0 `scripts\dev-run.ps1` set `TIMEVAULT_LISTEN_ADDR=127.0.0.1:8470` in the PowerShell session that ran it and left it there; an environment variable wins over `.env`, silently. The scripts no longer leave variables behind, remove such leftovers, and the service reports the override.

### Upgrade notes

1. Run `scripts\dev-run.ps1` (or any other `scripts\dev-*.ps1`) once. It moves the settings of `data\local.env`, `data\pgdev\db.env`, `data\dev\server.env` and `data\everything\everything.env` into `.env` and deletes those files.
2. Close the terminals in which you ran an older `dev-run.ps1`, or run `Remove-Item Env:TIMEVAULT_LISTEN_ADDR` there. The scripts remove such variables too.
3. Whatever of your own read one of the four old files (a shortcut or a script that loads `data\pgdev\db.env`, for example) has to read `.env` now.

## 0.1.1

Open browsing, company sign-in from Redis, a Settings page for the monitored folders, automatic backups, and a new default port. The reasons are in [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md) and [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md).

### Added

- **Open browsing.** Anyone who can reach the address can use the Explorer, the Time Machine, file versions, Backups and backup jobs without an account. Everything that changes data needs an account, and so do the audit log, the status and settings pages and restore jobs. A visitor never sees the folders' paths on the server's disk, who requested a job, why it failed or its log. See [Who may call what](./api.md#who-may-call-what).
- **Company sign-in from Redis.** Accounts are read, read-only, from the `app_user_*` hashes of the other (dash) application. A user signs in with the employee ID, the name or the e-mail address. `Type` = `admin` makes an administrator and any other type a normal user. A deactivated account cannot sign in and loses an open session within a minute. If Redis does not answer, browsing keeps working. New settings: `TIMEVAULT_REDIS_URL`, `TIMEVAULT_REDIS_KEY_PREFIX` (default `app_user_`), `TIMEVAULT_REDIS_TIMEOUT` (default 3 seconds).
- **Two roles.** Every signed-in account can back up, verify, cancel a job, restore, and open the audit log, status and settings pages. Only `admin` accounts add or change monitored folders. The local administrator (`TIMEVAULT_ADMIN_PASSWORD_HASH`) is optional and counts as an `admin`.
- **Settings page** (`/settings`). It shows the monitored folders with their schedule, the backup folder and the restore folder with the free space of the repository, and the running configuration without secrets. Administrators add and edit monitored folders there (name, folder, exclude patterns, on or off, automatic schedule); the API offers `POST /api/v1/sources` and `PATCH /api/v1/sources/{id}`, and `GET /api/v1/settings` returns the running configuration.
- **Automatic backups.** A monitored folder can have a schedule of 1 to 43200 minutes. A run that finds nothing changed leaves no recovery point, no job and no audit record.
- **`.env` file.** Deployment settings (database, Redis, listen address, backup and restore folders, session key, search) are read from the environment and from a `.env` file; the environment wins. [configs/timevault.example.env](../configs/timevault.example.env) is the template.
- **`timevault-server check-redis-users`** reports how much of the Redis user directory can be used, without printing names, e-mail addresses, keys or passwords.
- Migration `0002_schedule.sql` (`sources.schedule_minutes`, `sources.last_run_at`), the audit action `source.update`, and the errors `source_busy` and `source_disabled` (HTTP `409`).

### Changed

- **The default port is `8660`** (it was `8470`).
- **A way to sign in is required.** At least one of `TIMEVAULT_REDIS_URL` and `TIMEVAULT_ADMIN_PASSWORD_HASH` must be set (unless `TIMEVAULT_AUTH_DISABLED=true`, loopback only). Before, the local administrator was required.
- **"Source" is called "monitored folder"** in the interface. The path of a monitored folder that already has a recovery point cannot be edited, and monitored folders are switched off, never deleted.
- **Session cookies have a new format.** The cookies of 0.1.0 are rejected, so everybody signs in again once.
- **Sign-in limits.** An attempt is counted when it starts: 5 attempts per client address and 10 failures per account name in 10 minutes. A sign-in that failed because Redis did not answer is not held against the account.
- `POST /backups/sources` was replaced by `POST /settings/sources` (administrators only), and `POST /api/v1/sources` now needs an administrator. Starting a backup of a switched-off folder returns `409 source_disabled`. Changing the folder of a monitored folder while its backup runs returns `409 source_busy`.
- **Scripts.** `scripts\dev-run.ps1` works with a PostgreSQL server you already have (the private cluster on port 55432 is started only when the database settings point at it), creates a commented `.env`, and `-Port` is optional. `scripts\dev-db.ps1` refuses to overwrite or delete a `db.env` that points at another server. `scripts\dev-demo.ps1` uses port 8660.
- **New Go modules.** `github.com/redis/go-redis/v9` (v9.22.0) to read the Redis user directory, and `github.com/alicebob/miniredis/v2` (v2.40.0, tests only). See [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md).

### Fixed

- The language links and the sign-in link returned to the page that was first loaded, instead of the page on screen, after navigating inside the page without a reload.

### Upgrade notes

1. **Port.** The default listen address is now `127.0.0.1:8660`. Update bookmarks and any reverse proxy, or set `TIMEVAULT_LISTEN_ADDR` to keep the old port.
2. **`.env`.** A `.env` file in the working directory, next to `timevault-server.exe`, or the one named by `TIMEVAULT_ENV_FILE` is now read. Environment variables win over the file.
3. **Database.** Apply migration `0002` before the new version serves requests: set `TIMEVAULT_AUTO_MIGRATE=true` or run `timevault-server migrate`. `scripts\dev-run.ps1` already does it for the development setup. The migration only adds two columns; existing data and monitored folders stay as they are, and every existing monitored folder stays manual-only until you give it a schedule.
4. **Redis.** To let company accounts sign in, put `TIMEVAULT_REDIS_URL=redis://:<password>@127.0.0.1:6379/0` (your own address and password) into `.env`. Then run `timevault-server check-redis-users` once from the same folder, against the real Redis, and fix what it reports. Without a Redis URL only the local administrator can sign in.
5. **Local administrator.** `TIMEVAULT_ADMIN_PASSWORD_HASH` and `TIMEVAULT_ADMIN_USER` keep working. The administrator is optional now.
6. **Sessions.** Everybody has to sign in again once.
7. **API.** Callers of `POST /api/v1/sources` need an administrator account. `GET /api/v1/jobs/{id}` hides `requested_by`, `error` and `events` from callers without an account.
8. **Tests.** `go test` needs `TIMEVAULT_TEST_DATABASE_URL`. When `data\pgdev\db.env` points at your own PostgreSQL server, create the `timevault_test` database there; the private cluster on port 55432 is no longer required.
9. **Network.** Do not open the service to other computers without HTTPS in front of it (see [security.md](./security.md)): company passwords are posted to it and are stored in plain text in Redis.

### Known limits

- The Redis user directory has been tested against an in-process fake with pickle data written by Python, not against the company's own Redis. Run `check-redis-users` on it once.
- Anonymous visitors see the names, sizes, modification times and structure of every monitored folder and its history. Not the contents.
- Behind a reverse proxy all visitors share one address in the sign-in limits, because `X-Forwarded-For` is not trusted. A name can be locked out for ten minutes by ten wrong attempts, the local administrator included.
- A login that matches more than 200 Redis keys is refused as ambiguous. A stale duplicate record of an employee ID is not resolved.
- There is no retention, so automatic runs that find changes add recovery points for good. A scheduled folder that cannot be read produces one failed job and one audit record per interval.
- A manual backup request that finds an automatic backup already queued or running is attached to it. If that run finds nothing changed, its job is deleted, so `GET /api/v1/jobs/{id}` then returns `404`.
- Two administrators who edit the same monitored folder at the same time: the last save wins.

The full lists are in [security.md](./security.md) and [mvp-status.md](./mvp-status.md).

## 0.1.0 (MVP)

A vertical slice on a local NTFS folder: browse, back up, keep history, verify and restore, with one administrator, a manual start only and port `8470`. See [mvp-status.md](./mvp-status.md) for what was done and what was not.

---

# 中文

## 概述

TimeVault 每个版本改了什么，以及升级时要做什么。最新的在最前面。0.1.1 是最新的有版本号的版本；它上面的小节记录的是之后的改动。

## 未发布（0.1.1 之后）

开发环境只用一个设置文件；环境变量覆盖了它时会给出警告；设置页的说明更清楚。它还没有版本号。

### 变更

- **仓库文件夹中的 `.env` 是唯一的设置文件。** 开发脚本以前把设置放在另外四个文件里：`data\local.env`（`TIMEVAULT_GO_ROOT`）、`data\pgdev\db.env`（数据库 URL）、`data\dev\server.env`（备份和恢复文件夹、会话密钥、本地管理员）和 `data\everything\everything.env`（搜索）。现在它们读写的都是 `.env`。脚本第一次运行时，会把这些文件里的设置移入 `.env`（`.env` 里已有的键保留它的值）并删除旧文件，但只有在确认每一项设置都已在 `.env` 中之后才会删除；含有非 `KEY=VALUE` 行的文件会被保留，并给出警告。
- `scripts\dev-run.ps1` 在启动服务之前，会移除终端里遗留的 `TIMEVAULT_*` 变量，并说明是哪些，因为它们会覆盖 `.env`。`scripts\dev-env.ps1` 对你的会话做同样的事，并设置 `TIMEVAULT_ENV_FILE`，只加载测试和工具才读取的内容（`TIMEVAULT_TEST_*`、`TIMEVAULT_GO_ROOT`、`TIMEVAULT_PG_BIN`、`TIMEVAULT_EVERYTHING_EXE`）；服务自己的设置不再被复制到环境变量中。`dev-run.ps1` 结束时不再把 `TIMEVAULT_LISTEN_ADDR` 或 `TIMEVAULT_ENV_FILE` 留在终端里。
- `scripts\dev-demo.ps1` 从 `.env` 中的 `TIMEVAULT_LISTEN_ADDR` 取得服务器地址（以前是固定的默认值）。
- 设置页解释了备份文件夹和恢复文件夹，并说明 `TIMEVAULT_*` 环境变量会覆盖文件。`.env` 和 `configs/timevault.example.env` 里的注释也做了同样的说明。

### 新增

- **服务会点名被环境变量覆盖的设置。** 当某个 `TIMEVAULT_*` 环境变量的值与 `.env` 文件中同名键的值不同时，启动日志中会有一条列出这些名字（绝不列出值）的警告，`check-redis-users` 也会打印它们。

### 修复

- **`.env` 里写的是 8660，服务却监听在旧端口 8470。** 0.1.0 的 `scripts\dev-run.ps1` 在运行它的 PowerShell 会话中设置了 `TIMEVAULT_LISTEN_ADDR=127.0.0.1:8470` 并把它留在那里；环境变量会悄无声息地覆盖 `.env`。现在脚本不再留下变量，会移除这类遗留变量，服务也会报告这种覆盖。

### 升级说明

1. 运行一次 `scripts\dev-run.ps1`（或任何其他 `scripts\dev-*.ps1`）。它会把 `data\local.env`、`data\pgdev\db.env`、`data\dev\server.env` 和 `data\everything\everything.env` 中的设置移入 `.env`，并删除这些文件。
2. 关闭运行过旧版 `dev-run.ps1` 的终端，或在其中运行 `Remove-Item Env:TIMEVAULT_LISTEN_ADDR`。脚本也会移除这类变量。
3. 你自己写的、读取过这四个旧文件之一的东西（例如加载 `data\pgdev\db.env` 的快捷方式或脚本），现在要改为读取 `.env`。

## 0.1.1

开放浏览、通过 Redis 的公司账号登录、用于管理监控文件夹的设置页、自动备份，以及新的默认端口。原因见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md) 和 [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md)。

### 新增

- **开放浏览。** 任何能访问该地址的人，无需账号就可以使用文件浏览、时间机器、文件版本、备份页和备份任务。所有会改变数据的操作都需要账号，审计日志、状态页、设置页和恢复任务也是如此。访问者看不到文件夹在服务器磁盘上的路径、任务是谁发起的、为什么失败以及任务日志。见[谁可以调用什么](./api.md#谁可以调用什么)。
- **通过 Redis 的公司账号登录。** 账号以只读方式读取自另一个（dash）应用的 `app_user_*` 哈希。用户可以用工号、姓名或邮箱登录。`Type` 为 `admin` 的是管理员，其他类型是普通用户。已停用的账号不能登录，已打开的会话在一分钟内失效。Redis 无应答时，浏览功能照常可用。新增设置：`TIMEVAULT_REDIS_URL`、`TIMEVAULT_REDIS_KEY_PREFIX`（默认 `app_user_`）、`TIMEVAULT_REDIS_TIMEOUT`（默认 3 秒）。
- **两种角色。** 每个已登录的账号都可以备份、校验、取消任务、恢复，以及打开审计日志、状态页和设置页。只有 `admin` 账号可以添加或修改监控文件夹。本地管理员（`TIMEVAULT_ADMIN_PASSWORD_HASH`）是可选的，并视为 `admin`。
- **设置页**（`/settings`）。显示监控文件夹及其计划、备份文件夹和恢复文件夹（含仓库剩余空间），以及不含机密信息的当前运行配置。管理员在这里添加和编辑监控文件夹（名称、文件夹、排除规则、开关、自动计划）；API 提供 `POST /api/v1/sources` 和 `PATCH /api/v1/sources/{id}`，`GET /api/v1/settings` 返回当前运行配置。
- **自动备份。** 监控文件夹可以设置 1 到 43200 分钟的计划。没有发现任何变化的一次运行，不会留下恢复点、任务或审计记录。
- **`.env` 文件。** 部署相关的设置（数据库、Redis、监听地址、备份和恢复文件夹、会话密钥、搜索）从环境变量和 `.env` 文件读取；环境变量优先。[configs/timevault.example.env](../configs/timevault.example.env) 是模板。
- **`timevault-server check-redis-users`** 报告 Redis 用户目录中有多少可用，不会打印姓名、邮箱、键或密码。
- 迁移 `0002_schedule.sql`（`sources.schedule_minutes`、`sources.last_run_at`）、审计动作 `source.update`，以及错误 `source_busy` 和 `source_disabled`（HTTP `409`）。

### 变更

- **默认端口是 `8660`**（原来是 `8470`）。
- **必须有一种登录方式。** `TIMEVAULT_REDIS_URL` 和 `TIMEVAULT_ADMIN_PASSWORD_HASH` 至少要设置一个（除非 `TIMEVAULT_AUTH_DISABLED=true`，且仅限回环地址）。以前必须有本地管理员。
- **界面中把"备份源"称为"监控文件夹"。** 已经有恢复点的监控文件夹不能再修改其路径，监控文件夹只能停用，绝不删除。
- **会话 Cookie 换了新格式。** 0.1.0 的 Cookie 会被拒绝，所以每个人要重新登录一次。
- **登录限制。** 尝试在开始时就计数：10 分钟内每个客户端地址 5 次尝试、每个账号名 10 次失败。因为 Redis 无应答而失败的登录，不会算在该账号头上。
- `POST /backups/sources` 被 `POST /settings/sources`（仅限管理员）取代，`POST /api/v1/sources` 现在需要管理员。对已停用的文件夹发起备份会返回 `409 source_disabled`。在监控文件夹的备份运行期间更改其文件夹会返回 `409 source_busy`。
- **脚本。** `scripts\dev-run.ps1` 可以配合你已有的 PostgreSQL 服务器使用（只有数据库设置指向 55432 端口的私有集群时才会启动它），会创建带注释的 `.env`，`-Port` 变为可选。`scripts\dev-db.ps1` 拒绝覆盖或删除指向其他服务器的 `db.env`。`scripts\dev-demo.ps1` 使用 8660 端口。
- **新的 Go 模块。** `github.com/redis/go-redis/v9`（v9.22.0）用于读取 Redis 用户目录，`github.com/alicebob/miniredis/v2`（v2.40.0，仅用于测试）。见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)。

### 修复

- 在页面内无刷新地跳转之后，语言链接和登录链接会回到最初加载的页面，而不是当前屏幕上的页面。

### 升级说明

1. **端口。** 默认监听地址现在是 `127.0.0.1:8660`。更新书签和反向代理，或设置 `TIMEVAULT_LISTEN_ADDR` 以保留旧端口。
2. **`.env`。** 现在会读取工作目录中的 `.env`、`timevault-server.exe` 旁边的 `.env`，或 `TIMEVAULT_ENV_FILE` 指定的文件。环境变量优先于文件。
3. **数据库。** 新版本开始处理请求之前，先应用迁移 `0002`：设置 `TIMEVAULT_AUTO_MIGRATE=true`，或运行 `timevault-server migrate`。开发环境的 `scripts\dev-run.ps1` 已经做了这件事。该迁移只增加两列；已有的数据和监控文件夹保持不变，每个已有的监控文件夹在你为它设置计划之前仍然是仅手动。
4. **Redis。** 要让公司账号登录，请把 `TIMEVAULT_REDIS_URL=redis://:<password>@127.0.0.1:6379/0`（换成你自己的地址和密码）写入 `.env`。然后在同一个文件夹中，针对真实的 Redis 运行一次 `timevault-server check-redis-users`，并处理它报告的问题。没有 Redis 地址时，只有本地管理员可以登录。
5. **本地管理员。** `TIMEVAULT_ADMIN_PASSWORD_HASH` 和 `TIMEVAULT_ADMIN_USER` 继续有效。管理员现在是可选的。
6. **会话。** 每个人都要重新登录一次。
7. **API。** 调用 `POST /api/v1/sources` 需要管理员账号。`GET /api/v1/jobs/{id}` 对没有账号的调用者隐藏 `requested_by`、`error` 和 `events`。
8. **测试。** `go test` 需要 `TIMEVAULT_TEST_DATABASE_URL`。如果 `data\pgdev\db.env` 指向你自己的 PostgreSQL 服务器，请先在那里创建 `timevault_test` 数据库；55432 端口的私有集群不再是必需的。
9. **网络。** 没有 HTTPS 在前面时，不要把服务开放给其他电脑（见 [security.md](./security.md)）：公司账号的密码会被提交到本服务，而且在 Redis 中是明文保存的。

### 已知限制

- Redis 用户目录是针对进程内的模拟服务和 Python 生成的 pickle 数据测试的，没有针对公司自己的 Redis 测试过。请对它运行一次 `check-redis-users`。
- 匿名访问者可以看到每个监控文件夹及其历史的名称、大小、修改时间和结构，但看不到内容。
- 放在反向代理之后时，所有访问者在登录限制中共用同一个地址，因为不信任 `X-Forwarded-For`。十次错误尝试就能把一个账号名锁定十分钟，本地管理员也不例外。
- 匹配到超过 200 个 Redis 键的登录会因为有歧义而被拒绝。同一工号的陈旧重复记录不会被消解。
- 没有保留策略，所以发现变化的自动运行会永久增加恢复点。无法读取的、设置了计划的文件夹，每个间隔会产生一个失败的任务和一条审计记录。
- 手动备份请求如果发现已有排队或运行中的自动备份，会并入该备份。如果那次运行没有发现变化，它的任务会被删除，此后 `GET /api/v1/jobs/{id}` 返回 `404`。
- 两个管理员同时编辑同一个监控文件夹：以最后一次保存为准。

完整的列表见 [security.md](./security.md) 和 [mvp-status.md](./mvp-status.md)。

## 0.1.0（MVP）

在本地 NTFS 文件夹上的垂直切片：浏览、备份、保留历史、校验和恢复；一个管理员，只能手动启动，端口为 `8470`。做了什么、没做什么，见 [mvp-status.md](./mvp-status.md)。
