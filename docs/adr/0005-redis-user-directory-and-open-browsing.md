# English

## Status

Accepted for 0.1.1 (2026-11-15). Replaces the "one administrator, everything behind sign-in" model of the MVP (see [0004](./0004-restore-safety-and-privileges.md), last bullet). plan.md says not to add Redis; this decision makes one deliberate exception, for the user directory only (see "Decision").

## Context

The MVP had one local administrator and put every page behind sign-in. The project owner asked for the opposite default: **anyone can browse; only an operation that touches data needs an account**, and the accounts are the company accounts that another application of the same company (the "dash" application) already keeps in Redis, in hashes named `app_user_<ee_code>_<name>_<email>`. TimeVault must not create a second set of passwords.

## Decision

**Who may do what**

| Visitor | May |
|---|---|
| Anyone, no account | Browse: Explorer, Time Machine, file versions, the Backups page (state, schedule and results of each monitored folder, and the state of its backup jobs) and the read-only JSON API. Not: the folders' paths on the server's disk, restore jobs, who requested a job, why one failed and its log. Never file contents (no endpoint serves them). |
| Any signed-in active account | Everything above, plus: start a backup or an integrity check, cancel a job, preview and start a restore, read restore jobs, the reason and the log of a failed job, who requested a job, the audit log, the status page and the (read-only) settings page. |
| Account of type `admin` | Everything above, plus: add, edit, switch off and schedule monitored folders. |
| Optional local administrator | Same as `admin`. It exists only when `TIMEVAULT_ADMIN_PASSWORD_HASH` is set, as the way in when Redis is down. Its name is never looked up in Redis. |

**Accounts**

- Read from Redis by the adapter in `internal/redisuser`. Every field of a hash is `zlib.compress(pickle.dumps(value))`; a restricted decoder reads the few value types that occur and never executes anything. A login may be the employee ID (`ee_code`), the name or the e-mail address; when several records match, the newest `UpdateDate` wins and two different employee IDs for one login mean the login is refused. Only records with `activated` set and a stored password can sign in. The role is `admin` when `Type` is `admin`, `user` otherwise.
- TimeVault only reads Redis (`SCAN`, `HGETALL`, `PING`). It never writes, never uses `KEYS`, and never logs a record or a password. A failed sign-in is audited with the account name as it was typed, cut to 64 characters, because the audit trail must show who was guessed at.
- Settings: `TIMEVAULT_REDIS_URL` (empty = no company accounts), `TIMEVAULT_REDIS_KEY_PREFIX` (default `app_user_`), `TIMEVAULT_REDIS_TIMEOUT` (default 3 s).

**Sessions**

- Still stateless, HMAC-signed cookies, now carrying the account, its name, its role and the Redis key it was read from; cookies of the MVP format are rejected.
- Pages that are open to everyone only read the cookie, so browsing never depends on Redis. An operation checks the account in Redis again (`Refresh`) and the answer is trusted for 60 seconds: a deactivated account or a changed role takes effect within a minute, and a deactivated account's cookie is ended on the spot.
- **Fail-soft**: when Redis cannot be asked, an already signed-in session is trusted as issued and Redis is asked again after 15 seconds. New sign-ins of company accounts fail with `503` and the page says so; they are not held against the account, but they do count for the client address, so nobody can make Redis work without limit. Browsing and the local administrator are unaffected. A request that the visitor abandons while Redis is being asked says nothing about the account and is never mistaken for an outage.
- Sign-in attempts are counted when they start, so guesses that run in parallel cannot all pass before the first failure is known: per client address (5 attempts in 10 minutes; a successful sign-in gives its own attempt back and does not wipe the failures of that address) and per account name (10 failures in 10 minutes; names are compared without case and, for an e-mail address, by the part before the `@`, which is how the employee ID is matched). Company passwords can be short and a guesser may use many addresses, which is why both limits exist.
- Pages that show an account more than a visitor (`/backups`, `/jobs/{id}`, `GET /api/v1/sources`, `GET /api/v1/jobs/{id}`) ask the directory about a visitor who has a cookie before they show it: who requested a job, why it failed, the log, the restore jobs and the folders on the server's disk are for active accounts only.

## Alternatives considered

- **Local accounts only.** Simple, but a second set of passwords to keep in step with the company's, and no link to who actually left the company.
- **A users table in PostgreSQL filled from Redis.** Adds a synchronisation job and a stale copy; the live read is one hash per sign-in or minute.
- **Sharing the other application's session.** Couples two applications' cookies and secrets.
- **LDAP or Active Directory.** Not what the company uses for this application today; may be added behind the same `auth.Directory` interface.
- **`KEYS app_user_*`.** Blocks Redis on large data sets; `SCAN` with a bounded number of candidates is used instead.

## Consequences

- A new sign-in needs Redis; browsing, an existing session and the local administrator do not.
- **Passwords in that Redis data are stored in plain text** by the other application, and the sign-in form posts the password to TimeVault. Anyone on the network path can read it unless the connection is HTTPS: put a TLS reverse proxy in front before any other computer uses TimeVault and set `TIMEVAULT_COOKIE_SECURE=true`. Redis itself should require a password and not be reachable from the network.
- The file names, sizes and modification times of every monitored folder are visible to anyone who can reach the port. Contents are not. See [security.md](../security.md).
- Authorization is coarse: no per-source or per-path rights. Any signed-in account may restore any source and cancel any job. *(Changed in 0.1.7: what an account may do is decided for each monitored folder, see [0013](./0013-per-folder-access-levels.md). The other rows of the table above are the 0.1.1 rules; for folders, read them with that record.)*
- The adapter assumes **one live record per employee ID**. When the other application leaves stale records behind (for example after an e-mail change), the newest `UpdateDate` wins at sign-in, but a session keeps following the record it signed in with; `check-redis-users` reports "ee_codes on more than one key" so that they can be cleaned up.
- A sign-in costs one `SCAN` over the keyspace of the Redis database, and `/login` is open to everyone; the per-address limit bounds how often a visitor can cause it. A login that matches more than 200 keys (`MaxScan`) is refused as ambiguous instead of being answered from some of them.
- Changing a password in the other application does not end TimeVault sessions that already exist; deactivating the account does, within a minute.
- A record that cannot be read (damaged or in an unknown format) refuses the sign-in and is counted in `timevault-server check-redis-users`, which prints a report without any names, e-mail addresses or passwords.
- Two Go modules were added. `github.com/redis/go-redis/v9` (v9.22.0, pinned so that `go.mod` stays at `go 1.25.0`) is needed because TimeVault has to talk to Redis and the standard library has no client. `github.com/alicebob/miniredis/v2` (v2.40.0, tests only) lets the Redis tests run in-process without a Redis server. The pickle decoder is written in this repository (`internal/redisuser`): a restricted subset that never executes anything.

---

# 中文

## 状态

于 2026-11-15 在 0.1.1 中被接受。取代 MVP 中"一个管理员、所有页面都需登录"的模型（见 [0004](./0004-restore-safety-and-privileges.md) 的最后一条）。plan.md 说明不要引入 Redis；本决定仅为用户目录这一项做了有意的例外（见"决定"）。

## 背景

MVP 只有一个本地管理员，并且所有页面都需要登录。项目负责人要求相反的默认值：**任何人都可以浏览；只有涉及数据的操作才需要账号**，而账号就是公司另一个应用（"dash"应用）已经保存在 Redis 里的公司账号，键名为 `app_user_<ee_code>_<name>_<email>` 的哈希。TimeVault 不应再建立第二套密码。

## 决定

**谁可以做什么**

| 访问者 | 可以 |
|---|---|
| 任何人，无需账号 | 浏览：文件浏览、时间机器、文件版本、备份页（每个监控文件夹的状态、计划和结果，以及其备份任务的状态）和只读 JSON API。不包括：文件夹在服务器磁盘上的路径、恢复任务、任务是谁发起的、失败原因及其日志。绝不能看到文件内容（没有任何接口提供文件内容）。 |
| 任何已登录且已启用的账号 | 上述全部，另外可以：发起备份或完整性校验、取消任务、预览并启动恢复、查看恢复任务、查看失败任务的原因和日志、查看任务是谁发起的、审计日志、状态页以及（只读的）设置页。 |
| 类型为 `admin` 的账号 | 上述全部，另外可以：添加、编辑、停用并设置监控文件夹的计划。 |
| 可选的本地管理员 | 与 `admin` 相同。只有设置了 `TIMEVAULT_ADMIN_PASSWORD_HASH` 才存在，用作 Redis 不可用时的入口。它的名字永远不会到 Redis 中查找。 |

**账号**

- 由 `internal/redisuser` 中的适配器从 Redis 读取。哈希的每个字段都是 `zlib.compress(pickle.dumps(value))`；受限的解码器只读取实际出现的少数几种值类型，绝不执行任何内容。登录名可以是工号（`ee_code`）、姓名或邮箱；多条记录匹配时取 `UpdateDate` 最新的一条，同一个登录名对应两个不同的工号时拒绝登录。只有已设置 `activated` 并保存了密码的记录才能登录。`Type` 为 `admin` 时角色为 `admin`，否则为 `user`。
- TimeVault 只读取 Redis（`SCAN`、`HGETALL`、`PING`）。从不写入，从不使用 `KEYS`，也从不把记录或密码写入日志。登录失败会连同输入的账号名一起写入审计（截断到 64 个字符），因为审计需要显示是谁被猜测。
- 设置项：`TIMEVAULT_REDIS_URL`（为空表示没有公司账号）、`TIMEVAULT_REDIS_KEY_PREFIX`（默认 `app_user_`）、`TIMEVAULT_REDIS_TIMEOUT`（默认 3 秒）。

**会话**

- 仍然是无状态、HMAC 签名的 Cookie，现在带有账号、姓名、角色以及读取它的 Redis 键；MVP 格式的 Cookie 会被拒绝。
- 对所有人开放的页面只读取 Cookie，所以浏览从不依赖 Redis。执行操作时会再次到 Redis 检查账号（`Refresh`），答复被信任 60 秒：账号被停用或角色变化会在一分钟内生效，被停用账号的 Cookie 会当场结束。
- **Fail-soft**：当 Redis 无法询问时，已经登录的会话按签发时的内容被信任，15 秒后再询问 Redis。公司账号的新登录会返回 `503`，页面会说明原因；这类失败不会算到账号头上，但会计入客户端地址，所以没有人能让 Redis 无限制地工作。浏览和本地管理员不受影响。访问者在询问 Redis 期间放弃的请求，不说明任何关于账号的事，也绝不会被误认为 Redis 故障。
- 登录尝试在开始时就被计数，所以并行的猜测不会在第一次失败被知道之前全部通过：按客户端地址（10 分钟内 5 次尝试；成功登录会归还它自己的那一次，且不会清除该地址的失败记录），也按账号名（10 分钟内 10 次失败；账号名不区分大小写，邮箱地址按 `@` 之前的部分比较，这正是匹配工号的方式）。公司密码可能很短，而且猜密码的人可能使用很多地址，所以需要这两种限制。
- 对账号比对访问者展示更多内容的页面（`/backups`、`/jobs/{id}`、`GET /api/v1/sources`、`GET /api/v1/jobs/{id}`），在展示之前会先向目录询问带有 Cookie 的访问者：任务是谁发起的、为什么失败、日志、恢复任务以及服务器磁盘上的文件夹，只有已启用的账号才能看到。

## 考虑过的备选方案

- **只用本地账号。** 简单，但要多维护一套与公司账号不同步的密码，也无法知道谁已经离职。
- **用 Redis 的数据填充 PostgreSQL 中的用户表。** 需要同步任务，还会有过期的副本；而实时读取每次登录或每分钟只读一个哈希。
- **共享另一个应用的会话。** 会把两个应用的 Cookie 和密钥耦合在一起。
- **LDAP 或 Active Directory。** 目前该应用并不用它；以后可以放在同一个 `auth.Directory` 接口后面添加。
- **`KEYS app_user_*`。** 数据量大时会阻塞 Redis；改用候选数量有上限的 `SCAN`。

## 影响

- 新的登录需要 Redis；浏览、已有的会话和本地管理员不需要。
- **该 Redis 数据中的密码由另一个应用以明文保存**，而登录表单会把密码提交给 TimeVault。除非使用 HTTPS，否则网络路径上的任何人都能读到：在其他计算机使用 TimeVault 之前，请在前面放置 TLS 反向代理，并设置 `TIMEVAULT_COOKIE_SECURE=true`。Redis 本身应当要求密码，并且不能从网络访问。
- 能访问该端口的任何人都能看到每个监控文件夹的文件名、大小和修改时间，但看不到内容。见 [security.md](../security.md)。
- 授权比较粗：没有按备份源或按路径的权限。任何已登录账号都可以恢复任何备份源、取消任何任务。*（0.1.7 中已改变：账号能做什么按监控文件夹分别决定，见 [0013](./0013-per-folder-access-levels.md)。上面表格中的其他行是 0.1.1 的规则；涉及文件夹时，请结合那份记录阅读。）*
- 适配器假定**每个工号只有一条有效记录**。如果另一个应用留下了过期记录（例如修改邮箱之后），登录时取 `UpdateDate` 最新的一条，但会话会沿用它登录时的那条记录；`check-redis-users` 会报告"ee_codes on more than one key"，以便清理。
- 一次登录会对 Redis 数据库的键空间执行一次 `SCAN`，而 `/login` 对所有人开放；按地址的限流限制了访问者能触发它的频率。匹配到超过 200 个键（`MaxScan`）的登录名会被当作有歧义而拒绝，而不是只依据其中一部分来回答。
- 在另一个应用中修改密码，不会结束 TimeVault 中已有的会话；停用账号则会在一分钟内结束。
- 无法读取的记录（损坏或格式未知）会拒绝登录，并会被 `timevault-server check-redis-users` 计数；该命令输出的报告中没有任何姓名、邮箱或密码。
- 新增了两个 Go 模块。`github.com/redis/go-redis/v9`（v9.22.0，固定在该版本以使 `go.mod` 保持 `go 1.25.0`），因为 TimeVault 需要访问 Redis，而标准库没有 Redis 客户端。`github.com/alicebob/miniredis/v2`（v2.40.0，仅用于测试），让 Redis 相关测试在进程内运行，不需要 Redis 服务器。pickle 解码器是在本仓库中自行编写的（`internal/redisuser`）：只支持受限的子集，绝不执行任何内容。
