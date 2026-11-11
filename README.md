# LocalSharedTools：
> A WeChat-style LAN file & text transfer helper: a single Go binary, scan a QR code to send text/files between computer and phone—data never leaves your LAN. V1/V2 via web-based DeepSeek chat; V3 productized with DeepSeek Harness + V4.1-Flash (~¥5.92).
> 
> 局域网里的「文件传输助手」：Go 单程序 + 微信风网页，扫码即可在电脑与手机间传文本/文件，数据不出内网。V1/V2 由网页版 DeepSeek 对话生成；V3 由 DeepSeek Harness + V4.1-Flash 完成产品化（权限/角色/审计/测试），成本约 ¥5.92。
# 两天从 V1 玩具到 V3 产品的迭代总览：
> **项目**：LocalSharedTools（模块名 `LocalShT`）
> **定位**：局域网里的「文件传输助手」——不装客户端，浏览器打开即用；文本、文件、预览、二维码、历史持久化。
> **时间**：2026-11-10 → 2026-11-11，约两天完成 V1、V2、V3 三个版本。
> **V1 / V2**：作者通过网页版 AI（DeepSeek） 反复对话生成，属于「玩具 → 半成品」。
> **V3**：作者通过 **DeepSeek Harness** 中的 **DeepSeek-V4.1-Flash** 生成，完成产品化改造。
> **V3 消耗**：¥5.92 CNY ｜ 403 次 API 请求 ｜ 139,521,665 Tokens。

---

## 目录

- [一、一页速览](#一一页速览)
- [二、两天时间线](#二两天时间线)
- [三、三个版本对照](#三三个版本对照)
- [四、V1：单文件玩具](#四v1单文件玩具)
- [五、V2：模块化半成品](#五v2模块化半成品)
- [六、V3：DeepSeek Harness 产品化](#六v3deepseek-harness-产品化)
- [七、V3 关键设计](#七v3-关键设计)
- [八、成本与效率](#八成本与效率)
- [九、代码与文件统计](#九代码与文件统计)
- [十、运行、构建与迁移](#十运行构建与迁移)
- [十一、当前边界与后续路线](#十一当前边界与后续路线)
- [十二、复盘：为什么 V1/V2 停在玩具，V3 能收敛](#十二复盘为什么-v1v2-停在玩具v3-能收敛)
- [附录 A：V3 接口清单](#附录-av3-接口清单)
- [附录 B：V3 验收清单摘要](#附录-bv3-验收清单摘要)
- [附录 C：版本资料索引](#附录-c版本资料索引)

---

## 一、一页速览

| 项目 | 内容 |
| --- | --- |
| 产品 | 局域网文本 / 文件传输助手（Go + SQLite + WebSocket + 二维码） |
| 目标用户 | 同一 Wi-Fi 下的电脑 / 手机，不想装 App、不想把文件传公网 |
| V1 | 单文件原型：`main.go` 1086 行 + `index.html` 1609 行；能聊天、传文件、扫码、端口回退 |
| V2 | 模块化半成品：Go 拆成 9 个文件 / 1430 行；双构建模式、日志双写、静态内嵌；前端仍是同一份 1609 行页面 |
| V3 | 产品化：九表权限模型、token 身份、7 角色、6 权限位、分享/撤销、host 批量用户管理、服务端角色页面、31 个测试 |
| V3 代码量 | 生产代码 6157 行 / 24 个文件；测试 1424 行 / 9 个文件；文档 460 行 / 3 个文件（不含 405 行计划书） |
| V3 验证 | `go vet`、`go test ./...`、Windows 磁盘/内嵌构建、Linux 交叉编译、真实服务端 E2E、jsdom 角色页面验证 |
| V3 成本 | ¥5.92 CNY；403 次 API 请求；139,521,665 Tokens |
| 当前定位 | 可信局域网工具，不是公网 SaaS；后端 `can()` 是唯一安全边界 |

一句话总结：

> **V1 证明「这个需求有人用」，V2 证明「这个工程能维护」，V3 证明「这个产品可以有身份、权限和管理」。两天完成，V3 的 API 成本不到 6 元。**

---

## 二、两天时间线

> 时间按目录时间戳与开发记录整理，取「天」为粒度；具体时刻仅作参考。

| 时间 | 版本 | 主要动作 | 产出 |
| --- | --- | --- | --- |
| 11-10 下午 | V1 | 网页版 AI 对话生成，先做「能跑」：聊天、文件、二维码、历史、端口回退、单实例 | 单文件 `main.go` 1086 行 + 微信风 `index.html` 1609 行 |
| 11-10 傍晚 | V2 | 继续网页版对话，做工程化：拆平台文件、日志双写、静态内嵌、双构建模式、默认运行目录 | Go 9 文件 / 1430 行；前端与 V1 完全相同（文件哈希一致） |
| 11-11 白天 | V3 规划 | 在 DeepSeek Harness 中先写 [V3 迭代升级计划](V3/迭代升级计划.md)，冻结九表、角色、权限、五阶段验收 | 405 行计划书；B1–B16 待确认参数 |
| 11-11 下午 | V3 M1–M2 | 九表 schema、迁移、双写、token、host 引导、登录 | `schema.sql` / `migrate.go` / `auth.go` / `perm.go` |
| 11-11 傍晚 | V3 M3–M4 | 权限判定、分享/撤销、WS 过滤、服务端角色页面、admin/host 管理面板 | `records.go` / `hub.go` / `web/app.html` / `admin.js` / `host.js` |
| 11-11 晚间 | V3 M5 + 验证 | 审计、备份、批量用户管理、测试、真实服务端 E2E、jsdom 验证 | 31 个测试全绿；Windows / Linux 构建通过 |

```text
V1 单文件原型 ──► V2 模块化半成品 ──► V3 权限产品
  11-10 下午          11-10 傍晚          11-11
  能演示              能维护              能管理、能验收
```

---

## 三、三个版本对照

| 维度 | V1 | V2 | V3 |
| --- | --- | --- | --- |
| 生成方式 | 网页版 AI 对话 | 网页版 AI 对话 | **DeepSeek Harness + DeepSeek-V4.1-Flash** |
| 代码形态 | 单文件 `main.go` | 9 个 Go 文件，按平台拆分 | 分层：db/auth/perm/records/views/hub/web + 测试 |
| 代码量 | 1086 Go + 1609 HTML | 1430 Go + 同一份 1609 HTML | 6157 生产行 + 1424 测试行 + 460 文档行 |
| 数据模型 | 单 `messages` 表 | 单 `messages` 表 | **九表 + audit_log**：角色、分享类型/等级、token、用户、集合、记录、分享、文件类型 |
| 身份 | 浏览器 `localStorage` 自生成 UUID，可伪造 | 同 V1 | 服务端签发 token（UUID），Cookie + Authorization + WS query |
| 鉴权 | 无 | 无 | token 中间件；无 token/无效/停用 → 401 |
| 角色 | 无 | 无 | host / admin / lv1~lv5（7 角色） |
| 权限 | 无；全量广播 | 无；全量广播 | 6 权限位 + 分享等级 + `can()`；WS 按接收者过滤 |
| 分享/撤销 | 无 | 无 | 生成即分享；admin 撤销自己的分享；host 全局管理 |
| 前端 | 单 `index.html`，所有角色同一页面 | 同 V1（文件哈希一致） | **服务端按角色实时组合页面**；普通用户不收到 admin/host DOM 与脚本 |
| 登录 | 打开即用 | 打开即用 | 只接受链接 / 二维码；`/login?token=` 写 Cookie 后跳主页 |
| 管理 | 无 | 无 | host 批量改权限、撤销 token、软删除/恢复；admin 分享管理 |
| 可观测 | 文件日志 | 控制台 + 文件双写 | 审计表 + 日志 + 日快照 + 迁移前快照 |
| 测试 | 无 | 无 | 31 个 Go 测试（迁移、权限、HTTP、WS、角色页面） |
| 构建 | `index.html` 内嵌 + 开发期磁盘覆盖 | 磁盘 / `-tags embedstatic` 全静态内嵌 | 磁盘 / 内嵌；Windows + Linux 交叉编译 |
| 定位 | 玩具 | 半成品 | 可交付的局域网产品（仍有边界） |

---

## 四、V1：单文件玩具

V1 的目标很简单：**先让它跑起来**。

### 4.1 做了什么

- 文本消息：WebSocket 实时推送；
- 文件上传 / 下载 / 图片视频预览；
- 同一人 60 秒内文件自动合组；
- SQLite 历史持久化；
- 局域网二维码；
- 端口占用自动顺延；
- 同一 exe 的单实例检测；
- 微信风单页 UI：左右气泡、头像、时间分隔、设置抽屉。

### 4.2 工程形态

```text
V1/
├── main.go            # 1086 行，所有后端逻辑
├── static/index.html  # 1609 行，所有前端逻辑 + CSS + JS
├── go.mod / go.sum
└── dist/              # 可执行文件
```

### 4.3 玩具属性

- **身份可伪造**：浏览器 localStorage 自己生成 UUID，任何人可以改成任意 `cid`；
- **没有鉴权**：所有接口无 token，同网段任何人都能读写；
- **全量广播**：消息推给所有在线客户端，没有接收者过滤；
- **没有测试**：改一处只能靠手工回归；
- **单文件承载一切**：平台差异、日志、静态资源、业务全在 `main.go`；
- **前端不可拆分**：HTML/CSS/JS 全在一个 `index.html`。

> 结论：V1 是一个**能演示、能自用**的玩具；它验证了需求，但离“产品”还很远。

---

## 五、V2：模块化半成品

V2 是 V1 的工程化整理，不是业务升级。

### 5.1 主要变化

| 变化 | 说明 |
| --- | --- |
| 按平台拆文件 | `instance_windows.go` / `instance_linux.go`：单实例检测；`logsetup_*`：控制台探测 |
| 静态资源抽象 | `staticfs.go` / `static_embed.go` / `static_noembed.go` |
| 双构建模式 | 默认磁盘 `static/`；`-tags embedstatic` 单文件分发 |
| 日志双写 | 有控制台时同时写控制台 + `LocalShT.log`；无控制台只写文件 |
| 运行目录统一 | 默认 `LocalShT/shared` + `LocalShT/data/LocalShT.db` |
| 前端 | **与 V1 完全相同**：1609 行 `index.html`，哈希一致 |

### 5.2 工程形态

```text
V2/
├── main.go
├── instance_windows.go / instance_linux.go
├── logsetup.go / logsetup_windows.go / logsetup_linux.go
├── staticfs.go / static_embed.go / static_noembed.go
├── static/index.html
└── go.mod / go.sum
```

Go 代码从 1 个文件 1086 行变成 9 个文件 1430 行；功能基本不变，但结构清晰了。

### 5.3 半成品属性

- **业务安全仍为零**：没有 token、没有角色、没有权限；
- **前端仍是单文件**：所有页面、所有角色、所有逻辑都塞在同一个 `index.html`；
- **没有测试**：平台差异靠手工验证；
- **管理能力缺失**：没有用户管理、分享、撤销、审计、备份；
- **数据库仍是单表**：`messages` 无法表达角色与分享关系。

> 结论：V2 是「工程上像样、业务上仍半残」的半成品；它适合继续演进，但不适合直接给多人使用。

---

## 六、V3：DeepSeek Harness 产品化

V3 是作者用 **DeepSeek Harness + DeepSeek-V4.1-Flash** 完成的产品化版本。与网页版对话不同，V3 不是“一段段贴代码”，而是：

1. 先在 Harness 中写 [V3 迭代升级计划](V3/迭代升级计划.md)，把 M1–M5、验收标准、待确认参数固定下来；
2. Agent 直接读写仓库文件，按 M1 → M5 分阶段实现；
3. 每轮修改后跑 `go vet` / `go test` / 构建 / 真实服务端验证；
4. 前端用真实服务端页面 + jsdom 做角色组合验证，而不是只看截图。

### 6.1 V3 交付范围

- 九表数据层 + `audit_log`；
- token 身份与登录；
- 7 角色 / 6 权限位 / 分享类型与等级；
- `can()` 权限判定与 WS 按接收者过滤；
- 服务端按角色组合页面；
- admin 分享管理、host 用户批量管理；
- 审计、日快照、迁移前快照；
- 31 个测试 + 权限矩阵 / 运维手册 / README。

### 6.2 V3 目录

```text
V3/
├── main.go / migrate.go / auth.go / perm.go
├── records.go / views.go / hub.go / audit.go
├── schema.sql
├── web/
│   ├── app.html          # 服务端按角色渲染（条件输出 DOM）
│   ├── admin.js          # 只内联到 admin / host 页面
│   ├── host.js           # 只内联到 host 页面
│   └── noauth.html       # 缺少登录链接时的说明页
├── static/
│   ├── app.css           # 公共样式
│   └── app.js            # 公共核心，无 admin/host 逻辑
├── docs/权限矩阵.md / 运维手册.md
├── *_test.go
└── LocalShT/             # 运行时生成，勿提交
```

### 6.3 从「隐藏」到「组合」

V1/V2 的前端只有一份页面；V3 最初也尝试过“按权限隐藏节点”，但随后改成：

- `/` 每次请求根据 token 角色**服务端渲染**；
- 普通用户 HTML 中**不存在**输入区/工具栏/分享/用户管理；
- admin 页面才内联 admin 脚本；
- host 页面才内联 host 脚本；
- 普通用户 `/auth/me` 不再返回角色和权限字典，只返回 `caps`；
- `/static/index.html`、`/static/login.html` 即使旧目录残留也被 404 屏蔽。

这仍然不能阻止用户阅读**自己的**前端代码，但它消除了“普通用户拿到 admin/host 代码”的问题；真正的安全边界仍是后端 `can()`。

### 6.4 登录方式的变化

V3 后来删除了 `login.html`：

- 不再有 token 输入框；
- `/login?token=...` 校验后写 Cookie 并 302 到 `/`；
- 无有效 token 的 `/login` 返回说明页（401）；
- host 首启的 token 文件 + 登录链接仍然保留；
- 新用户 / 重签用户的二维码只用于把登录链接发给对应的人。

---

## 七、V3 关键设计

### 7.1 九表 + 审计

| 表 | 作用 |
| --- | --- |
| `role_types` | 角色字典（host/admin/lv1~lv5，id 0~6） |
| `share_types` | 分享来源类型（0 指定来源 / 1 全体来源） |
| `share_levels` | 分享等级与权限掩码 |
| `file_types` | 文件类型字典（0=文本，非 0=文件） |
| `tokens` | 身份 token，值 = UUID，带 active / revoked_at |
| `users` | 用户、角色、active、legacy client_id |
| `collections` | 记录集合，每用户默认集合 |
| `records` | 文本 / 文件记录，含 `legacy_message_id` 迁移幂等键 |
| `shares` | 分享者 → token 的授权关系，可撤销 |
| `audit_log` | 登录、生成用户、改权限、撤销、重签等审计 |

### 7.2 角色与权限

| 角色 | id | 掩码 | VIEW | SEND | UPLOAD | DOWNLOAD | SHARE | MANAGE |
| --- | ---: | ---: | :-: | :-: | :-: | :-: | :-: | :-: |
| host | 0 | 63 | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| admin | 1 | 31 | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ |
| lv1 | 2 | 1 | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ |
| lv2 | 3 | 3 | ✓ | ✓ | ✗ | ✗ | ✗ | ✗ |
| lv3 | 4 | 11 | ✓ | ✓ | ✗ | ✓ | ✗ | ✗ |
| lv4 | 5 | 7 | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ |
| lv5 | 6 | 15 | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ |

权限位：

```text
VIEW 1 / SEND 2 / UPLOAD 4 / DOWNLOAD 8 / SHARE 16 / MANAGE 32
```

判定顺序：token → user/role → 自有记录 → host 全量 → admin 范围 → user 分享行 → `mask & need`。

### 7.3 管理能力

host 设置中支持：

- 勾选 / 全选用户；
- 批量修改角色（不能改为 host）；
- 批量撤销 token（不重签）；
- 批量软删除 / 恢复；
- 单行重签、撤销 token、软删除 / 恢复；
- host 自身不可被操作；
- 角色变更推送 `permissions_changed`，软删除/撤销推送 `session_revoked`。

### 7.4 加固与运维

- `audit_log` 保留最近 5000 行；
- 迁移前快照 + 每日 `VACUUM INTO` 快照，默认保留 7 天；
- `-dualwrite` 默认开启，保留 V2 回滚桥；
- Cookie 只用于 GET，写操作必须带 `Authorization`；
- `CheckOrigin` 同 Host；
- `/download?file=` 仅 host；
- `/login` 不再提供 token 输入框；
- 服务端按角色组合页面，普通用户不收到 admin/host 代码。

### 7.5 测试

31 个 Go 测试覆盖：

- 迁移幂等 / 双写开关 / 备份 / schema 版本；
- 七角色权限矩阵、分享类型、等级、撤销、admin 范围、host 绕过；
- 登录 Cookie / CSRF、无 token / 无效 / 停用 token；
- 历史过滤、发文本、上传、下载权限；
- host 批量改权限 / 撤销 token / 软删除 / 恢复；
- WS 鉴权、广播过滤、`acl_changed`、`permissions_changed`、`session_revoked`；
- 角色页面组合、普通用户 payload 不含角色、登录跳转；
- 文件合组、时间分隔、下载直链门控、路径边界、唯一文件名。

---

## 八、成本与效率

### 8.1 V3 实际消耗

> 以下为作者提供的 DeepSeek Harness 账单数据。

| 指标 | 数值 |
| --- | ---: |
| 消费金额 | **¥5.92 CNY** |
| API 请求次数 | **403** |
| Tokens | **139,521,665** |

粗算：

- 每次 API 请求约 **¥0.0147**；
- 每次请求平均约 **34.6 万 tokens**（含上下文 / 缓存 / 多轮验证）；
- 按总 token 量平摊约 **¥0.042 / 百万 tokens**。

> 注意：平台计费包含输入、输出、缓存命中等等差异，上面的单价只是按给定总量做的量级参考，不等于官方价目表。

### 8.2 这笔钱买到了什么

- 6157 行生产代码（Go / SQL / HTML / CSS / JS）；
- 1424 行测试代码，31 个测试；
- 460 行权限 / 运维 / README 文档；
- 405 行迭代计划书；
- 一次完整的 M1–M5 产品化改造；
- 真实服务端 E2E 与 jsdom 角色页面验证；
- Windows 磁盘/内嵌构建 + Linux 交叉编译通过。

对比网页版：

- V1/V2 的直接 API 费用低，但依赖作者反复贴代码、手工回归，返工成本高；
- V3 用 403 次请求把「多文件修改 + 测试 + 验证 + 文档」串起来，单位时间的产出更稳定；
- 5.92 元的核心价值不是“生成代码”，而是**让 Agent 自己跑测试、自己发现回归、自己收敛**。

---

## 九、代码与文件统计

> 统计只算源码 / 文档，不含运行时 `LocalShT/` 数据和用户上传文件。

| 版本 | 代码行数 | 测试 / 其他 | 说明 |
| --- | ---: | ---: | --- |
| V1 | 1086 Go + 1609 HTML | 0 测试 | 单文件原型 |
| V2 | 1430 Go + 1609 HTML | 0 测试 | 模块化半成品；前端与 V1 相同 |
| V3 | 6157 生产行（含 SQL/HTML/CSS/JS，24 文件） | 1424 测试行（9 文件） | 另有 460 行文档（3 文件）+ 405 行计划书 |

V3 生产文件速览：

```text
auth.go 1015      migrate.go 676     records.go 460
perm.go 404       main.go 279        hub.go 273
views.go 215      schema.sql 110     staticfs.go 65
web.go 43         app.html 154       app.js 803
app.css 792       admin.js 154       host.js 240
noauth.html 84    audit.go 37        platform files...
```

- 20 条 HTTP 路由（2 条页面 + 18 条 API/WS）；
- 10 张表（九表 + audit_log）；
- 7 个角色、6 个权限位、3 个分享等级、2 个分享类型、6 个文件类型；
- 31 个 Go 测试。

---

## 十、运行、构建与迁移

### 10.1 V1

```bash
cd V1
go run . -port 5050
```

默认：

- 共享目录 `LocalShT-shared/`
- 数据库 `data/LocalShT.db`
- 打开 `http://localhost:5050` 即用，无登录。

### 10.2 V2

```bash
cd V2
go run . -port 5050
```

默认：

- 共享目录 `LocalShT/shared`
- 数据库 `LocalShT/data/LocalShT.db`
- 内嵌构建：

```bash
go build -tags embedstatic -ldflags "-s -w" -o LocalSharedTools.exe .
```

### 10.3 V3

```bash
cd V3
go run . -port 5050
```

首次启动：

1. 建库并写九表 + 审计表；
2. 创建 host 用户与 token；
3. token 写入 `LocalShT/data/host_token.txt`；
4. 控制台打印 host 登录链接（带 token），并自动打开浏览器；
5. `/login?token=...` 校验后写 Cookie 并跳 `/`。

构建：

```bash
# 磁盘模式
go build -ldflags "-s -w" -o LocalShT.exe .

# 内嵌模式（单文件分发）
go build -tags embedstatic -ldflags "-s -w" -o LocalShT.exe .

# Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embedstatic -trimpath \
  -ldflags "-s -w" -o LocalShT-linux .
```

### 10.4 从 V2 迁移到 V3

1. 停 V2；
2. 备份 V2 的 `LocalShT/data/LocalShT.db`；
3. 复制到 `V3/LocalShT/data/LocalShT.db`（或 `-db` 指向副本）；
4. 启动 V3：自动 `messages → records`、聚合 users/tokens、补 host 分享；
5. 迁移用户默认 lv2 占位，需要文件下载/上传时用 host 提升到 lv5（手册有 SQL）。

> **不要同时用 V2 和 V3 写同一个数据库。**

---

## 十一、当前边界与后续路线

### 11.1 当前边界

- 仍是**可信局域网工具**：不做 TLS、不做登录限流、不做病毒扫描；
- token 在 WS query / 链接中可能进入代理日志；
- 单 SQLite 连接串行写，适合小规模；
- 迁移用户默认 lv2，需要手工提升；
- 无消息搜索 / 分页 / 删除 / 分享到期；
- 前端代码对**合法接收者**仍可阅读（这是浏览器本质，不是 bug）；
- 真正的安全边界是后端 `can()` 与接口校验。

### 11.2 后续可做

1. 反向代理 + HTTPS，公网化前先做 TLS；
2. 登录失败限流、上传频率/大小限制；
3. token 哈希存储、定期轮换；
4. 消息搜索、分页、删除、审计 UI；
5. 迁移用户的角色提升做成 host 表单（现在已有批量改权限，可继续引导）；
6. 备份/恢复 UI；
7. 多语言 / 无障碍；
8. 更严格的文件类型扫描。

---

## 十二、复盘：为什么 V1/V2 停在玩具，V3 能收敛

### 12.1 网页版 AI 的强项与短板

**强项**：

- 从 0 到 1 很快；
- 单文件、单页面、UI 细节可以一轮轮磨；
- 不需要工程环境，复制粘贴即可运行。

**短板**：

- 多文件工程容易“贴错文件”“重复定义”；
- 没有测试反馈，改 A 坏 B 只能靠人眼；
- 跨文件一致性差（schema、权限、handler、前端各说各话）；
- 上下文窗口有限，越到后期越难保持全局视图。

V1/V2 正是这个模式的产物：**能跑、能看、能演示，但一加权限就散架。**

### 12.2 V3 为什么换了工作流

V3 用 DeepSeek Harness 做的关键改变：

1. **先计划后写码**：M1–M5、验收标准、待确认参数先冻结；
2. **Agent 直接改仓库**：多文件修改不会“贴错文件”；
3. **每轮验证**：`go vet` / `go test` / 构建 / 真实服务端；
4. **失败可回滚**：测试和快照把回归挡住；
5. **前端也验证**：服务端角色页面 + jsdom，不靠“看起来没问题”。

结果：

- 数据层、鉴权、权限、页面、管理、审计、备份能串成闭环；
- 31 个测试让“改权限”这种高风险改动可回归；
- 403 次请求、5.92 元，把“贴代码”变成“收敛产品”。

### 12.3 最重要的三条经验

1. **安全边界只能在服务端**。前端拆分、隐藏、角色页面都只是减少暴露面，不能替代 `can()`。
2. **测试是 Agent 的刹车**。没有测试，AI 写得越快，回归越隐蔽。
3. **先冻结决策，再生成代码**。B1–B16 这类“待确认参数”不冻结，后面就会在 handler、SQL、前端之间反复打架。

---

## 附录 A：V3 接口清单

> 下表按「路径 + 方法」拆开；同一路径的 GET/POST 在路由层算一条，因此 mux 路由总数为 20。

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| GET | `/` | 任意 token | 服务端按角色组合的主页 / 设置页 |
| GET | `/login` | 公开 | 链接落地：有效 token 写 Cookie 跳 `/`，否则说明页 |
| GET | `/healthz` | 公开 | 健康检查 |
| POST | `/auth/login` | token | 脚本/API 登录，写 Cookie |
| POST | `/auth/logout` | 公开 | 清 Cookie |
| GET | `/auth/me` | 任意 token | 身份 + caps；admin/host 额外角色与字典 |
| POST | `/profile` | 任意 token | 修改昵称 |
| GET | `/history` | VIEW | 按接收者过滤的历史 |
| POST | `/texts` | SEND | 发文本 |
| POST | `/upload` | UPLOAD | 上传文件 |
| GET | `/download?msg=` | DOWNLOAD + can(owner) | 下载 / 预览 |
| GET | `/download?file=` | MANAGE | 仅 host |
| GET | `/ws?token=` | token | WebSocket，按接收者过滤 |
| GET | `/share/qr?url=` | 任意 token | 新用户 / 重签用户二维码 |
| POST | `/share/revoke` | SHARE | 撤销分享 |
| POST | `/admin/users` | SHARE | 生成用户 + token + share |
| GET | `/admin/users` | MANAGE | host 用户列表 |
| POST | `/admin/users/role` | MANAGE | 批量改角色 |
| POST | `/admin/users/active` | MANAGE | 批量软删除 / 恢复 |
| POST | `/admin/users/revoke-token` | MANAGE | 批量撤销 token |
| GET | `/admin/shares` | SHARE | host 全部 / admin 自有 |
| POST | `/admin/reissue` | MANAGE | 重签非 host、未停用用户 |

WS 事件：`history`、`message_upsert`、`acl_changed`、`permissions_changed`、`session_revoked`。

---

## 附录 B：V3 验收清单摘要

- [x] 九表 + audit_log 可重复启动；
- [x] V2 `messages` 可迁移到 `records`，文本/文件类型正确；
- [x] 双写开/关行为正确，重启不重复迁移；
- [x] host/admin/lv1~lv5 token 可区分，无/坏/停用 token 401；
- [x] 扫码 / 链接登录，不再有 token 输入页；
- [x] lv1 只读，lv2 能发不能下，lv3/lv5 可下载，lv4 可上传；
- [x] admin 撤销后 HTTP + WS 同时失权；
- [x] host 批量改权限 / 撤销 token / 软删除 / 恢复；
- [x] 服务端按角色组合页面，普通用户不收到 admin/host DOM/脚本；
- [x] 普通用户 `/auth/me` 不含角色与权限字典；
- [x] `/static/index.html`、`/static/login.html`、`/assets/*` 旧入口不可用；
- [x] 31 个 Go 测试、`go vet`、Windows 磁盘/内嵌构建、Linux 交叉编译；
- [x] 真实服务端 E2E + jsdom 三种角色无运行时错误。

---

## 附录 C：版本资料索引

- V1 原始说明：[V1/LocalSharedTools.md](V1/LocalSharedTools.md)
- V2 使用说明：[V2/README.md](V2/README.md)
- V2 编译说明：[V2/编译说明.md](V2/编译说明.md)
- V3 使用说明：[V3/README.md](V3/README.md)
- V3 迭代计划：[V3/迭代升级计划.md](V3/迭代升级计划.md)
- V3 权限矩阵：[V3/docs/权限矩阵.md](V3/docs/权限矩阵.md)
- V3 运维手册：[V3/docs/运维手册.md](V3/docs/运维手册.md)

---

## 结语

V1 是一个「能用的玩具」，V2 是一个「像样的半成品」，V3 才是「能交给别人用的局域网产品」。

两天时间、403 次请求、5.92 元，把 V1/V2 里缺失的数据模型、身份、权限、角色页面、批量管理、审计、备份和测试补齐。这个项目最有价值的部分不是“AI 写了多少行代码”，而是：

> **把需求拆成可验收的阶段，让 Agent 在真实仓库里改代码、跑测试、做验证，最后把产品收敛出来。**

这也是 DeepSeek Harness + DeepSeek-V4.1-Flash 在这个项目里真正起到的作用。
