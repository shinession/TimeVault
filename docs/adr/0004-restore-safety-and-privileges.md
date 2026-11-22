# English

## Status

Accepted for the MVP (2026-11-15). Covers the "service privilege boundary" item of plan.md P0 and the restore safety rules of plan.md §7.

**Update, 1.0.1.** The service now runs with administrator rights, because reading an NTFS volume needs them ([0018](./0018-native-ntfs-index-replaces-everything.md)), so "a single unprivileged process" no longer holds; the restore safety rules of this record are unchanged. The record is left as it was written.

## Context

A restore writes to a computer's file system, from a catalog whose names came from another file system. A mistake there overwrites a user's good data, and a crafted name or a junction in the destination can point a write outside the intended folder. plan.md also recommends isolating highly privileged VSS and restore functions in a controlled interface.

## Decision

**Restore safety**

- A restore never writes into a source or into an original location. It writes into a **folder below `TIMEVAULT_RESTORE_ROOT`**, created when it does not exist; when it already exists (for example for a repeated restore) the conflict policy decides what happens to files that are already there. The folder name is validated as one path component, and the destination must be a plain folder (an existing junction or symbolic link is refused). The restore root must not overlap the repository or any source; overlaps are compared by real location, with junctions and links resolved. An operator copies the result to its final place.
- Two steps. The **preview** is a dry run that writes nothing and returns the plan: counts, size, conflicts, and blocked items (missing repository content, never-captured files, unsafe names). The **start** request must carry a confirmation token: an HMAC over the user and the exact plan, valid for 15 minutes. If anything changes (selection, policy, destination, the files that are already at the destination) a new preview is needed.
- Conflict policies are **skip**, **rename** and **overwrite**. Overwrite also needs an explicit extra confirmation (`confirm_overwrite`). If some items are blocked, the restore stops unless the requester asked for a partial restore.
- Every file is streamed from the repository into a temporary file inside the destination, its size and SHA-256 are compared with the catalog, and only then is it renamed into place; modification time and the common attribute bits are applied. A mismatch means the file is not restored and is reported.
- All destination operations go through a root handle (`os.Root`) opened on the destination folder, so `..`, absolute names, drive letters and links cannot reach outside it. Names from the catalog are validated again at restore time and unsafe ones (`..`, `:`, `\`, reserved device names, trailing dots or spaces) are blocked.
- The audit record "restore requested" is written **before** the job is queued; if it cannot be written the restore does not run. Sending the same request twice with the same idempotency key returns the existing job instead of starting another.
- Restores never delete anything in the repository or in a source.

**Privileges**

- The MVP is one process that runs as one operating-system account. It needs read access to the sources and write access to the repository and the restore root, and nothing more; it does not need administrator rights because VSS and USN are not used. A separate privileged worker is therefore not built.
- When VSS is implemented, the part that needs elevated rights (creating and removing snapshots) should move behind a narrow interface and a separate account, as plan.md recommends. That decision depends on the VSS spike (see [0003](./0003-live-scan-without-vss-and-usn.md)).
- Authorization is a single administrator role: whoever can sign in can browse and restore every source. See [security.md](../security.md).

## Consequences

- A restore can never destroy source data, which keeps the first version safe, but "restore in place" is a manual copy.
- NTFS ACLs, owners, alternate data streams and hard links are not recreated.
- The preview must be repeated whenever the situation changes, which is intended.
- Limits: a restore can select at most 500 paths (50 up to 0.1.2; 500 is the largest page of a folder list) and plan at most 2,000,000 items.

---

# 中文

## 状态

已接受，用于 MVP（2026-11-15）。对应 plan.md P0 中的"服务权限边界"一项以及 plan.md §7 的恢复安全规则。

**更新，1.0.1 版。** 服务现在以管理员权限运行，因为读取 NTFS 卷需要它（[0018](./0018-native-ntfs-index-replaces-everything.md)），所以"单一的非特权进程"不再成立；本记录中的恢复安全规则没有变。本记录保持原样。

## 背景

恢复会写入计算机的文件系统，而用于恢复的目录库中的名称来自另一个文件系统。这里的错误会覆盖用户完好的数据；精心构造的名称，或目标位置中的联接点，可能让写入指向预期文件夹之外。plan.md 还建议把高权限的 VSS 和恢复功能隔离在受控接口之后。

## 决定

**恢复安全**

- 恢复绝不会写入备份源或原位置。它写入 **`TIMEVAULT_RESTORE_ROOT` 之下的文件夹**，不存在时创建；已存在时（例如重复恢复），由冲突策略决定如何处理已有的文件。文件夹名称按单个路径组件校验，并且目标位置必须是普通文件夹（已存在的联接点或符号链接会被拒绝）。恢复根目录不得与仓库或任何备份源重叠；重叠按真实位置比较，即解析了联接点和链接之后的位置。由操作人员把结果复制到最终位置。
- 分两步。**预览**是不写入任何内容的演练，返回计划：数量、大小、冲突，以及受阻项目（仓库内容缺失、从未备份成功的文件、不安全的名称）。**开始**请求必须带有确认令牌：它是对用户和精确计划计算的 HMAC，有效期 15 分钟。如果任何内容发生变化（所选内容、策略、目标位置、目标位置中已有的文件），就需要重新预览。
- 冲突策略有**跳过**、**改名**和**覆盖**。覆盖还需要额外的明确确认（`confirm_overwrite`）。如果存在受阻项目，除非请求者要求部分恢复，否则恢复会停止。
- 每个文件从仓库流式写入目标位置内的临时文件，其大小和 SHA-256 与目录库比对，然后才重命名到位；之后应用修改时间和常用的属性位。不一致则该文件不会被恢复并会被报告。
- 对目标位置的所有操作都通过在目标文件夹上打开的根句柄（`os.Root`）进行，因此 `..`、绝对路径名、盘符和链接都无法到达它的外面。目录库中的名称在恢复时会再次校验，不安全的名称（`..`、`:`、`\`、保留设备名、以点或空格结尾）会被阻止。
- "已请求恢复"的审计记录在任务入队**之前**写入；如果无法写入，恢复不会运行。使用相同幂等键重复发送同一请求时，会返回已有任务，而不会再启动一个。
- 恢复绝不会删除仓库或备份源中的任何内容。

**权限**

- MVP 是一个进程，以一个操作系统账户运行。它需要对备份源的读取权限，以及对仓库和恢复根目录的写入权限，仅此而已；由于没有使用 VSS 和 USN，它不需要管理员权限。因此没有构建独立的高权限 worker。
- 实现 VSS 时，需要提升权限的部分（创建和删除快照）应当像 plan.md 建议的那样，放到狭窄的接口之后并使用独立的账户。这一决定取决于 VSS 验证的结果（见 [0003](./0003-live-scan-without-vss-and-usn.md)）。
- 授权是单一的管理员角色：能登录的人就能浏览和恢复所有备份源。见 [security.md](../security.md)。

## 影响

- 恢复永远不会破坏源数据，这使第一个版本是安全的，但"原位置恢复"是手动复制。
- 不会重建 NTFS ACL、所有者、备用数据流和硬链接。
- 每当情况发生变化都必须重新预览，这是有意为之。
- 限制：一次恢复最多可选择 500 个路径（到 0.1.2 为止是 50；500 是文件夹列表最大的一页），最多规划 2,000,000 个条目。
