# English

## Status

Accepted for the MVP (2026-11-15). Covers the "content object format" item of plan.md P0.

## Context

A recovery point is only useful if the file contents it refers to survive on storage that is independent of the sources, and a crash during a backup must never leave something that looks like valid content but is not. The format must be simple enough to verify with ordinary tools.

## Decision

- The repository is a folder, separate from every source. It holds `timevault-repository.json` (format version, random repository id, creation time), `objects\` and `tmp\`. A non-empty folder without the marker file is refused, so TimeVault never fills a folder it does not own.
- One object is the **whole content of one file**, addressed by its SHA-256, stored at `objects\ab\cd\<64 hex digits>` (the first two byte pairs of the hash are the folder names). There is no chunking, compression or encryption.
- Writing: the file is streamed into `tmp\<random>.part` while it is hashed, flushed to disk, and then published with an atomic rename under a per-prefix lock, so a crash never leaves a partial object under `objects\`. Identical content is detected by its hash path (and size) and stored once. A damaged object whose size differs is replaced by the good copy. Published objects get the read-only attribute. Before a recovery point is committed, the folders that received new objects are flushed too (`Barrier`), so a power failure is unlikely to leave the catalog pointing at an object whose directory entry was lost; this is best effort and untested against a real power loss.
- The catalog records every stored object (`objects`: hash, size) in the same transaction that writes the versions that use it, so the catalog never refers to an object that was not stored first.
- Only existence and size are checked on every read; the **verify** job re-reads objects and compares the hash (deep mode). Same-size corruption is therefore found only by a deep verify, and restores re-check the hash of every file they write.
- A source cannot overlap the repository or the restore folder: this is checked when the source is added and again when a backup starts, and the scan also skips those folders as a second guard.

## Alternatives not chosen now

- **Chunked, deduplicated packs (restic or borg style).** Saves space for large files with small edits and avoids millions of small files, but needs chunk indexes, pack management and garbage collection. Deferred until the MVP shows it is needed.
- **Compression and encryption.** Both change the object format and the key management story; deferred.

## Consequences

- Easy to inspect and to verify: any SHA-256 tool can check an object against its name.
- A change of one byte in a large file stores a second complete copy of the file.
- Millions of small files become millions of repository files; the two-level fan-out keeps folders small, but file-system overhead is real. The 5-million-file target is not benchmarked.
- Nothing is ever deleted: there is no retention or garbage collection, so the repository only grows.

---

# 中文

## 状态

已接受，用于 MVP（2026-11-15）。对应 plan.md P0 中的"内容对象格式"一项。

## 背景

恢复点只有在它所引用的文件内容保存在与备份源相互独立的存储上时才有用；备份过程中发生崩溃，也绝不能留下看起来像有效内容但实际无效的东西。格式必须足够简单，能用普通工具校验。

## 决定

- 仓库是一个文件夹，与所有备份源分开。它包含 `timevault-repository.json`（格式版本、随机的仓库 id、创建时间）、`objects\` 和 `tmp\`。没有标记文件的非空文件夹会被拒绝，因此 TimeVault 不会往不属于自己的文件夹里写东西。
- 一个对象就是**一个文件的完整内容**，以其 SHA-256 作为地址，存放在 `objects\ab\cd\<64 位十六进制>`（哈希的前两组字节作为文件夹名）。没有分块、压缩或加密。
- 写入：文件在被哈希的同时流式写入 `tmp\<random>.part`，刷新到磁盘，然后在按前缀加的锁保护下通过原子重命名发布，因此崩溃绝不会在 `objects\` 下留下不完整的对象。相同的内容通过其哈希路径（及大小）识别，只存一份。大小不一致的损坏对象会被良好的副本替换。已发布的对象会被设置为只读属性。在恢复点提交之前，接收了新对象的文件夹也会被刷新（`Barrier`），这样断电后目录库指向一个目录项已丢失的对象的可能性就很小；这是尽力而为的做法，没有在真实断电下测试过。
- 目录库在写入使用某个对象的版本的同一个事务中记录每个已存储的对象（`objects`：哈希、大小），因此目录库绝不会引用一个尚未先存储的对象。
- 每次读取只检查存在性和大小；**校验**任务会重新读取对象并比较哈希（深度模式）。所以大小相同的损坏只有深度校验才能发现，而恢复会重新核对它写出的每个文件的哈希。
- 备份源不能与仓库或恢复目录重叠：添加备份源时和每次备份开始时都会检查，扫描时也会跳过这些文件夹作为第二道保护。

## 暂不采用的方案

- **分块去重的 Pack（restic 或 borg 风格）。** 对于小幅修改的大文件能节省空间，也能避免数百万个小文件，但需要分块索引、Pack 管理和垃圾回收。在 MVP 证明确有需要之前暂缓。
- **压缩和加密。** 两者都会改变对象格式和密钥管理方案；暂缓。

## 影响

- 易于检查和校验：任何 SHA-256 工具都能核对对象与其名称是否一致。
- 大文件改动一个字节，就会再存一份完整的文件副本。
- 数百万个小文件会变成仓库中的数百万个文件；两级目录分散让文件夹保持较小，但文件系统开销依然存在。500 万文件的目标没有做过基准测试。
- 不会删除任何内容：没有保留策略和垃圾回收，仓库只会增长。
