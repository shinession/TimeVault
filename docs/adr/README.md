# English

## Architecture decision records

Decisions that shape the TimeVault MVP, with their alternatives and consequences. Each file is bilingual (English first, then Simplified Chinese). plan.md P0 asks for ADRs on snapshot semantics, the data model, the content object format and the service privilege boundary; the table says where each is covered.

| ADR | Decision | Covers (plan.md P0) |
|---|---|---|
| [0001](./0001-recovery-point-history-model.md) | Recovery points and per-path validity ranges in PostgreSQL | Data model; what a recovery point means in the catalog |
| [0002](./0002-content-addressed-repository.md) | One content-addressed object per file in a separate repository folder | Content object format |
| [0003](./0003-live-scan-without-vss-and-usn.md) | Live scan instead of VSS and USN in the MVP (a deliberate deviation from plan.md) | Snapshot semantics (provisional) |
| [0004](./0004-restore-safety-and-privileges.md) | Restore only below the restore root, with preview, token and confinement; single unprivileged process | Service privilege boundary; restore safety |

Not decided yet, because the spikes were not run: VSS usage, USN checkpoints, chunking and packs, retention and garbage collection, role model and Windows SSO.

---

# 中文

## 架构决策记录

塑造 TimeVault MVP 的各项决定，包括备选方案和影响。每个文件都是双语的（先英文，后简体中文）。plan.md P0 要求为快照语义、数据模型、内容对象格式和服务权限边界编写 ADR；下表说明各项分别在哪里覆盖。

| ADR | 决定 | 覆盖的内容（plan.md P0） |
|---|---|---|
| [0001](./0001-recovery-point-history-model.md) | 在 PostgreSQL 中使用恢复点和按路径的有效区间 | 数据模型；恢复点在目录库中的含义 |
| [0002](./0002-content-addressed-repository.md) | 在独立的仓库文件夹中，每个文件对应一个内容寻址对象 | 内容对象格式 |
| [0003](./0003-live-scan-without-vss-and-usn.md) | MVP 采用实时扫描，而不是 VSS 和 USN（有意偏离 plan.md） | 快照语义（暂定） |
| [0004](./0004-restore-safety-and-privileges.md) | 只恢复到恢复根目录之下，需要预览、令牌和约束；单个非特权进程 | 服务权限边界；恢复安全 |

尚未决定的内容（因为没有做验证）：VSS 的使用、USN 检查点、分块与 Pack、保留策略与垃圾回收、角色模型与 Windows SSO。
