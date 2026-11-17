# English

## Status

Accepted for 0.1.5 (2026-11-16). Extends the web layer of [0005](./0005-redis-user-directory-and-open-browsing.md) and the client address of [0009](./0009-single-sign-on-and-trusted-proxies.md); the routes, the accounts and the data are unchanged.

## Context

- One host name of the company serves several applications through one nginx, each under its own path: `/qdb/`, `/mer/`, `/gitea/` and, for TimeVault, `/tv/` (for example `http://prod_eng.co.murata.local/tv/`).
- Up to 0.1.4 TimeVault assumed that it is at the root of its address. Every address it wrote started with a slash: the redirects (`Location: /explorer`), the links and forms of the pages, the files of the pages (`/static/...`), the requests of the script (`/api/v1/jobs/<id>`, `/auth/sso/...`) and the cookies (`Path=/`).
- Behind `/tv/` that goes wrong at the first step. The proxy took `/tv` off and TimeVault answered `GET /` with `303 Location: /explorer`. The browser followed it to `http://<host>/explorer`, outside `/tv/`, where nginx sends everything that is not an application to the portal (`return 307 /api/`). The TimeVault log showed one request and the browser ended on another application. Had the redirect been right, the pages would still have lost their style and every link would have left `/tv/`.
- nginx cannot repair this reliably: `proxy_redirect` only rewrites `Location` headers, and rewriting the HTML and the script with `sub_filter` is fragile.
- The proxy may or may not take the prefix off, and nginx blocks written for other applications do both.

## Decision

- **A setting, `TIMEVAULT_BASE_PATH`** (for example `/tv`; empty by default, which changes nothing). `tv`, `/tv` and `/tv/` mean the same. It is one or more segments of letters, digits and `- _ . ~`, at most 128 characters, and the first segment must not be a path that the application serves itself (`api`, `static`, `login`, `explorer` and so on), because a request is only taken to carry the prefix when it starts with it. The service refuses to start otherwise, with an error that names the setting.
- **Inside, nothing changes.** The routes, the redirect targets and the paths in the templates stay as they were, written without the prefix. The prefix is taken off on the way in and put on on the way out, in a few places that cannot be forgotten:
  - `withBase`, the outermost handler, takes the prefix off a request whose path starts with it (`http.StripPrefix`, so the encoded form of the path is kept), sends the bare prefix to the same address with a slash (`301`, `308` for a form post) and serves every other request as it is. A proxy that already took the prefix off therefore does no harm.
  - `prefixWriter` puts the prefix on every `Location` header that names a path of the application, whoever wrote it: the redirects of the pages, the `Location` of the API (`/tv/api/v1/jobs/7`) and the redirects of the `ServeMux` itself (a folder without its slash). An address with a host, a relative address and `//host` are left alone.
  - The templates write every address with the `url` function, which puts the prefix on (`href="{{url "/explorer"}}"`); an address that Go code built and the template only prints goes through the same function. The address that a link carries for the page to come back to (`next`, `back`) is the application's own, written with `route`. A test fails when a template has an address that starts with a slash and does not use `url`.
  - The cookies take their path from the server: `Path=/tv` for the session, the language and the one-time message. A test fails when code sets a cookie for the whole host.
  - The script reads the prefix from `<meta name="tv-base">` for its own requests and for the links it does not follow itself; a link to another application on the host is not taken over by the in-page navigation.
- **`next` and `back` are accepted in both forms.** The script puts the address of the page, with the prefix, into the links to the login and the language switch, so the server takes the prefix off an address it is given (`/tv/history?x=1` is `/history?x=1`) and checks it again afterwards: `/tv//host` would otherwise become `//host`, which a browser reads as another site. The poll of a single sign-on answers with the page to go to as the browser needs it (`/tv/backups`).
- **The proxy may keep the prefix (recommended) or take it off.** Both work and both were run against this version. Keeping it is the simplest block and has no rewriting rule to get wrong. [runbook.md](../runbook.md) has the nginx block that was tested.
- **The proxy has to tell the truth about the client.** The proxy that the browser reaches sets `X-Forwarded-For` to `$remote_addr`. A block that passes `$http_x_forwarded_for` on, as one written for a second proxy does, lets the browser choose the address that TimeVault records when `TIMEVAULT_TRUSTED_PROXIES` lists the proxy; it was reproduced on 2026-11-16. When another proxy sits in front of it, the proxy next to TimeVault has to take the address of the browser from that proxy; see the update for 0.1.6 in [0009](./0009-single-sign-on-and-trusted-proxies.md).
- **Shown and logged.** The start-up log says `serving under a path prefix`; the Settings page and `GET /api/v1/settings` show the prefix (`base_path`).

## Alternatives considered

- **Another host name or another port for TimeVault.** Needs no change in the application and gives TimeVault an origin of its own, which is stronger (see the consequences). It was not chosen because the company portal links the applications by path, and `/tv/` was what was asked for. It stays possible: leave the setting empty.
- **Rewrite the responses in the proxy** (`proxy_redirect`, `sub_filter`). Catches only what the rules name, and the script, the JSON `Location` and the cookies need their own rules. Every new page is a new chance to miss.
- **Relative addresses and `<base href>`.** The pages are at different depths, `<base>` changes the meaning of `#fragment` links and of the icons' `<use href>`, the Content-Security-Policy forbids it (`base-uri 'none'`), and the redirects and the cookies would still need the prefix.
- **A prefix from the proxy's `X-Forwarded-Prefix` header.** The service would have to believe a header from the network. A setting cannot be forged.
- **Mount the routes under the prefix only** (no request without it). Simpler, but it breaks as soon as the proxy takes the prefix off, which is what the nginx blocks for the other applications do.

## Consequences

- With the setting, every address that the application gives out carries the prefix, whichever way the proxy passes the request on. Without it nothing changes; the tests that exist for the root were not changed.
- The prefix is part of the address people bookmark and the links they send. Changing it later breaks those links (a request without the prefix still works, but the cookie then is not sent).
- The cookies are separated from the other applications of the host name by their path. The origin is not: TimeVault shares scheme, host and port with them, so a script flaw in another application on that host name could call TimeVault's pages and API with the visitor's session. A host name or port of its own removes that; [security.md](../security.md) says so.
- The service can still be opened on its own address (`http://127.0.0.1:8660/`). It leads to the prefixed pages. An address typed there without the prefix shows its page, but the browser does not send the session cookie to it, so the visitor appears signed out until the prefix is used.
- A new place that writes an address by hand has to use `url`, `route` or the cookie path of the server; the two tests that read the templates and the code catch the usual mistakes, and the end-to-end tests run every page under a prefix with the proxy keeping it and removing it.
- Checked on 2026-11-16 with the real nginx 1.24 and the `/tv/` block of the company's configuration, then with the recommended block: the redirect chain stayed under `/tv/`, a browser could sign in, follow links, switch the language, run a backup and watch the job, and the cookies had `Path=/tv`. Not checked: the company's own nginx on the real server, and single sign-on through it (the sign-on service and Redis were not available).

---

# 中文

## 状态

于 2026-11-16 在 0.1.5 中被接受。扩展 [0005](./0005-redis-user-directory-and-open-browsing.md) 的 Web 层和 [0009](./0009-single-sign-on-and-trusted-proxies.md) 的客户端地址；路由、账号和数据保持不变。

## 背景

- 公司的一个主机名通过同一个 nginx 提供多个应用，每个应用在自己的路径下：`/qdb/`、`/mer/`、`/gitea/`，TimeVault 则是 `/tv/`（例如 `http://prod_eng.co.murata.local/tv/`）。
- 直到 0.1.4，TimeVault 都假定自己位于地址的根路径。它写出的每个地址都以斜杠开头：重定向（`Location: /explorer`）、页面的链接和表单、页面的文件（`/static/...`）、脚本发出的请求（`/api/v1/jobs/<id>`、`/auth/sso/...`）和 Cookie（`Path=/`）。
- 在 `/tv/` 之后，第一步就会出错。代理去掉了 `/tv`，TimeVault 对 `GET /` 回答 `303 Location: /explorer`。浏览器跟着跳到 `http://<主机>/explorer`，这已经在 `/tv/` 之外，而 nginx 会把所有不属于某个应用的请求送到门户页（`return 307 /api/`）。TimeVault 的日志里只有一次请求，浏览器却落在了另一个应用上。即使重定向是对的，页面也会失去样式，每个链接都会离开 `/tv/`。
- nginx 无法可靠地修复这一点：`proxy_redirect` 只改写 `Location` 头，用 `sub_filter` 改写 HTML 和脚本则很脆弱。
- 代理可能去掉前缀，也可能不去掉，而为其他应用写的 nginx 配置块两种都有。

## 决定

- **一个设置项 `TIMEVAULT_BASE_PATH`**（例如 `/tv`；默认为空，此时什么都不变）。`tv`、`/tv` 和 `/tv/` 含义相同。它由一个或多个段组成，段内只能有字母、数字和 `- _ . ~`，最长 128 个字符，并且第一段不能是应用自己提供的路径（`api`、`static`、`login`、`explorer` 等），因为只有当请求以前缀开头时才会被认为带有前缀。否则服务会拒绝启动，并给出点名该设置的错误。
- **内部什么都不变。** 路由、重定向目标和模板中的路径都和以前一样，写的时候不带前缀。前缀在进入时去掉、在出去时加上，只在几个不可能被遗忘的地方：
  - `withBase` 是最外层的处理器：路径以前缀开头的请求会去掉前缀（用 `http.StripPrefix`，所以路径的编码形式得以保留）；只有前缀本身而没有斜杠的请求会被送到带斜杠的同一地址（`301`，表单提交则是 `308`）；其他请求原样处理。所以已经去掉前缀的代理不会造成任何问题。
  - `prefixWriter` 给每个指向应用路径的 `Location` 头加上前缀，无论是谁写的：页面的重定向、API 的 `Location`（`/tv/api/v1/jobs/7`），以及 `ServeMux` 自己的重定向（缺少斜杠的文件夹）。带主机的地址、相对地址和 `//host` 不会被改动。
  - 模板用 `url` 函数写每个地址，由它加上前缀（`href="{{url "/explorer"}}"`）；Go 代码构造、模板只是输出的地址也经过同一个函数。链接中携带的、用于回到某个页面的地址（`next`、`back`）是应用自己的地址，用 `route` 写。当某个模板里有以斜杠开头却没有使用 `url` 的地址时，会有测试失败。
  - Cookie 的路径来自服务器：会话、语言和一次性提示都是 `Path=/tv`。当代码设置了作用于整个主机的 Cookie 时，会有测试失败。
  - 脚本从 `<meta name="tv-base">` 读取前缀，用于它自己的请求和它不自己跟随的链接；指向同一主机上其他应用的链接不会被页面内导航接管。
- **`next` 和 `back` 两种写法都接受。** 脚本会把带前缀的页面地址放进登录和切换语言的链接，所以服务器会从收到的地址上去掉前缀（`/tv/history?x=1` 就是 `/history?x=1`），然后再检查一遍：否则 `/tv//host` 会变成 `//host`，浏览器会把它当成另一个站点。单点登录的轮询以浏览器所需的形式回答要去的页面（`/tv/backups`）。
- **代理可以保留前缀（推荐），也可以去掉。** 两种都可以，并且都在本版本上运行过。保留前缀是最简单的配置块，没有可能写错的改写规则。[runbook.md](../runbook.md) 中有测试过的 nginx 配置块。
- **代理必须如实告知客户端地址。** 浏览器直接访问的那层代理把 `X-Forwarded-For` 设为 `$remote_addr`。像为第二层代理写的配置块那样传递 `$http_x_forwarded_for`，在 `TIMEVAULT_TRUSTED_PROXIES` 列出该代理时，会让浏览器自己选择 TimeVault 记录的地址；这一点已于 2026-11-16 复现。如果它前面还有另一层代理，紧挨着 TimeVault 的代理必须从那一层代理取得浏览器的地址；见 [0009](./0009-single-sign-on-and-trusted-proxies.md) 中 0.1.6 的更新。
- **显示与记录。** 启动日志会写出 `serving under a path prefix`；设置页和 `GET /api/v1/settings` 会显示前缀（`base_path`）。

## 考虑过的备选方案

- **给 TimeVault 另一个主机名或另一个端口。** 应用无需任何修改，并且让 TimeVault 有自己的源，更强（见"影响"）。没有选它，是因为公司门户按路径链接各个应用，而要求的正是 `/tv/`。它仍然可行：不设置该设置项即可。
- **在代理中改写响应**（`proxy_redirect`、`sub_filter`）。只能抓到规则点名的内容，脚本、JSON 中的 `Location` 和 Cookie 都需要各自的规则。每增加一个页面，就多一次漏掉的机会。
- **相对地址和 `<base href>`。** 页面的层级深度不同，`<base>` 会改变 `#fragment` 链接和图标 `<use href>` 的含义，内容安全策略禁止它（`base-uri 'none'`），而且重定向和 Cookie 仍然需要前缀。
- **使用代理的 `X-Forwarded-Prefix` 头给出前缀。** 服务必须信任来自网络的头。设置项则无法被伪造。
- **只把路由挂在前缀之下**（不接受不带前缀的请求）。更简单，但代理一旦去掉前缀就会失效，而为其他应用写的 nginx 配置块正是这样做的。

## 影响

- 设置之后，应用给出的每个地址都带有前缀，无论代理怎样转发请求。不设置则什么都不变；针对根路径已有的测试都没有改动。
- 前缀是人们收藏的地址和发送的链接的一部分。以后再更改它会让这些链接失效（不带前缀的请求仍然可以访问，但此时 Cookie 不会被发送）。
- Cookie 通过路径与同一主机名下的其他应用隔离。源却不是：TimeVault 与它们共享协议、主机和端口，所以该主机名下另一个应用中的脚本缺陷，可能会以访问者的会话调用 TimeVault 的页面和 API。给 TimeVault 单独的主机名或端口可以消除这一点；[security.md](../security.md) 中有说明。
- 服务仍然可以通过自己的地址打开（`http://127.0.0.1:8660/`），它会导向带前缀的页面。在那里手动输入不带前缀的地址，会显示对应页面，但浏览器不会把会话 Cookie 发给它，所以访问者看起来是未登录的，直到改用带前缀的地址。
- 新增手写地址的地方必须使用 `url`、`route` 或服务器给出的 Cookie 路径；读取模板和代码的两个测试能抓住常见的错误，端到端测试则让每个页面都在前缀之下、在代理保留和去掉前缀两种情况下各运行一遍。
- 2026-11-16 用真实的 nginx 1.24 和公司配置中的 `/tv/` 配置块检查过，随后用推荐的配置块再检查：重定向链始终在 `/tv/` 之下，浏览器可以登录、点击链接、切换语言、运行备份并观看任务，Cookie 的 `Path=/tv`。没有检查的：真实服务器上公司自己的 nginx，以及通过它进行的单点登录（单点登录服务和 Redis 当时不可用）。
