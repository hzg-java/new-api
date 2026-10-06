# AGENTS.md — new-api 项目规范

不要发送非必要的说明。

## 项目概述

这是一个使用 Go 构建的 AI API 网关/代理。它通过统一 API 接入 40 多家上游 AI 服务提供商（OpenAI、Claude、Gemini、Azure、AWS Bedrock 等），并提供用户管理、计费、限流和管理后台。

## 技术栈

- **后端**：Go 1.25.1（参见各模块的 `go.mod`）、Gin Web 框架、GORM v2 ORM
- **前端**：React 19、TypeScript、Rsbuild 2、TanStack Router/Query/Table、Zustand、Base UI、Tailwind CSS 4
- **数据库**：主数据库支持 SQLite、MySQL、PostgreSQL（三者必须全部支持）；独立配置的日志数据库还支持 ClickHouse
- **缓存**：Redis（go-redis）+ 内存缓存
- **身份认证**：浏览器会话、API 令牌和个人访问令牌、JWT、WebAuthn/Passkeys、TOTP、OAuth/OIDC；`service/authz/` 中使用 Casbin 进行授权
- **扩展**：通过 Sobek 执行的 JavaScript 任务插件；Electron 桌面封装
- **前端包管理器**：Bun（优先于 npm/yarn/pnpm）

## 架构

- Go 网关通过 `router/`、`middleware/`、`controller/`、`service/`、`model/` 和 `relay/` 处理管理 API、上游请求中转、计费和后台任务。
- `relaykit/` 是独立的 Go 模块，负责协议 DTO 和转换；传输、身份认证、数据库访问和计费仍由宿主负责。
- JavaScript 任务插件位于 `plugins/tasks/`，通过 `pkg/jsplugin/` 运行，并与宿主的任务轮询和结算机制集成。
- `web/` 是 React 前端（参见 `web/AGENTS.md`）；`electron/` 是桌面封装。

## 国际化（i18n）

### 后端（`i18n/`）
- 使用库：`nicksnyder/go-i18n/v2`
- 语言：en、zh

### 前端（`web/src/i18n/`）
- 使用库：`i18next` + `react-i18next` + `i18next-browser-languagedetector`
- 语言：en（基础语言）、zh（回退语言）、zh-TW、fr、ru、ja、vi
- 翻译文件：`web/src/i18n/locales/{lang}.json` — 扁平 JSON，以英文原文字符串作为键
- 使用方式：使用 `useTranslation()` Hook，在组件中调用 `t('English key')`
- 命令行工具：`bun run i18n:sync`（在 `web/` 目录中运行）

## 规则

### 通用代码质量

- 新代码应保持直接、易读。优先使用提前返回、清晰的分支和命名明确的局部变量，避免深层嵌套或多层控制流。
- 尽量减少嵌套函数定义。仅在回调 API 要求这样做，或将闭包保留在局部明显比新增一个符号更简单时使用。
- 避免添加只有一个调用方、且不表达稳定业务概念的包级或模块级辅助函数。应将该逻辑直接内联到调用处。
- 当函数代表可复用行为、接口/框架要求的回调、导出 API、测试夹具，或值得直接测试的复杂业务逻辑时，适合将其独立定义。
- 如果保留仅使用一次的辅助函数，其名称必须描述长期有效的领域概念，而不是仅为缩短调用方代码而提取出的机械步骤。

### 身份认证安全（强制遵循 OWASP）

- 任何涉及身份认证流程的实现、修改或审查，都必须符合最新稳定版 [OWASP 应用安全验证标准（ASVS）](https://owasp.org/www-project-application-security-verification-standard/) 的适用要求，以及相关的 [OWASP 安全速查表系列](https://cheatsheetseries.owasp.org/)。这适用于后端和前端变更，包括注册、登录/退出、密码修改与找回、邮箱验证、MFA、WebAuthn/Passkeys、OAuth/OIDC、账号绑定/解绑、会话、JWT、API 凭证，以及敏感操作的重新认证。
- 修改这些流程之前，必须阅读适用的 OWASP 指南，首先阅读[身份认证速查表](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)和[会话管理速查表](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)。涉及密码存储、忘记密码、MFA、OAuth 和 CSRF 机制时，还应查阅对应指南。在实现之前明确适用的安全控制措施；现有代码不能成为保留或引入不安全模式的理由。
- 必须在服务端强制执行安全控制。落实有关凭证存储和传输、防账号枚举和暴力破解、CSRF 和重放防护、按要求设置令牌/挑战的过期与一次性使用、协议专属验证、会话轮换与失效，以及敏感账号变更重新认证的要求。前端检查不得替代服务端强制执行，账号恢复或其他登录路径也不得绕过所需的身份认证保障。
- 身份认证审计事件不得包含密码、验证码、恢复码、私钥，以及可用的会话令牌或身份认证令牌。应记录足够的非机密上下文，以便调查身份认证失败和敏感账号变更。
- 按照现有后端/前端测试规范，使用有针对性的回归测试验证受影响的安全控制，包括适用的失败、过期、重放和绕过场景。在变更摘要或 PR 描述中记录 OWASP 参考资料（使用 ASVS 时包括版本和要求编号）、已执行的验证，以及尚未解决的缺口。只要仍有适用的安全要求未满足或未验证，就不得声称已合规或已完成。

### 后端规则

**现代 Go 规范：** 对新增或修改的 Go 代码（包括测试和 `relaykit/`），在保持行为不变且提高可读性的前提下应用这些规范。以相关模块 `go.mod` 中声明的 Go 版本作为兼容性基线。

- 使用 `any` 代替 `interface{}`，包括 map 的值、切片元素、参数和返回类型。
- 对固定次数循环，优先使用 `for i := range n`；不使用索引时，使用 `for range n`。遍历切片索引时，优先使用 `for i := range items`。如果边界在迭代过程中发生变化，或循环需要不同的起点或步长，则保留传统循环。
- 如果分割结果只遍历一次，且不需要索引访问或重复使用，优先使用 `strings.SplitSeq` 或 `bytes.SplitSeq`，而不是使用 `Split` 分配切片。
- 在第一个分隔符处拆分时使用 `strings.Cut`；检查并移除前缀或后缀时使用 `strings.CutPrefix` / `strings.CutSuffix`。避免为同一操作分别执行查找和手动切片。
- 使用 `slices.Contains` / `slices.ContainsFunc` 进行成员检查，使用 `slices.Sort` 对可排序元素类型进行自然排序，代替等效的手写循环或排序回调。
- 使用 `maps.Copy` 进行 map 的浅拷贝和合并。按需初始化目标 map，并保留 nil 与空 map 的行为差异，以及后续值覆盖先前值的顺序。它不能替代深拷贝。
- 使用内置的 `min` / `max` 处理简单边界，代替等效的条件赋值。保持数值语义不变；这些函数不能防止参数中的运算溢出，也不能替代计费校验和安全额度转换。
- 在循环中反复拼接字符串时使用 `strings.Builder`；简单的固定表达式仍使用直接拼接。
- 静态已知类型时使用 `reflect.TypeFor[T]()`，并使用 `reflect.Pointer` 代替 `reflect.Ptr`。需要获取值的动态类型时，保留 `reflect.TypeOf`。
- 当生命周期和 panic 约定适用时，对于标准的 `Add(1)` / goroutine / 延迟执行 `Done()` 模式，优先使用 `sync.WaitGroup.Go`。保留现有的恢复行为；传入 `Go` 的函数不得发生 panic。
- 移除仅为 Go 1.22 之前的闭包捕获行为而存在的冗余循环变量拷贝，例如 `tc := tc`。如果拷贝用于真正的快照语义，或变量在循环外赋值，则应保留。
- 只有确认当前使用的 JSON 编码器能够保持输出不变后，才可移除非指针结构体字段上无效的 `omitempty` 标签。不得以风格清理为由改变字段类型或省略行为；可选的中转标量字段仍须遵守下文的指针规则。
- 使用 `gofmt` 格式化修改后的 Go 文件，并移除这些变更产生的未使用导入。

**relaykit 模块独立性：** `relaykit/` Go 模块必须保持可独立构建。

- `relaykit/` 下的代码不得导入或依赖根 `new-api` 模块中的包，也不得依赖仅在根模块中存在的配置、生成文件或工作区连接配置。
- 任何影响 `relaykit/` 或其公共 API 的变更，都必须通过 `cd relaykit && GOWORK=off go build ./...` 验证；仅根模块构建成功并不足够。

**JSON 包：** 在根 Go 模块中，所有 JSON 序列化/反序列化操作都必须使用 `common/json.go` 中的封装函数：

- `common.Marshal(v any) ([]byte, error)`
- `common.Unmarshal(data []byte, v any) error`
- `common.UnmarshalJsonStr(data string, v any) error`
- `common.DecodeJson(reader io.Reader, v any) error`
- `common.GetJsonType(data json.RawMessage) string`

不得在业务代码中直接导入或调用 `encoding/json` 来执行 JSON 操作。仍可引用 `encoding/json` 中的 `json.RawMessage`、`json.Number` 等类型定义，但实际的序列化/反序列化调用必须通过 `common.*`。

在 `relaykit/` 内，使用 `relaykit/relayconvert/kitutil/json.go` 中的 `kitutil.*`，绝不能使用宿主的 `common`。直接调用编码器的代码只能放在编解码器实现中。

**数据库兼容性：** 所有数据库代码必须同时兼容 SQLite、MySQL >= 5.7.8 和 PostgreSQL >= 9.6。

- 任何可能影响数据库行为的变更，都必须在认定工作完成之前进行验证。这包括 ORM/数据库驱动依赖变更、连接/DSN/协议或预处理语句配置、模型与 GORM 标签、迁移与 `AutoMigrate`、约束和索引、`Scanner`/`Valuer`/序列化器行为、原生 SQL、事务和行锁。
- 要求的数据库验证必须使用真实的 SQLite、MySQL 和 PostgreSQL 实例。单元测试、mock、构建成功、代码检查或仅测试一种数据库方言都不能替代。每种引擎至少使用一个受支持版本；依赖特定版本行为的变更，还必须覆盖最低支持版本。
- 将 GORM 核心及其数据库方言/驱动包视为一组相互兼容的版本。修改其中任意一项，都需要检查上游兼容性并运行完整的三数据库验证矩阵；不得仅升级核心包，就推断现有驱动仍然兼容。
- 数据库结构或迁移变更必须同时在全新数据库上测试，并通过升级由最新发布版本创建的代表性数据库进行测试。至少运行两次启动/迁移以证明幂等性，并验证现有数据、索引、约束和唯一性保证均得到保留。如果受影响的路径与独立配置的日志数据库共用，或被其使用，还必须覆盖该日志数据库。
- 在最终交付说明或 PR 中记录准确的数据库版本、命令和结果。如果无法执行任何必需的数据库验证，必须明确报告阻塞原因，不得声称该变更已兼容数据库或已完成。
- 优先使用 GORM 方法（`Create`、`Find`、`Where`、`Updates` 等），而不是原生 SQL。
- 让 GORM 处理主键生成；不要直接使用 `AUTO_INCREMENT` 或 `SERIAL`。
- 在 `model/` 中，通过 GORM 查询方法构建的标准 `SELECT ... FOR UPDATE` 行锁必须使用 `lockForUpdate(tx)`。不得使用旧的 GORM v1 写法 `tx.Set("gorm:query_option", "FOR UPDATE")`，因为 GORM v2 会静默忽略它，导致没有获取任何锁。不要在调用处重复编写 `clause.Locking{Strength: "UPDATE"}`；共享辅助函数会对 MySQL/PostgreSQL 生成 `FOR UPDATE`，对不支持该语法的 SQLite 则跳过。具有不同语义的数据库方言专属锁（例如 MySQL 的 next-key/gap lock）只有在明确的数据库类型分支下，并为每种受支持数据库提供有效回退方案时，才可以使用原生 SQL。
- 无法避免原生 SQL 时，应处理数据库方言差异：
  - PostgreSQL 使用 `"column"` 引用列名，MySQL/SQLite 使用 `` `column` ``。
  - 对于 `group` 和 `key` 等保留字列名，使用 `model/main.go` 中的 `commonGroupCol`、`commonKeyCol`。
  - 布尔值使用 `commonTrueVal`/`commonFalseVal`。
  - 主数据库分支使用 `common.UsingMainDatabase(...)`，日志数据库分支使用 `common.UsingLogDatabase(...)`。
- 没有跨数据库回退方案时，不得使用数据库专属功能，包括 MySQL 专属函数、PostgreSQL 专属运算符、SQLite 不支持的 `ALTER COLUMN`，以及未提供 `TEXT` 回退的数据库专属 JSON 列类型。
- 迁移必须在三种数据库上都能工作。对于 SQLite，使用 `ALTER TABLE ... ADD COLUMN`，而不是 `ALTER COLUMN`（参见 `model/main.go` 中的模式）。
- 如果默认值是已由代码强制执行的业务规则，应避免使用 `gorm:"default:true"` 等 GORM 布尔默认值标签。MySQL 和 PostgreSQL 对布尔默认值的规范化方式可能不同，导致 GORM `AutoMigrate` 在每次重启时重复执行 `ALTER TABLE`。优先在请求/模型规范化、钩子、构造函数或服务逻辑中设置这些默认值；除非已在 SQLite、MySQL 和 PostgreSQL 上验证行为，否则不得将 `default:true` 替换为 `default:1`。

**请求中转与服务提供商行为：**

- 实现新渠道时，确认服务提供商是否支持 `StreamOptions`；如果支持，将该渠道加入 `streamSupportedChannels`。
- 对于从客户端 JSON 解析后再重新序列化并发送给上游服务提供商的请求结构体，可选标量字段必须使用带 `omitempty` 的指针类型（例如 `*int`、`*uint`、`*float64`、`*bool`）。
- 保留上游中转请求 DTO 中显式设置的零值：客户端 JSON 中缺失的字段必须变为 `nil` 并省略；显式设置的 `0`、`0.0` 或 `false` 必须保持非 `nil`，并发送给上游。
- 对可选请求参数，避免使用带 `omitempty` 的非指针标量，因为零值会在序列化时被静默丢弃。

**JavaScript 任务插件（强制要求）：**

- 在实现、修改或审查 JavaScript 任务插件或其宿主 API/运行时之前，必须阅读[任务插件 API v1](docs/plugin-api/v1.md)，包括描述编写和翻译规范。修改插件契约时，还需检查 `docs/plugin-api/v1.schema.json` 和 `docs/plugin-api/v1.d.ts` 的一致性。
- 对于 `usageSchema` 和 `usageProfiles[].schema` 中的数值计费字段，`description` 必须表述为**计费对象 + 单价**，因为它用于标注 UI 中的价格输入框。例如，`image_count` 使用 `Image generation unit price` / `图片生成单价`，而不是 `Generated image count` / `生成图片张数`；`seconds` 使用 `Video generation unit price` / `视频生成单价`。字段值仍然是用量，而不是价格。
- 单位放在 `unit` 中。协议限制、用量来源、默认值、预估和结算细节应放在代码注释或技术文档中。描述必须简短、各语言语义一致，不得包含具体价格数字或末尾标点。对于动作、布尔值和其他枚举条件，应遵循 API 文档中各自的措辞规则。
- 完成插件工作之前，必须明确审查元数据措辞。这些属于编写要求；编译成功、schema 验证通过或测试通过，并不能证明描述符合这些要求。

**计费规则（强制阅读前置条件）：** `.agents/rules/billing.md` 包含计费规范（表达式系统、内置定价、安全不变量、从上游响应中推导可计费用量）。如果任务涉及下文定义的计费范围，在规划、编码或审查之前，必须使用文件读取工具完整阅读该文件，不能以 grep、局部浏览、记忆或摘要代替，并遵守其中的每一条规则。满足以下任一条件的任务都属于涉及计费：

- 修改 `pkg/billingexpr/`、`setting/billing_setting/`、`common/quota_math.go`、`types/price_data.go`、`relay/request_billing.go`、`relay/image_handler.go`、`relay/relay_task.go`、`relay/helper/price.go`、`relay/helper/billing_expr_request.go`、`relay/helper/valid_request.go`、`service/quota.go`、`service/text_quota.go`、`service/image_billing.go`、`service/tiered_settle.go`、`service/task_billing.go`、`service/responses_usage.go`、`service/log_info_generate.go` 的计费部分、`model/pricing*.go` / `model/model_pricing*.go` 文件，或 `relay/common/relay_info.go` 的计费字段和方法（`PriceData`、`TieredBillingSnapshot`、`BillingImageCount`、`UpdateImageCount`）。
- 在其他任何位置读取或写入 `PriceData` / `OtherRatios`，或者涉及额度预扣、结算、退款或消费日志中的计费字段。
- 在任何渠道适配器、响应处理器或任务插件中，从上游响应或流中推导可计费用量或 `Usage`（图片数量、秒数、token 数、任务扣费）。
- 校验、限制或转发会成为计费乘数的请求字段（`n`、`max_tokens` 系列字段、时长、分辨率或质量、批次数量），包括透传和 multipart 路径。
- 新增或修改模型价格，或任务插件中数值类型的 `usageSchema` / `usageProfiles[].schema` 字段。

不涉及上述任何内容的任务（例如无关的前端工作、身份认证、数据库迁移，或不改变用量的协议转换）无需阅读该文件。

**后端测试质量：** 后端测试必须保护真实行为、API 契约、计费/账务不变量、数据兼容性或回归路径。

- **不要为小改动分散创建测试：** 对于范围集中的功能或修复，优先扩展现有且合适的测试文件。如果必须新增测试文件，最多新增一个，并将关键回归用例集中在其中。不得仅因为调用链跨越多个层，就在 `controller/`、`service/`、`setting/` 或其他层为同一个小功能分别创建测试文件。不要在每一层重复测试夹具和断言。用例应紧凑，并聚焦可观察行为；修改的生产代码文件数量不是增加测试文件的理由。
- 不要添加仅用于提高覆盖率数字、证明代码碰巧能运行，或在没有用户可见契约或跨模块契约的情况下固化实现细节的测试。
- 避免使用随机输入、大量循环、休眠、耗时比较或仅检查日志的断言构建伪模糊/压力/冒烟/性能测试。
- 避免用不同名称重复测试同一分支，却没有保护任何新的不变量。
- 避免通过测试迫使生产代码采用错误的服务提供商/协议语义。
- 如果其他位置已覆盖可观察行为，不要再对私有常量、查询字段列表、辅助函数内部细节或文件布局进行断言。
- 优先使用确定性的表驱动测试，提供明确输入和精确的预期输出。
- 测试需要数据库、请求上下文、用户分组、设置或缓存状态时，应在测试夹具中显式初始化这些状态。
- 新增或大幅重写的 Go 后端测试必须使用 `github.com/stretchr/testify/require` 进行初始化和失败即终止的断言，使用 `github.com/stretchr/testify/assert` 进行失败不终止的值检查。
- 避免手写断言辅助函数，除非它们表达了可复用的项目特定不变量。
- 清理测试时，保留有意义的回归覆盖。如果删除的测试间接覆盖了真实契约，应使用更小、直接断言该契约的测试替代。

**文档文件：**

- 除非用户明确要求，否则不得在 `docs/` 或其任何子目录下新增文件。
- 不得在本仓库 `plugins/` 下的插件目录中创建或生成文档文件，包括 `plugins/tasks/<plugin>/` 及其子目录。无论文件格式如何，都包括 README、更新日志、使用指南和其他文档文件。

### 前端规则

- **优先复用现有 UI 组件（强制要求）：** 在实现或修改前端 UI 之前，阅读 `web/AGENTS.md` 和项目的 `shadcn-ui` 技能，搜索 `web/src/components/` 及相关功能中的现有组件，并阅读匹配的实现和调用处。未经仓库检查，不得直接从自定义标记或组件注册表安装开始。
- 如果项目的共享业务组件能够覆盖使用场景，应优先使用它们，而不是底层 UI 基础组件。在引入替代实现之前，评估现有 props、组合方式和兼容扩展。如果 `CopyButton` 或 `ConfirmDialog` 等共享组件已经提供相同行为，仅导入 `Button` 或 `AlertDialog` 并不满足本规则。
- 对常见 UI 行为新增实现，必须存在具体的能力缺口：在变更摘要或 PR 描述中指出现有候选组件，并解释为什么复用、组合或兼容扩展不适用。仅有文字、尺寸、颜色或功能位置不同，不足以成为重复实现的理由。功能组件可以将共享组件与业务数据和操作进行组合。遵循 `web/AGENTS.md` 中的复用流程和组件入口；通用库或组件注册表指南不能覆盖这一项目特定优先级。
- 前端（`web/`）优先使用 `bun` 作为包管理器和脚本运行器：
  - `bun install`：安装依赖
  - `bun run dev`：启动开发服务器
  - `bun run build`：执行生产构建
  - `bun run i18n:*`：运行国际化工具
- 前端 UI 文本必须通过 `i18next`/`react-i18next` 支持国际化。使用 `web/src/i18n/locales/{lang}.json` 中的扁平 JSON 语言文件，以英文原文字符串作为键。
- 在 React 组件中，使用 `useTranslation()`，并通过 `t('English key')` 输出面向用户的文本。
- **数字格式化与 Intl 区域设置（强制要求）：** 普通数字/紧凑数字显示复用 `@/lib/format`，金额显示复用 `@/lib/currency`；保留各格式化器的精度和单位语义。`zhCN` / `zhTW` 等界面语言代码不是有效的 Intl 区域设置。任何传入 `Intl.*`、`toLocaleString` / `toLocaleDateString` / `toLocaleTimeString` 或支持区域设置的格式化辅助函数的界面语言，都必须先经过 `@/i18n/languages` 中的 `toIntlLocale` 转换。不得重复实现语言映射，也不得通过别名传递原始语言代码。lint 规则和回归要求遵循 `web/AGENTS.md`。
- 详细前端规范遵循 `web/AGENTS.md`，包括 TypeScript、组件结构、样式、无障碍、测试和构建检查。