# English

## Status

Accepted for 0.1.1 (2026-11-15). Adds to [0004](./0004-restore-safety-and-privileges.md) (restores stay manual-confirmed) and to [0001](./0001-recovery-point-history-model.md) (a recovery point is only written when something changed, for automatic runs).

**Update, 1.0.1.** Replaced in part by [0017](./0017-site-settings-in-the-database.md): only the bootstrap settings and the secrets are in `.env` or the environment now. The other settings, named here as `TIMEVAULT_*` variables, are Site settings in the database, edited by the local administrator on the Site settings page and used after a restart; their key is the name in lower case without the prefix. The monitored folders and the automatic schedule of this record are unchanged. The record is left as it was written.

## Context

Three questions came up while testing the MVP:

1. Where do the monitored folders and the backup folder (the repository) get set? The MVP could add a source on the Backups page, could not edit or switch one off, and showed the repository path only on the Status page.
2. Settings can live in the environment, in a file read by the service, or in the database, and each fits some settings and not others.
3. Backups were started by hand only; a backup system that has to be remembered is not one.

## Decision

**Where settings live**

| What | Where | Why |
|---|---|---|
| Deployment: database URL, Redis URL, listen address, repository folder, restore folder, session key, search, workers, logging | The process environment and/or a `.env` file that the service reads itself | The database URL has to be known before the database can be asked. The repository folder must not change while backups exist. Secrets belong in one file that is easy to protect and never in the database. |
| Monitored folders: name, folder, exclude patterns, on or off, automatic schedule | The database (table `sources`), edited by administrators on the **Settings** page or with `PATCH /api/v1/sources/{id}` | They are data that people change while the service runs, and they have history. |
| Accounts and roles | Redis, written by another application | See [0005](./0005-redis-user-directory-and-open-browsing.md). |

- The `.env` file is `TIMEVAULT_ENV_FILE` when that is set (it must exist), otherwise `.env` in the working directory, otherwise `.env` next to the executable. The first one found is used; files are not merged. A variable that is set in the process environment wins over the file. A line is `KEY=VALUE`, the value is taken literally to the end of the line (a path with `\` or a password with `#` needs no escaping), `#` starts a comment only at the start of a line, and an error names the line number and never its content. `/.env` is in `.gitignore`.
- The Settings page shows the repository folder and the restore folder read-only, with the free space of the repository, and the path of the settings file. Secrets are never shown: the database and Redis appear as `host:port/name`.
- A monitored folder can be switched off but not deleted, because its history stays. The folder of a source cannot be changed once it has any recovery point, in any state; to move a folder, switch the old one off and add the new one. Changing the folder of a source that has none needs the source's writer lock, so it is refused (`409`) while a backup of that source runs, and it forgets when the source last ran.

**Automatic backups**

- A source has an optional `schedule_minutes` (1 to 43200) and a `last_run_at`. A source is due when it is enabled, has a schedule and either never ran or its last run ended at least `schedule_minutes` ago. The interval is counted from the **end** of the previous run, whatever its outcome (succeeded, failed, canceled or found nothing changed), so a source that keeps failing is retried once per interval and never in a loop.
- `RunScheduler` looks every 15 seconds (once at start) and queues a backup for each due source that has no queued or running backup. The job's requester is `scheduler`. Setting a schedule on a source, or switching it on again, starts a backup within one look when its last run is older than the interval.
- Automatic runs skip unchanged work: when nothing was added, modified or deleted and the number of items that could not be read is the same as in the newest recovery point, the run records **nothing**: no recovery point, no job record, no audit event; only `last_run_at` moves. The first backup of a source is always recorded, and a different number of unreadable items is worth a point of its own. A backup started by a person always records a point. The comparison is by number, not by which items.

## Alternatives considered

- **Everything in the environment.** One mechanism, but monitored folders then need a restart and the UI could not manage them, which was the complaint.
- **Everything in the database.** The database URL cannot live there, and the repository folder would become editable while it is in use.
- **A structured config file (TOML or YAML).** Another parser and format for a handful of keys; `.env` is what the owner's other application already uses.
- **Cron expressions or times of day.** More to explain and to get wrong than "every N minutes"; may be added on top of `last_run_at`.
- **Windows Task Scheduler calling the API.** Works today with `POST /api/v1/backups`, but needs a credential outside the service and gives no schedule in the UI.

## Consequences

- Changing the repository folder, the restore folder, a port or a secret needs a restart. Monitored folders never do.
- There is **no retention yet**. An automatic run that finds changes adds a recovery point for good, so a short interval on a folder that changes all the time grows the repository without bound; pick the interval with that in mind.
- A scheduled folder that cannot be read (an unplugged drive, a deleted folder) produces one failed job and one audit record per interval; at an interval of 1 minute that is 1,440 a day.
- A manual **Back up now** that meets an automatic job of the same folder is answered "already queued or running" and joins it. If that job finds nothing changed it disappears, and the requested full re-read is not performed; its page then shows "not found".
- Two administrators editing the same folder at once: the form replaces every field with what it shows, so the last save wins; a `PATCH` is merged with what was read a moment before, so a concurrent change to a field it does not mention can be undone.
- The scheduler uses the clock of the database server (`now()`), not of the computer that runs the service.

---

# 中文

## 状态

于 2026-11-15 在 0.1.1 中被接受。是对 [0004](./0004-restore-safety-and-privileges.md)（恢复仍然需要人工确认）和 [0001](./0001-recovery-point-history-model.md)（自动运行只有在发生变化时才写入恢复点）的补充。

**更新，1.0.1 版。** 已被 [0017](./0017-site-settings-in-the-database.md) 部分取代：现在只有引导设置和机密仍在 `.env` 或环境变量中。其他设置（本记录中以 `TIMEVAULT_*` 变量命名的那些）是保存在数据库中的站点设置，由本地管理员在站点设置页上编辑，重启之后使用；它们的键是去掉前缀的小写名称。本记录中的监控文件夹和自动备份计划没有变。本记录保持原样。

## 背景

测试 MVP 时出现了三个问题：

1. 监控文件夹和备份文件夹（仓库）在哪里设置？MVP 可以在备份页添加备份源，但不能编辑或停用，并且只在状态页显示仓库路径。
2. 设置可以放在环境变量、服务自己读取的文件或数据库中，每一种只适合一部分设置。
3. 备份只能手动启动；需要靠人记得去做的备份系统不算备份系统。

## 决定

**设置放在哪里**

| 内容 | 位置 | 原因 |
|---|---|---|
| 部署设置：数据库 URL、Redis URL、监听地址、仓库文件夹、恢复文件夹、会话密钥、搜索、worker、日志 | 进程环境变量和/或服务自己读取的 `.env` 文件 | 必须先知道数据库 URL 才能去询问数据库。存在备份时仓库文件夹不能更改。机密信息应放在一个容易保护的文件里，绝不放进数据库。 |
| 监控文件夹：名称、文件夹、排除规则、启用或停用、自动备份计划 | 数据库（表 `sources`），由管理员在**设置**页或通过 `PATCH /api/v1/sources/{id}` 编辑 | 它们是服务运行期间由人修改的数据，并且带有历史。 |
| 账号和角色 | Redis，由另一个应用写入 | 见 [0005](./0005-redis-user-directory-and-open-browsing.md)。 |

- `.env` 文件是 `TIMEVAULT_ENV_FILE`（设置时它必须存在），否则是工作目录下的 `.env`，否则是可执行文件旁边的 `.env`。使用找到的第一个；不会合并多个文件。进程环境变量中已设置的变量优先于文件。每行是 `KEY=VALUE`，值按字面一直取到行尾（带 `\` 的路径或带 `#` 的密码无需转义），`#` 只有在行首才开始注释，出错时只指出行号，绝不显示内容。`/.env` 已在 `.gitignore` 中。
- 设置页以只读方式显示仓库文件夹和恢复文件夹、仓库的剩余空间以及设置文件的路径。机密信息绝不显示：数据库和 Redis 只显示为 `host:port/name`。
- 监控文件夹可以停用，但不能删除，因为它的历史要保留。备份源一旦有任何状态的恢复点，它的文件夹就不能再更改；要迁移文件夹，请停用旧的并添加新的。更改还没有恢复点的备份源的文件夹需要该备份源的写入锁，所以在它的备份运行时会被拒绝（`409`），并且会忘记它上次运行的时间。

**自动备份**

- 备份源有可选的 `schedule_minutes`（1 到 43200）和 `last_run_at`。当备份源已启用、设置了计划，并且从未运行过或上次运行结束至少 `schedule_minutes` 分钟之前，它就到期了。间隔从上一次运行的**结束**开始计算，无论结果如何（成功、失败、取消或没有发现变化），所以一直失败的备份源每个间隔重试一次，绝不会陷入循环。
- `RunScheduler` 每 15 秒查看一次（启动时也看一次），为每个到期且没有排队或正在运行的备份任务的备份源排入一个备份。任务的发起人是 `scheduler`。给备份源设置计划，或重新启用它时，如果它上次运行早于间隔，会在一次查看之内启动备份。
- 自动运行会跳过没有变化的工作：如果没有新增、修改或删除，并且无法读取的项目数量与最新恢复点相同，这次运行**什么都不记录**：没有恢复点，没有任务记录，没有审计事件；只有 `last_run_at` 会前移。备份源的第一次备份总会被记录，无法读取的项目数量不同也值得单独记一个恢复点。由人发起的备份总会记录恢复点。比较的是数量，而不是具体是哪些项目。

## 考虑过的备选方案

- **全部放在环境变量中。** 只有一种机制，但这样监控文件夹就需要重启，界面也无法管理它们，而这正是被抱怨的问题。
- **全部放在数据库中。** 数据库 URL 不可能放在那里，而且仓库文件夹会在使用期间变得可编辑。
- **结构化配置文件（TOML 或 YAML）。** 为少数几个键引入另一种解析器和格式；`.env` 是负责人的另一个应用已经在用的方式。
- **cron 表达式或每天的时刻。** 比"每 N 分钟"更难解释，也更容易写错；以后可以在 `last_run_at` 之上添加。
- **用 Windows 任务计划程序调用 API。** 现在用 `POST /api/v1/backups` 就可以做到，但需要在服务之外保存凭据，并且界面里看不到计划。

## 影响

- 更改仓库文件夹、恢复文件夹、端口或机密信息需要重启。监控文件夹永远不需要。
- 目前**没有保留策略**。发现变化的自动运行会永久增加一个恢复点，所以对一直在变化的文件夹使用很短的间隔，会让仓库无限增长；选择间隔时请考虑这一点。
- 无法读取的计划备份源（拔掉的磁盘、被删除的文件夹）每个间隔产生一个失败任务和一条审计记录；间隔为 1 分钟时，每天就是 1,440 个。
- 手动点击**立即备份**时，如果同一文件夹已有自动任务，会得到"已在排队或运行"的提示并并入该任务。如果该任务发现没有变化，它就会消失，请求的完整重读也不会执行；它的页面此时会显示"未找到"。
- 两个管理员同时编辑同一个文件夹：表单会用它显示的内容替换每个字段，所以最后保存者生效；`PATCH` 则与一刻之前读到的内容合并，因此它没有提到的字段如果被并发修改，可能会被撤销。
- 调度器使用数据库服务器的时钟（`now()`），而不是运行服务的计算机的时钟。
