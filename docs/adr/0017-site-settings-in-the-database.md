# English

## Status

Accepted for 1.0.1 (2026-11-22). Replaces the part of [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md) that says the deployment settings live in `.env` or the environment: from 1.0.1 only the settings the service needs before it can reach its database, and its secrets, live there; everything else is stored in the database. The monitored folders and their schedules stay in the database as before. Nothing is carried over from an old `.env`.

## Context

- Up to 1.0.0 every operating setting was a `TIMEVAULT_*` environment variable or a line of `.env` (about forty of them). To change one, somebody edited the file on the server and restarted the service; a mistake showed only when the service started; and the **Settings** page showed the running configuration (host name, listen address, database, user directory, the single sign-on service, the web log, the settings file) to every signed-in account.
- The owner of the deployment asked that the settings be edited by the administrator, signed in on a web page, stored in the database, and invisible to everybody else, with `.env` kept only for what is really necessary.
- Some settings cannot move: the service has to know the database before it can read anything from it, and a secret should not be kept in a table that a page shows and an API returns.
- There is one deployment, and downtime is acceptable, so no transition for the old values is needed.

## Decision

- **Two kinds of settings.** *Bootstrap settings and secrets* stay in the environment and `.env`: `TIMEVAULT_DATABASE_URL`, `TIMEVAULT_AUTO_MIGRATE`, `TIMEVAULT_LISTEN_ADDR`, `TIMEVAULT_ALLOW_NON_LOOPBACK`, `TIMEVAULT_REPOSITORY_PATH`, `TIMEVAULT_RESTORE_ROOT`, `TIMEVAULT_AUTH_DISABLED`, `TIMEVAULT_ADMIN_USER`, `TIMEVAULT_ADMIN_PASSWORD_HASH`, `TIMEVAULT_SESSION_KEY`, `TIMEVAULT_REDIS_URL`, `TIMEVAULT_REDIS_KEY_PREFIX`, `TIMEVAULT_REDIS_TIMEOUT` (and `TIMEVAULT_ENV_FILE`, which names the file). Everything else is a **Site setting**: 26 settings in seven groups (address and display, sign-in, performance, scheduler, search index, automatic updates, logging), listed in `internal/config/site.go` with their kind (text, number, true/false, choice) and default.
- **Where they are stored.** The table `site_settings` (migration `0006`): one row of `key` (the lower-case name, such as `job_workers`), `value`, `updated_at` and `updated_by` for each setting that has a value. A setting without a row has its default, and a value that equals the default is not stored, so a default that a later version changes reaches everybody who never chose a value. The name in the environment (`TIMEVAULT_JOB_WORKERS`) is the key in capitals behind `TIMEVAULT_`, which is how the one existing function, `config.Load`, reads it: `SiteLookup` answers those names from the stored values, so a Site setting is validated by the code that always validated it.
- **Only the local administrator may read or change them.** The permission `site` (`auth.PermSite`) belongs to the local administrator (the account of `TIMEVAULT_ADMIN_USER`) and to nobody else, not to a company account of type `admin`. The page `/settings/site` and the endpoints `GET` and `PATCH /api/v1/site-settings` and `POST /api/v1/site-settings/restart` answer anybody else who is signed in as if they did not exist (`404`), and the attempt is audited as `access.denied`; the **Settings** page offers the link to the local administrator only. The page shows what the environment and `.env` hold as well (never a secret).
- **A setting is checked before it is saved.** The values that would be stored are checked together with the bootstrap settings they depend on (`config.SiteProblems`: the web log needs the Redis of `TIMEVAULT_REDIS_URL`, an update address needs `TIMEVAULT_AUTO_MIGRATE=true`, the display folder rules need `server_host`, and so on), and the messages are written with the keys of the page and never repeat a value.
- **A setting takes effect after a restart.** Saving changes nothing in the running service; the page compares what is stored with what the service started with and, while they differ, says so and offers **Restart now**. The restart is the one that an automatic update uses (`update_restart`: a small launcher starts the program again, or the process ends with exit code 75 for a service manager). It is refused while a job runs: the job runner is drained first and resumed when the restart does not happen.
- **Start in two steps.** The pool of the database connections is sized by a Site setting (`job_workers`), so the service reads `.env`, connects with a small pool, applies the migrations (or checks that they were applied), reads the Site settings, builds and validates the configuration and only then opens the real pool. Stored settings that are not valid together stop the service with a message that names the problems.
- **No transition.** A Site setting that is still written in `.env` or in the environment is ignored, and the start-up log names it (never its value), so an old line can never be the reason a value that was saved on the page does not take effect. The table starts empty, and the administrator enters the values again. The removed `TIMEVAULT_EVERYTHING_*` settings are named in the same warning.
- **Audit.** A change is recorded as `site.update` and a restart as `site.restart`, with the names of the settings that changed or were reset and never their values (they can be addresses or paths). These events are visible to the local administrator only: the audit page and `GET /api/v1/audit` leave them out for everybody else (`audit.Filter.WithoutSite`) and do not offer them as a filter.
- **The way back is the command line.** `timevault-server settings list|get|set|reset` works on the database with the URL from `.env`, for the case that the page cannot be reached because a setting locked everybody out, or that there is no local administrator. `set` checks a value like the page does and refuses one that makes the settings worse. It is not audited: the row keeps `updated_by` = `cli` and the time.
- **`check-config` checks the bootstrap settings only.** It connects to nothing, so a service that updates itself can still ask the new program whether it accepts the settings of the service; the Site settings were checked when they were saved.

## Alternatives considered

- **Keep everything in `.env`.** It does not meet the request: the administrator cannot change a setting without access to the server's files, and every account that can see the Settings page saw the configuration.
- **Put every setting in the database, the database URL too.** Impossible for the URL (the service needs it to read the table), and a secret in a table that a page and an API show is the wrong place for it. The secrets stay in `.env`.
- **Apply a setting at once, without a restart.** Most settings are used when something is built: the pool, the workers, the listener, the cookies, the prefix of every address, the trusted proxies, the web log, the updater. Reloading them one by one gives states that are hard to reason about (a half-applied change of the prefix is a broken site), and one restart, with a button, is simple and the same for all.
- **Let company `admin` accounts edit them.** Not chosen by the owner. Those accounts come from another application's Redis, and a Site setting such as the trusted proxies, the sign-in options or the update address decides who may sign in and what program runs; the root of trust is the server's own `.env`, where the hash of the local administrator is.
- **Copy the values of the old `.env` on the first start.** Not chosen by the owner: there is one deployment, downtime is acceptable, and a one-time migration adds a code path that is run once and silently decides the meaning of old lines.
- **One JSON document or typed columns instead of one row per setting.** Rows are the simplest to extend (a new setting needs no migration) and to audit by name; the types are checked in Go, where the defaults are.

## Consequences

- A wrong value that is valid can still lock people out (`base_path`, `trusted_proxies`, `cookie_secure`, `session_ttl`): the command line is the way back, and the runbook says so.
- Whoever can run `timevault-server settings` with the `.env`, or write the table, changes a Site setting without the page and without an audit event; the table keeps who and when (`cli`).
- Upgrading from 1.0.0 means entering the values again, and a server that updates itself stops doing so until `update_url` is entered (the update that brings 1.0.1 is the last one it installs by itself until then). The upgrade notes in [changelog.md](../changelog.md) and the runbook say so.
- The Site settings are stored in plain text, so the secrets are not among them; anybody with access to the database can read and change them.
- The **Settings** page shows less (the version and the state of the automatic update); the rest moved to the Site settings page.
- With sign-in switched off (`TIMEVAULT_AUTH_DISABLED=true`, development only) everybody is the local administrator, so everybody can open the page in that mode.

---

# 中文

## 状态

于 2026-11-22 在 1.0.1 中被接受。取代 [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md) 中"部署设置放在 `.env` 或环境变量中"的那一部分：从 1.0.1 起，只有服务在能连上数据库之前就需要的设置以及机密仍放在那里，其余一切都保存在数据库中。监控文件夹及其计划仍和以前一样保存在数据库中。不会从旧的 `.env` 带过来任何东西。

## 背景

- 直到 1.0.0，每个运行设置都是一个 `TIMEVAULT_*` 环境变量或 `.env` 中的一行（大约四十个）。要修改其中一个，得有人在服务器上编辑文件并重启服务；写错了要到服务启动时才发现；而**设置**页会把当前运行的配置（主机名、监听地址、数据库、用户目录、单点登录服务、Web 日志、设置文件）显示给每个已登录的账号。
- 部署的所有者提出：设置应由管理员在网页上登录后编辑，保存在数据库中，对其他所有人不可见，并且 `.env` 只保留真正必要的内容。
- 有些设置不能移动：服务在读取数据库里的任何东西之前，必须先知道数据库；机密也不应该放在一个由页面显示、由 API 返回的表里。
- 只有一台部署，并且可以停机，所以不需要为旧的值做过渡。

## 决定

- **两类设置。** *引导设置和机密*仍在环境变量和 `.env` 中：`TIMEVAULT_DATABASE_URL`、`TIMEVAULT_AUTO_MIGRATE`、`TIMEVAULT_LISTEN_ADDR`、`TIMEVAULT_ALLOW_NON_LOOPBACK`、`TIMEVAULT_REPOSITORY_PATH`、`TIMEVAULT_RESTORE_ROOT`、`TIMEVAULT_AUTH_DISABLED`、`TIMEVAULT_ADMIN_USER`、`TIMEVAULT_ADMIN_PASSWORD_HASH`、`TIMEVAULT_SESSION_KEY`、`TIMEVAULT_REDIS_URL`、`TIMEVAULT_REDIS_KEY_PREFIX`、`TIMEVAULT_REDIS_TIMEOUT`（以及指明文件的 `TIMEVAULT_ENV_FILE`）。其余一切都是**站点设置**：分七组共 26 个设置（地址与显示、登录、性能、调度器、搜索索引、自动更新、日志），列在 `internal/config/site.go` 中，带有类型（文本、数字、真/假、选项）和默认值。
- **保存在哪里。** 表 `site_settings`（迁移 `0006`）：每个有值的设置一行，含 `key`（小写的名称，例如 `job_workers`）、`value`、`updated_at` 和 `updated_by`。没有行的设置取默认值，与默认值相同的值不会被保存，这样以后版本改变的默认值，会惠及所有从未选择过值的人。环境变量中的名字（`TIMEVAULT_JOB_WORKERS`）就是大写的键加上 `TIMEVAULT_` 前缀，现有的唯一函数 `config.Load` 就是按这个名字读取它的：`SiteLookup` 用已保存的值回答这些名字，所以站点设置由一直在校验它的那段代码来校验。
- **只有本地管理员可以读取或修改它们。** 权限 `site`（`auth.PermSite`）属于本地管理员（`TIMEVAULT_ADMIN_USER` 的账号），不属于其他任何人，包括类型为 `admin` 的公司账号。对于其他已登录的人，页面 `/settings/site` 以及接口 `GET` 和 `PATCH /api/v1/site-settings`、`POST /api/v1/site-settings/restart` 的应答就好像它们不存在一样（`404`），并且这次尝试会作为 `access.denied` 记入审计；**设置**页只向本地管理员提供这个链接。该页面也显示环境变量和 `.env` 里的内容（绝不显示机密）。
- **设置在保存之前先检查。** 将要保存的值会连同它们所依赖的引导设置一起检查（`config.SiteProblems`：Web 日志需要 `TIMEVAULT_REDIS_URL` 所指的 Redis，更新地址需要 `TIMEVAULT_AUTO_MIGRATE=true`，显示文件夹的规则需要 `server_host`，等等），提示信息用页面上的键来书写，并且绝不重复任何值。
- **设置在重启之后才生效。** 保存不会改变正在运行的服务；页面把已保存的内容与服务启动时所用的比较，不同时会这样说明并提供**立即重启**。这个重启就是自动更新所用的那个（`update_restart`：由一个小型启动器再次启动程序，或者进程以退出码 75 结束，交给服务管理器）。有任务在运行时它会拒绝：任务运行器会先被排空，在没有重启时再恢复。
- **分两步启动。** 数据库连接池的大小由一个站点设置（`job_workers`）决定，所以服务先读取 `.env`，用一个小连接池连接，应用迁移（或检查迁移已应用），读取站点设置，构建并校验配置，然后才打开真正的连接池。已保存的设置放在一起无效时，服务会停止并给出点名问题的消息。
- **不做过渡。** 仍写在 `.env` 或环境变量里的站点设置会被忽略，启动日志会点名它们（绝不显示它们的值），这样旧的一行就永远不会成为"在页面上保存的值没有生效"的原因。表一开始是空的，管理员重新填写这些值。已删除的 `TIMEVAULT_EVERYTHING_*` 设置也在同一条警告中被点名。
- **审计。** 修改记为 `site.update`，重启记为 `site.restart`，其中有发生变化或被重置的设置的名称，绝不包含它们的值（值可能是地址或路径）。这些事件只有本地管理员能看到：审计页和 `GET /api/v1/audit` 对其他所有人都会略去它们（`audit.Filter.WithoutSite`），也不会把它们作为筛选项提供。
- **回头的路是命令行。** `timevault-server settings list|get|set|reset` 使用 `.env` 中的 URL 直接操作数据库，用于页面无法访问（例如某个设置把所有人都锁在了外面）或没有本地管理员的情况。`set` 会像页面一样检查值，并拒绝会让设置变得更糟的值。它不会被审计：该行会保留 `updated_by` = `cli` 和时间。
- **`check-config` 只检查引导设置。** 它不连接任何东西，所以会自己更新的服务仍然可以问新程序是否接受服务的设置；站点设置在保存时已经检查过了。

## 考虑过的备选方案

- **一切都留在 `.env` 中。** 这不符合要求：管理员不访问服务器上的文件就无法修改设置，而且每个能看到设置页的账号都看到了配置。
- **把每个设置都放进数据库，包括数据库 URL。** URL 做不到（服务需要它才能读表），而且机密放在一个由页面显示、由 API 返回的表里是不合适的。机密仍留在 `.env` 中。
- **设置立即生效，不用重启。** 大多数设置是在构建某个东西时使用的：连接池、worker、监听器、Cookie、每个地址的前缀、受信任的代理、Web 日志、更新器。逐个重新加载会产生难以推断的状态（前缀改到一半，站点就坏了），而一次重启加一个按钮，既简单，对所有设置又一致。
- **让公司的 `admin` 账号编辑它们。** 所有者没有选择这个方案。这些账号来自另一个应用的 Redis，而受信任的代理、登录选项或更新地址这样的站点设置，决定谁能登录和运行什么程序；信任的根是服务器自己的 `.env`，本地管理员的哈希就在那里。
- **第一次启动时复制旧 `.env` 中的值。** 所有者没有选择这个方案：只有一台部署，可以停机，而一次性的迁移会增加一条只运行一次、并且悄悄决定旧行含义的代码路径。
- **用一个 JSON 文档或带类型的列，而不是每个设置一行。** 行最容易扩展（新设置不需要迁移），也最容易按名称审计；类型在 Go 中检查，默认值也在那里。

## 影响

- 一个有效但错误的值仍可能把人锁在外面（`base_path`、`trusted_proxies`、`cookie_secure`、`session_ttl`）：回头的路是命令行，运维手册里这样说明。
- 能用该 `.env` 运行 `timevault-server settings`、或者能写入该表的人，不用页面、也不产生审计事件就能修改站点设置；该表保留了谁和何时（`cli`）。
- 从 1.0.0 升级意味着重新填写这些值，并且会自己更新的服务器在重新填写 `update_url` 之前就不再自动更新（带来 1.0.1 的这次更新，就是它在那之前自己安装的最后一次更新）。[changelog.md](../changelog.md) 的升级说明和运维手册都这样说明。
- 站点设置以明文保存，所以机密不在其中；任何能访问数据库的人都可以读取和修改它们。
- **设置**页显示的内容变少了（版本和自动更新的状态）；其余的移到了站点设置页。
- 关闭登录时（`TIMEVAULT_AUTH_DISABLED=true`，仅用于开发），每个人都是本地管理员，所以在该模式下每个人都能打开该页面。
