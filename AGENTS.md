# Project Engineering Instructions

These instructions apply to all AI-assisted development in this repository.

本说明适用于本代码仓库中的所有 AI 辅助开发工作。

---

# English

## 1. General Principles

Prefer correctness, maintainability, compatibility, and clarity over clever solutions.

Before changing existing behavior, understand how the current implementation works.

Reuse existing architecture, patterns, utilities, services, and conventions whenever possible.

Keep changes focused on the requested task.

Avoid unnecessary refactoring, speculative redesign, and unrelated cleanup.

Do not claim that a task, test, verification step, or migration was completed unless it was actually performed.

## 2. Architecture

Do not introduce a new architectural pattern when the existing architecture can reasonably support the requirement.

Prefer existing:

- services
- repositories
- utilities
- components
- configuration mechanisms
- logging systems
- error-handling patterns
- database access patterns
- routing conventions
- background-job patterns
- authentication and authorization mechanisms

Avoid duplicate implementations and parallel frameworks for the same responsibility.

If a major architectural change is necessary, explain why the existing architecture is insufficient before implementing the change.

## 3. Code Changes

Make the smallest complete change that satisfies the requirement.

Do not modify unrelated files.

Do not rename public interfaces, routes, APIs, database objects, or configuration keys without a clear requirement.

Do not remove working behavior simply because another implementation appears cleaner.

Do not leave temporary debug code, temporary files, commented-out obsolete implementations, or unused experimental code.

Preserve backward compatibility unless the approved plan explicitly requires a breaking change.

Before modifying shared code, inspect relevant call sites and assess regression risk.

## 4. Configuration

Do not hard-code:

- machine-specific file paths
- IP addresses
- server hostnames
- credentials
- passwords
- API keys
- access tokens
- environment-specific URLs
- environment-specific database connection strings

Use the project's existing configuration mechanism, such as environment variables or configuration files.

Do not expose secret values in source code, documentation, logs, screenshots, test output, or generated examples.

## 5. Database

Do not perform destructive database operations without explicit approval.

Preserve backward compatibility whenever practical.

For database changes, consider:

- existing data
- migrations
- indexes
- constraints
- nullability
- transactions
- rollback behavior
- concurrency
- duplicate insertion
- data integrity
- performance impact

Use parameterized queries.

Do not construct SQL using untrusted string concatenation.

Do not run production migrations, destructive DDL, bulk data changes, or irreversible operations unless explicitly requested and approved.

When inspecting a database for planning or analysis, prefer read-only queries and minimize query scope.

## 6. API

Preserve existing API contracts unless a breaking change is explicitly required.

Validate external input.

Return predictable error responses.

Do not expose stack traces, credentials, internal file paths, SQL statements containing secrets, or sensitive implementation details to end users.

When modifying an API, inspect known callers and related frontend/backend integrations before finalizing the change.

## 7. Security

Never place secrets in source code.

Do not expose:

- passwords
- API keys
- access tokens
- private keys
- connection credentials
- authentication tokens

Treat authentication and authorization as separate concerns.

Never trust identity, role, permission, path, filename, SQL fragment, shell argument, or other sensitive input supplied directly by an untrusted client.

Use the project's existing security controls where available.

## 8. Error Handling and Logging

Do not silently ignore exceptions.

Handle errors at the appropriate layer.

Use the project's existing logging mechanism.

Provide enough logging to diagnose failures without exposing secrets or sensitive information.

Do not use broad exception handling merely to suppress a real implementation problem.

## 9. Testing and Verification

Changes should be verified proportionally to their risk.

Prefer this order:

1. targeted tests
2. affected integration tests
3. broader regression tests
4. relevant manual workflow verification

Do not modify or weaken tests only to hide a real implementation failure.

Do not claim that a test passed unless it was actually executed and passed.

If verification cannot be completed, clearly state what was not verified and why.

## 10. Dependency Management

Do not add a new dependency when the existing stack already provides equivalent functionality.

If a dependency is necessary:

- explain why it is needed
- prefer a maintained and reputable package
- minimize version conflicts
- follow the project's existing dependency-management method

Do not upgrade unrelated dependencies unless necessary for the requested task.

## 11. Documentation Language Requirements

All documentation created or modified by AI agents MUST be bilingual in English and Simplified Chinese.

### 11.1 Language Order

English MUST always appear first.

The required order is:

1. Complete English version
2. Complete Simplified Chinese version

Do not place Chinese before English.

### 11.2 Scope

This rule applies to all documentation content, including but not limited to:

- README files
- Markdown documentation
- implementation plans
- design documents
- architecture documents
- technical specifications
- user guides
- deployment guides
- operation guides
- troubleshooting guides
- API documentation
- database documentation
- migration documents
- release notes
- change logs
- project summaries
- test plans
- handover documents
- generated documentation files

### 11.3 Required Document Structure

Unless there is a strong reason to use another format, documentation MUST use the following structure:

- complete English version first
- horizontal separator if useful
- complete Simplified Chinese version second

The Chinese version MUST mirror the structure and technical content of the English version.

English MUST always be the first complete version.

Recommended structure:

```text
# English

## Overview
...

## Architecture
...

## Database Design
...

---

# 中文

## 概述
...

## 架构
...

## 数据库设计
...
```

### 11.4 Translation Consistency

The English and Chinese versions MUST convey the same technical meaning.

Do not:

- omit information from either language
- simplify one language while keeping the other detailed
- introduce technical information in only one language
- change requirements between language versions
- change code examples between language versions
- change technical identifiers between language versions

Technical terms and identifiers should normally remain unchanged, including:

- product names
- framework names
- file names
- directory paths
- API paths
- database names
- schema names
- table names
- column names
- class names
- function names
- variable names
- environment variables
- configuration keys
- command names

Examples of identifiers that should normally remain unchanged:

- `FastAPI`
- `PostgreSQL`
- `job_id`
- `process_module`
- `DATABASE_URL`
- `/api/jobs`
- `JobService`

### 11.5 Code Blocks

Do not duplicate identical code blocks solely for translation.

Write the English explanation first and the Chinese explanation second, then provide one shared code block when the code itself is identical.

Example:

English:
Configure the database connection using the environment variable below.

中文：
使用以下环境变量配置数据库连接。

```env
DATABASE_URL=postgresql://...
```

Always close fenced code blocks correctly before continuing with normal Markdown content.

Never place unrelated documentation sections inside a code block.

### 11.6 Tables

For small documentation tables, bilingual text may be placed in the same table when it improves readability.

Example:

| Item | Description / 描述 |
|---|---|
| Database | Stores application data. / 存储应用程序数据。 |
| Redis | Used for caching and background task coordination. / 用于缓存和后台任务协调。 |

For large or complex tables, separate English and Chinese tables are preferred. English MUST appear first.

### 11.7 Existing Documentation

When modifying an existing documentation file:

- preserve existing technical content unless the task requires a change
- add the missing language when necessary
- keep English before Chinese
- keep both language versions synchronized
- update both language versions when technical content changes

If an existing document is English-only and the task requires modifying that document, update the affected documentation so that the modified document follows this bilingual requirement whenever practical.

### 11.8 Documentation Generated During Development

When an agent creates documentation as part of a task, the documentation MUST follow this bilingual requirement even if the user does not explicitly repeat the bilingual requirement in that task.

This includes automatically generated planning, design, implementation, review, migration, deployment, and handover documents.

### 11.9 Exceptions

The bilingual documentation requirement does NOT automatically apply to:

- source code
- variable names
- function names
- class names
- database identifiers
- API paths
- configuration keys
- log messages
- exception messages
- Git commit messages
- inline source-code comments

unless the task explicitly requires bilingual content for those items.

## 12. Documentation Quality

Documentation must describe the actual implementation, not an assumed implementation.

Before documenting architecture, APIs, database structures, configuration, or workflows, inspect the relevant implementation when available.

Do not document features as completed if they are only planned.

Do not invent paths, APIs, tables, fields, configuration keys, commands, or behavior that do not exist.

Clearly distinguish among:

- current behavior
- planned behavior
- assumptions
- optional recommendations

Write each kind of documentation where it belongs:

- `AGENTS.md` records only what applies to every app: engineering rules, infrastructure and debugging lessons.
- Documentation of a single module (a page, a crawler, a sync or a task) goes to its own file in `docs/`, together with the lessons learned about that module. Do not add it to `AGENTS.md`.
- `docs/README.md` is the index of those files. Look a module up there before changing or debugging it, and add every new document to it.

## 13. Completion

A task is not complete simply because code was generated.

Completion requires, where applicable:

- implementation
- validation
- regression consideration
- cleanup of temporary artifacts
- synchronized documentation
- clear reporting of unresolved issues

Before finishing, review the final changes for accidental edits, debugging leftovers, hard-coded values, missing error handling, missing tests, and documentation inconsistency.

## 14. Known Pitfalls

Lessons learned from real defects in this project that apply to every app. Check them before debugging a similar symptom. Add new entries as 14.3, 14.4, and so on, in both languages. Lessons about a single module belong in that module's document in `docs/`, not here.

### 14.1 Dash renderer: a page swapped into `#app-content` goes blank or shows stale content after the first click

Found on 2026-11-11 (HC monthly summary by process, Leave button) with the Dash 3.2.0 renderer. Reproduced locally with the real app, fixed in `build_process_summary_div` and confirmed by testing. A similar symptom had been fixed once before without finding the cause, so this may explain other past incidents.

Symptoms:

- The page was opened by a callback that rebuilds `app-content` (tab or mode switch, drill-down, Back), not by F5 or a direct URL.
- The first click inside the page makes the whole page disappear (the banner and tabs stay), or an older build of the page reappears. The page is not dimmed by a loading mask, the click sends no callback request, and the console shows no error.
- F5 fixes it. It looks random because it depends on which pages were opened earlier in the same browser session.

Root cause:

- A component without an `id` gets its React key from its position in the layout. The renderer keeps a change counter per position (`layoutHashes`), and the counter is never reset when another component later takes that position.
- When the counter is not 0, the next re-render of the parent redraws the old component that used to sit at that position instead of the new one. The first click inside `#app-content` triggers it, because Dash counts `n_clicks` on every `html.Div` that has an id, `app-content` included.
- The children of `app-content` are one flat list, `[generate_visit_monitor(data), *build_tabN(data)]`, and `build_tab9` returns `[dmc.Center(...), *hc_div]`. Different HC pages therefore share the same positions from index 2. A page that leaves a non-zero counter there can break the next page opened at that position. HC Data Input does this: its first component, `mqpd-hc-daily-attn-table-container`, is a `FefferyDiv` that updates its own props on mouse and click events.

Rule:

- The root component of a page that a callback swaps into `app-content` must have an `id` that is unique per build, for example `id={'type': 'process-summary-page', 'index': gen_nanoid()}`. React then remounts the page instead of reusing the old wrapper.
- A fixed id is not enough. Rebuilding the same page in place keeps the same key, and the next click brings back the previous build (tested with one id per process).
- Cost: the whole page remounts on every rebuild, including the idle auto refresh, so AG Grid is recreated and the scroll position resets, as after F5. Do not reference this id in callbacks, because it changes on every build.
- Applied so far: `build_process_summary_div` only. Other page builders have not been audited. Apply the rule to a page that shows these symptoms, after checking that losing client state on rebuild is acceptable (for example unsaved edits in an input form).

How to confirm in production: open the page the same way the user did (no F5), do not click yet, and run the code below in the browser console (Dash 3.x exposes `window.store`). Replace `2` with the child index of the blank area under `#app-content`. In the local reproduction the page went blank on the first click whenever the value was not 0, and never when it was 0.

```js
(() => { const s = window.store.getState(); const p = s.paths.strs['app-content'].concat(['props', 'children', 2]).join(','); return s.layoutHashes[p]?.hash || 0 })()
```

Ruled out, so do not spend time on them first: the loading mask, AG Grid data, row count, `rowSpan` and style-function errors (these raise red console errors and the rest of the page stays), server latency and HTTP errors, and the browser or display scaling.

References: plotly/dash#3330 reports a similar problem (children replaced by a callback are not remounted). Per the upstream changelog, #3570 (Dash 4.2.0) addressed it, and #3881 (Dash 4.5.0) fixed a regression of that change and added `dash.remount`. Upgrading Dash was not tried with this project's component libraries.

### 14.2 Popup windows: a popup suddenly shrinks to a small box with scrollbars when a control inside it is used (Plan "Options Settings", Process select)

Found on 2026-11-12 with the Dash 3.2.0 renderer (Plan tab, Options Settings popup, Process select). Reproduced with the real IE app in headless Chrome, fixed with `popup_modal_props()` in `IE.py`, and confirmed in the browser and with `tests/test_popup_window.py`.

Symptoms:

- A popup opened into `popup-window` looks correct when it opens. After the first click on a control inside it (here the Process select), the `.modal` turns into a box of about 300 x 180 px with scrollbars, a transparent background and no page dimming, and the content is squeezed to about 267 px.
- The console shows no error. F5 fixes it. It looks random because it depends on which popups were opened earlier in the same browser session.

Root cause:

- Same renderer defect as 14.1, on the `popup-window` slot. Every popup is rebuilt into that one container as `[generate_visit_monitor(data), html.Div(className='modal', ...)]`. A component without an `id` is keyed by its position, the renderer keeps a change counter per position (`layoutHashes`) that is never reset, and when the counter is not 0 the next re-render of the wrapper redraws the props of the earlier component of that position.
- The Menu popup (`build_menu_window`, `id="menu-window"`), Learn More (`id="markdown"`) and Login (`id="auth-window"`) have an `id` on their `.modal`, so every click inside them raises `n_clicks` and leaves a non-zero counter at `popup-window.props.children[1]`. The `.modal` of `build_popup_window` and `build_right_click_menu` had no `id`, so the next popup reused that wrapper. The first re-render after the new popup was open (the click on the Process select) redrew the old style around the new content, in the reproduction the Kanban right-click menu style (`width: 30rem; height: 18rem; background-color: transparent`).
- Reproduction: open the Menu popup, click inside it twice, close it, open a Kanban right-click menu, dismiss it, open Plan > Options Settings, click the Process select. A fresh page, the Kanban menu alone, and a progress popup before the Plan popup do not reproduce it. It was reproduced with the Menu popup; Learn More and Login have the same structure but were not tried.

Rule:

- The root `.modal` of every popup built into `popup-window` gets a unique `id` per build and `disable_n_clicks=True`, through `**popup_modal_props()`. Do not reference this id in callbacks, because it changes on every build.
- Applied so far: `build_popup_window` and both branches of `build_right_click_menu` in `IE.py`. The fixed-id popups (Menu, Learn More, Login) were left as they are, and the `.modal` builders of `QDB.py`, `MER.py`, `QAQC.py` and `test.py` were not audited. Apply the rule to a popup that shows these symptoms.
- Cost: the whole popup remounts on every rebuild. A callback that returns `build_popup_window(...)` again for a popup that is already open (for example `tab1_hourly_output_input_selection_onchange`) now recreates the popup, so client-only state such as a grid scroll position is lost, as when the popup is opened again.

How to confirm in production: open the popups in the same order as the user, do not click yet, and run the code below in the browser console (Dash 3.x exposes `window.store`). In the reproduction the value was 3 while the Plan popup was open, and the shrink happened on the next click. With the fix the value can still be non-zero and is harmless, because the new `.modal` has its own key, so check the fix by clicking the control instead.

```js
(() => { const s = window.store.getState(); const p = s.paths.strs['popup-window'].concat(['props', 'children', 1]).join(','); return s.layoutHashes[p]?.hash || 0 })()
```

Ruled out, so do not spend time on them first: the Plan settings content and the Process select options (a fresh page does not reproduce it), `plan_table_setting_onclick`, the CSS of `.modal` (the inline style of the element itself changed), and the browser or display scaling.

## 15. Git Commits and GitHub Sync

Commit only when the user explicitly asks for a commit in that request. Do not commit on your own initiative, when a task is finished, or because an earlier commit in the same session was requested. Without such a request, leave the changes in the working tree and say that they are not committed.

When the user asks for a commit, sync it with GitHub as part of the same request: fetch the remote, integrate any new remote commits (rebase, so that the history stays linear), then push the current branch to `origin`, and report the commit hash and the result of the push. If the sync cannot be completed (a conflict, a rejected push, no access, a history that has diverged), stop, report what happened and ask; never force-push, and never push a branch other than the one that was committed to.

---

# 中文

## 1. 通用原则

优先考虑正确性、可维护性、兼容性和清晰度，而不是追求过度复杂或炫技式的解决方案。

在修改现有行为之前，先理解当前实现的工作方式。

尽可能复用现有架构、模式、工具、服务和项目约定。

修改范围应聚焦于当前任务。

避免不必要的重构、推测性的架构重设计以及与任务无关的清理工作。

除非实际已经执行，否则不得声称某项任务、测试、验证步骤或迁移已经完成。

## 2. 架构

如果现有架构可以合理支持需求，不要引入新的架构模式。

优先使用现有的：

- services
- repositories
- utilities
- components
- 配置机制
- 日志系统
- 错误处理模式
- 数据库访问模式
- 路由约定
- 后台任务模式
- 身份验证和授权机制

避免为相同职责创建重复实现或并行框架。

如果确实需要进行较大的架构调整，应在实施前说明现有架构为什么无法满足需求。

## 3. 代码修改

采用能够完整满足需求的最小修改范围。

不要修改与任务无关的文件。

除非需求明确要求，否则不要重命名公共接口、路由、API、数据库对象或配置项。

不要仅仅因为另一种实现看起来更简洁，就删除目前正常工作的行为。

不要遗留临时调试代码、临时文件、已废弃的注释代码或未使用的实验代码。

除非已批准的方案明确要求破坏性变更，否则应保持向后兼容。

修改共享代码前，应检查相关调用位置并评估回归风险。

## 4. 配置

不要硬编码：

- 特定机器的文件路径
- IP 地址
- 服务器主机名
- 凭据
- 密码
- API key
- access token
- 特定环境 URL
- 特定环境数据库连接字符串

应使用项目现有的配置机制，例如环境变量或配置文件。

不要在源代码、文档、日志、截图、测试输出或生成的示例中暴露机密值。

## 5. 数据库

未经明确批准，不要执行破坏性的数据库操作。

在可行的情况下保持向后兼容。

数据库变更应考虑：

- 现有数据
- migration
- index
- constraint
- nullability
- transaction
- rollback behavior
- concurrency
- duplicate insertion
- data integrity
- performance impact

使用参数化查询。

不要通过拼接不可信字符串构造 SQL。

除非得到明确请求和批准，否则不要执行生产环境 migration、破坏性 DDL、大批量数据修改或不可逆操作。

在规划或分析阶段检查数据库时，应优先使用只读查询，并尽量缩小查询范围。

## 6. API

除非明确需要破坏性变更，否则应保持现有 API contract。

验证外部输入。

返回可预测的错误响应。

不要向最终用户暴露 stack trace、凭据、内部文件路径、包含机密信息的 SQL 或其他敏感实现细节。

修改 API 时，在最终确认前检查已知调用方以及相关前后端集成。

## 7. 安全

绝不要把机密信息写入源代码。

不要暴露：

- 密码
- API key
- access token
- private key
- connection credential
- authentication token

身份验证和授权必须作为两个独立问题处理。

绝不能直接信任来自不可信客户端的身份、角色、权限、路径、文件名、SQL 片段、shell 参数或其他敏感输入。

如果项目已经存在安全控制机制，应优先复用。

## 8. 错误处理与日志

不要静默忽略异常。

应在正确的层级处理错误。

使用项目现有日志机制。

日志应包含足够的诊断信息，但不得暴露机密或敏感内容。

不要为了掩盖真实实现问题而使用过于宽泛的异常捕获。

## 9. 测试与验证

修改应按照风险程度进行相应验证。

推荐顺序：

1. 针对性测试
2. 受影响的集成测试
3. 更广泛的回归测试
4. 相关手工流程验证

不要仅仅为了让测试通过而修改或削弱测试，从而掩盖真实实现问题。

除非测试实际执行并通过，否则不得声称测试已经通过。

如果无法完成某项验证，应明确说明未验证的内容及原因。

## 10. 依赖管理

如果现有技术栈已经提供等效功能，不要引入新的依赖。

如果确实需要新增依赖：

- 说明为什么需要
- 优先使用维护活跃且可信的包
- 尽量减少版本冲突
- 遵循项目现有的依赖管理方式

除非当前任务确实需要，否则不要升级无关依赖。

## 11. 文档语言要求

所有由 AI Agent 创建或修改的文档都必须同时提供英文和简体中文版本。

### 11.1 语言顺序

英文必须始终放在前面。

固定顺序为：

1. 完整英文版
2. 完整简体中文版

不要把中文放在英文之前。

### 11.2 适用范围

此规则适用于所有文档内容，包括但不限于：

- README 文件
- Markdown 文档
- implementation plan
- design document
- architecture document
- technical specification
- user guide
- deployment guide
- operation guide
- troubleshooting guide
- API documentation
- database documentation
- migration document
- release note
- change log
- project summary
- test plan
- handover document
- 自动生成的文档文件

### 11.3 文档结构要求

除非存在明确理由采用其他格式，否则文档必须使用以下结构：

- 先提供完整英文版
- 必要时使用水平分隔线
- 再提供完整简体中文版

中文版必须与英文版保持相同的结构和技术内容。

英文必须始终作为第一个完整版本。

推荐结构：

```text
# English

## Overview
...

## Architecture
...

## Database Design
...

---

# 中文

## 概述
...

## 架构
...

## 数据库设计
...
```

### 11.4 翻译一致性

英文版和中文版必须表达相同的技术含义。

禁止：

- 任一语言遗漏内容
- 一个版本被简化，而另一个版本保留详细内容
- 只在某一种语言中加入技术信息
- 两种语言中的需求不一致
- 两种语言使用不同的代码示例
- 两种语言修改技术标识符

以下技术术语和标识符通常应保持原样：

- 产品名称
- 框架名称
- 文件名
- 目录路径
- API path
- database name
- schema name
- table name
- column name
- class name
- function name
- variable name
- environment variable
- configuration key
- command name

通常不应翻译的标识符示例：

- `FastAPI`
- `PostgreSQL`
- `job_id`
- `process_module`
- `DATABASE_URL`
- `/api/jobs`
- `JobService`

### 11.5 代码块

不要仅为了翻译而重复完全相同的代码块。

当代码本身相同时，应先写英文说明，再写中文说明，然后只提供一个共享代码块。

示例：

English:
Configure the database connection using the environment variable below.

中文：
使用以下环境变量配置数据库连接。

```env
DATABASE_URL=postgresql://...
```

继续正常 Markdown 内容之前，必须正确关闭 fenced code block。

绝不能因为代码块没有正确关闭而把后续无关文档内容包含在代码块中。

### 11.6 表格

对于较小的文档表格，如果能提升可读性，可以在同一个表格中提供双语内容。

示例：

| Item | Description / 描述 |
|---|---|
| Database | Stores application data. / 存储应用程序数据。 |
| Redis | Used for caching and background task coordination. / 用于缓存和后台任务协调。 |

对于大型或复杂表格，优先使用独立的英文表格和中文表格。英文必须放在前面。

### 11.7 现有文档

修改现有文档时：

- 除非任务要求修改，否则保留现有技术内容
- 必要时补充缺失的语言版本
- 保持英文在前、中文在后
- 保证两个语言版本同步
- 技术内容发生变化时，同时更新两个语言版本

如果现有文档只有英文，而当前任务需要修改该文档，则在可行的情况下，应使修改后的文档符合双语要求。

### 11.8 开发过程中生成的文档

Agent 在任务过程中创建任何文档时，即使用户没有在该任务中再次明确说明双语要求，该文档仍必须遵守本双语规则。

这包括自动生成的 planning、design、implementation、review、migration、deployment 和 handover 文档。

### 11.9 例外

以下内容默认不要求中英文双语：

- source code
- variable name
- function name
- class name
- database identifier
- API path
- configuration key
- log message
- exception message
- Git commit message
- inline source-code comment

除非当前任务明确要求这些内容也使用双语。

## 12. 文档质量

文档必须描述实际实现，而不是假设中的实现。

在记录架构、API、数据库结构、配置或工作流程之前，如果相关实现可用，应先检查真实实现。

如果某项功能仍处于计划阶段，不要把它写成已经完成。

不要虚构不存在的路径、API、表、字段、配置项、命令或行为。

必须清楚区分：

- 当前行为
- 计划行为
- 假设
- 可选建议

文档要写在应该写的位置：

- `AGENTS.md` 只记录适用于所有 app 的内容：工程规则、基础设施和调试经验。
- 单个 module（页面、crawler、同步任务或定时任务）的文档写入 `docs/` 下它自己的文件，该 module 的经验教训也写在同一个文件里，不要加到 `AGENTS.md`。
- `docs/README.md` 是这些文件的索引。修改或排查某个 module 之前先在其中查找，新增文档时也要把它加到索引中。

## 13. 完成标准

仅生成代码并不意味着任务已经完成。

在适用情况下，完成标准包括：

- 实现完成
- 验证完成
- 考虑回归风险
- 清理临时产物
- 文档保持同步
- 清楚报告未解决问题

结束任务前，应检查最终修改是否存在意外变更、调试残留、硬编码值、缺失错误处理、缺失测试或中英文文档不一致的问题。

## 14. 已知问题与经验

来自本项目真实缺陷的经验，仅收录适用于所有 app 的内容。再次排查类似现象前先查阅。新增条目请按 14.3、14.4……编号，并同时更新中英文两个版本。只涉及单个 module 的经验写入 `docs/` 中该 module 的文档，不要写在这里。

### 14.1 Dash 渲染器：通过回调换入 `#app-content` 的页面在第一次点击后变空白或显示旧内容

发现于 2026-11-11（HC monthly summary by process 页面的 Leave 按钮），Dash 3.2.0 渲染器。已用真实应用在本地复现，在 `build_process_summary_div` 中修复，并经测试确认。此前曾出现过类似现象，当时修好了但没有找到原因，所以它可能也能解释其他历史问题。

现象：

- 页面是通过重建 `app-content` 的回调打开的（切换标签页或模式、下钻、Back），而不是 F5 或直接打开 URL。
- 在页面内第一次点击后，整个页面消失（顶部横幅和标签页还在），或者页面的旧版本重新出现。页面没有被加载遮罩变暗，这次点击没有发出任何回调请求，控制台也没有报错。
- F5 后恢复正常。看起来是随机的，因为它取决于同一浏览器会话中之前打开过哪些页面。

根因：

- 没有 `id` 的组件，其 React key 取决于它在布局中的位置。渲染器为每个位置保存一个变更计数（`layoutHashes`），之后即使其他组件占用了该位置，这个计数也不会清零。
- 计数不为 0 时，父组件下一次重新渲染会重绘原先位于该位置的旧组件，而不是新组件。在 `#app-content` 内的第一次点击就会触发，因为 Dash 会对每个带 id 的 `html.Div`（包括 `app-content`）累加 `n_clicks`。
- `app-content` 的子节点是一个扁平列表 `[generate_visit_monitor(data), *build_tabN(data)]`，而 `build_tab9` 返回 `[dmc.Center(...), *hc_div]`。因此不同的 HC 页面从下标 2 开始共用相同的位置。如果某个页面在该位置留下了非 0 的计数，就可能让随后在该位置打开的页面出问题。HC Data Input 就是这样：它的第一个组件 `mqpd-hc-daily-attn-table-container` 是 `FefferyDiv`，会在鼠标和点击事件中更新自己的属性。

规则：

- 由回调换入 `app-content` 的页面，其根组件必须带一个每次构建都唯一的 `id`，例如 `id={'type': 'process-summary-page', 'index': gen_nanoid()}`。这样 React 会重新挂载页面，而不是复用旧的 wrapper。
- 固定 id 不够。原地重建同一页面时 key 不变，下一次点击会让上一次构建的内容重新出现（已用"每个 process 一个固定 id"测试）。
- 代价：每次重建（包括空闲自动刷新）整个页面都会重新挂载，AG Grid 重新创建，滚动位置重置，效果和 F5 一样。不要在回调中引用这个 id，因为它每次构建都会变。
- 目前只应用于 `build_process_summary_div`。其他页面构建函数没有逐一检查。仅对出现上述现象的页面应用该规则，并先确认重建时丢失前端状态是可以接受的（例如输入表单中未保存的编辑内容）。

如何在生产环境确认：按用户的方式打开页面（不要 F5），先不要点击，在浏览器控制台运行下面的代码（Dash 3.x 暴露了 `window.store`）。把 `2` 换成 `#app-content` 下空白区域对应的子节点下标。本地复现中，该值不为 0 时第一次点击必定空白，为 0 时从未空白。

```js
(() => { const s = window.store.getState(); const p = s.paths.strs['app-content'].concat(['props', 'children', 2]).join(','); return s.layoutHashes[p]?.hash || 0 })()
```

已排除，所以不要优先在这些方向上花时间：加载遮罩、AG Grid 数据、行数、`rowSpan` 和样式函数错误（这些会在控制台报红色错误，页面其余部分仍在）、服务器延迟和 HTTP 错误、浏览器或显示缩放。

参考：plotly/dash#3330 报告了类似问题（回调替换的子节点没有重新挂载）。根据上游 changelog，#3570（Dash 4.2.0）处理了它，#3881（Dash 4.5.0）修复了该改动引入的回归并新增了 `dash.remount`。尚未在本项目的组件库上试过升级 Dash。

### 14.2 弹出窗口：操作窗口内的控件时，弹窗突然缩成带滚动条的小框（Plan 的 "Options Settings"，选择 Process 时）

发现于 2026-11-12，Dash 3.2.0 渲染器（Plan 标签页，Options Settings 弹窗，Process 下拉框）。已用真实的 IE 应用在无头 Chrome 中复现，通过 `IE.py` 中的 `popup_modal_props()` 修复，并在浏览器和 `tests/test_popup_window.py` 中确认。

现象：

- 打开到 `popup-window` 中的弹窗，刚打开时是正常的。第一次点击窗口内的控件（这里是 Process 下拉框）后，`.modal` 变成约 300 x 180 px 的小框，带滚动条、背景透明、页面不再变暗，内容被挤到约 267 px 宽。
- 控制台没有报错。F5 后恢复正常。看起来是随机的，因为它取决于同一浏览器会话中之前打开过哪些弹窗。

根因：

- 与 14.1 是同一个渲染器缺陷，发生在 `popup-window` 这个位置上。所有弹窗都被重建到这同一个容器中，结构为 `[generate_visit_monitor(data), html.Div(className='modal', ...)]`。没有 `id` 的组件，其 key 取决于它所在的位置；渲染器为每个位置保存一个从不清零的变更计数（`layoutHashes`）；计数不为 0 时，该位置的 wrapper 在下一次重新渲染时，会重绘这个位置上更早的那个组件的属性。
- Menu 弹窗（`build_menu_window`，`id="menu-window"`）、Learn More（`id="markdown"`）和 Login（`id="auth-window"`）的 `.modal` 带有 `id`，所以在它们里面的每次点击都会累加 `n_clicks`，并在 `popup-window.props.children[1]` 上留下非 0 的计数。`build_popup_window` 和 `build_right_click_menu` 的 `.modal` 没有 `id`，所以下一个弹窗复用了这个 wrapper。新弹窗打开后的第一次重新渲染（点击 Process 下拉框）会在新内容外面重绘旧样式，复现中重绘的是 Kanban 右键菜单的样式（`width: 30rem; height: 18rem; background-color: transparent`）。
- 复现步骤：打开 Menu 弹窗，在里面点击两次，关闭，打开 Kanban 右键菜单，让它消失，打开 Plan > Options Settings，点击 Process 下拉框。全新页面、只打开 Kanban 菜单、以及在 Plan 弹窗之前打开 progress 弹窗，都不会复现。复现用的是 Menu 弹窗；Learn More 和 Login 的结构相同，但没有试过。

规则：

- 所有构建到 `popup-window` 中的弹窗，其根 `.modal` 必须通过 `**popup_modal_props()` 获得每次构建都唯一的 `id`，并设置 `disable_n_clicks=True`。不要在回调中引用这个 id，因为它每次构建都会变。
- 目前已应用于：`IE.py` 中的 `build_popup_window` 和 `build_right_click_menu` 的两个分支。固定 id 的弹窗（Menu、Learn More、Login）保持不变，`QDB.py`、`MER.py`、`QAQC.py` 和 `test.py` 中的 `.modal` 构建函数没有检查过。对出现这些现象的弹窗应用该规则。
- 代价：每次重建都会重新挂载整个弹窗。对已经打开的弹窗再次返回 `build_popup_window(...)` 的回调（例如 `tab1_hourly_output_input_selection_onchange`）现在会重新创建弹窗，所以只存在于前端的状态（如表格滚动位置）会丢失，效果与重新打开弹窗相同。

如何在生产环境确认：按用户的顺序打开弹窗，先不要点击，在浏览器控制台运行下面的代码（Dash 3.x 暴露了 `window.store`）。复现中 Plan 弹窗打开时该值为 3，下一次点击就缩小了。修复后该值仍可能不为 0，但没有影响，因为新的 `.modal` 有自己的 key，所以请通过点击控件来验证修复，而不是看这个计数。

```js
(() => { const s = window.store.getState(); const p = s.paths.strs['popup-window'].concat(['props', 'children', 1]).join(','); return s.layoutHashes[p]?.hash || 0 })()
```

已排除，所以不要优先在这些方向上花时间：Plan settings 的内容和 Process 下拉框选项（全新页面不会复现）、`plan_table_setting_onclick`、`.modal` 的 CSS（变化的是元素自身的 inline style），以及浏览器或显示缩放。

## 15. Git 提交与 GitHub 同步

只有在用户于该次请求中明确要求提交（commit）时才提交。不要自行决定提交，不要在任务完成时顺手提交，也不要因为同一会话中之前应要求提交过就再次提交。没有这样的要求时，把修改留在工作区，并说明它们尚未提交。

用户要求提交时，把同步到 GitHub 作为同一请求的一部分：先 fetch 远程，整合远程的新提交（用 rebase，让历史保持线性），再把当前分支 push 到 `origin`，并报告提交哈希和 push 的结果。如果同步无法完成（冲突、push 被拒绝、没有权限、历史已分叉），就停下来，说明发生了什么并询问用户；绝不 force-push，也绝不推送被提交的分支之外的其他分支。
