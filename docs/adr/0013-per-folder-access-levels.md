# English

## Status

Accepted for 0.1.7 (2026-11-17). Refines the "Who may do what" table and the "Authorization is coarse" consequence of [0005](./0005-redis-user-directory-and-open-browsing.md): the sign-in, the Redis user directory and the roles `user` and `admin` stay as they are; what an account may do with a monitored folder is now decided for each folder.

## Context

Until 0.1.6 the rights were the same for every folder: anyone could browse all of them, every signed-in account could back up, verify, cancel jobs and restore on all of them, and only `admin` accounts could add or change folders. With 10 folders that belong to different teams this is too coarse: a team wants its folder to be seen, backed up or managed by some people only, and a new folder should not be open to everybody before somebody has thought about it.

The project owner asked for a permission system with three levels for each monitored folder, set on the Settings page when the folder is set up: **Browse**, **Back up now** (which includes the full re-read), and **Full**. Back-up includes browse and full includes back-up. A grant can be for **guest** (a visitor who has not signed in) or **everyone** (every signed-in account), and also for single accounts. An administrator has full control of every folder; a signed-in account can be given full control of one particular folder.

## Decision

**Levels.** Each level includes the ones before it.

| Level | An account with it may |
|---|---|
| Browse | See the folder: its files, history, versions, search, the state of its backups, its jobs and the audit events about it; restore from it (a restore needs a signed-in account, and it writes below the restore folder only, see [0004](./0004-restore-safety-and-privileges.md)). |
| Back up | Also: back up now (also with the full re-read), verify recovery points, and stop the jobs of the folder. |
| Full | Also: change the settings of the folder (name, display folder, exclude patterns, switched on or off, schedule, version limit) and who may do what with it. |

- **No level** means the folder does not exist for that visitor (see "Hidden folders").
- **Administrators** (`admin` accounts, the local administrator, and the synthetic session when sign-in is switched off) have Full on every folder without being listed. They alone add folders and see or change the folder on the server's disk (`root_path`; see [0007](./0007-display-folders-and-hiding-server-paths.md)). Somebody who has Full on a folder but is not an administrator may change everything about it **except** `root_path`: pointing the folder at another place of the server would let them read anything the service can read, and the real folder stays hidden from them like from everybody else.
- **Whoever started a restore may stop it** even when they only browse the folder; anything else a job needs to be stopped takes Back up.

**Who a grant is for.** A grant gives one level to **guest**, to **everyone** or to **one employee ID**. The level of a visitor is the highest of: Full for an administrator, what the guest grant gives (anybody), what the everyone grant gives (a signed-in account), and what their own employee ID is given. A signed-in account therefore never has less than a guest has. A guest can only be given **Browse**: somebody who has not signed in has no session to carry a CSRF token and nobody to put in the audit log, so they cannot start anything. In the API an employee ID is written as it is; the prefix `user:` is accepted for every ID and needed for one that is spelled like a group (`user:EVERYONE`), which would otherwise be taken for the group. The Settings form always writes `user:<ID>`.

**The editor.** The Settings page shows, for the folder being edited, three lists: who has Browse, who has Back up, and who has Full. Each list is a set of check boxes: **Guests (not signed in)** (only in the Browse list), **Everyone who is signed in**, and the accounts of the user directory by name and employee ID (ticked people first; a box to filter the list). Rules, checked on the server (the script only helps):

- In one list, guest and everyone cannot be combined with each other or with named people, because they already include them.
- A person who is in more than one list gets the highest level.
- An employee ID has to be valid (1 to 64 letters, digits or `.` `_` `-` `@`, stored in upper case) and, when it is added, to be an active account of the user directory. An account that already has a grant stays even when it has been deactivated since or when the directory cannot be asked, so that a folder can still be edited then; the editor marks such a person "not in the directory".
- At most 500 named accounts per folder.
- The form remembers which grants it was made from (a digest, `access_rev`). A form whose editor was not touched leaves the grants alone, so saving the schedule cannot bring back grants that somebody else took away while the form was open. A form whose lists were changed is refused (`409`, `access_changed`) when the grants of the folder are no longer those it was made from; it comes back with the lists as they are now and with what else was typed. The check is made in the transaction of the change, under the lock of the folder. The API takes the same `access_rev` next to `access` in `PATCH` (`GET /api/v1/sources` gives it to those who have Full); without it the lists are replaced as they are.
- Somebody who is not an administrator and saves lists that no longer give them Full on the folder is not let through at once: they could not undo it themselves. The form asks for a tick (`confirm_lockout`) and the API answers `409 lockout_not_confirmed` until it is sent. It is a warning, not a ban, because handing a folder over is normal. An administrator is never asked.
- The editor is in the form of the folder, which the page builds only when asked (`/settings?edit=<id>`, `/settings?add=1`), because it lists every account. An administrator adds a folder with a list too; a folder that is added without one is seen by administrators only.

**Hidden folders.** A visitor who has no level on a folder does not see it anywhere: not in the Explorer or Time Machine selector, not on the Backups, Settings, Audit or job pages, not in the API. A direct request for it (its id in an address, a job of it, a point of it) is answered exactly like a request for something that does not exist (404), also in the text; a visitor who has not signed in is told that signing in may show it. This includes a recovery point number of such a folder that is sent together with a folder the visitor can see: a restore accepts the points of its own folder only, as the history pages do, and answers any other number like one that does not exist. A visitor who can see a folder but not do what they asked gets 403, which is audited as `access.denied` with the folder. What is shown is cut to match: the table that hides the server's real folders in text is built from the visible folders only, the audit record of a refusal holds the route (the pattern that matched, with the numbers it captured filled in) and not the path that was typed, and the Status page counts the jobs of the visible folders only.

**Audit log and jobs.** Administrators see everything. Everybody else sees the events of the folders they can browse and the events that belong to no folder (sign-ins, failed sign-ins and the like, as before), and the jobs of the folders they can browse. The record of a refusal, of a job being stopped and of the creation or change of a folder belongs to its folder. Who may do what is not for everybody who may browse the folder: the lists in the details of the events that create a folder or change its lists (`access`) are shown only to those who have Full on it, and are `[hidden]` for the others, as on the Settings page and in the API.

**Defaults and upgrade.** The migration gives every folder that exists when it runs `guest = Browse` and `everyone = Back up`, which is what everybody could do until 0.1.6, so nothing changes for the folders that exist. A folder that is added afterwards has no grant: administrators see it, nobody else, until somebody is given access.

**Where it is enforced.** The service layer does not know who asks; it takes an `Actor` for the audit record only. The check is in the HTTP layer (`internal/server/access.go`): every handler that reaches a folder gets it through `loadSource` (a folder with at least the level the handler needs, "not found" when the visitor cannot browse it, a 403 when they can but may not do this), `visibleSources` (the folders to list), or `jobSource` (the folder of a job). A test (`TestHandlersReachFoldersThroughTheAccessChecks`) fails when a handler reads folders from the service directly. The rules that decide a level live in `internal/access`, which knows nothing of HTTP or the database.

Three details keep the checks honest when things change while a request runs. First, the pages and API calls that show a folder are registered with `openChecked`: the account of a visitor who looks signed in is checked with the user directory before the page is built, so an account that was deactivated or demoted meanwhile is a guest at once. Second, a folder and its grants are read in one read-only repeatable-read transaction, so what a request sees never mixes two moments. Third, a change of a folder runs in one transaction that locks the row of the folder, reads the grants that are in force at that moment and only then lets the caller's `Authorize` function decide (`catalog.SourceUpdate.Authorize`): somebody whose Full was revoked while their form was open is refused (403) instead of undoing the revocation. For an account that is not an administrator the update also sets `KeepRoot`, so the SQL cannot write the real folder whatever the body says; a `root_path` in the body of such a request is a 403.

**Data and API.** Table `source_access(source_id, kind, account, level)` (`kind` is `guest`, `everyone` or `user`; migration `0005_source_access.sql`). `GET /api/v1/sources` lists only the folders the caller may browse, each with `level` (`browse`, `backup` or `full`), and `access` (`{"browse": [...], "backup": [...], "full": [...]}` with `guest`, `everyone` and employee IDs) for those who have Full. `POST /api/v1/sources` (administrators) and `PATCH /api/v1/sources/{id}` (Full) take `access`; `PATCH` takes `root_path` from administrators only, and `confirm_lockout` (see the editor). The user directory gained an optional `auth.AccountLister` (the active accounts, by name, kept for a minute) for the editor.

## Alternatives considered

- **More global roles.** They do not say "this team's folder". A role per folder is what the three levels are.
- **Groups from Active Directory or NTFS ACLs.** The company's accounts for this application are in Redis; no group data is there, and the folders are on one server that people reach over SMB with their own rights, which TimeVault does not read (a gap listed in [security.md](../security.md)).
- **One list of people with a level each, instead of a list per level.** Easier to see one person's level, harder to see who has a level. The owner asked for a multi-select per level, and a "who has Full" list is what a folder's manager needs.
- **Hide the contents of a folder but keep its name in the lists.** The name of a team's folder can be as sensitive as what is in it, and "you may not" invites asking who to ask.
- **Let guests back up.** Rejected: nothing could be audited or protected against forged requests.
- **Let Full change the folder on the server's disk.** Rejected (see above); an administrator does it.
- **A deny entry** (everybody except this person). Not offered; the same is done by naming the people.
- **Checks in the service layer.** The service would have to take the visitor in every call, a larger change with the same result; the single point (`loadSource`) and the guard test cover the HTTP layer, which is the only caller.

## Consequences

- A folder that nobody has been given is visible to administrators only. Existing folders keep what everybody could do until 0.1.6, and an administrator narrows them one by one.
- Rights are read from the database on every request, so a change takes effect on the next request of every visitor; a session cookie is unaffected. An account that was deactivated or demoted is a guest on the next page it opens (the user directory is asked at most once a minute per account). While the directory cannot be reached the session is trusted as issued, as in [0005](./0005-redis-user-directory-and-open-browsing.md), so the rights that the session says stay until Redis answers again.
- A guest who may browse a folder and wants to restore from it is sent to sign in; so is somebody who follows a restore job. The folder stays visible to them.
- The people in the editor are the accounts of the user directory at that moment (at most a minute old); the editor is as long as the directory, and its cost is the cost of reading it from Redis once a minute.
- The Status page and `GET /api/v1/health` count the queued and running jobs of the folders the visitor may browse (administrators: all). The Settings page still shows the service settings to every signed-in account; none of it names a folder.
- Somebody with Full control can give it up; they are asked to confirm it first (`confirm_lockout`), and an administrator can always put it right.
- The rights of a person are the rights of their employee ID in the lists; renaming or re-hiring somebody under another employee ID needs the lists to be updated.
- **Going back to 0.1.6 switches the rights off.** 0.1.6 accepts a database with a newer schema and does not know `source_access`, so it would again let every visitor see every folder and every signed-in account back up and restore them. Do not start 0.1.6 against a database that migration `0005` has changed; put the database from before the upgrade back if you have to go back.
- **A restore that is already queued or running goes on** when the account that started it loses Browse on the folder meanwhile: it writes below the restore folder only, and its job page is then "not found" for that account. An administrator can still stop it.
- **Keys of restores.** Repeating a restore with the same `idempotency_key` returns the original job only to the account that started it for that folder; a key that somebody else used is a conflict (`409`). A key therefore cannot be used to find somebody else's restore.
- **A rename can show that a hidden folder exists.** Folder names are unique, so somebody with Full who renames their folder to the name of one that they cannot see is told that the name is taken. Nothing else about that folder is shown, and adding a folder is for administrators.
- What is not covered: the contents of files are still not served by any endpoint; a folder's file names, sizes and times are visible to everybody who may browse it; NTFS permissions are not consulted.

---

# 中文

## 状态

于 2026-11-17 在 0.1.7 中被接受。细化 [0005](./0005-redis-user-directory-and-open-browsing.md) 中的"谁可以做什么"表和"授权比较粗"这一条影响：登录、Redis 用户目录以及 `user` 和 `admin` 两种角色都保持不变；账号能对某个监控文件夹做什么，现在按文件夹分别决定。

## 背景

到 0.1.6 为止，所有文件夹的权限都一样：任何人都能浏览全部文件夹，每个已登录账号都能对所有文件夹备份、校验、取消任务和恢复，只有 `admin` 账号可以添加或修改文件夹。当有 10 个属于不同团队的文件夹时，这样太粗了：团队希望自己的文件夹只被某些人查看、备份或管理，而新添加的文件夹在有人考虑清楚之前，也不应该对所有人开放。

项目负责人要求为每个监控文件夹建立一个三级权限系统，并在设置页面设置文件夹时一并设定：**浏览**、**立即备份**（包括完整重读）和**完全**。备份包含浏览，完全包含备份。授权对象可以是 **guest**（未登录的访问者）或 **everyone**（所有已登录账号），也可以是单个账号。管理员对所有文件夹拥有完全控制权；已登录的账号可以被授予某个特定文件夹的完全控制权。

## 决定

**级别。** 每个级别都包含它前面的级别。

| 级别 | 拥有该级别的账号可以 |
|---|---|
| 浏览 | 查看文件夹：其中的文件、历史、版本、搜索、备份的状态、它的任务以及与它有关的审计事件；从中恢复（恢复需要已登录的账号，并且只会写入恢复文件夹之下，见 [0004](./0004-restore-safety-and-privileges.md)）。 |
| 备份 | 另外：立即备份（也包括完整重读）、校验恢复点、停止该文件夹的任务。 |
| 完全 | 另外：修改文件夹的设置（名称、显示文件夹、排除规则、启用或停用、计划、版本数上限）以及谁能对它做什么。 |

- **没有任何级别**表示这个文件夹对该访问者不存在（见"隐藏的文件夹"）。
- **管理员**（`admin` 账号、本地管理员，以及登录功能关闭时的合成会话）无需被列出，就对所有文件夹拥有"完全"。只有管理员能添加文件夹，也只有管理员能看到或修改服务器磁盘上的文件夹（`root_path`；见 [0007](./0007-display-folders-and-hiding-server-paths.md)）。对某个文件夹拥有"完全"但不是管理员的人，可以修改它的一切，**除了** `root_path`：把文件夹指向服务器的其他位置，会让他们读到服务能读到的任何内容，而且真实文件夹对他们和对其他人一样是隐藏的。
- **谁启动了恢复，谁就可以停止它**，即使他们只有该文件夹的"浏览"权限；任务的其他停止操作需要"备份"。

**授权对象。** 一条授权把一个级别授予 **guest**、**everyone** 或**某一个工号**。访问者的级别取以下各项中最高的：管理员得到"完全"、guest 授权所给的级别（任何人）、everyone 授权所给的级别（已登录的账号），以及他们自己的工号所获得的级别。因此已登录的账号永远不会比访客权限更低。guest 只能被授予**"浏览"**：未登录的人没有可以携带 CSRF 令牌的会话，审计日志里也没有可记录的人，所以他们不能启动任何操作。在 API 中，工号按原样书写；前缀 `user:` 对每个工号都可以使用，而对于拼写得像组名的工号则是必需的（`user:EVERYONE`），否则它会被当成那个组。设置页表单总是写成 `user:<工号>`。

**编辑器。** 设置页面会为正在编辑的文件夹显示三个列表：谁拥有"浏览"、谁拥有"备份"、谁拥有"完全"。每个列表都是一组复选框：**访客（未登录）**（只在"浏览"列表中）、**所有已登录的人**，以及用户目录中的账号（按姓名和工号显示；已勾选的人排在最前面；有一个筛选框）。规则由服务器检查（脚本只是辅助）：

- 在同一个列表中，guest 和 everyone 不能互相搭配，也不能与指定的人员同时选择，因为它们已经包含了这些人。
- 同时出现在多个列表中的人，取最高的级别。
- 工号必须有效（1 到 64 个字母、数字或 `.` `_` `-` `@`，以大写形式保存），并且在新添加时必须是用户目录中有效的账号。已经有授权的账号即使之后被停用、或者目录当前无法查询，也会保留，这样文件夹在那时仍然可以编辑；编辑器会把这样的人标为"不在用户目录中"。
- 每个文件夹最多可以指定 500 个账号。
- 表单会记住它所依据的授权（一个摘要，`access_rev`）。编辑器没有被动过的表单不会去动授权，所以保存计划时，不会把别人在表单打开期间取消的授权又带回来。修改了列表的表单，如果文件夹的授权已不再是它所依据的那些，就会被拒绝（`409`，`access_changed`）；它会带着授权现在的样子和其他已输入的内容重新出现。检查是在更改所在的事务中、在锁定文件夹之后进行的。API 在 `PATCH` 中的 `access` 旁边接受同样的 `access_rev`（`GET /api/v1/sources` 会把它给拥有"完全"的人）；没有它时，列表按原样替换。
- 不是管理员的人，如果保存的列表不再让他们对该文件夹拥有"完全"，不会被直接放行：他们自己无法撤销这个更改。表单会要求勾选确认（`confirm_lockout`），在它被发送之前，API 会返回 `409 lockout_not_confirmed`。这是警告而不是禁止，因为把文件夹移交出去是正常的事。管理员从不会被询问。
- 编辑器在文件夹的表单中，而页面只在被要求时才构建它（`/settings?edit=<id>`、`/settings?add=1`），因为它要列出所有账号。管理员添加文件夹时也可以同时设置列表；添加时没有设置列表的文件夹，只有管理员能看到。

**隐藏的文件夹。** 对某个文件夹没有任何级别的访问者，在任何地方都看不到它：文件浏览或时间机器的选择框中没有，备份、设置、审计或任务页面上没有，API 中也没有。对它的直接请求（地址里的 id、它的某个任务、它的某个恢复点）会得到与请求一个不存在的东西完全相同的回答（404），文字也一样；未登录的访问者会被告知登录后也许能看到。这也包括与访问者能看到的文件夹一起发来的、属于这样一个文件夹的恢复点编号：恢复只接受它自己文件夹的恢复点，与历史页面一样，对任何其他编号的回答都与不存在的编号一样。能看到文件夹、但无权执行所请求操作的访问者会得到 403，并且会以 `access.denied` 记入审计，同时带上该文件夹。展示的内容也相应裁剪：在文本中隐藏服务器真实文件夹的映射表只由可见的文件夹构建，被拒绝请求的审计记录保存的是路由（匹配到的模式，其中捕获到的数字已填入），而不是输入的路径，状态页只统计可见文件夹的任务。

**审计日志与任务。** 管理员能看到全部。其他人看到他们可以浏览的文件夹的事件，以及不属于任何文件夹的事件（登录、登录失败等，和以前一样），还有他们可以浏览的文件夹的任务。拒绝记录、任务被停止的记录，以及文件夹的创建或修改记录，都属于它所在的文件夹。谁能做什么并不是所有能浏览该文件夹的人都能看的：创建文件夹或修改其列表的事件详情中的列表（`access`），只显示给对它拥有"完全"的人，对其他人是 `[hidden]`，与设置页和 API 一致。

**默认值与升级。** 迁移会给运行时已有的每个文件夹设置 `guest = 浏览` 和 `everyone = 备份`，这正是到 0.1.6 为止所有人都能做的事，所以已有的文件夹没有任何变化。之后添加的文件夹没有任何授权：管理员能看到，其他人看不到，直到有人被授予访问权。

**在哪里执行。** 服务层不知道是谁在请求；它接收 `Actor` 只用于审计记录。检查在 HTTP 层（`internal/server/access.go`）：每个要访问文件夹的处理函数，都通过 `loadSource`（获得至少具备该处理函数所需级别的文件夹；访问者不能浏览它时返回"未找到"；能浏览但不能做这件事时返回 403）、`visibleSources`（要列出的文件夹）或 `jobSource`（任务所属的文件夹）取得。一个测试（`TestHandlersReachFoldersThroughTheAccessChecks`）会在有处理函数直接从服务读取文件夹时失败。决定级别的规则在 `internal/access` 中，它不涉及 HTTP 和数据库。

当请求运行期间情况发生变化时，有三处细节让检查保持可靠。第一，显示文件夹的页面和 API 调用都用 `openChecked` 注册：看起来已登录的访问者，其账号会在页面构建之前先向用户目录核实，所以期间被停用或降级的账号会立即变成访客。第二，文件夹及其授权在同一个只读的可重复读事务中读取，所以一个请求看到的内容不会混合两个时刻。第三，对文件夹的修改在一个事务中进行：它先锁定文件夹的行，读取那一刻有效的授权，然后才让调用者的 `Authorize` 函数做决定（`catalog.SourceUpdate.Authorize`）：表单打开期间其"完全"权限被撤销的人，会被拒绝（403），而不是把撤销撤销掉。对于不是管理员的账号，更新还会设置 `KeepRoot`，所以无论请求体里写了什么，SQL 都不可能写入真实文件夹；这类请求的请求体里只要有 `root_path`，就是 403。

**数据与 API。** 表 `source_access(source_id, kind, account, level)`（`kind` 为 `guest`、`everyone` 或 `user`；迁移 `0005_source_access.sql`）。`GET /api/v1/sources` 只列出调用者可以浏览的文件夹，每个都带有 `level`（`browse`、`backup` 或 `full`），拥有"完全"的人还会得到 `access`（`{"browse": [...], "backup": [...], "full": [...]}`，其中是 `guest`、`everyone` 和工号）。`POST /api/v1/sources`（管理员）和 `PATCH /api/v1/sources/{id}`（完全）接受 `access`；`PATCH` 只接受管理员提交的 `root_path`，并接受 `confirm_lockout`（见编辑器）。用户目录新增了一个可选的 `auth.AccountLister`（有效账号，按姓名排序，保留一分钟），供编辑器使用。

## 考虑过的备选方案

- **增加更多全局角色。** 它们说不出"这个团队的文件夹"。每个文件夹一个角色，就是这三个级别。
- **使用 Active Directory 的组或 NTFS ACL。** 该应用使用的公司账号在 Redis 中，那里没有组数据；而且文件夹在一台服务器上，人们通过 SMB 用自己的权限访问，TimeVault 并不读取这些权限（这是 [security.md](../security.md) 中列出的一个缺口）。
- **用一个"人员加各自级别"的列表，而不是每个级别一个列表。** 更容易看到某个人的级别，但更难看到谁拥有某个级别。负责人要求每个级别一个多选框，而"谁拥有完全"这个列表正是文件夹管理者需要的。
- **隐藏文件夹的内容，但在列表中保留它的名称。** 一个团队的文件夹的名称可能和其中的内容一样敏感，而且"你没有权限"会引发"该问谁"。
- **允许访客备份。** 否决：无法审计，也无法防范伪造的请求。
- **让"完全"可以修改服务器磁盘上的文件夹。** 否决（见上文），由管理员来做。
- **拒绝项**（除了这个人之外的所有人）。不提供；通过指定具体的人也能做到同样的事。
- **在服务层检查。** 服务需要在每次调用中接收访问者，改动更大而结果相同；单一的检查入口（`loadSource`）加上守卫测试，已经覆盖了 HTTP 层，而它是唯一的调用方。

## 影响

- 没有被授权给任何人的文件夹只有管理员能看到。已有的文件夹保持到 0.1.6 为止所有人都能做的事，管理员再逐个收紧。
- 权限在每次请求时从数据库读取，所以修改会在每个访问者的下一次请求中生效；会话 Cookie 不受影响。被停用或被降级的账号，在它打开的下一个页面上就是访客（用户目录对每个账号最多每分钟询问一次）。用户目录无法访问期间，按签发时的状态信任会话，与 [0005](./0005-redis-user-directory-and-open-browsing.md) 一样，所以会话所说的权限会保留到 Redis 重新应答为止。
- 可以浏览某个文件夹的访客如果想从中恢复，会被引导去登录；查看恢复任务的人也是如此。文件夹对他们仍然可见。
- 编辑器中的人员，是那一刻用户目录中的账号（最多是一分钟之前的）；编辑器有多长取决于目录有多大，成本就是每分钟从 Redis 读取一次的成本。
- 状态页面和 `GET /api/v1/health` 统计访问者可以浏览的文件夹的排队和运行中的任务数（管理员：全部）。设置页面仍然向每个已登录账号显示服务设置；其中没有任何内容会点出某个文件夹。
- 拥有"完全"权限的人可以放弃它；他们会先被要求确认（`confirm_lockout`），管理员总是可以改正。
- 一个人的权限就是他的工号在各个列表中的权限；如果某人改名或以另一个工号重新入职，需要更新这些列表。
- **回到 0.1.6 会让这些权限失效。** 0.1.6 接受带有更新架构的数据库，也不认识 `source_access`，所以它会再次让每个访问者看到每个文件夹，让每个已登录账号对它们备份和恢复。不要让 0.1.6 去启动一个已被迁移 `0005` 改过的数据库；如果必须回退，请把升级之前的数据库放回去。
- **已经排队或正在运行的恢复会继续**，即使启动它的账号期间失去了该文件夹的"浏览"权限：它只会写入恢复文件夹之下，而它的任务页面对该账号来说就是"未找到"。管理员仍然可以停止它。
- **恢复的幂等键。** 用相同的 `idempotency_key` 重复恢复，只会把原来的任务返回给针对该文件夹启动它的那个账号；别人用过的键是冲突（`409`）。所以不能用一个键去找到别人的恢复。
- **重命名可能暴露一个被隐藏的文件夹的存在。** 文件夹名称是唯一的，所以拥有"完全"的人把自己的文件夹重命名为一个他们看不到的文件夹的名称时，会被告知这个名称已被占用。关于那个文件夹的其他任何信息都不会显示，而添加文件夹只有管理员能做。
- 没有涵盖的内容：文件内容仍然没有任何接口提供；能浏览某个文件夹的人都能看到它的文件名、大小和时间；不会参考 NTFS 权限。
