# English

## Status

Accepted for 0.1.4 (2026-11-16). Extends the sign-in of [0005](./0005-redis-user-directory-and-open-browsing.md); the accounts, the roles and open browsing are unchanged.

## Context

- Up to 0.1.3 a person signed in with an account (employee ID, name or e-mail) and the password stored in the Redis user directory.
- The company already has a single sign-on service that the dash application uses. A browser opens `http://<sign-on host>/sso/api/login?token=<token>`; the service finds out who is at the computer (the Windows account) and stores the result in Redis under `sso_login_<token>` for 5 minutes (`zlib.compress(pickle.dumps(dict))` with `status`, `ee_code`, `windows_user`, `ip` and `ts`). The application then reads that key and deletes it.
- The address of the sign-on service can change, so it must be a setting.
- The Redis account of TimeVault is read-only on purpose; it must not delete keys.
- People reach TimeVault through a reverse proxy, so the connection that TimeVault sees comes from the proxy and not from the browser.

## Decision

- **The login page offers a second button, "Sign in with Windows (SSO)",** when `TIMEVAULT_SSO_HOST` is set (host, `host:port` or `http(s)://host[:port]`; the scheme defaults to `http`). Without that setting the page is as before.
- **The flow.** The click opens a window and calls `POST /auth/sso/start`. The server issues a token of 24 random characters (`a-z`, `2-7`), remembers it in memory for 5 minutes together with the address of the client and the page to go to afterwards, and returns the address `<sign-on host>/sso/api/login?token=<token>`. The window opens that address. The page asks `POST /auth/sso/poll` every 2 seconds. The server reads `sso_login_<token>` (a `GET`, never a delete), and when the sign-on service has written a result with `status` `ok` and an employee ID, it looks the account up in the Redis directory (the newest record of that employee ID, which must be activated) and starts the normal session, with the role of that account. The flow is then removed, so one result can be used once.
- **Outcomes** of a poll are `pending`, `ok` (with the page to go to), `expired`, `mismatch`, `no_user` (the browser has no Windows user), `denied` (no active account) and `unavailable` (Redis does not answer). Each has a message on the login page.
- **Only tokens that this server issued are accepted.** Nobody can make TimeVault read an arbitrary `sso_login_*` key, and a result cannot be used twice. At most 5 sign-ins wait per client address and 2000 in all; a flow can be looked at once a second. The page a flow returns to is kept in memory and is at most 2,048 bytes long (a longer one becomes `/`), so the memory a visitor can make the server hold is bounded. `start` and `poll` refuse requests that do not come from the site itself.
- **Binding to the address of the browser.** A sign-in could be forced on a victim: the attacker starts a flow, gets the victim to open the sign-on link with the attacker's token, and then polls for the result that now holds the victim's identity. With `TIMEVAULT_SSO_REQUIRE_SAME_IP=true` (the default) the `ip` that the sign-on service recorded must be the address of the client that started the flow, otherwise the outcome is `mismatch`. A recorded address with a port (`ip:port`) is understood.
- **Trusted proxies.** `TIMEVAULT_TRUSTED_PROXIES` lists the reverse proxies (addresses or CIDR ranges) whose `X-Forwarded-For` header is believed. For a request from such a proxy the client is the right-most address of that header that is not itself a trusted proxy; for every other request the header is ignored. The address is used for the check above, for the **From** column of the audit log and for the sign-in throttle. The proxy has to keep the `Host` header, because the check that a request comes from the site itself compares `Origin` with `Host`.
- **Audit.** `auth.login` records `method` (`sso` or `password`) and `windows_user`. A refused sign-in is `auth.login_failed` with a reason: `address_mismatch`, `browser_address_changed`, `no_windows_user`, `no_active_account` or `result_unreadable`.
- **Reading the result** is done by a strict decoder of the pickle format (no code is run), with the same limits as the account records. A result that is not a dict, has a field of the wrong type or has no `status` is refused.

## Alternatives considered

- **Read the key and delete it, as dash does.** Needs write access to Redis for TimeVault, which was refused on purpose.
- **A token chosen by the sign-on service or by the browser.** The server could not tell a token that it issued from one that an attacker picked.
- **Let the proxy do the Windows authentication and send the user in a header.** A different deployment and a header that must never be trusted from anywhere else; the sign-on service already exists and is what people know.
- **Turn the address check off.** Allowed with `TIMEVAULT_SSO_REQUIRE_SAME_IP=false` when the proxy cannot pass the real address, but it re-opens the attack above.

## Consequences

- Behind a reverse proxy, either `TIMEVAULT_TRUSTED_PROXIES` is set, or `TIMEVAULT_SSO_REQUIRE_SAME_IP=false` is set. With neither, TimeVault compares the address of the proxy with the address that the sign-on service saw, and every sign-in is a `mismatch`.
- With the check off, the attack above is possible for as long as a flow lives (5 minutes) and needs a victim who opens a link and a service that records the victim's account. The token cannot be guessed (120 bits).
- The browser has to be allowed to open the window; when it is blocked, the page says so.
- TimeVault needs Redis (already needed for the accounts) and the browser needs to reach the sign-on service. TimeVault does not call the sign-on service itself.
- A result stays in Redis until its 5 minutes are over; only the flow, which is in memory, is removed.

---

# 中文

## 状态

于 2026-11-16 在 0.1.4 中被接受。扩展 [0005](./0005-redis-user-directory-and-open-browsing.md) 的登录；账号、角色和开放浏览保持不变。

## 背景

- 直到 0.1.3，人们用账号（工号、姓名或电子邮件）和保存在 Redis 用户目录中的密码登录。
- 公司已经有一个 dash 应用在使用的单点登录服务。浏览器打开 `http://<单点登录主机>/sso/api/login?token=<token>`；服务确认电脑前是谁（Windows 账号），并把结果以 `sso_login_<token>` 为键存入 Redis，保存 5 分钟（`zlib.compress(pickle.dumps(dict))`，包含 `status`、`ee_code`、`windows_user`、`ip` 和 `ts`）。应用随后读取该键并删除它。
- 单点登录服务的地址可能变更，所以必须是一个设置项。
- TimeVault 的 Redis 账号有意设为只读；它不能删除键。
- 人们通过反向代理访问 TimeVault，所以 TimeVault 看到的连接来自代理，而不是浏览器。

## 决定

- **当设置了 `TIMEVAULT_SSO_HOST`（主机、`host:port` 或 `http(s)://host[:port]`，协议默认为 `http`）时，登录页提供第二个按钮"使用 Windows 账号登录（SSO）"。** 没有这个设置时，页面与以前一样。
- **流程。** 点击后打开一个窗口并调用 `POST /auth/sso/start`。服务器签发一个由 24 个随机字符（`a-z`、`2-7`）组成的令牌，连同客户端地址和登录后要去的页面一起在内存中保存 5 分钟，并返回地址 `<单点登录主机>/sso/api/login?token=<token>`。窗口打开该地址。页面每 2 秒请求一次 `POST /auth/sso/poll`。服务器读取 `sso_login_<token>`（只用 `GET`，从不删除），当单点登录服务写入了 `status` 为 `ok` 且带有工号的结果时，到 Redis 目录中查找账号（该工号最新的记录，且必须已激活），并按该账号的角色开始正常的会话。随后该流程被移除，所以一个结果只能使用一次。
- **轮询的结果**有 `pending`、`ok`（附带要去的页面）、`expired`、`mismatch`、`no_user`（浏览器没有 Windows 用户）、`denied`（没有有效账号）和 `unavailable`（Redis 没有响应）。登录页对每一种都有提示。
- **只接受由本服务器签发的令牌。** 没有人能让 TimeVault 去读任意的 `sso_login_*` 键，结果也不能用两次。每个客户端地址最多同时等待 5 个登录，总共最多 2000 个；一个流程每秒最多查看一次。流程登录后要返回的页面地址保存在内存中，最长 2,048 字节（更长的会变成 `/`），所以访问者能让服务器占用的内存是有界的。`start` 和 `poll` 会拒绝不是来自站点本身的请求。
- **与浏览器地址绑定。** 登录可能被强加给受害者：攻击者启动一个流程，让受害者打开带有攻击者令牌的单点登录链接，然后轮询已经保存了受害者身份的结果。当 `TIMEVAULT_SSO_REQUIRE_SAME_IP=true`（默认）时，单点登录服务记录的 `ip` 必须是启动该流程的客户端的地址，否则结果为 `mismatch`。带端口的记录地址（`ip:port`）也能识别。
- **受信任的代理。** `TIMEVAULT_TRUSTED_PROXIES` 列出其 `X-Forwarded-For` 头会被信任的反向代理（地址或 CIDR 范围）。对来自这类代理的请求，客户端是该头中最右边的、本身不是受信任代理的地址；对其他所有请求，该头被忽略。这个地址用于上述检查、审计日志的**来源**列和登录限流。代理必须保留 `Host` 头，因为"请求来自站点本身"的检查会比较 `Origin` 和 `Host`。
- **审计。** `auth.login` 记录 `method`（`sso` 或 `password`）和 `windows_user`。被拒绝的登录是带原因的 `auth.login_failed`：`address_mismatch`、`browser_address_changed`、`no_windows_user`、`no_active_account` 或 `result_unreadable`。
- **读取结果**使用严格的 pickle 格式解码器（不会运行任何代码），限制与账号记录相同。结果不是字典、某个字段类型不对或没有 `status` 时会被拒绝。

## 考虑过的备选方案

- **像 dash 一样读取键后删除它。** 需要 TimeVault 对 Redis 有写权限，这被有意拒绝了。
- **由单点登录服务或浏览器选择令牌。** 服务器无法区分是自己签发的令牌还是攻击者选的令牌。
- **让代理做 Windows 认证并通过头传递用户。** 这是另一种部署方式，而且这个头绝不能被其他来源信任；单点登录服务已经存在，也是人们熟悉的方式。
- **关闭地址检查。** 当代理无法传递真实地址时，可以用 `TIMEVAULT_SSO_REQUIRE_SAME_IP=false` 关闭，但这会重新打开上面的攻击。

## 影响

- 在反向代理之后，要么设置 `TIMEVAULT_TRUSTED_PROXIES`，要么设置 `TIMEVAULT_SSO_REQUIRE_SAME_IP=false`。两者都不设置时，TimeVault 会拿代理的地址与单点登录服务看到的地址比较，每次登录都是 `mismatch`。
- 关闭检查后，上述攻击在一个流程存在的时间内（5 分钟）是可能的，需要受害者打开链接，并且服务记录了受害者的账号。令牌无法被猜出（120 位）。
- 浏览器必须被允许打开窗口；被拦截时页面会提示。
- TimeVault 需要 Redis（账号本来就需要），浏览器需要能访问单点登录服务。TimeVault 自己不调用单点登录服务。
- 结果会留在 Redis 中直到 5 分钟结束；只有保存在内存中的流程会被移除。
