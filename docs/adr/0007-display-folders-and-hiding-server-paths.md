# English

## Status

Accepted for 0.1.3 (2026-11-16). Builds on [0005](./0005-redis-user-directory-and-open-browsing.md) (anyone can browse, an account does the rest) and [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md) (where settings live).

**Update, 1.0.1.** The settings that this record names as `TIMEVAULT_*` variables (`TIMEVAULT_SERVER_HOST`, `TIMEVAULT_RESTORE_DISPLAY_ROOT`) are Site settings now ([0017](./0017-site-settings-in-the-database.md)): `server_host` and `restore_display_root`. Where this record says that the search (Everything) only works with the folders of this server, the NTFS index ([0018](./0018-native-ntfs-index-replaces-everything.md)) does that work now, and a folder that is not on a local NTFS drive letter has no search. The record is left as it was written.

## Context

- People open a monitored folder over the network (SMB), as `\\sgmeslt595.co.murata.local\TimeVault\monitor`, not as the `D:\TimeVault\monitor` it is on the server. They do not know the server's own layout and have no use for it.
- Until 0.1.2 every signed-in account saw the real folders: on the Backups, Settings and Status pages, in restore destinations, in job errors and logs, in audit details and in the API.
- The search (Everything), the backup and the restore only work with folders on this server, so a monitored folder that lives on another server must not be accepted.

## Decision

**Display folders**

- A monitored folder has an optional **display folder** (`sources.display_path`, migration `0003`): the network path `\\host\share[\folder…]` that people open it with. `pathutil.ParseUNC` checks it: at most 520 characters, a plain host name, a share that does not end with `$` (hidden and administrative shares are not for users), folder names that are valid on Windows. It is stored with backslashes and without a trailing separator. It can be changed at any time, because nothing in the history refers to it.
- **`TIMEVAULT_SERVER_HOST`** is the name of this server as people type it. When it is set, the display folder is **required**, and its host must equal the setting without regard to case (`\\SGMESLT595.co.murata.local\…` is stored with the host spelled as in the setting). The short name of a server that has a domain name is another name and is refused. The edit form and `PATCH` send the current value too, so the same rules hold for every update. When the setting is not set, the display folder is optional and only its syntax is checked; this keeps older setups and the tests working.
- **`TIMEVAULT_RESTORE_DISPLAY_ROOT`** is the restore folder as people reach it. It is checked at start-up: a network path on `TIMEVAULT_SERVER_HOST`, and ignored when there is no restore folder.

**Who sees what**

- Administrators (the permission `manage`) see the display folder and the real folder. Everybody else, signed in or not, sees the display folder only. The backup folder (the repository) is not named for them at all; the Settings and Status pages tell them how full it is.
- A **Location** line with a **Copy** button on the Explorer, the Time Machine and the page of a file's versions shows the display folder of what is on screen. For administrators the real folder follows it, and it is shown alone when the monitored folder has no display folder.

**Hiding the real folders in text**

- `service.PathMap` is built per request from the `sources` table and the repository and restore folders. `Mask(text)` replaces every real folder in a text with what the reader may see, `MaskValue(value)` does the same for the strings of a structured value (audit details). A folder with a display folder becomes it, a folder without one becomes `[<name of the monitored folder>]`, the repository `[backup folder]`, the restore folder its display root or `[restore folder]`; what follows the folder in the path is kept.
- A folder is found without regard to case, with either slash and with a `\\?\` in front, and only where it ends: `D:\a\b2` is not `D:\a\b`. The longest folder wins. Masking repeats until nothing is left to hide, and a text that cannot be made safe becomes `[path hidden]`.
- The server layer (`internal/server/visibility.go`) applies it before anything is sent: job errors and logs, restore item messages and problems, the reasons a file was not captured and a recovery point failed, audit details, component details, restore destinations. The page handlers and the API handlers use the same code. When the map cannot be built, the texts are left out instead of shown.

## Alternatives considered

- **Derive the display folder from the shares of the computer** (`NetShareEnum`). Needs administrator rights, and the service runs unprivileged ([0004](./0004-restore-safety-and-privileges.md)). A stored value also lets the administrator choose which share people should use.
- **Hide the real folders from everybody.** Administrators need them to run the service, and the logs have them anyway.
- **Rewrite the text where it is produced, or store the masked text.** The real text is the truth that administrators need; masking when it is read keeps one stored version.
- **Always require a display folder, or never.** The setting `TIMEVAULT_SERVER_HOST` makes it a choice of the deployment: strict where people come over SMB, relaxed for a laptop and for the tests.

## Consequences

- With `TIMEVAULT_SERVER_HOST` set, the monitored folders that were added before have no display folder and cannot be edited until an administrator gives each one. Browsing, backups and restores are not affected.
- The service checks the host, **not** that the share leads to the monitored folder (that would need the share list). A wrong share name shows people a path that does not exist.
- Hiding works on decoded text. A folder written in an unusual way (an 8.3 short name, a drive letter that is another name for the same disk) is not recognised. Masked JSON is never produced from raw JSON text, only from structured values.
- Anonymous visitors see the host name inside display folders; they need it to open the files.
- `root_path` of `GET /api/v1/sources` and `repository`, `restore_root` and `env_file` of `GET /api/v1/settings` are for administrators only, which breaks ordinary accounts that read them (see [changelog.md](../changelog.md)).
- A page that shows texts that may quote a folder reads the `sources` table once more (once per request, only when there is such a text).

---

# 中文

## 状态

于 2026-11-16 在 0.1.3 中被接受。以 [0005](./0005-redis-user-directory-and-open-browsing.md)（任何人都可以浏览，其余操作需要账号）和 [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md)（设置放在哪里）为基础。

**更新，1.0.1 版。** 本记录中以 `TIMEVAULT_*` 变量命名的设置（`TIMEVAULT_SERVER_HOST`、`TIMEVAULT_RESTORE_DISPLAY_ROOT`），现在都是站点设置（[0017](./0017-site-settings-in-the-database.md)）：`server_host` 和 `restore_display_root`。本记录中说搜索（Everything）只能处理本服务器上的文件夹的地方，现在由 NTFS 索引（[0018](./0018-native-ntfs-index-replaces-everything.md)）承担这项工作，而不在本地 NTFS 盘符上的文件夹没有搜索。本记录保持原样。

## 背景

- 人们通过网络（SMB）打开监控文件夹，看到的是 `\\sgmeslt595.co.murata.local\TimeVault\monitor`，而不是它在服务器上的 `D:\TimeVault\monitor`。他们不知道服务器自己的目录布局，也用不上。
- 直到 0.1.2，每个已登录账号都能看到真实文件夹：在备份、设置和状态页面上，在恢复目标位置中，在任务错误和日志中，在审计详情中，以及在 API 中。
- 搜索（Everything）、备份和恢复只能处理本服务器上的文件夹，所以位于另一台服务器上的监控文件夹不能被接受。

## 决定

**显示文件夹**

- 监控文件夹有一个可选的**显示文件夹**（`sources.display_path`，迁移 `0003`）：人们打开它时使用的网络路径 `\\host\share[\folder…]`。`pathutil.ParseUNC` 负责检查：最多 520 个字符，普通的主机名，共享名不能以 `$` 结尾（隐藏共享和管理共享不是给用户用的），各级文件夹名在 Windows 上有效。保存时使用反斜杠，且不带结尾的分隔符。它可以随时更改，因为历史记录中没有任何内容引用它。
- **`TIMEVAULT_SERVER_HOST`** 是人们输入的本服务器名称。设置后，显示文件夹是**必填**的，并且它的主机必须与该设置相同（不区分大小写；`\\SGMESLT595.co.murata.local\…` 保存时主机使用设置中的写法）。有域名的服务器的短名称被视为另一个名称，会被拒绝。编辑表单和 `PATCH` 也会发送当前值，所以每次更新都遵守同样的规则。没有设置时，显示文件夹是可选的，只检查语法；这样旧的部署和测试仍然可以工作。
- **`TIMEVAULT_RESTORE_DISPLAY_ROOT`** 是人们访问恢复文件夹的路径。它在启动时检查：必须是 `TIMEVAULT_SERVER_HOST` 上的网络路径；没有恢复文件夹时会被忽略。

**谁能看到什么**

- 管理员（权限 `manage`）可以看到显示文件夹和真实文件夹。其他人无论是否登录，只能看到显示文件夹。备份文件夹（仓库）根本不会向他们点名；设置页和状态页只告诉他们它用了多少。
- 文件浏览、时间机器和文件版本页面上带有**复制**按钮的**位置**行，显示当前屏幕内容的显示文件夹。对管理员来说，真实文件夹跟在后面；监控文件夹没有显示文件夹时，则只显示真实文件夹。

**在文本中隐藏真实文件夹**

- `service.PathMap` 每个请求根据 `sources` 表以及仓库文件夹和恢复文件夹构建一次。`Mask(text)` 把文本中的每个真实文件夹换成读者可以看到的内容，`MaskValue(value)` 对结构化值（审计详情）中的字符串做同样的事。有显示文件夹的文件夹变成它的显示文件夹，没有的变成 `[<监控文件夹名称>]`，仓库变成 `[backup folder]`，恢复文件夹变成它的显示根路径或 `[restore folder]`；路径中跟在文件夹后面的部分保持不变。
- 查找文件夹时不区分大小写，两种斜杠都可以，前面带 `\\?\` 也可以，并且只在它结束的地方才算：`D:\a\b2` 不是 `D:\a\b`。取最长的文件夹。替换会一直重复到没有可隐藏的内容；无法确保安全的文本会变成 `[path hidden]`。
- 服务器层（`internal/server/visibility.go`）在发送任何内容之前应用它：任务错误和日志、恢复条目消息和问题、文件未能备份和恢复点失败的原因、审计详情、组件详情、恢复目标位置。页面处理程序和 API 处理程序使用同一段代码。无法构建这张映射时，相关文本会被省略而不是显示。

## 考虑过的备选方案

- **从计算机的共享列表推导显示文件夹**（`NetShareEnum`）。需要管理员权限，而服务以非特权方式运行（[0004](./0004-restore-safety-and-privileges.md)）。保存下来的值还让管理员可以选择人们应该使用哪个共享。
- **对所有人隐藏真实文件夹。** 管理员需要它们来运维服务，而且日志里本来就有。
- **在生成文本的地方改写，或保存已隐藏的文本。** 真实文本才是管理员需要的事实；在读取时隐藏，可以只保存一份。
- **总是要求或从不要求显示文件夹。** 设置 `TIMEVAULT_SERVER_HOST` 让它成为部署时的选择：人们通过 SMB 访问的地方严格，笔记本电脑和测试则宽松。

## 影响

- 设置了 `TIMEVAULT_SERVER_HOST` 之后，之前添加的监控文件夹没有显示文件夹，管理员给每个文件夹填上之前不能编辑它们。浏览、备份和恢复不受影响。
- 服务检查主机，**不**检查共享是否通向该监控文件夹（那需要共享列表）。共享名写错时，人们看到的是一个不存在的路径。
- 隐藏是基于已解码的文本进行的。以不寻常的方式书写的文件夹（8.3 短名称、同一磁盘的另一个盘符别名）无法被识别。隐藏后的 JSON 从来不是由原始 JSON 文本生成的，只来自结构化的值。
- 匿名访问者可以在显示文件夹中看到主机名；他们需要它才能打开文件。
- `GET /api/v1/sources` 的 `root_path` 以及 `GET /api/v1/settings` 的 `repository`、`restore_root` 和 `env_file` 只给管理员，这会影响读取它们的普通账号（见 [changelog.md](../changelog.md)）。
- 显示可能引用文件夹的文本的页面，会多读一次 `sources` 表（每个请求一次，并且只在有这类文本时）。
