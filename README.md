# 序流 xuliu

> 让项目有序，让协作流动。

需求、任务、迭代、缺陷与测试一体化的研发管理平台。Go 单二进制，前端内嵌，
默认用 SQLite 开箱即跑，改两行配置即可换到 MySQL / PostgreSQL。

**它借鉴禅道的领域模型与工作流口径，但不复刻禅道** —— 292 张表里我们只做
核心闭环需要的 35 张，并在几处关键设计上有意偏离（见文末「与禅道的差异」）。

---

## 三十秒跑起来

```bash
cd web && npm ci && npm run build && cd ..   # 产物直接落到 internal/web/dist
go run ./cmd/xuliu
```

打开 <http://127.0.0.1:8080>，用 `admin` / `admin123456` 登录。

不想在本机装 Node，用镜像最省事（前端在镜像里编译，产物内嵌进二进制）：

```bash
docker build -t xuliu .
docker run -d -p 8080:8080 -v xuliu-data:/app/data xuliu
```

> 前端产物 `internal/web/dist/` **不入库**（`.gitignore` 里只留了 `.gitkeep` 占位），
> 所以 clone 之后必须先 `npm run build` 再 `go build`，否则二进制起来了但没有界面 ——
> 此时访问页面会明确提示「前端资源尚未构建」，而不是一个令人困惑的 404。
> 只要接口、不要界面的话，`go run ./cmd/xuliu` 本身就够用（API 全部可用）。

首次启动会自动建表、写入权限点字典与内置角色组，并按配置创建初始管理员。
所有接口都在 `/api/v1` 下；`/healthz` 是免鉴权的探针。

> 开发模式下 JWT 密钥是每次启动临时生成的，重启后所有人要重新登录。
> 想保住登录状态，配 `auth.jwtSecret` 或 `XULIU_AUTH__JWTSECRET`。
> **生产环境（`app.env=prod`）不显式配密钥会直接拒绝启动**，这是有意的。

---

## 能力现状

| 域 | 状态 | 说明 |
|---|---|---|
| 身份与权限 | 已完成 | 用户 / 部门 / 角色组 / 权限点，bcrypt + JWT 双令牌 |
| 产品与需求 | 已完成 | 产品线、模块树、产品计划、需求全生命周期（含评审与变更版本化） |
| 项目 / 迭代 / 任务 | 已完成 | 项目-产品多对多、需求排期、团队、任务状态机、工时与进度 |
| 缺陷 | 已完成 | 确认 / 指派 / 解决 / 关闭 / 重新激活 / 转需求 |
| 测试用例与测试单 | 已完成 | 用例评审（提交/撤回/通过/驳回）、步骤与版本快照、测试单纳入用例、逐次执行记录、结单给结论 |
| 工作台与通知 | 已完成 | 我的待办、通知、操作历史时间线、燃尽图 |
| 前端 | 已完成 | Vue3 + Element Plus，18 个页面，构建产物内嵌进二进制 |
| 接口文档 | 已完成 | Swagger UI 在 `/swagger/index.html`（非生产环境） |
| 发布 / 报表 | 未开始 | 下一阶段 |

### 核心闭环

提需求 → 提交评审 → 评审通过 → 排进迭代 → 拆任务 → 开始 → 记工时 → 完成 →
报缺陷 → 指派 → 解决 → 关闭 → 重新激活，
再到写用例 → 提交评审 → 评审通过 → 纳入测试单 → 记录执行 → 复测 → 结单，
每一步都留操作历史。

这条链路不只是写在文档里 —— 它有一个 **172 项断言的脚本**在用真实 HTTP 请求跑它：

```bash
bash scripts/e2e.sh
```

脚本会自己编译并拉起一个用临时 SQLite 库的实例，跑完自己收尾。
只想对着已在跑的实例验证时：

```bash
XULIU_E2E_BASE=http://127.0.0.1:8080 XULIU_E2E_SKIP_BOOT=1 \
  XULIU_E2E_ACCOUNT=admin XULIU_E2E_PASSWORD=你的口令 \
  bash scripts/e2e.sh
```

它还会顺手做越权检查：造一个局外人、把产品设为私有，然后断言他列表里看不到、
详情 403、评论也 403 —— 白名单内的人则一切正常。

---

## 技术选型

**后端** Go 1.27 · gin · GORM · koanf（配置）· golang-jwt · bcrypt · validator · slog · swaggo
**前端** Vue 3.5 · Element Plus 2.14 · vue-router 5 · pinia 4 · axios · Vite 8
**存储** SQLite（`ncruces/go-sqlite3` 纯 Go 驱动，`CGO_ENABLED=0` 可静态编译）· MySQL · PostgreSQL

刻意**不引入**的东西，以及为什么：

- **Casbin** —— 权限本质是「组 → 权限点集合」的包含关系，自研一个 Enforcer 更透明，
  也方便把判定逻辑和业务守卫写在一处。
- **viper** —— 依赖树远重于 koanf，而我们要的能力只有「yaml + 环境变量覆盖」。
- **google/uuid** —— Go 1.27 标准库已有 `uuid.NewV7()`。
- **第三方分页库** —— 分页要配「可排序/可过滤列白名单」，这层必须自己攥着；
  白名单不是防锦上添花，是防注入。
- **Redis / WebSocket** —— 通知红点 30 秒轮询一次足够，为它牵一条长连接会让部署
  多一层状态（尤其多实例时）。

Go 1.27 的几处新东西用在了实处：标准库 `uuid` 做有序主键、`http.CrossOriginProtection`
做跨站写请求防护（省掉第三方 CSRF 中间件）、`os.Root` 把附件读写锁死在根目录内
（天然防 `../` 穿越）、`crypto/rand.Text()` 生成密钥、`Server.MaxHeaderValueCount`
限流恶意大 header。

---

## 架构

分层严格单向：`handler → service → repository → db`。业务规则不写在 handler 里，
HTTP 细节不渗进 service。

```
cmd/xuliu/            入口：配置 → 日志 → 组装 → 优雅退出
configs/config.yaml   默认配置（可读的示例，值与内置默认一致）
internal/
  acl/                59 个权限点 + Enforcer（含数据级可见性）
  bootstrap/          依赖装配、AutoMigrate、种子数据
  config/             koanf：默认值 < yaml < 环境变量
  db/                 三驱动 gorm.Open、WithTx、迁移
  dto/                请求/响应结构 + validator 标签
  handler/            HTTP 入出参，无业务
  middleware/         RequestID / Recover / Logger / JWT / RequirePerm / 安全头
  model/              GORM 模型与枚举常量
  pkg/                apperr 错误码 / response 外壳 / pagination / hash / token
  repository/         泛型基类 Repo[T] + 各实体查询
  router/             按域拆分的路由表
  service/            业务逻辑，事务边界在这一层
  storage/            Storage 接口：local(os.Root) / s3 双实现
  web/                //go:embed all:dist —— 前端产物
  workflow/           声明式状态机
scripts/e2e.sh        核心闭环回归
web/                  Vue3 SPA 源码
docs/                 swag 生成的接口文档（构建产物，需入库以让 embed 生效）
```

### 三个承重设计

**1. 声明式状态机（`internal/workflow`）**

需求、任务、缺陷的生命周期如果用 `switch` 散在 service 里，三五个里程碑后必然失控。
这里改成转移表 —— 一个动作声明它的来源状态、目标状态、权限点、额外守卫：

```go
"finish": {
    Action: "finish",
    From:   []string{StatusWait, StatusDoing, StatusPause},
    To:     StatusDone,
    Perm:   "task-finish",
    Guard:  leftMustBeZero,        // 剩余工时不为 0 就不许完成
    Kind:   KindStatus,
    LogAction: model.ActionFinished,
}
```

新增一条流转是**加一行表项**，而不是在 service 里再插一个 `if`。
更关键的是：**这份表同时驱动前端的按钮**。每个对象的详情响应里带一个 `actions` 数组，
每项是 `{action, label, perm, to, kind, danger, enabled}` —— 前端照着渲染，
`enabled=false` 的按钮给出「需要权限：xxx」的提示而非静默隐藏。
于是「这个状态能不能关闭」这个问题在全世界只有一个答案。

**2. 权限（`internal/acl`）**

权限点是字符串对，如 `bug-resolve`、`story-review`。路由上挂
`middleware.RequirePerm("bug-resolve")`。

有两处值得一提的选择：

- **超管短路只短路功能权限，不短路数据可见性。** 超级管理员能执行任何动作，
  但不该自动看到所有私有产品的内容 —— 「能改」和「能看」是两件事。
- **每个动作各挂一个权限点**，而不是「一个 `/actions/:action` + 一个笼统权限」。
  后者会让「能确认缺陷」的人顺带拿到关闭缺陷的权力。项目的关闭同理：
  开始/挂起/激活共用 `project-start`，关闭单独用 `project-close`。

数据级还有一层：`VisibleProductIDs` / `VisibleProjectIDs`。返回**空切片**意味着
「什么都看不到」，实现上渲染成 `IN (NULL)`。这一层必须永远应用 ——
漏一次就是全库泄露。任务的可见性是**合取**的（产品若存在、项目、迭代三者都要通过），
缺陷则是「最具体的一层说了算」。

**3. 统一列表协议（`internal/pkg/pagination`）**

```
GET /api/v1/bugs?page=1&size=20&order=id_desc&productId=1&status=active&keywords=登录
```

约 40 行，配可排序/可过滤列白名单。统一分页响应 `{list,total,page,size}`（默认 20，上限 200）。
注意并非所有接口都分页 —— 下拉数据源这类返回裸数组，所以「有 `data.list`」不是通用假设。

### 响应外壳

```jsonc
// 成功
{ "data": …, "requestId": "…" }
// 失败
{ "code": "story_not_found", "message": "需求不存在", "requestId": "…" }
```

判成功只看 HTTP 状态码是否 2xx，不在成功体里再判一次 `code`。
`requestId` 两侧都有，用户报错时直接拿它去日志里定位。
5xx 才写 `slog.Error`（4xx 是调用方用错了，不该刷错误日志）。
内部原因（cause）只进日志、不回客户端 —— 不把 SQL 语句和文件路径漏给前端。

### 工时与进度

每个 `pm_effort` 行存本次消耗的 `consumed` 和**报工时之后**的剩余 `left`：

```
left > 0  →  progress = round(consumed / (consumed + left) * 100)
left == 0 →  progress = consumed == 0 ? 0 : 100
```

所以记 5 小时还剩 3 小时是 **63%**（不是 62.5，也不是一半），
这一条在 e2e 脚本里有断言。任务完成前剩余工时必须归零，否则 409 ——
「做完了但还有 3 小时没销」是自我矛盾的状态。

### 通知是待办，不是流水

同一 `(收件人, 对象类型, 对象 ID)` 的未读通知会被**覆盖**而不是追加：一个缺陷反复
流转，待办里始终只有一条，标题跟着变成最新那个动作。只有「通知下一个要动手的人」
才触发通知（任务被指派/完成/激活、缺陷被指派/解决/激活、需求提交评审/已评审），
且不给自己的操作发通知。

---

## 换数据库

默认 SQLite，零外部依赖。换库只改两行：

```yaml
database:
  driver: postgres     # sqlite / mysql / postgres
  dsn: host=127.0.0.1 user=xuliu password=xxx dbname=xuliu port=5432 sslmode=disable TimeZone=UTC
```

或走环境变量（前缀 `XULIU_`，双下划线表层级）：

```bash
XULIU_DATABASE__DRIVER=mysql \
XULIU_DATABASE__DSN='xuliu:xuliu@tcp(127.0.0.1:3306)/xuliu?charset=utf8mb4&parseTime=True&loc=UTC' \
  go run ./cmd/xuliu
```

能这么换的前提是模型层的自律：**只用三库通用的类型** —— 枚举存 `varchar` 不存
DB enum，JSON 字段用 `text`，时间统一 UTC，工时用 `decimal` 不用 `float`，
不用 PG 专属的 JSONB。同一份 `AutoMigrate` 在三个库上各建出等价的表。

用 compose 起来验证：

```bash
docker compose --profile mysql up -d       # 应用在 :8081
docker compose --profile postgres up -d    # 应用在 :8082
```

跨库回归 —— 同一个 e2e 脚本打 compose 起好的那套服务：

```bash
XULIU_E2E_SKIP_BOOT=1 XULIU_E2E_BASE=http://127.0.0.1:8081 bash scripts/e2e.sh   # mysql
XULIU_E2E_SKIP_BOOT=1 XULIU_E2E_BASE=http://127.0.0.1:8082 bash scripts/e2e.sh   # postgres
```

`SKIP_BOOT=1` 不是可省的：脚本默认会**自己起一个临时 SQLite 服务**并把目标地址改成它，
只给 `XULIU_E2E_BASE` 的话，跑完全绿而一次也没碰到 MySQL。现在这种给法会被脚本直接拒绝。

也可以让脚本自己起服务、只把库换成外部实例：

```bash
XULIU_E2E_DRIVER=mysql XULIU_E2E_DSN='xuliu:xuliu@tcp(127.0.0.1:3306)/xuliu?charset=utf8mb4&parseTime=True&loc=UTC' \
  bash scripts/e2e.sh
```

### 这次实际验证到了哪一步

说清楚边界，不含糊：

| 验证项 | 状态 | 怎么验的 |
|---|---|---|
| SQLite 上的完整核心闭环（含测试域） | 已跑通 | `scripts/e2e.sh`，172 项断言 |
| 全部 35 张表真建出来 | 已跑通 | `TestAutoMigrateAllModelsOnSQLite` |
| 508 个列在 MySQL / PG 方言下映射成合法类型 | 已跑通 | `TestColumnTypesAcrossDialects` |
| 工时列是 `decimal` 而非 `double`（三库精度一致） | 已断言 | 同上 |
| 没用 PG 专属的 `jsonb` / 数组列 | 已断言 | 同上 |
| **对着真实 MySQL / PostgreSQL 跑 e2e** | **未执行** | 本机既没有 Docker 也没有 mysql/psql 客户端 |

那个方言测试不需要数据库服务器 —— 它直接问每个方言的 Dialector「这个字段落成什么列」，
走的正是 `AutoMigrate` 内部的类型映射路径，所以「模型里混进了方言专属类型」这类
会在用户换库当天才爆的问题，现在在 CI 阶段就会红。

但它验不到排序规则、标识符大小写敏感、真实数据下的唯一索引行为、以及查询层的方言差异。
那几条只能对着真实例跑 —— 命令已经写在上面，等有库了执行即可。


---

## 前端开发

```bash
cd web
npm install
npm run dev          # Vite 开发服务器，/api 代理到 127.0.0.1:8080
```

另开一个终端跑后端（`go run ./cmd/xuliu`），前后端分离调试。
`npm run build` 会把产物直接写到 `../internal/web/dist`（`go:embed` 只能引用
包目录内的文件，所以输出目录不在 `web/` 下），然后 `go build` 出一个带前端的单二进制。

几个约定：

- **菜单不是写死的**，是「路由表 ∩ 当前用户的权限点」。后端在登录时把权限点整份
  给了前端，前端据此决定侧边栏显示什么、这个 URL 进不进得去。加权限点、改角色，
  前端一行都不用动。这仍然只是体验层拦截，真正的授权在服务端。
- **不用 `keep-alive`**：列表页的筛选条件跟着 URL 走（从产品页点「需求」会带
  `productId` 进来），缓存住组件会让查询条件和页面显示对不上。
- 状态、按钮可用性全部来自后端的 `actions`，前端不自己判断状态流转。
- **index.html 与无哈希的根级文件一律 `no-store`**，带内容哈希的 `assets/` 才长缓存。
  别把它改回 `no-cache` + 固定 `Last-Modified`：那样浏览器每次条件请求都会拿到 304，
  发版后老用户永远加载旧的前端 —— 症状是「明明改了却不生效」，既没有报错也没有 404。
  `internal/web/web_test.go` 把这条钉死了。

---

## 接口文档

非生产环境下自动挂载：`http://127.0.0.1:8080/swagger/index.html`。

重新生成：

```bash
go tool swag init -g cmd/xuliu/main.go -o docs --parseDependency --parseInternal --parseDepth 3
```

生产环境不挂这个路由 —— 接口文档等于一份完整的内部结构说明书，对开发期极有价值，
对公网暴露只是信息泄露。要在线上开，应由网关做白名单，而不是把默认值改成「开着」。

生成器有两个坑值得记一下（swag v1.16.6）：它解析不了 `pagination.Result[dto.X]`
这种泛型实例化（报 `cannot find type definition`），所以分页接口的响应要写成
`{object} response.Body` 加一句中文说明；`@Router` 的路径参数要用 `{id}` 花括号语法。

---

## 测试

```bash
go build ./... && go vet ./... && go test ./...
bash scripts/e2e.sh
```

单元测试覆盖的是「算错就会静默污染数据」的地方：配置加载与环境变量覆盖、
三驱动打开与拒绝未知驱动、密码哈希、令牌、分页白名单与 LIKE 转义、
状态机（尤其需求评审的 `revert`：版本 -1 且删最新 spec/review 行，顺序错了会留下脏数据）、
工时与进度公式、变更历史 diff、存储层的路径越界防护、
测试用例的评审与版本（步骤重排后仍算内容变更）、测试单的执行快照与结单约束。

另有几条**交付期加的回归**，它们各自钉住一件「出错不会报警」的事：

- `internal/db/migrate_dialect_test.go`：同一套模型在 MySQL / PostgreSQL 方言下
  生成的列类型必须一致（工时是 decimal/numeric、不许出现 jsonb 或数组列）。
- `internal/router/swagger_coverage_test.go`：把真实注册的路由表与 `docs/swagger.json`
  做双向比对 —— 新增接口忘了写注释、或 `@Router` 的路径写错，都会在这里失败。
- `internal/web/web_test.go`：index.html 必须 `no-store` 且永远不能回 304（否则发版
  后用户永远拿到旧前端），SPA 回退必须显式写 200（否则页面内容对、状态码是 404）。
  它抓到过一处真实问题：三个状态动作接口被记成 `/projects/{id}/{action}` 这类模板路径，
  而路由表里其实是 `start` / `suspend` / `close` / `activate` 四条具体路由，
  照文档去调只会得到 404。

---

## 与禅道的差异

以下是**有意为之**的偏离，不是没抄到：

| 禅道 | 序流 | 为什么 |
|---|---|---|
| 项目与迭代共用 `zt_project`，靠 `project`/`type` 列区分 | 拆成 `pm_project` 与 `pm_execution` 两张表 | 多态同表在 Go 里意味着到处做类型判断；拆开之后模型清晰、外键正确 |
| 表间外键用 `account` 字符串 | `uint` 主键外键（`account` 保留唯一索引） | 改账号名不需要级联更新，join 更快 |
| 软删用 `deleted tinyint` | GORM `DeletedAt` 时间戳 | 顺带保留删除时间，与 GORM 软删原生集成 |
| `zt_action` + `zt_history` 两张表 | `sys_action` 单表 + `changes` JSON 字段 | 字段级 diff 只用于渲染、从不用于查询，免掉 N+1 join |
| 产品线是 `zt_module` 里 `type='line'` 的行 | 独立 `pm_productline` 表 | 复用树表存扁平列表是历史包袱 |
| 口令 `md5`（新版 md5+salt） | bcrypt，cost 12 | md5 系列不该再用于口令存储 |
| `vision` 列区分 rnd/lite/ipd | 不做 | 只做研发版 |
| 292 张表 | 35 张 | 核心闭环优先 |

**保留**的禅道口径：领域模型与枚举取值、状态机守卫条件（含 `activate` 承担重新激活、
需求 `revert` 的版本回退、任务完成前剩余工时归零）、操作历史的 `action` 动词与
`extra` 语义（状态变更存 `"old|new"`，需求激活靠它恢复关闭前的状态）、
工时与进度公式。

还有一处自己的设计：**迭代团队必须是项目团队的子集**。往迭代里加人前，
他必须先在这个项目的团队里 —— 于是「能看到项目里有哪些人」这一份名单就够用了。

---

## 配置

全部配置项见 `configs/config.yaml`，文件里的值与内置默认一致，作用是「可读的示例」。
优先级：内置默认值 < YAML 文件 < 环境变量。

值得单独一提的：

- `app.bootstrap` —— 首次启动建初始管理员，**仅在库中没有任何用户时生效**。
  生产环境请改掉默认口令。
- `server.corsOrigins` —— 只在前后端分离开发时填 Vite 的地址；生产同源部署留空，
  此时启用最严格的跨站防护。
- `server.trustedProxies` —— **默认不信任任何代理**。gin 默认是信任全部（`0.0.0.0/0`），
  意味着任何请求都能靠 `X-Forwarded-For` 伪造 `ClientIP()`。
- `storage.backend` —— `local` / `s3` 双实现。本地实现走 `os.Root`，
  路径锁死在附件根目录内；S3 兼容 MinIO / OSS / COS / AWS S3。
- `database.autoMigrate` —— 首次部署建完表之后可以关掉。

---

## 许可

待定。
