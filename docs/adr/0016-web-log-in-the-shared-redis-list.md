# English

## Status

Accepted for 1.0.0 (2026-11-18). It adds the first write that TimeVault makes to Redis, and so amends two statements: "TimeVault only reads Redis" in [0005](./0005-redis-user-directory-and-open-browsing.md) and "The Redis account of TimeVault is read-only on purpose" in [0009](./0009-single-sign-on-and-trusted-proxies.md). The accounts and the single sign-on results are still only read, and the Redis account still must not delete keys; what changes is that it has to be allowed to append to one list. The routes, the accounts, the permissions and the data are unchanged.

## Context

- The company's applications share one web log: the Redis list `web_log`. Each application appends lines about what it does, and a log page reads the list. TimeVault wrote nothing to it, so the pages people open in TimeVault and what they do there were not in the log that the company reads.
- TimeVault already talks to that Redis, for the user directory ([0005](./0005-redis-user-directory-and-open-browsing.md)) and for the single sign-on results ([0009](./0009-single-sign-on-and-trusted-proxies.md)), and `internal/redisuser` only reads. The audit trail is in PostgreSQL and the service has its own console log; neither is the shared one.
- The real list was looked at once, read-only (nothing was written to it): about 75,000 lines, no expiry on the key, about 9 MB, Redis 5.0, `maxmemory` 0. Every element is `zlib.compress(pickle.dumps(line, protocol=4))` of a text line with six fields told apart by ` | `: `ip | time | message | status | level | host`, for example `127.0.0.1 | 2026-11-18 13:58:06.446524 | home_page: access | 200 | info | sgmeslt595`.
- A reader that splits a line by position has to keep working, so TimeVault cannot add a field for the account; it has to say it in the message.
- The list is shared, it never expires, and the other applications manage it. What TimeVault writes stays there, and anybody who can reach TimeVault can cause pages to be opened. The Redis has no `maxmemory` limit and `noeviction`, and it also holds the accounts that every company application signs in with.
- TimeVault knows a person by the employee ID of the account, so a line about a person holds an employee ID and an address: personal data, in a list that others read.

## Decision

- **TimeVault appends to the shared list.** The list is `web_log` by default (`TIMEVAULT_WEB_LOG_KEY`), in the Redis of `TIMEVAULT_REDIS_URL`, in the same database. TimeVault only appends (`RPUSH`); it never reads the list back, and never trims, expires or deletes anything in it.
- **The lines are exactly the ones the other applications write.** The element is `zlib.compress(pickle.dumps(line, protocol=4))`, written byte for byte the way Python writes a `str` (`internal/weblog/encode.go`; the tests compare it with pickles that Python printed), and the line has the six fields `ip | time | message | status | level | host`, for example `10.0.0.5 | 2026-11-18 14:05:12.250431 | TimeVault: /explorer user=E123456 0.112(s) | 200 | info | sgmeslt595`. No field is added. The account is in the message as `user=<employee ID>` (`guest` for a visitor who is not signed in), and every message starts with `TimeVault: `. `ip` is the address of the browser (`Server.clientIP`; `127.0.0.1` for what the service does on its own), the time is the local time of the server, `status` is the HTTP status (or 200, 403 or 500 for what the audit trail and the scheduler report), `level` is `info`, `warning`, `error` or `debug`, and `host` is `TIMEVAULT_WEB_LOG_HOST` or, by default, the name of the computer in lower case. A `|` in a message becomes `/`, a control character becomes a space, and a message is cut at 400 characters, so a line always has six fields.
- **Where the lines come from.**
  - *A page that somebody opened* (every `GET` of a page; not a static file, `/healthz`, an API call, a redirect, or a reload that the page script made by itself): `TimeVault: <route> user=<employee ID> <seconds>(s)`. The route is the pattern that matched, with the numbers filled in (`/jobs/12`), never the address as it was typed. The level is `info` for a `2xx` and `warning` for a `4xx`.
  - *A request that failed on the server* (any `5xx`, also of the API, and a panic): `TimeVault: error <route> user=<employee ID> ref=<reference>`, level `error`. The reference is the one the error page shows; the console log holds it as `id=` together with the error text, which is never written to Redis.
  - *Every audit event,* after it is stored in the database (`audit.Logger.SetMirror`): `TimeVault: <action> …` with the monitored folder (`source=<id>`), another target (`point=17`), the actor and, when the event has one, the duration. `ok` is `info` and 200, `denied` is `warning` and 403, `failed` is `error` and 500. Of the detail only the method and the route of a refusal and the stage, `from` and `to` of a program update are used. The name that was typed at a failed sign-in, which may be a password, is never copied (`user=-`). The address is that of the request; the scheduler and the updater have `127.0.0.1`.
  - *The scheduler:* `scheduler.queued`, `scheduler.error` (a warning with the status 500, at most once per folder and minute, like the warning in the console) and, at the debug level only, `scheduler.skipped`.
  - *The service:* `TimeVault: started version=<version>` once everything is built, and `TimeVault: stopped` when it stops.
  - *At the debug level* (`TIMEVAULT_LOG_LEVEL=debug`) the page line is also written for `GET /api/…` (the polling of a job) and for the reloads that the page script makes by itself (the request header `X-TV-Auto: 1`). The other log levels change nothing in the web log.
- **Nothing that a visitor typed goes in.** Never a path, the name of a file or a folder, a query string, a search text or an error text: only routes, numbers (monitored folder, recovery point, job), employee IDs and addresses.
- **On by default when Redis is configured.** `TIMEVAULT_WEB_LOG` is `auto` (on exactly when `TIMEVAULT_REDIS_URL` is set), `on` (it needs `TIMEVAULT_REDIS_URL`, otherwise the service refuses to start) or `off`. The Settings page and `GET /api/v1/settings` show it.
- **It is best effort and never gets in the way.** `Write` only puts the line into a queue of 1,024 and returns. A worker sends up to 100 lines with one `RPUSH`, through a pool of its own with 2 connections and the timeout `TIMEVAULT_REDIS_TIMEOUT`. A token bucket cuts a flood at the door (page lines: 10 per second on average, a burst of 100; event lines: 20 per second, a burst of 200), a full queue drops the line, and after a failed `RPUSH` the worker leaves Redis alone for 30 seconds, then longer, up to 5 minutes, and drops what comes meanwhile. It says so with one warning (the password of the URL masked), again at most every 5 minutes, and with an info line when Redis takes lines again; the lines that the limits or a full queue drop are counted and reported about once a minute. A dropped line is not written later. At shutdown the queue is sent first, for at most 3 seconds. When Redis is down the service starts and works as before.
- **The key stays out of the namespaces of the accounts and of the sign-on results.** `TIMEVAULT_WEB_LOG_KEY` may only have ASCII letters, digits and `_ . : -` (at most 128 characters), and it must not start, in any case, like `TIMEVAULT_REDIS_KEY_PREFIX` (`app_user_` by default) or `sso_login_`: the web log appends to a list, and a key there would be a way to write into those. The service refuses to start with such a key.
- **`internal/redisuser` stays read-only.** `redisuser.Connect` hands a client with the same settings to the writer (`internal/weblog`); the `Store` that reads the accounts and the sign-on results does not use it.

## Alternatives considered

- **A list of its own** (for example `tv_web_log`). It would be out of the way of the other applications, but it is not the list that the company's log page reads, so TimeVault would still be missing from the log that people look at, and it would be one more list that grows without a limit in the same Redis. It stays possible with `TIMEVAULT_WEB_LOG_KEY`.
- **Writing through another application.** Asking an application that already writes to the list to write the lines for TimeVault would make TimeVault a client of that application and its web log dependent on that application being up, to avoid one append to a list in a Redis that TimeVault already uses.
- **A log file.** The console log already exists, and a file is not what the company's log page reads; it would also need rotation and a place that people can read it.
- **Every request.** The static files, the health check, the polling of a job (about every 1.5 seconds while a job runs) and the reloads of a job page would fill the shared list with lines that tell nobody anything. They stay out; the API calls and the automatic reloads are written at the debug level only.
- **Trimming the shared list** (`LTRIM`, an expiry on the key). The list belongs to all the applications, which manage it; a trim by TimeVault could delete their lines, and TimeVault would need rights that appending does not need. TimeVault never trims.
- **Off by default.** The safer default for a Redis that is shared, but a setting that has to be found and turned on is one that stays off: TimeVault would be missing from the company's log without anybody noticing. The volume is bounded by the limits above and one line in the `.env` turns it off, so it is on when Redis is configured.

## Consequences

- **TimeVault now writes to Redis.** The Redis account in `TIMEVAULT_REDIS_URL` has to be allowed to `RPUSH` to the key. When it is not, the service logs the warning `web log: the lines cannot be written to Redis` and drops the lines; nothing else stops. It still never deletes a key.
- **The risks are bounded, not zero.** `web_log` has no expiry and the Redis has no `maxmemory` limit and `noeviction`, so the list only grows, and nothing makes room for the accounts. The limits bound it: at most 10 page lines and 20 event lines per second, about 100 to 150 bytes each in Redis, that is about 4.5 KB per second, or about 390 MB a day for a flood that never stops (worked out from the limits, not measured; the real list holds about 9 MB in all). Anybody who can reach TimeVault can cause page lines, up to their limit. TimeVault never trims or expires the list; cutting it is a decision of whoever manages that Redis.
- **Personal data.** A line holds an employee ID and the address of the browser, and everybody who reads that list reads them. Nothing that a visitor typed, no path and no error text is written.
- **A line can be lost.** The web log is best effort: a full queue, the limits and a Redis that does not answer drop lines, and a dropped line is not written later. An audit event reaches the web log only after it is stored, and an event that could not be stored is not in it. The web log is not a substitute for the audit trail in PostgreSQL.
- **Upgrading starts it.** A service of an earlier version that has `TIMEVAULT_REDIS_URL` writes the web log as soon as it runs 1.0.0, also when it updated itself, unless its `.env` says `TIMEVAULT_WEB_LOG=off`. Older versions ignore a setting they do not know, so that line can be added to the `.env` before the release is published, at the servers where the web log is not wanted.
- **The key is checked at start,** so a key inside the namespace of the accounts or of the sign-on results is a configuration error and not a way to write there. A `TIMEVAULT_REDIS_KEY_PREFIX` that the default key starts with needs another `TIMEVAULT_WEB_LOG_KEY`, or `off`.
- **The page lines name routes only,** so a reader of the log sees which pages were opened and by whom, not what was looked for.
- **Tests.** The writer, the limits, the back-off, the shutdown and the lines (also that no path, typed name or error text reaches a line) are tested against an in-process Redis; the encoding is compared with pickles that Python printed (golden vectors in `internal/weblog/encode_test.go`); the settings, the mirror of the audit trail and the request log have their own tests. The web log was not written to the production Redis, and that the log page of the other application shows the lines of TimeVault was not checked (see [mvp-status.md](../mvp-status.md)).

---

# 中文

## 状态

于 2026-11-18 在 1.0.0 中被接受。它加入了 TimeVault 对 Redis 的第一次写入，因此修订了两处表述：[0005](./0005-redis-user-directory-and-open-browsing.md) 中的"TimeVault 只读取 Redis"，以及 [0009](./0009-single-sign-on-and-trusted-proxies.md) 中的"TimeVault 的 Redis 账号有意设为只读"。账号和单点登录结果仍然只读取，Redis 账号仍然不能删除键；变化在于，它现在必须被允许向一个列表追加内容。路由、账号、权限和数据都不变。

## 背景

- 公司的各个应用共用一份 Web 日志：Redis 列表 `web_log`。每个应用把自己做的事追加成行，由一个日志页面读取这个列表。TimeVault 没有向它写入任何东西，所以人们在 TimeVault 中打开的页面和在那里做的事，都不在公司查看的日志里。
- TimeVault 本来就在访问这个 Redis，用于用户目录（[0005](./0005-redis-user-directory-and-open-browsing.md)）和单点登录结果（[0009](./0009-single-sign-on-and-trusted-proxies.md)），而 `internal/redisuser` 只读取。审计记录在 PostgreSQL 中，服务有自己的控制台日志；两者都不是共用的那一份。
- 真实的列表只查看过一次，并且是只读的（没有向它写入任何东西）：约 75,000 行，键没有过期时间，约 9 MB，Redis 5.0，`maxmemory` 为 0。每个元素都是对一行文本做 `zlib.compress(pickle.dumps(line, protocol=4))` 的结果，这行文本有六个字段，用 ` | ` 分隔：`ip | time | message | status | level | host`，例如 `127.0.0.1 | 2026-11-18 13:58:06.446524 | home_page: access | 200 | info | sgmeslt595`。
- 按位置拆分一行的读取方必须继续可用，所以 TimeVault 不能为账号再加一个字段，只能把它写进 message。
- 这个列表是共用的，永不过期，由其他应用管理。TimeVault 写进去的内容会一直留在那里，而任何能访问 TimeVault 的人都能让页面被打开。该 Redis 没有 `maxmemory` 限制，并且是 `noeviction`，它同时保存着每个公司应用用来登录的账号。
- TimeVault 用账号的工号来识别一个人，所以关于某个人的一行里有工号和地址：这是个人数据，写在一个别人也会读取的列表里。

## 决定

- **TimeVault 向共用的列表追加内容。** 列表默认是 `web_log`（`TIMEVAULT_WEB_LOG_KEY`），位于 `TIMEVAULT_REDIS_URL` 所指的 Redis 中，使用同一个数据库。TimeVault 只追加（`RPUSH`）；它从不把列表读回来，也从不裁剪、过期或删除其中的任何内容。
- **行与其他应用写的完全一样。** 元素是 `zlib.compress(pickle.dumps(line, protocol=4))`，逐字节地按 Python 写 `str` 的方式写出（`internal/weblog/encode.go`；测试把它与 Python 打印出的 pickle 比较），这一行有六个字段 `ip | time | message | status | level | host`，例如 `10.0.0.5 | 2026-11-18 14:05:12.250431 | TimeVault: /explorer user=E123456 0.112(s) | 200 | info | sgmeslt595`。不增加任何字段。账号写在 message 中，形式为 `user=<工号>`（没有登录的访问者是 `guest`），每条 message 都以 `TimeVault: ` 开头。`ip` 是浏览器的地址（`Server.clientIP`；服务自己做的事用 `127.0.0.1`），时间是服务器的本地时间，`status` 是 HTTP 状态码（审计记录和调度器报告的内容则是 200、403 或 500），`level` 是 `info`、`warning`、`error` 或 `debug`，`host` 是 `TIMEVAULT_WEB_LOG_HOST`，默认是小写的计算机名。message 中的 `|` 会变成 `/`，控制字符会变成空格，message 最长 400 个字符，超出的部分被截断，所以一行始终有六个字段。
- **这些行从哪里来。**
  - *有人打开的页面*（页面的每一次 `GET`；不包括静态文件、`/healthz`、API 调用、重定向，以及页面脚本自己发起的重新加载）：`TimeVault: <route> user=<工号> <seconds>(s)`。route 是匹配到的模式，其中的数字已填入（`/jobs/12`），绝不是输入时的地址。`2xx` 的级别是 `info`，`4xx` 的级别是 `warning`。
  - *在服务器上失败的请求*（任何 `5xx`，包括 API 的，以及 panic）：`TimeVault: error <route> user=<工号> ref=<参考编号>`，级别 `error`。参考编号就是错误页面显示的那个；控制台日志把它记作 `id=`，并连同错误文本一起保存，而错误文本绝不写入 Redis。
  - *每一个审计事件*，在它被存入数据库之后（`audit.Logger.SetMirror`）：`TimeVault: <action> …`，带有监控文件夹（`source=<id>`）、另一个对象（`point=17`）、操作者，以及事件有耗时时的耗时。`ok` 是 `info` 和 200，`denied` 是 `warning` 和 403，`failed` 是 `error` 和 500。详情中只使用被拒绝请求的方法和路由，以及程序更新的阶段、`from` 和 `to`。登录失败时输入的名称（它可能是密码）绝不复制（`user=-`）。地址是该请求的地址；调度器和更新器用 `127.0.0.1`。
  - *调度器：* `scheduler.queued`、`scheduler.error`（状态码为 500 的警告，每个文件夹每分钟最多一次，与控制台中的警告一样），以及只在调试级别才写的 `scheduler.skipped`。
  - *服务本身：* 一切构建完成后写 `TimeVault: started version=<版本>`，停止时写 `TimeVault: stopped`。
  - *在调试级别*（`TIMEVAULT_LOG_LEVEL=debug`）下，页面行还会为 `GET /api/…`（任务的轮询）以及页面脚本自己发起的重新加载（请求头 `X-TV-Auto: 1`）而写出。其他日志级别不会改变 Web 日志的任何内容。
- **访问者输入的任何内容都不会写入。** 绝不写路径、文件或文件夹的名称、查询串、搜索文本或错误文本：只有路由、数字（监控文件夹、恢复点、任务）、工号和地址。
- **配置了 Redis 时默认开启。** `TIMEVAULT_WEB_LOG` 可以是 `auto`（恰好在设置了 `TIMEVAULT_REDIS_URL` 时开启）、`on`（需要 `TIMEVAULT_REDIS_URL`，否则服务拒绝启动）或 `off`。设置页和 `GET /api/v1/settings` 会显示它。
- **它是尽力而为的，并且从不妨碍服务。** `Write` 只把行放进一个容量为 1,024 的队列就返回。一个 worker 用一次 `RPUSH` 发送最多 100 行，使用它自己的、含 2 个连接的连接池，超时为 `TIMEVAULT_REDIS_TIMEOUT`。令牌桶在入口处截断大量涌入（页面行：平均每秒 10 行，突发 100 行；事件行：每秒 20 行，突发 200 行），队列满了就丢弃这一行，`RPUSH` 失败之后，worker 会先暂停访问 Redis 30 秒，然后更久，最长 5 分钟，期间到来的行都被丢弃。它会用一条警告说明这一点（URL 中的密码被遮盖），最多每 5 分钟再说一次，Redis 重新接收时会有一条 info 行；被限制或队列满而丢弃的行会被计数，大约每分钟报告一次。被丢弃的行以后不会再写。关闭时先发送队列，最多等待 3 秒。Redis 不可用时，服务照常启动和运行。
- **键保持在账号和单点登录结果的命名空间之外。** `TIMEVAULT_WEB_LOG_KEY` 只能包含 ASCII 字母、数字和 `_ . : -`（最多 128 个字符），并且无论大小写，都不能以 `TIMEVAULT_REDIS_KEY_PREFIX`（默认 `app_user_`）或 `sso_login_` 开头：Web 日志是向列表追加内容，而那里的一个键就会成为向它们写入的途径。服务遇到这样的键会拒绝启动。
- **`internal/redisuser` 仍然只读。** `redisuser.Connect` 把一个设置相同的客户端交给写入方（`internal/weblog`）；读取账号和单点登录结果的 `Store` 不使用它。

## 考虑过的备选方案

- **使用自己的列表**（例如 `tv_web_log`）。这样不会干扰其他应用，但它不是公司日志页面读取的那个列表，所以 TimeVault 仍然不在人们查看的日志里，而且它是同一个 Redis 里又一个不受限制地增长的列表。仍然可以通过 `TIMEVAULT_WEB_LOG_KEY` 这样做。
- **通过另一个应用来写。** 请一个已经向该列表写入的应用替 TimeVault 写这些行，会让 TimeVault 成为那个应用的客户端，并让它的 Web 日志依赖于那个应用在运行，而目的只是避免向 TimeVault 本来就在使用的 Redis 中的一个列表追加一次内容。
- **日志文件。** 控制台日志已经有了，而文件不是公司日志页面读取的东西；它还需要轮转，以及一个人们可以读取它的地方。
- **每一个请求。** 静态文件、健康检查、任务的轮询（任务运行期间约每 1.5 秒一次）和任务页的重新加载，会让共用的列表里充满什么也说明不了的行。它们不写入；API 调用和自动重新加载只在调试级别才写。
- **裁剪共用的列表**（`LTRIM`、给键设置过期时间）。这个列表属于所有应用，由它们管理；TimeVault 去裁剪可能删掉它们的行，而且 TimeVault 需要追加所不需要的权限。TimeVault 从不裁剪。
- **默认关闭。** 对共用的 Redis 来说这是更安全的默认值，但需要自己去找到并打开的设置，往往就一直关着：TimeVault 会在没有人注意的情况下缺席公司的日志。上面的限制已经约束了写入量，而 `.env` 中的一行就能关闭它，所以配置了 Redis 时它就是开启的。

## 影响

- **TimeVault 现在会写 Redis。** `TIMEVAULT_REDIS_URL` 中的 Redis 账号必须被允许对该键执行 `RPUSH`。不被允许时，服务会记录警告 `web log: the lines cannot be written to Redis` 并丢弃这些行；其他一切照常。它仍然从不删除键。
- **风险是有界的，但不为零。** `web_log` 没有过期时间，Redis 没有 `maxmemory` 限制并且是 `noeviction`，所以列表只会增长，也没有任何东西会为账号腾出空间。上面的限制约束了它：每秒最多 10 行页面行和 20 行事件行，每行在 Redis 中约 100 到 150 字节，也就是约每秒 4.5 KB，一次永不停止的涌入每天约 390 MB（由限制推算出来，不是实测的；真实的列表一共只有约 9 MB）。任何能访问 TimeVault 的人都能制造页面行，直到它们的限制。TimeVault 从不裁剪或让列表过期；要裁剪它，由管理那个 Redis 的人决定。
- **个人数据。** 一行里有工号和浏览器的地址，所有读取这个列表的人都能读到它们。访问者输入的内容、路径和错误文本都不会写入。
- **一行可能丢失。** Web 日志是尽力而为的：队列已满、限制，以及不应答的 Redis 都会丢弃行，被丢弃的行以后不会再写。审计事件只有在被存储之后才会进入 Web 日志，没能存储的事件不在其中。Web 日志不能替代 PostgreSQL 中的审计记录。
- **升级就会开启它。** 较早版本的服务只要有 `TIMEVAULT_REDIS_URL`，一运行 1.0.0 就会写 Web 日志，自己更新的服务也一样，除非它的 `.env` 写了 `TIMEVAULT_WEB_LOG=off`。较旧的版本会忽略它不认识的设置，所以在不需要 Web 日志的服务器上，可以在发布之前先把这一行加进 `.env`。
- **键在启动时检查，** 所以位于账号或单点登录结果命名空间之内的键是配置错误，而不是向那里写入的途径。默认键以其开头的 `TIMEVAULT_REDIS_KEY_PREFIX` 需要另一个 `TIMEVAULT_WEB_LOG_KEY`，或者 `off`。
- **页面行只写路由，** 所以日志的读者能看到打开了哪些页面、是谁打开的，却看不到他们找了什么。
- **测试。** 写入方、限制、退避、关闭和这些行（也包括路径、输入的名称或错误文本不会进入任何一行）是针对进程内的 Redis 测试的；编码与 Python 打印出的 pickle 做了比较（`internal/weblog/encode_test.go` 中的标准向量）；设置、审计记录的镜像和请求日志各有自己的测试。Web 日志没有写入过生产环境的 Redis，另一个应用的日志页面是否会显示 TimeVault 的行，也没有检查过（见 [mvp-status.md](../mvp-status.md)）。
