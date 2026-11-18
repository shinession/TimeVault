# English

## Architecture decision records

Decisions that shape TimeVault, with their alternatives and consequences. Each file is bilingual (English first, then Simplified Chinese). plan.md P0 asks for ADRs on snapshot semantics, the data model, the content object format and the service privilege boundary; the table says where each is covered. 0005 and 0006 were added for version 0.1.1, 0007 and 0008 for version 0.1.3, 0009, 0010 and 0011 for version 0.1.4, 0012 for version 0.1.5, 0013 for version 0.1.7; 0009 was updated for version 0.1.6.

| ADR | Decision | Covers (plan.md P0) |
|---|---|---|
| [0001](./0001-recovery-point-history-model.md) | Recovery points and per-path validity ranges in PostgreSQL | Data model; what a recovery point means in the catalog |
| [0002](./0002-content-addressed-repository.md) | One content-addressed object per file in a separate repository folder | Content object format |
| [0003](./0003-live-scan-without-vss-and-usn.md) | Live scan instead of VSS and USN in the MVP (a deliberate deviation from plan.md) | Snapshot semantics (provisional) |
| [0004](./0004-restore-safety-and-privileges.md) | Restore only below the restore root, with preview, token and confinement; single unprivileged process | Service privilege boundary; restore safety |
| [0005](./0005-redis-user-directory-and-open-browsing.md) | Anyone can browse; company accounts from the Redis user directory sign in to do anything else; roles `user` and `admin` (a deliberate exception to plan.md's "no Redis") | Role model (first version) |
| [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md) | Deployment settings in `.env` or the environment, monitored folders in the database, automatic backups every N minutes that record nothing when nothing changed | Configuration; scheduling (first version) |
| [0007](./0007-display-folders-and-hiding-server-paths.md) | Monitored folders have a display folder (the network path people use); only administrators see the real folders of the server, and texts that quote them are rewritten for everybody else; `TIMEVAULT_SERVER_HOST` makes the display folder mandatory and checks its host | Role model (what each role sees) |
| [0008](./0008-folder-lists-are-cut-on-the-server.md) | Folder lists are filtered by type, sorted and cut into pages on the server by one engine, for the pages and the API alike | Browsing and search UX |
| [0009](./0009-single-sign-on-and-trusted-proxies.md) | Single sign-on through the company sign-on service: the result is read from Redis (never deleted) for a token this server issued, bound to the address of the browser; reverse proxies are named in `TIMEVAULT_TRUSTED_PROXIES`, also when two proxies are in a row (0.1.6) | Role model (sign-in); Windows SSO |
| [0010](./0010-version-limit-and-garbage-collection.md) | A limit of versions per file for each monitored folder: older versions roll off, the recovery points that lose versions are marked partial, and repository content nobody refers to is swept while no backup runs | Retention and garbage collection |
| [0011](./0011-staggered-scheduler-and-change-probe.md) | Automatic backups run one at a time with a gap and a rest, ordered by need; Everything can tell that a folder did not change so that it is not scanned; the service runs at a lower priority | Scheduling (second version) |
| [0012](./0012-base-path-behind-a-reverse-proxy.md) | The site can be served under a path prefix such as `/tv` behind a reverse proxy: the prefix is taken off a request and put on the redirects, links, assets, script requests and cookies, and the proxy may keep or remove it | Web layer, deployment |
| [0013](./0013-per-folder-access-levels.md) | What an account may do is decided for each monitored folder: Browse, Back up (with the full re-read) and Full, granted to guests, to everybody who is signed in or to single employee IDs; a folder without a level is invisible; administrators have Full everywhere; new folders are seen by administrators only until they are given access | Role model (per-source permissions) |

Not decided yet, because the spikes were not run: VSS usage, USN checkpoints, chunking and packs.

---

# 中文

## 架构决策记录

塑造 TimeVault 的各项决定，包括备选方案和影响。每个文件都是双语的（先英文，后简体中文）。plan.md P0 要求为快照语义、数据模型、内容对象格式和服务权限边界编写 ADR；下表说明各项分别在哪里覆盖。0005 和 0006 是为 0.1.1 版本新增的，0007 和 0008 是为 0.1.3 版本新增的，0009、0010 和 0011 是为 0.1.4 版本新增的，0012 是为 0.1.5 版本新增的，0013 是为 0.1.7 版本新增的；0009 在 0.1.6 版本中有更新。

| ADR | 决定 | 覆盖的内容（plan.md P0） |
|---|---|---|
| [0001](./0001-recovery-point-history-model.md) | 在 PostgreSQL 中使用恢复点和按路径的有效区间 | 数据模型；恢复点在目录库中的含义 |
| [0002](./0002-content-addressed-repository.md) | 在独立的仓库文件夹中，每个文件对应一个内容寻址对象 | 内容对象格式 |
| [0003](./0003-live-scan-without-vss-and-usn.md) | MVP 采用实时扫描，而不是 VSS 和 USN（有意偏离 plan.md） | 快照语义（暂定） |
| [0004](./0004-restore-safety-and-privileges.md) | 只恢复到恢复根目录之下，需要预览、令牌和约束；单个非特权进程 | 服务权限边界；恢复安全 |
| [0005](./0005-redis-user-directory-and-open-browsing.md) | 任何人都可以浏览；其他操作需要用 Redis 用户目录中的公司账号登录；角色为 `user` 和 `admin`（有意偏离 plan.md 的"不引入 Redis"） | 角色模型（第一版） |
| [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md) | 部署设置放在 `.env` 或环境变量中，监控文件夹放在数据库中，每 N 分钟自动备份，没有变化时不记录任何内容 | 配置；计划任务（第一版） |
| [0007](./0007-display-folders-and-hiding-server-paths.md) | 监控文件夹有显示文件夹（人们使用的网络路径）；只有管理员能看到服务器上的真实文件夹，引用它们的文本会为其他人改写；`TIMEVAULT_SERVER_HOST` 让显示文件夹成为必填并检查它的主机 | 角色模型（各角色能看到什么） |
| [0008](./0008-folder-lists-are-cut-on-the-server.md) | 文件夹列表由一个引擎在服务器上按类型筛选、排序并切分成页，页面和 API 一致 | 浏览与搜索体验 |
| [0009](./0009-single-sign-on-and-trusted-proxies.md) | 通过公司单点登录服务实现单点登录：对本服务器签发的令牌，从 Redis 读取结果（从不删除），并与浏览器的地址绑定；反向代理在 `TIMEVAULT_TRUSTED_PROXIES` 中指明，两层代理时也是如此（0.1.6） | 角色模型（登录）；Windows SSO |
| [0010](./0010-version-limit-and-garbage-collection.md) | 每个监控文件夹设置每个文件的版本数上限：较旧的版本滚动删除，失去版本的恢复点被标记为不完整，没有备份运行时清扫无人引用的仓库内容 | 保留策略与垃圾回收 |
| [0011](./0011-staggered-scheduler-and-change-probe.md) | 自动备份一次运行一个，带间隔和休息，并按需要排序；Everything 可以告知某个文件夹没有变化从而不扫描它；服务以较低优先级运行 | 计划任务（第二版） |
| [0012](./0012-base-path-behind-a-reverse-proxy.md) | 站点可以放在反向代理之后的路径前缀（如 `/tv`）下提供：请求上的前缀会被去掉，重定向、链接、静态资源、脚本请求和 Cookie 会加上前缀，代理可以保留也可以去掉它 | Web 层、部署 |
| [0013](./0013-per-folder-access-levels.md) | 账号能做什么按监控文件夹分别决定：浏览、备份（含完整重读）和完全，可以授予访客、所有已登录的人或单个工号；没有任何级别的文件夹是不可见的；管理员对所有文件夹拥有完全控制权；新文件夹在被授予访问权之前只有管理员能看到 | 角色模型（按备份源的权限） |

尚未决定的内容（因为没有做验证）：VSS 的使用、USN 检查点、分块与 Pack。
