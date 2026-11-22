# English

## Status

Accepted for 0.1.9 (2026-11-18). It replaces how 0.1.8 read the pictures of the accounts (a `fetch` to the host of the site that only took `image/*`); the rest of 0.1.8 stands. It also records that the address of the update manifest stays an explicit setting.

**Update, 1.0.1.** The settings that this record names as `TIMEVAULT_*` variables (`TIMEVAULT_AVATAR_URL`, `TIMEVAULT_SERVER_HOST`) are Site settings now ([0017](./0017-site-settings-in-the-database.md)): `avatar_url` and `server_host`. The record is left as it was written.

## Context

- The wish: show the picture of an account next to its name, read from the employee pictures that the other company applications keep, `http://<TIMEVAULT_SERVER_HOST>/api/static/hr/<employee ID>.webp`, and show the default picture when there is none.
- 0.1.8 asked for `/api/static/hr/<id>.webp` on the host of the site with `fetch`, and took only a `200` whose `Content-Type` was `image/*`. No picture ever showed. The real application (checked on the build computer, through its nginx) sends the `.webp` files as `Content-Type: text/plain; charset=utf-8`: its file responses fall back to `text/plain` for a type they do not know (Starlette 0.36: `guess_type(...)[0] or "text/plain"`), and the Python of that server has no type for `.webp`. It answers a file that is not there with **`202` and a placeholder picture of 480×320 pixels**, never `404`. It sends no CORS headers, also not for a request with an `Origin`.
- The people who use the site reach it by several addresses (the server by name, and the two reverse proxies in front of it), so "the host of the site" is not always the host that keeps the pictures.
- A content security policy of `default-src 'self'; img-src 'self' data:` stops a page from showing a picture of another host.
- Measured on the folder of the real pictures: 10,325 `.webp` files, mostly about 125×85 pixels (the largest 448×400, 18 KB); none is 480×320.

## Decision

- **The browser reads the pictures from the host that keeps them,** by an address that is written down or worked out from `TIMEVAULT_SERVER_HOST`: `http://<TIMEVAULT_SERVER_HOST>/api/static/hr/{id}.webp` by default. `TIMEVAULT_AVATAR_URL` can name another `http(s)` address, a path on the host of the site (the meaning it had in 0.1.8), or `off`. The service fetches and serves nothing.
- **An image element decides what is a picture,** not the headers. It loads the address; an error, or a picture of exactly 480×320 pixels (the placeholder), leaves the default picture. `text/plain` does not matter to an image. A missing picture is remembered for ten minutes of the browser session (not for the whole session, so that a host that was down is tried again soon).
- **The policy follows the address.** `img-src 'self' data:` gets the host of an absolute address, for images only. A host name with an underscore (the company has `prod_eng.co.murata.local`) cannot be written in a policy; the browser ignores the whole entry (tested in Edge), so the nearest domain of two labels is listed with a wildcard instead (`http://*.co.murata.local`). Without such a domain nothing is added, the start-up log says so, and the pictures show when the site is opened by that same address.
- **The address of the update manifest stays an explicit `TIMEVAULT_UPDATE_URL`.** It is not worked out from `TIMEVAULT_SERVER_HOST`, although the release is published to the same application: the manifest is `http://<host>/api/static/cd/timevault.json`, and a `package_url` that is a file name leads to `http://<host>/api/static/cd/<name>` (it is resolved next to the manifest, as in 0.1.8). Updates are only on because the `.env` says so.

## Alternatives considered

- **Keep the request of 0.1.8 and only accept `text/plain`, or look at the first bytes.** It works while the site is opened by the host of the pictures. A `fetch` to another origin needs CORS headers, which the application does not send, and a picture element cannot tell the `202` placeholder from a picture without a size rule.
- **Relay the pictures through the service** (`GET /avatar/<id>`: the service reads the picture, checks the status and the bytes, caches it, and serves it from its own origin). It would work for every address the site is opened by, needs no change of the policy, and can tell a `202` from a `200`. Not chosen: the decision was that the browsers read the pictures themselves. It would make the service a client of the other application and a cache of employee pictures.
- **Allow every `http:` host for images** (`img-src http:`) so that a host name with an underscore needs no wildcard. It weakens the policy for every deployment to help one kind of name.
- **Work out the update address from the server host.** No configuration, but it turns self-update on at every server that has the setting, and an old manifest left in the folder would be an update to it.

## Consequences

- **The pictures show whatever address the site is opened by,** as long as the browser can reach the host of the pictures. They are read in the clear over `http`; behind `https` the address has to be `https` too, because a browser does not show pictures from `http` in an `https` page.
- **The placeholder rule is tied to the other application.** If its `file_not_found.jpg` changes size, the placeholder shows as a picture until the size in `web/assets/js/app.js` is changed; a real picture of exactly 480×320 pixels would be hidden (none of the 10,325 is).
- **The host of the pictures sees every browser that shows a picture** and logs each request (`found OK | 200`, or `not found | 202`, in the other application). A picture is asked for once while a page lives, and only when it is about to be seen.
- **The policy of every page depends on one setting.** The forms of the address, the policy (hosts, ports, an underscore, no domain) and the pages (meta tag and header) are covered by tests; a browser (Edge) was run against the real application through the real nginx (a picture sent as `text/plain`, an account without a picture), against a stand-in on another origin with a host name with an underscore, against a host that cannot be named, a host that is down and a path. See [mvp-status.md](../mvp-status.md).
- The update address is documented for the static files of the other application, and covered by a test of that layout (the manifest, a relative `package_url`, a `202` for a file that is not there) and by a run of the compiled programs.

---

# 中文

## 状态

于 2026-11-18 在 0.1.9 中被接受。它替代了 0.1.8 读取账号头像的方式（对站点所在主机使用 `fetch`，并且只接受 `image/*`）；0.1.8 的其余部分保持不变。它也记录了更新清单的地址仍然是显式设置。

**更新，1.0.1 版。** 本记录中以 `TIMEVAULT_*` 变量命名的设置（`TIMEVAULT_AVATAR_URL`、`TIMEVAULT_SERVER_HOST`），现在都是站点设置（[0017](./0017-site-settings-in-the-database.md)）：`avatar_url` 和 `server_host`。本记录保持原样。

## 背景

- 需求：在账号名称旁边显示该账号的头像，读取公司其他应用保存的员工照片 `http://<TIMEVAULT_SERVER_HOST>/api/static/hr/<工号>.webp`，没有头像时显示默认头像。
- 0.1.8 用 `fetch` 在站点所在主机上请求 `/api/static/hr/<id>.webp`，并且只接受 `Content-Type` 为 `image/*` 的 `200` 应答。结果没有任何头像显示出来。真实的应用（在构建所用的电脑上通过它的 nginx 检查过）把 `.webp` 文件以 `Content-Type: text/plain; charset=utf-8` 发送：它的文件应答对不认识的类型回退到 `text/plain`（Starlette 0.36：`guess_type(...)[0] or "text/plain"`），而那台服务器的 Python 没有 `.webp` 的类型。对于不存在的文件，它回答 **`202` 和一张 480×320 像素的占位图**，从不回答 `404`。它不发送 CORS 头，带有 `Origin` 的请求也一样。
- 使用站点的人会通过多个地址访问它（按名称访问服务器，以及它前面的两层反向代理），所以"站点所在的主机"并不总是存放头像的主机。
- `default-src 'self'; img-src 'self' data:` 这样的内容安全策略会阻止页面显示另一个主机的图片。
- 在真实头像所在的文件夹中测量：10,325 个 `.webp` 文件，大多约 125×85 像素（最大的是 448×400，18 KB）；没有一张是 480×320。

## 决定

- **由浏览器从存放头像的主机读取头像**，地址是直接写明的，或者由 `TIMEVAULT_SERVER_HOST` 得出：默认是 `http://<TIMEVAULT_SERVER_HOST>/api/static/hr/{id}.webp`。`TIMEVAULT_AVATAR_URL` 可以指定另一个 `http(s)` 地址、站点所在主机上的路径（0.1.8 中它的含义），或 `off`。服务不获取也不提供任何东西。
- **由图片元素决定什么是头像**，而不是看响应头。它加载这个地址；出错，或者图片恰好是 480×320 像素（占位图），都保留默认头像。`text/plain` 对图片没有影响。缺失的头像在浏览器会话中被记住十分钟（不是整个会话，这样曾经不可用的主机很快会被再次尝试）。
- **策略跟随地址。** `img-src 'self' data:` 加上绝对地址的主机，只用于图片。带下划线的主机名（公司有 `prod_eng.co.murata.local`）无法写进策略；浏览器会忽略整条（在 Edge 中测试过），所以改为列出最近的、有两个标签的域并加通配符（`http://*.co.murata.local`）。没有这样的域时什么也不加，启动日志会说明，而头像在用同一个地址打开站点时才会显示。
- **更新清单的地址仍然是显式的 `TIMEVAULT_UPDATE_URL`。** 它不会从 `TIMEVAULT_SERVER_HOST` 得出，尽管发布放在同一个应用里：清单是 `http://<host>/api/static/cd/timevault.json`，是文件名的 `package_url` 指向 `http://<host>/api/static/cd/<name>`（和 0.1.8 一样，在清单旁边解析）。更新只会因为 `.env` 这样写才开启。

## 考虑过的备选方案

- **保留 0.1.8 的请求，只是接受 `text/plain`，或者查看开头的字节。** 站点用存放头像的主机打开时可以工作。对另一个源的 `fetch` 需要 CORS 头，而该应用不发送；不用尺寸规则的话，图片元素也无法区分 `202` 占位图和真正的头像。
- **通过服务转发头像**（`GET /avatar/<id>`：服务读取头像、检查状态和字节、缓存它，并从自己的源提供）。它对站点被打开时使用的每个地址都有效，不需要改策略，也能区分 `202` 和 `200`。没有选择它：决定是由浏览器自己读取头像。这样会让服务成为另一个应用的客户端，并成为员工照片的缓存。
- **允许图片来自所有 `http:` 主机**（`img-src http:`），这样带下划线的主机名就不需要通配符。这会为了一类名称而削弱每个部署的策略。
- **由服务器主机得出更新地址。** 不需要配置，但会在每个有这个设置的服务器上开启自动更新，而遗留在文件夹里的旧清单就会变成一次更新。

## 影响

- **无论用什么地址打开站点，头像都能显示**，只要浏览器能访问存放头像的主机。头像通过 `http` 明文读取；站点在 `https` 之后时，地址也必须是 `https`，因为浏览器不会在 `https` 页面中显示来自 `http` 的图片。
- **占位图规则与另一个应用绑定。** 如果它的 `file_not_found.jpg` 改了尺寸，在修改 `web/assets/js/app.js` 中的尺寸之前，占位图会被当成头像显示；恰好是 480×320 像素的真正头像会被隐藏（10,325 张中没有一张是这个尺寸）。
- **存放头像的主机能看到每个显示头像的浏览器**，并记录每次请求（在另一个应用中是 `found OK | 200` 或 `not found | 202`）。页面存活期间每张头像只请求一次，并且只在它即将被看到时才请求。
- **每个页面的策略都依赖一个设置。** 地址的各种形式、策略（主机、端口、下划线、没有域）和页面（meta 标签和响应头）都有测试覆盖；还在浏览器（Edge）中运行过：针对通过真实 nginx 访问的真实应用（以 `text/plain` 发送的头像、没有头像的账号）、针对另一个源上主机名带下划线的替身、无法命名的主机、不可用的主机和路径形式。见 [mvp-status.md](../mvp-status.md)。
- 更新地址已按另一个应用的静态文件写入文档，并有该布局的测试（清单、相对的 `package_url`、文件不存在时的 `202`）和编译后程序的运行覆盖。
