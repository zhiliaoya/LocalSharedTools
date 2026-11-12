# LocalShT V3

> 项目从 V1 玩具、V2 半成品到 V3 产品的完整两天复盘，见 [总项目.md](../总项目.md)。

局域网「文件传输助手」的 V3 版本。在 V2 的聊天 / 文件共享能力上，按 [V3 迭代升级计划](迭代升级计划.md) 叠加：

- **九表数据层**：`role_types / share_types / share_levels / file_types / tokens / users / collections / records / shares`（+ `audit_log`）。
- **token 身份**：服务端签发，删除浏览器自生成 UUID 的可伪造身份。
- **位掩码权限**：`VIEW / SEND / UPLOAD / DOWNLOAD / SHARE / MANAGE` 六条权限树，host / admin / lv1~lv5 七角色。
- **分享与撤销**：生成用户即分享；admin 撤销自己的分享；host 全局管理。
- **按权限拼装 UI**：无权节点不进入 DOM，而不是先渲染再隐藏。
- **加固**：审计日志、日快照、迁移前快照、CSRF / Origin 收敛、token 重签。

> V3 是独立目录、独立数据库。V2 原样保留，可继续运行、对比、回滚。

---

## 一、快速开始

环境：Go 1.27.3+，Windows / Linux，无需 CGO。

```bash
cd V3
go mod download
go run .
```

首次启动会：

1. 建库（默认 `LocalShT/data/LocalShT.db`）并写入字典；
2. 创建 host 用户与 token；
3. 把 token 写入 `LocalShT/data/host_token.txt`，并在控制台打印一次 host 登录 URL；
4. 自动打开浏览器（首次带 token，直接进入 host 身份）。

之后访问 `http://localhost:5050`，未登录会跳到 `/login`。**V3 不再提供 token 输入框**：登录只通过 host / admin 发出的链接或二维码完成，`/login?token=...` 校验后写 Cookie 并跳转主页。Host token 丢失时用 `-host-token <新token>` 重签。

> 设置里的二维码只用于把**新用户 / 重签用户**的登录链接发给对方；V3 不再提供「我的登录二维码」。自己的 token / 二维码等同于自己的登录凭证，不要外发。

页面由服务端按角色实时组合：普通用户页面只有聊天 / 基础设置；admin 页面额外包含分享面板；host 页面额外包含分享 + 用户管理。普通用户不会收到 admin / host 的 HTML 或增量脚本。

### 角色与能力

| 角色 | id | 能力 |
| --- | ---: | --- |
| host | 0 | 全部权限，不受分享表限制 |
| admin | 1 | 数据权限 + 分享管理；只看自己创建的分享/用户 |
| lv1 | 2 | 只读 |
| lv2 | 3 | 只读 + 发文本 |
| lv3 | 4 | 只读 + 发文本 + 下载/预览 |
| lv4 | 5 | 只读 + 发文本 + 上传 |
| lv5 | 6 | 只读 + 发文本 + 上传 + 下载 |

完整位定义、分享等级与 `can()` 流程见 [docs/权限矩阵.md](docs/权限矩阵.md)。

---

## 二、构建

| 模式 | 命令 | 运行时依赖 |
| --- | --- | --- |
| 磁盘模式（默认） | `go build -ldflags "-s -w" -o LocalShT.exe .` | 需要 exe 同级或工作目录有 `static/` |
| 内嵌模式（推荐分发） | `go build -tags embedstatic -ldflags "-s -w" -o LocalShT.exe .` | 单文件，无外部依赖 |

Linux amd64 交叉编译：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embedstatic -trimpath \
  -ldflags "-s -w" -o LocalShT-linux .
```

> Windows 11 的 **Smart App Control** 可能拦截新编译的未签名 exe。可在「Windows 安全中心 → 应用和浏览器控制 → 智能应用控制」中处理，或对 exe 做代码签名；这与项目本身无关。

### 启动参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-port` | `5050` | 监听端口；占用时自动顺延（最多 100 个） |
| `-dir` | `LocalShT/shared` | 共享文件目录 |
| `-db` | `LocalShT/data/LocalShT.db` | SQLite 数据库路径 |
| `-history` | `200` | 历史加载条数 |
| `-dualwrite` | `true` | 同时写 V2 `messages` 表（回滚桥）；`false` 即 M5 下线状态 |
| `-backup-days` | `7` | 日快照保留天数，`0` 关闭 |
| `-host-token` | 空 | 指定/恢复 host token；会停用旧 host token |

---

## 三、接口

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| GET | `/` | 任意 token | 按角色实时组合的主页 / 设置页；未登录跳 `/login` |
| GET | `/login` | 公开 | 链接 / 二维码落地：有效 token 写 Cookie 并跳 `/`，否则返回说明页 |
| GET | `/favicon.ico` | 公开 | 站点图标（ICO，编译期内嵌） |
| GET | `/static/` | 公开 | `app.css` / `app.js` 公共资源 |
| POST | `/auth/login` | token | 脚本 / API 用：校验 token、下发 Cookie、返回身份载荷 |
| GET | `/auth/me` | 任意 token | 身份 + `caps`；admin / host 额外返回角色与字典 |
| POST | `/auth/logout` | 公开 | 清 Cookie |
| POST | `/profile` | 任意 token | 修改昵称 |
| GET | `/healthz` | 公开 | 健康检查 |
| GET | `/history` | VIEW | 按接收者过滤的历史 |
| POST | `/texts` | SEND | 发文本；身份完全取自 token |
| POST | `/upload` | UPLOAD | 多文件上传 |
| GET | `/download?msg=ID` | DOWNLOAD + can(owner) | `inline=1` 预览 |
| GET | `/download?file=相对路径` | MANAGE | 仅 host |
| GET | `/ws?token=` | token | WebSocket；按接收者过滤 |
| GET | `/share/qr?url=` | 任意 token | 生成用户 / 重签后的新用户二维码（PNG） |
| POST | `/admin/users` | SHARE | 事务生成用户 + token + share |
| GET | `/admin/users` | MANAGE | host 用户列表 |
| POST | `/admin/users/role` | MANAGE | 批量修改角色（不能改为 host） |
| POST | `/admin/users/active` | MANAGE | 批量软删除 / 恢复用户 |
| POST | `/admin/users/revoke-token` | MANAGE | 批量撤销 token（不重签） |
| GET | `/admin/shares` | SHARE | host 全部 / admin 自有 |
| POST | `/share/revoke` | SHARE | 撤销分享 |
| POST | `/admin/reissue` | MANAGE | 重签非 host、未停用用户的 token |

WS 事件：`history`、`message_upsert`、`acl_changed`、`permissions_changed`、`session_revoked`。

错误码：`400` 参数错误、`401` 未登录/无效 token、`403` 有权登录但无权限、`404` 不存在、`405` 方法不允许。

---

## 四、从 V2 迁移

V3 默认在 `V3/LocalShT/` 下新建数据库。要迁移 V2 历史，推荐：

1. 停掉 V2；
2. 备份 V2 的 `LocalShT/data/LocalShT.db`；
3. 复制到 `V3/LocalShT/data/LocalShT.db`（或启动 V3 时用 `-db` 指向副本）；
4. 启动 V3，自动完成：
   - `messages → records`（文本→`file_type_id=0`，文件→非 0）；
   - 按 `client_id` 聚合 `users` / `tokens` / 默认 `collections`；
   - 每个迁移用户补一条 host 的「全体来源」分享，保留旧的全局可见性；
   - 迁移前自动 `VACUUM INTO` 快照。
5. 迁移用户默认是 **lv2 占位**（只读 + 发文本，不能下载/上传）。需要恢复 V2 的下载/上传能力时，用 host 执行：

   ```sql
   UPDATE users SET role_type_id = 6 WHERE name = '旧用户名';
   ```

详见 [docs/运维手册.md](docs/运维手册.md)。

> 不要同时用 V2 和 V3 写同一个数据库。回滚时先停 V3。

---

## 五、目录结构

```text
V3/
├── main.go / migrate.go / auth.go / perm.go / records.go / views.go / hub.go / audit.go
├── schema.sql
├── instance_*.go / logsetup*.go / staticfs.go / static_*.go
├── web/
│   ├── app.html              # 服务端按角色渲染的页面模板（条件输出 DOM）
│   ├── admin.js              # 仅发给 admin / host
│   ├── host.js               # 仅发给 host
│   └── noauth.html           # 缺少登录链接时的说明页
├── static/
│   ├── app.css               # 公共样式
│   └── app.js                # 公共核心脚本（不含 admin / host 逻辑）
├── docs/权限矩阵.md / 运维手册.md
├── 迭代升级计划.md
└── LocalShT/                 # 运行时生成，勿提交
```

## 六、测试

```bash
go vet ./...
go test ./...
go build -tags embedstatic ./...
```

自动化覆盖：迁移幂等 / 双写开关 / 备份、七角色权限矩阵、登录 Cookie 与 CSRF、分享类型与撤销、上传下载权限、WS 过滤与 `acl_changed` / `permissions_changed` / `session_revoked`、host 批量改权限 / 撤销 token / 软删除 / 恢复、角色页面组合与受保护脚本、文件合组与下载直链门控。手工验收清单见 [docs/运维手册.md](docs/运维手册.md#六验收清单)。

## 七、安全边界

- 仍定位为**可信局域网工具**：不做 TLS、不做登录限流、不做文件病毒扫描。
- 页面 / 脚本按角色在**服务端**组合：普通用户不会收到 admin / host 的 HTML 或增量脚本；但客户端代码本身仍可被其合法接收者阅读，**真正的权限边界始终是后端接口与 `can()`**。
- token 是唯一凭证；URL / WS query 中的 token 可能进入访问日志，介意时用 `Authorization` 头。
- 下载链接依赖 HttpOnly Cookie；Cookie 只用于 GET，写操作必须带 `Authorization` 头（CSRF 防护）。
- `CheckOrigin` 只允许同 Host 的浏览器来源；`/download?file=` 仅 host；`withinRoot` 路径边界保留。
- 撤销分享只停用 share，不吊销 token；重签 token 才让旧凭证失效。

## 八、与 V2 的差异

- 身份从浏览器 UUID 改为服务端 token；不再有 token 输入页，登录只走链接 / 二维码。
- 页面 / 设置按角色在服务端实时组合：普通用户、admin、host 各自只收到自己的 DOM 与增量脚本。
- 记录存 `records` 九表模型，`messages` 仅作回滚双写。
- 广播按接收者权限过滤，不再全量推送。
- 文件下载直链按 `DOWNLOAD` 能力逐行下发；无权者拿不到链接。
- 批量下载不再有「设置里的开关」，由 DOWNLOAD 能力直接决定是否存在勾选框/工具栏。
- 普通用户界面 / 设置不显示角色等级；角色、分享、用户管理只在 admin / host 的设置里出现。
- host 可在「用户管理」中批量修改角色、撤销 token、软删除 / 恢复用户；host 自身不可被操作。
- 迁移用户默认 lv2，需要 host 按需提升角色。
