# English

## Status

Accepted for 0.1.3 (2026-11-16). Changes how the lists of [0001](./0001-recovery-point-history-model.md)'s history and the live folder are shown; the history model itself is untouched.

## Context

- Up to 0.1.2 the Explorer and the Time Machine read 200 rows at a time behind a **Load more** button, and sorted the rows that had been loaded in the browser. A folder of 10,000 files could not be sorted by size, a file could not be found by its kind, and the way to the bottom of the page was a long scroll.
- Some folders hold tens of thousands of files.
- The list needs a **Type** column that people can sort and filter by.

## Decision

- **The lists are cut on the server, in memory, by one engine.** The package `internal/listing` takes all the children of a folder and returns the requested page: `Apply(items, view, options)` filters by type, sorts, cuts the window and counts the types of the whole folder. The live listing (`Service.ListLive`, reading the folder through `os.Root`), the history listing (`Service.ListHistory`, one SQL query without `LIMIT`, `Store.ListAllChildren`) and the API use it, so the page and the API cannot disagree.
- **Types.** `dir`, `link` (a junction or symbolic link), `noext` (a file without an extension) and `ext:<extension>`, lower case. The extension is the text after the last dot, 1 to 16 characters without white space (`Report. Final version` has none). The filter also knows `file` for everything that is not a folder. The filter offers the types that exist in the folder with their counts (at most 200 kinds), counted over the whole folder, whatever the current filter.
- **Sorting** by `name`, `type`, `size` or `mtime`, ascending or descending. The order is total (ties fall back to the case-insensitive name, then the exact name, then the position), so the same input always gives the same pages. The pages ask for folders first (`DirsFirst`) whatever the direction; the API does so only with `dirs_first=true`, because the order of the API without parameters had been by name only.
- **Pages.** `page` and `per_page` in the address (page sizes 50, 100, 200 or 500; 50, 100 or 200 for search results; default 100), with the sort (`sort`, `order=desc`) and the filter (`type`). A page past the end redirects (`303`) to the last page; a change of filter or page size goes back to page 1. Previous/next links and page numbers (first, last and two on each side of the current page) appear above and below the list.
- **Select all** in the Time Machine selects the items of the current page, and says so. A restore can take up to 500 paths, the largest page.
- **Jump buttons.** Two buttons, to the top and to the bottom of the page, appear on every page that has room to scroll.
- **The API** keeps its old behaviour without the new parameters: `files` sorts by name with `offset` and `limit`, `history/tree` pages with `cursor`. A request with `sort`, `order`, `type`, `dirs_first` or (for the tree) `offset` gets the engine and returns `total`, `unfiltered` and `types`.

## Alternatives considered

- **Sort and page in SQL for the history, in Go for the live folder.** Two implementations that must agree, and an index for every sort column and filter, to save time that is not the problem: the whole folder reads in about 0.5 s at 50,000 entries.
- **Keep sorting in the browser and add pages.** Sorts only what is on the page, which is what was wrong.
- **Endless scrolling or a virtual list.** Fast to the eye, but selecting "all", linking to a place and going to the end are harder, and a script is needed to read the list at all.
- **Cursor paging with a sort.** Cannot jump to page 40.

## Consequences

- Every page request reads the whole folder. Measured on the development machine, 10,000 files take about 0.1 s and 50,000 about 0.5 s (0.7 s at 500 a page); several hundred thousand entries in one folder would take seconds and a lot of memory.
- The page numbers are positions in a list that can change between two requests: a file that appears or disappears moves the following ones by one place.
- The deleted-items panel of the Explorer (items of the newest recovery point that are gone from disk) follows the type filter but not the sort or the page, shows at most 500 items and appears on the first page only.
- Search results keep the order of the search engine and have no type filter, because the engine returns one page at a time.

---

# 中文

## 状态

于 2026-11-16 在 0.1.3 中被接受。改变的是 [0001](./0001-recovery-point-history-model.md) 的历史记录和实时文件夹列表的显示方式；历史记录模型本身没有改动。

## 背景

- 直到 0.1.2，文件浏览和时间机器每次读取 200 行，放在**加载更多**按钮后面，并且在浏览器里对已加载的行排序。包含 10,000 个文件的文件夹无法按大小排序，无法按种类找文件，要到页面底部只能长时间滚动。
- 有些文件夹里有几万个文件。
- 列表需要一个人们可以排序和筛选的**类型**列。

## 决定

- **列表由一个引擎在服务器内存中切分。** 包 `internal/listing` 接收一个文件夹的全部子项，返回请求的那一页：`Apply(items, view, options)` 按类型筛选、排序、截取窗口，并统计整个文件夹的各种类型。实时列表（`Service.ListLive`，通过 `os.Root` 读取文件夹）、历史列表（`Service.ListHistory`，一条没有 `LIMIT` 的 SQL 查询，`Store.ListAllChildren`）和 API 都使用它，所以页面和 API 不会不一致。
- **类型。** `dir`、`link`（联接点或符号链接）、`noext`（没有扩展名的文件）和 `ext:<扩展名>`（小写）。扩展名是最后一个点之后的文本，1 到 16 个字符且不含空白（`Report. Final version` 没有扩展名）。筛选还认识 `file`，表示所有不是文件夹的项目。筛选框列出文件夹中存在的类型及其数量（最多 200 种），数量统计的是整个文件夹，与当前筛选无关。
- **排序**按 `name`、`type`、`size` 或 `mtime`，升序或降序。顺序是全序的（相同时依次按不区分大小写的名称、精确名称、原始位置决定），所以相同的输入总是得到相同的分页。页面总是要求文件夹排在最前面（`DirsFirst`），不论方向；API 只有在 `dirs_first=true` 时才这样，因为不带参数的 API 以前只按名称排序。
- **分页。** 地址中的 `page` 和 `per_page`（每页 50、100、200 或 500；搜索结果为 50、100 或 200；默认 100），以及排序（`sort`、`order=desc`）和筛选（`type`）。超出末尾的页码会重定向（`303`）到最后一页；更改筛选或每页数量会回到第 1 页。列表上方和下方有上一页/下一页链接和页码（第一页、最后一页以及当前页两侧各两页）。
- 时间机器中的**全选**选中当前页的项目，并且会这样说明。一次恢复最多可以带 500 个路径，即最大的一页。
- **直达按钮。** 两个按钮，分别回到页面顶部和跳到底部，出现在每个有滚动空间的页面上。
- **API** 在没有新参数时保持原有行为：`files` 按名称排序，使用 `offset` 和 `limit`；`history/tree` 使用 `cursor` 分页。带有 `sort`、`order`、`type`、`dirs_first` 或（对 tree 来说）`offset` 的请求会使用该引擎，并返回 `total`、`unfiltered` 和 `types`。

## 考虑过的备选方案

- **历史记录在 SQL 中排序和分页，实时文件夹在 Go 中处理。** 两套必须保持一致的实现，还要为每个排序列和筛选条件建立索引，而这样节省的时间并不是问题所在：50,000 个条目的整个文件夹约 0.5 秒就能读完。
- **继续在浏览器中排序，只加上分页。** 只会对当前页的内容排序，而这正是原来的问题。
- **无限滚动或虚拟列表。** 看起来快，但选择"全部"、链接到某个位置和跳到末尾都更困难，而且要读取列表还需要脚本。
- **带排序的游标分页。** 无法直接跳到第 40 页。

## 影响

- 每次页面请求都会读取整个文件夹。在开发机器上测得：10,000 个文件约 0.1 秒，50,000 个约 0.5 秒（每页 500 项时 0.7 秒）；单个文件夹中有几十万个条目时，会花几秒钟并占用大量内存。
- 页码是列表中的位置，而列表可能在两次请求之间变化：出现或消失的文件会让它后面的项目移动一位。
- 文件浏览的已删除项目面板（最新恢复点中有、但磁盘上已不存在的项目）遵循类型筛选，但不遵循排序和页码，最多显示 500 项，并且只出现在第一页。
- 搜索结果保持搜索引擎的顺序，没有类型筛选，因为引擎每次只返回一页。
