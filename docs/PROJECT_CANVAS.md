# TimeVault — Project Canvas

> **Enterprise File Backup, Search & Time Travel**  
> Project kickoff · v0.1 · 2026-11-14  
> English first, Chinese second / 英文在前、中文在后

## English — One-page charter

| Canvas block | Definition |
|---|---|
| **Problem** | Finding historical versions of individual files or entire shared folders is inconvenient; users need reliable recovery after accidental changes or deletion. |
| **Users** | Enterprise file-server administrators, IT operators, and authorized file owners; pilot administrators first. |
| **Value proposition** | Everything-speed current-file search + Apple Time Machine-style browsing of **retained, verifiable recovery points** + controlled restore. |
| **Core scenarios** | Search a file; find an older version; retrieve a deleted file; browse a historical folder; dry-run and restore a complete directory. |
| **Key functions** | Current explorer/search; recovery-point timeline; version details; full/incremental backup; restore preview; job monitor; retention; integrity checks; audit. |
| **Platform** | Windows Server first; ~5 million current files; local NTFS MVP. |
| **Technology** | Go backend and server-rendered Go UI; Everything local HTTP JSON; Windows VSS and NTFS USN; PostgreSQL historian; immutable backup repository. |
| **Security** | Intranet SSO/role and path permissions; independent allowlists; no direct Everything endpoint access; preview before overwrite; audit every restore. |
| **Success metrics** | Load test at ~5M files; stable paginated search; verified old-version and deleted-file restores; safe recovery after interruption; off-host restore drill. |
| **Not in MVP** | Per-second recovery guarantee, bare-metal OS restore, generic database-aware backups, remote SMB agents, multi-site cloud replication. |
| **Main risk** | Search index and change journal are *not* historical content backup; only committed, fully stored recovery points can be restored. |
| **First action** | Measure source volumes/data size/change rate, confirm RPO/RTO/storage, then complete the Everything+USN+VSS technical proof of concept (P0). |

### Delivery roadmap

| Phase | Deliverable | Completion gate |
|---|---|---|
| P0 | Everything, VSS, USN feasibility, 5M benchmark | Controlled test data and recorded results |
| P1 | Secure read-only file explorer | Authorized live search and navigation |
| P2 | Full/incremental backup + history engine | Verified, independently restorable recovery points |
| P3 | File/folder restore | Dry-run, confirmation, verified data, audit |
| P4 | Production hardening | 5M stress tests, failure handling, off-host recovery |

### Decisions to confirm

- Source server(s), volumes, filesystem and protected applications.
- Current data size, file size distribution and daily changed bytes.
- Recovery point interval, retention policy, RPO and RTO.
- Independent backup storage capacity, location and off-host copy policy.
- Windows identity, NTFS ACL visibility and restore authorization.
- Everything version and whether the separate Everything Server plugin is being used.

---

## 中文 — 一页式项目启动画布

| 画布要素 | 内容 |
|---|---|
| **现有问题** | 共享文件缺少方便的历史版本浏览和整目录回溯，误删或误改后的恢复流程较复杂。 |
| **目标用户** | 企业文件服务器管理员、IT 运维人员和获得授权的文件所有者，先从管理员试点。 |
| **核心价值** | Everything 级当前文件搜索 + 类似 Apple Time Machine 的**真实历史恢复点**浏览 + 安全可控的恢复。 |
| **典型场景** | 搜索文件、找回旧版、恢复误删文件、浏览旧目录、预览并恢复整个文件夹。 |
| **主要功能** | 当前文件浏览/搜索、历史时间轴、版本详情、全量和增量备份、恢复预览、任务监控、保留策略、完整性检查和审计。 |
| **部署范围** | 以 Windows Server 为主，当前约 500 万文件，MVP 优先本地 NTFS。 |
| **技术栈** | Go 后端及服务端渲染 Go 页面、Everything 本地 HTTP JSON、VSS 与 USN、PostgreSQL 版本库及独立备份仓库。 |
| **安全要求** | 内网身份认证、角色和路径权限、Everything 端点不直接暴露、覆盖前预览确认、恢复完整审计。 |
| **验收标准** | 通过 500 万文件规模验证；分页搜索稳定；旧版及误删文件可校验恢复；中断后可安全继续；通过异机恢复演练。 |
| **首版不做** | 每秒精度恢复保证、裸机 OS 恢复、通用数据库一致性备份、远程 SMB 代理及跨站点云复制。 |
| **最大风险** | Everything 和 USN 不保存历史文件内容；只有实际备份成功的恢复点才能提供真正恢复。 |
| **第一步** | 测量文件系统规模和日变化量，确认恢复策略与仓库，再完成 Everything + USN + VSS 的 P0 技术验证。 |

### 开发路线

| 阶段 | 主要交付物 | 通过条件 |
|---|---|---|
| P0 | Everything / VSS / USN 验证、500 万文件性能 PoC | 隔离测试与书面测试结果 |
| P1 | 只读文件浏览器 | 权限安全的当前文件搜索与目录浏览 |
| P2 | 全量/增量备份与历史引擎 | 具备校验能力的真实恢复点 |
| P3 | 文件/文件夹恢复 | 支持预览、确认、恢复校验和审计 |
| P4 | 生产稳定化 | 完成规模、故障、异机恢复验证 |

### 需要确认的决定

- 哪些服务器、磁盘卷、文件系统和应用被纳入备份。
- 总数据容量、文件大小分布和每天变更的数据量。
- 恢复点频率、版本保留周期、RPO、RTO。
- 备份仓库容量、位置，以及是否有异机/离线副本要求。
- Windows 账户认证、NTFS ACL 可见性与覆盖权限。
- Everything 版本及是否使用独立的 Everything Server 插件。

> **Important / 重要：** This is a product canvas and an interactive UI concept, not a running backup system. / 此文件为产品启动画布，尚未连接真实文件服务器或备份服务。
