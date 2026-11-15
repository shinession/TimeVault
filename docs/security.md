# English

## Overview

This document lists the security controls that exist in the MVP and the gaps that remain. It describes the implementation, not an intention; where plan.md asks for more, the gap is listed.

## Controls that exist

| Area | Control |
|---|---|
| Network exposure | The service listens on `127.0.0.1` by default. A non-loopback address is refused unless `TIMEVAULT_ALLOW_NON_LOOPBACK=true`, and sign-in can never be disabled on a non-loopback address. TLS is not terminated by the service: put a reverse proxy with HTTPS in front and set `TIMEVAULT_COOKIE_SECURE=true`. |
| Sign-in | One administrator whose password hash (`pbkdf2-sha256`, 600,000 iterations, random salt) is read from `TIMEVAULT_ADMIN_PASSWORD_HASH`. Passwords and hashes are compared in constant time and a hash is always computed, so timing does not reveal whether the user exists. |
| Brute force | At most 5 failed sign-ins per client address in 10 minutes (then `429` with `Retry-After`), even when the right password is then supplied. Failed and throttled attempts are audited. The throttle is in memory and keyed by the connecting address. |
| Sessions | Stateless, HMAC-signed cookie: `HttpOnly`, `SameSite=Lax`, `Secure` when configured, expiry from `TIMEVAULT_SESSION_TTL`. The signing key comes from `TIMEVAULT_SESSION_KEY` or is random per start (sessions then end on restart). |
| CSRF | Every state-changing request needs the session's token (header or form field) and is rejected when `Origin` or `Sec-Fetch-Site` shows another site. The sign-in form checks the origin as well. Open redirects through `next` are blocked (only local paths are accepted). |
| Browser hardening | `Content-Security-Policy: default-src 'self'` with no inline script or style (a test scans every page), `frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Cache-Control: no-store` for pages. Output is escaped by `html/template`; a test shows names such as `{{7+7}}.txt` and `a&b'c.txt` are displayed literally. |
| Input validation | Client paths go through `pathutil` (no `..`, drive letters, reserved device names, invalid characters, excessive depth). Request bodies are limited to 1 MiB, JSON is strict (unknown fields rejected). All SQL uses bind parameters. |
| Reading sources | Folders are read through `os.Root`, so a crafted path or a link swapped in during a scan cannot lead outside the source. Junctions and symbolic links are never followed, and a file or folder that is replaced by a link after it was listed is detected and skipped. A source cannot overlap the repository, the restore folder or another source: the folders are compared by their real location (junctions and links resolved), when a source is added and again when each backup starts, and the scan skips those folders as a second guard. Sources are only read. |
| Restore | Writes only below `TIMEVAULT_RESTORE_ROOT`, into a folder whose name is validated as a single path component (created when it does not exist; when it already exists, for example for a repeated restore, the conflict policy decides what happens to files that are already there). The destination must be a plain folder (an existing junction or symbolic link is refused) and the restore root is compared with the repository and every source by real location. Preview first; confirmation token bound to user, exact plan and a 15-minute expiry; replacing files needs an explicit extra confirmation; all writes go through `os.Root`; unsafe catalog names (`..`, `:`, `\`, reserved names) are blocked; each file is written to a temporary file, checked against its SHA-256 and size, and only then renamed. The audit record is written before the job is queued, and a restore that cannot be audited does not run. Nothing in a restore deletes source or repository data. |
| Service instance | Only one service can run against a database: a PostgreSQL advisory lock is taken before start-up recovery, so a second launch cannot mark the first one's running jobs as failed or abort its recovery points. |
| Everything | Only loopback hosts are accepted for the engine URL, enforced when the connection is made; redirects are not followed; responses are size-limited; optional HTTP Basic credentials come from the environment and are never logged. The user's text is split into words that are all quoted so Everything operators (`|`, `!`, `<>`, `regex:`) are literal, the query is confined to the source root, and every result is checked against the root again. |
| Audit | `audit_events` is append-only (a database trigger rejects UPDATE and DELETE). Sign-ins, denials, source creation, backup/verify requests and results, restore previews, requests and results and job cancellations are recorded with the user and remote address. |
| Secrets | Read only from environment variables, redacted when the configuration is logged, never stored in the repository. Development secrets are generated under the git-ignored `data/` folder. |

## Gaps and limits (do not rely on the MVP for these)

- **No per-user or per-path authorization.** There is one administrator role; anyone who can sign in can browse and restore every source. NTFS ACLs and Active Directory groups are not consulted and Windows SSO is not implemented.
- **Sessions cannot be revoked individually.** They are stateless; changing `TIMEVAULT_SESSION_KEY` ends all of them. Logging out only clears the browser's cookie.
- **No TLS in the service** and no rate limit beyond the sign-in throttle.
- **The repository is not encrypted or tamper-proof.** Objects get the read-only attribute and are verified by hash, but anyone with file-system access to the repository can alter or delete them. There is no retention policy, garbage collection or off-host copy.
- **Power-loss durability is best effort.** File contents are flushed before an object is published and the folders that received objects are flushed before a recovery point is committed, but this has not been tested against a real power failure. Run a deep integrity check after an unclean shutdown.
- **Missing repository content is found late.** A normal backup does not re-check the repository objects of files it carries over unchanged. An integrity check (Verify) finds missing or damaged objects, a restore refuses to use them, and a **Full re-read** backup writes missing content again.
- **Live-scan consistency.** Files are read from the live disk (no VSS). Torn reads are detected and retried, locked files are skipped and reported, but a set of related files may come from slightly different moments.
- **Metadata fidelity.** NTFS ACLs, alternate data streams, ownership, EFS and hard-link relationships are not preserved; restored files take the destination's permissions. Modification time and the common attribute bits are restored.
- **Privileges.** The service runs as the account that starts it. Run it with the least rights that can read the sources and write the repository and restore folders. A separate privileged worker is not implemented.
- **Everything instance.** The adapter cannot see how Everything is bound. Keep its "Bind to interfaces" option at `127.0.0.1`, disable "Allow file download", and set a username and password.
- **Source path check.** The check that refuses network shares only looks for a leading `\\` on the text as typed. `//host/share` and `\\?\UNC\host\share` are not recognized, so the service would contact that SMB server (and send Windows credentials) when such a source is added. Only the single administrator can add sources; use local drive paths.
- **Restore plan versus execution.** The confirmation covers the plan that was previewed, but the restore job plans again when it runs and does not compare the result with the approved plan. A file that appears in the destination while the job is queued can be replaced under the `overwrite` policy without a new confirmation, and `create` and `rename` items are published with an unconditional rename, so two restores into the same folder at the same time can replace each other's files. Do not run restores into the same folder concurrently.
- **Restore folder is trusted storage.** The restore root is resolved by path at every step, so replacing it between validation and use is not detected; a staged `.tvpart-*` file is verified by handle but published by path; and the attributes of an existing target are cleared before it is replaced, so a hard link planted there to another file would change that file's attributes. Whoever can write inside the restore folder can change restored files anyway, so keep its permissions tight.
- **Development mode.** With `TIMEVAULT_AUTH_DISABLED=true` there is no Host header check, so a malicious web page using DNS rebinding could in principle reach the service from the browser of the person who runs it (not tested). Do not use that mode on a computer that browses untrusted sites; use sign-in instead.

---

# 中文

## 概述

本文档列出 MVP 中已有的安全控制和仍然存在的缺口。它描述的是实际实现，而不是意图；plan.md 要求更多的地方，都列在缺口里。

## 已有的控制

| 方面 | 控制措施 |
|---|---|
| 网络暴露 | 服务默认监听 `127.0.0.1`。除非设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，否则会拒绝非回环地址；并且在非回环地址上永远不允许关闭登录。服务本身不终止 TLS：请在前面放置支持 HTTPS 的反向代理，并设置 `TIMEVAULT_COOKIE_SECURE=true`。 |
| 登录 | 只有一个管理员，其密码哈希（`pbkdf2-sha256`，600,000 次迭代，随机盐）从 `TIMEVAULT_ADMIN_PASSWORD_HASH` 读取。口令和哈希以恒定时间比较，且始终会计算哈希，因此耗时不会暴露用户是否存在。 |
| 暴力破解 | 每个客户端地址 10 分钟内最多 5 次登录失败（之后返回 `429` 和 `Retry-After`），即使随后提供了正确的密码也一样。失败和被限流的尝试都会写入审计。限流保存在内存中，以连接地址为键。 |
| 会话 | 无状态、HMAC 签名的 Cookie：`HttpOnly`、`SameSite=Lax`，配置后带 `Secure`，有效期来自 `TIMEVAULT_SESSION_TTL`。签名密钥来自 `TIMEVAULT_SESSION_KEY`，否则每次启动随机生成（此时重启后会话失效）。 |
| CSRF | 每个会改变状态的请求都需要会话令牌（请求头或表单字段），并且当 `Origin` 或 `Sec-Fetch-Site` 显示来自其他站点时会被拒绝。登录表单同样检查来源。通过 `next` 的开放重定向被阻止（只接受本站路径）。 |
| 浏览器加固 | `Content-Security-Policy: default-src 'self'`，没有内联脚本或样式（有测试扫描每个页面），`frame-ancestors 'none'`，`X-Content-Type-Options: nosniff`，`Referrer-Policy: same-origin`，页面使用 `Cache-Control: no-store`。输出由 `html/template` 转义；测试表明 `{{7+7}}.txt` 和 `a&b'c.txt` 这样的名称会被原样显示。 |
| 输入校验 | 客户端路径经过 `pathutil`（不允许 `..`、盘符、保留设备名、非法字符、过深层级）。请求体限制为 1 MiB，JSON 为严格模式（拒绝未知字段）。所有 SQL 都使用绑定参数。 |
| 读取备份源 | 通过 `os.Root` 读取文件夹，所以精心构造的路径或扫描期间被替换进来的链接都无法指向备份源之外。联接点和符号链接永不被跟随；如果文件或文件夹在被列出之后被替换成链接，会被检测到并跳过。备份源不能与仓库、恢复目录或其他备份源重叠：比较的是文件夹的真实位置（解析了联接点和链接之后），添加备份源时以及每次备份开始时都会检查，扫描时也会跳过这些文件夹作为第二道保护。备份源只会被读取。 |
| 恢复 | 只写入 `TIMEVAULT_RESTORE_ROOT` 之下的文件夹，其名称按单个路径组件校验（不存在时创建；已存在时，例如重复恢复，由冲突策略决定如何处理已有的文件）。目标位置必须是普通文件夹（已存在的联接点或符号链接会被拒绝），恢复根目录与仓库以及每个备份源比较的是真实位置。先预览；确认令牌与用户、精确的计划以及 15 分钟有效期绑定；替换文件需要额外的明确确认；所有写入都通过 `os.Root`；目录库中不安全的名称（`..`、`:`、`\`、保留名）会被阻止；每个文件先写入临时文件，核对 SHA-256 和大小，再重命名。审计记录在任务入队之前写入，无法审计的恢复不会运行。恢复不会删除任何源数据或仓库数据。 |
| 服务实例 | 同一个数据库只能运行一个服务：在启动恢复之前会获取 PostgreSQL advisory 锁，所以第二次启动不能把第一个服务正在运行的任务标记为失败，也不能中止它的恢复点。 |
| Everything | 引擎 URL 只接受回环主机，并在建立连接时强制检查；不跟随重定向；限制响应大小；可选的 HTTP Basic 凭据来自环境变量，绝不写入日志。用户输入的文本被拆分为词并全部加引号，使 Everything 的操作符（`|`、`!`、`<>`、`regex:`）都按字面处理；查询被限定在备份源根目录内，并且对每个结果再次检查是否在根目录内。 |
| 审计 | `audit_events` 只追加（数据库触发器会拒绝 UPDATE 和 DELETE）。登录、拒绝、创建备份源、备份/校验的请求与结果、恢复预览、请求与结果、任务取消，都会连同用户和远端地址一起记录。 |
| 机密信息 | 仅从环境变量读取，记录配置日志时会脱敏，绝不存入代码仓库。开发用的机密信息生成在被 git 忽略的 `data/` 文件夹下。 |

## 缺口与限制（这些方面请勿依赖 MVP）

- **没有按用户或按路径的授权。** 只有一个管理员角色；能登录的人就能浏览和恢复所有备份源。不会查询 NTFS ACL 或 Active Directory 组，也没有实现 Windows SSO。
- **会话无法单独撤销。** 会话是无状态的；更改 `TIMEVAULT_SESSION_KEY` 会使所有会话失效。退出登录只会清除浏览器端的 Cookie。
- **服务内没有 TLS**，除登录限流外没有其他限速。
- **仓库没有加密，也不防篡改。** 对象带有只读属性并通过哈希校验，但任何能访问仓库文件系统的人都可以修改或删除它们。没有保留策略、垃圾回收或异机副本。
- **断电后的持久性是尽力而为。** 对象在发布之前会先刷新文件内容，恢复点提交之前会刷新接收了对象的文件夹，但这一点没有在真实断电下测试过。非正常关机之后请运行一次深度完整性校验。
- **仓库内容缺失发现得较晚。** 普通备份不会重新检查它原样沿用的未变化文件所对应的仓库对象。完整性校验（Verify）可以发现缺失或损坏的对象，恢复会拒绝使用它们，而**完整重读**备份会把缺失的内容重新写入。
- **实时扫描的一致性。** 文件读取自在线磁盘（没有 VSS）。撕裂读取会被检测并重试，被锁定的文件会被跳过并报告，但一组相关文件可能来自略有不同的时刻。
- **元数据保真度。** NTFS ACL、备用数据流、所有者、EFS 和硬链接关系不会被保留；恢复出的文件采用目标位置的权限。会恢复修改时间和常用的属性位。
- **权限。** 服务以启动它的账户运行。请使用能读取备份源、写入仓库和恢复目录的最小权限来运行。尚未实现独立的高权限 worker。
- **Everything 实例。** 适配器无法知道 Everything 绑定在哪里。请把它的"绑定到接口"选项保持为 `127.0.0.1`，关闭"允许文件下载"，并设置用户名和密码。
- **备份源路径检查。** 拒绝网络共享的检查只看输入文本开头是否为 `\\`。`//host/share` 和 `\\?\UNC\host\share` 不会被识别，所以添加这样的备份源时，服务会去连接该 SMB 服务器（并发送 Windows 凭据）。只有唯一的管理员可以添加备份源；请使用本地盘符路径。
- **恢复的计划与执行。** 确认针对的是预览时的计划，但恢复任务运行时会重新生成计划，并不会与已批准的计划比较。任务排队期间出现在目标位置的文件，可能在 `overwrite` 策略下被替换而无需再次确认；`create` 和 `rename` 项目用无条件的重命名发布，所以同时向同一个文件夹进行两次恢复可能互相替换对方的文件。请不要同时向同一个文件夹运行多个恢复。
- **恢复目录是受信任的存储。** 恢复根目录在每一步都是按路径解析的，因此在校验与使用之间被替换并不会被发现；暂存的 `.tvpart-*` 文件按句柄校验，却按路径发布；已有目标在被替换之前会先清除其属性，所以被人放在那里、指向其他文件的硬链接，会改变那个文件的属性。能写入恢复目录的人本来就能修改恢复出来的文件，所以请收紧它的权限。
- **开发模式。** 设置 `TIMEVAULT_AUTH_DISABLED=true` 时没有 Host 头检查，所以使用 DNS rebinding 的恶意网页理论上可以从运行该服务的人的浏览器访问它（未经测试）。不要在会浏览不可信网站的计算机上使用该模式；请使用登录。
