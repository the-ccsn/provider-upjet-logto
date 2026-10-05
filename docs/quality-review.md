# Logto Terraform / Upjet 质量审查

> 这是 2026-10-04 的历史验证快照，资源数量、覆盖率和 package 结果针对当时的六类资源／17 CRD。此后的本地迁移扩展见 `docs/migration-validation.md`；不能将下方历史结果当作新增资源的验证结论。

日期：2026-10-04。范围：本地 `terraform-provider-logto` 与 `provider-upjet-logto` 的当前工作区，包括未提交改动。审查基线分别为 `9ca998a15d9b3c1399d0cf9ca0ce863f88140707` 和 `fa46ff9a40b1858ad4f91cda09b4ad695fe71bc0`。

## 结论

**已完成核心代码、测试与交付链路的企业级工程加固，并补齐真实 Logto 和本地 Kubernetes API Server 的端到端验收。生产发布仍须通过实际 Crossplane 集群安装／升级和远端 required checks。**

六类资源已在 Logto 1.40.1 完成真实协议 CRUD/import；六类 CRD 的引用、漂移修复、重启恢复、Secret 发布和空角色撤销已在本地 API Server 实测。嵌入运行镜像的 XPKG、SPDX SBOM 和 SHA-256 清单已经构建并验证。GitHub CI 新增临时 Logto、API Server 和 package 验收，并通过 actionlint；远端执行尚未发生。

“企业级”以放行条件判定，不以编译成功、单元测试通过或总覆盖率单独判定。本轮没有操作生产 Logto、应用 Kubernetes 清单、发布镜像、推送 Git 或发布包。

## 已修复的问题

| 级别 | 问题与后果 | 修复和验证 |
| --- | --- | --- |
| P1 | 启动时未初始化异步操作跟踪器，首次调和可能空指针 | 两种作用域均初始化 OperationTrackerStore；实际启动 provider 进程验收 |
| P1 | 新建资源创建前的 Read 接收空 ID，直接报错导致无法创建 | 六类资源将未初始化 ID 视为不存在；真实创建及全资源无 API 请求回归 |
| P1 | 创建应用时空回调列表编码成 null，真实 API 返回 400 | 客户端复制并归一化为空数组；真实 Logto 及无输入修改回归 |
| P1 | CRD 的 omitempty 丢失显式 []，无法撤销全部角色／scope | 生成阶段使用 JSON omitzero 和无 omitempty 的 TF tag，保留 nil/[] 区别；序列化、late-initialize 和真实撤销验收 |
| P1 | 用户角色 PUT 接受 M2M 角色，但 GET 隐藏这些角色，造成永久漂移 | PUT 前验证角色存在且为 User；任何非法角色都不产生写入 |
| P2 | 两种作用域各有独立全局限流器，总请求速率超出设定上限 | 两组控制器共用同一个全局限流器；实际双作用域调和验收 |
| P2 | 启动缺健康探针及参数校验；changelog 使用错误 provider 名 | 增加 healthz/readyz、正数／端口校验、默认不缓存 Secret，修正标识；进程集成验收 |
| P1 | 应用 PATCH 写入只读 `isAdmin:false`，并替换不完整的 metadata，可能丢失管理权限、TTL、刷新令牌与其他登录设置 | PATCH 只发送管理字段；先读取原始 metadata 并合并，保留未知字段；HTTP 回归测试验证 |
| P1 | Optional+Computed 字段在普通更新中变为 unknown，被转成 false／空字符串，可能清空用户标识、profile、描述或布尔标记 | schema 使用 `UseStateForUnknown`；协议回归包含已存在的 true 标记、身份信息和 profile |
| P1 | 省略应用 URI／CORS 配置会被默认为空并清除现有配置 | 省略时保留已有状态，新建省略时默认空；显式空列表仍可清空；协议测试覆盖保留现有回调 URI |
| P1 | 生成的嵌套对象转换不处理 null／unknown，计划变更时报 `Attribute Missing` | 生成阶段通过 Go AST 给对象转换补上 null／unknown 分支；协议测试覆盖整个 profile 为 unknown 的场景 |
| P1 | scope 和用户角色只读取默认第一页，误判删除或漏掉权限 | 共用完整分页读取；重复页、空标识、超过页数限制返回错误并保留状态；跨页回归测试 |
| P1 | 空用户角色列表无法撤销全部角色 | PUT 明确发送 `[]`；创建时也通过 PUT 管理完整指定集合 |
| P1 | 角色创建后读取 scopes 失败可能遗留无状态的角色 | 在第二次请求前保存已创建 ID；部分创建失败测试 |
| P1 | 角色 scope 变更需要替换角色，丢失成员关联；普通 PATCH 返回值缺 scopes | 原地调和关联；更新保留角色 ID；保留已配置列表顺序，避免 API 无序返回造成错误状态 |
| P2 | 满额角色先添加新 scope 再删除旧 scope，永久无法收敛 | 先撤销过期授权再添加；配额及添加失败后重试恢复测试；中间失败可能暂时减少授权，保留必要授权 |
| P1 | 外部 ID 经 path.Join 归一化后可能请求错误资源；null 集合可能被当作资源不存在 | 路径标识与 secret 名称校验，拒绝路径跳转／保留字符；null 集合报错并保留状态；无 API 请求回归测试 |
| P1 | DELETE 遇到远端已删除资源时报错，部分成功响应未关闭 | 删除接受 404；所有成功与错误路径关闭响应体；重复删除测试 |
| P1 | token 缓存、401 恢复、并发刷新与错误内容泄露缺乏保障 | token 校验、缓存与一次刷新；并发缓存测试；阻止凭据跟随重定向；错误不包含响应正文 |
| P1 | 不明确的重试可能重复创建资源 | 仅 GET 对 429/502/503/504 有有限重试；支持取消；Retry-After 秒数／HTTP 日期均支持，超大数值截断后计算；POST/PATCH/DELETE 不重试不明确的暂态失败，401 仅刷新一次 |
| P1 | 可达依赖漏洞 | 两个模块升级 gRPC 至 v1.83.2，Terraform 同时升级 x/net、x/text 等；最终 govulncheck 均未发现漏洞 |
| P1 | 重新生成会丢失 sensitive schema，或给手写 secret 生成重复实现 | 以受审查的 `provider_code_spec.json` 为唯一 schema 输入；固定生成器 v0.4.1；排除手写 secret；验证再次生成无差异 |
| P1 | Upjet 无独立可运行 CI，依赖相邻工作区，镜像 Makefile 引用缺失 build 子模块 | 使用带 SHA-256 清单的本地源码快照；独立构建；修复镜像入口；加入 CI |
| P1 | 普通 Terraform CI 依赖生产／上游组织私有凭据 | 无凭据 CI；真实验收使用独立 integration build tag 和临时凭据；移除上游 Vault 自动发布流程 |
| P1 | 凭据来源及 namespace 行为不明确 | 仅允许 Kubernetes Secret；namespaced ProviderConfig 强制本 namespace，复制 selector 避免修改共享对象；错误、跨 namespace、缺字段测试 |

## 测试与 CI

| 检查 | Terraform | Upjet |
| --- | --- | --- |
| 离线资源生命周期 | 六类资源创建、读取、更新、导入、远端删除、重复删除；secret 参数变更使用 replacement | 嵌入同一组测试 |
| Terraform protocol v6 | Validate、Plan、Apply 创建／更新／删除、Read；敏感字段、unknown 与状态转换 | 使用同一 Framework provider，并编译所有生成控制器 |
| `make verify` | race、跨包覆盖率、完整 vet、二进制构建 | 快照校验、嵌入模块 race/vet、根模块 race/vet、静态控制器构建 |
| 静态检查 | staticcheck 通过 | staticcheck 通过；仅豁免模板／兼容 API 的 SA1019 弃用提示，其他检查启用 |
| 漏洞扫描 | govulncheck 无漏洞 | govulncheck 无漏洞 |
| CRD | 不适用 | 全部 17 个 CRD 通过结构／CEL 校验，并安装进实际本地 API Server |
| 生成 | 固定输入、工具和嵌套对象修补，再次生成一致 | 本地 schema 提取、固定模块工具、生成器及 17 CRD，再次生成一致 |
| 工作流 | actionlint 通过 | actionlint 通过 |
| 真实验收 | Logto 1.40.1 六类 protocol CRUD/import，101 scopes／roles 和 metadata 保留 | 六类 CRD 引用图、漂移、重启、Secret、清空角色；故障注入 Observe／403 |
| 交付 | 独立二进制构建 | 无 daemon 构建嵌入镜像的 XPKG，检查二进制、non-root、CA、17 CRD 和 SBOM |

CI 使用只读仓库权限、固定 Actions SHA、固定 Go 版本、超时和取消重复任务；PR 仅使用自己启动的临时 Logto 凭据，不接触生产凭据。CI 包含 race、vet、build、staticcheck、govulncheck、生成差异检查及覆盖率产物。Upjet 另含镜像 build 与 `--help` 启动检查。手动 release-readiness 只验证，不发布。

本地工具为 Go 1.26.8、staticcheck 2026.2.1、govulncheck 1.8.0、actionlint。Terraform 的最低 Go 版本因安全依赖升级提高至 1.25。

覆盖率详见下方最终记录。Terraform 的跨包覆盖率按同一语句位置合并后计数；手写运行时代码排除生成文件与生成脚本。Upjet 全量覆盖率包含大量生成代码。单元覆盖率不包含独立 provider 进程的运行覆盖；真实调和证据来自独立集成测试。

## 架构与维护性

- Terraform resource 负责状态和诊断，HTTP client 负责认证、协议、分页与重试，Upjet adapter 负责 schema、引用、敏感数据和 Kubernetes 凭据。边界合理，本轮保持六类资源的接口与现有字段。
- 应用和用户 PATCH 使用明确的写入 payload，避免将读取模型里的只读字段直接当作写入合同。保留未知 JSON 字段，但 GET+PATCH 并非原子操作；同一对象的并发 UI 修改仍需实际验收与运行约束。
- 两个项目都使用受审查的 schema 与固定依赖。Upjet 不再依赖相邻 checkout；快照记录上游基线和全部文件摘要。摘要证明副本完整性，不证明发布签名或供应链 provenance。
- 嵌入依赖由 Terraform 项目维护，更新后执行 `make sync-provider`，审查快照差异，再生成、验证。Upjet Dependabot 不直接修改嵌入模块，避免绕过快照清单。
- Crossplane runtime 使用稳定 v2.4.2，Crossplane APIs v2.4.1，Upjet 已升级至正式版 v2.5.1，Go 工具链使用 Nix 提供的 1.26.8。
- 两个项目的 Nix 输入同时固定提交和 narHash，生成了 flake.lock；官方源码下载、内容摘要和 flake 输出求值已验证。
- `Application` 与 named `Secret` 的凭据留在 connection details，测试确保不写入 CRD status；真实 connection Secret 发布和重启恢复已验收；生产 RBAC 和 package 安装另有发布门禁。

## 生产发布门禁

| 状态 | 条件 | 证据／剩余工作 |
| --- | --- | --- |
| 已通过 | 真实 Logto 1.40.1 | 六类 protocol CRUD/import；101 scopes 和用户角色；TTL／刷新令牌／profile 保留 |
| 已通过 | 实际 API Server 和 provider 进程 | 六类引用图、漂移、重启、状态重建、connection Secret、Observe 接管／403、legacy cluster API、探针、显式空角色与 scope 撤销；race 无告警 |
| 已通过 | 本地 amd64／arm64 package 交付 | 两种架构经 Crossplane CLI 构建、非 root、CA、嵌入二进制 SHA、全部 17 CRD、SPDX 与校验清单 |
| 待发布环境执行 | Crossplane Core package 安装／升级、容器运行、集群 RBAC | 本机 newuidmap 权限限制仍存在；本地 API Server 没有 kubelet 和 package manager，不能代替此项 |
| 待仓库配置 | Required checks、registry、版本、签名／provenance | CI 仅产出验证后的包；本轮没有远端 Git 写入或发布 |
| 已提供运行规程 | 监控、凭据切换、部分失败和回滚 | 见 [operations.md](operations.md)；模糊创建结果须先检查并 Observe 接管，不能盲目重建 |
| 有明确边界 | Named-secret 生命周期 | CRUD/import/重启发布已验收；到期调度、消费者切换和自动轮换不在当前实现范围 |

实际环境：PostgreSQL 17.11、Logto 1.40.1、API Server 1.37.0、etcd 3.6.14。所有变更仅发生于本地临时实例。单元 mock、envtest 和容器／Crossplane 安装门禁各自独立，不相互替代。

这轮审查以现有六类资源的可靠性为范围。Connector、sign-in experience、organizations、custom claims 等尚不在 provider 管理范围，因此“全部现有 SSO 配置纳入 CRD”仍未实现。

## 主要合同来源

- [Logto 1.40.1 应用路由](https://github.com/logto-io/logto/blob/v1.40.1/packages/core/src/routes/applications/application.ts)
- [角色和 scope 关系](https://github.com/logto-io/logto/blob/v1.40.1/packages/core/src/routes/role.scope.ts)
- [用户及角色分页和赋权](https://github.com/logto-io/logto/blob/v1.40.1/packages/core/src/routes/admin-user/role.ts)
- [用户 PATCH 合同](https://github.com/logto-io/logto/blob/v1.40.1/packages/core/src/routes/admin-user/basics.ts)
- [gRPC HTTP/2 漏洞](https://pkg.go.dev/vuln/GO-2026-6348)、[gRPC authority 漏洞](https://pkg.go.dev/vuln/GO-2026-6443)、[x/text 漏洞](https://pkg.go.dev/vuln/GO-2026-5970)、[x/net 漏洞](https://pkg.go.dev/vuln/GO-2026-5026)

## 最终执行记录

| 指标 | Terraform | Upjet |
| --- | --- | --- |
| 全部语句覆盖率 | 61.4%（1105/1799） | 2.4%（88/3594） |
| 手写运行时代码覆盖率 | 77.1%（933/1210） | 35.6%（88/247） |
| 重点包 | 生命周期／协议覆盖六类资源，provider 配置验证含缺字段和 unknown | config 95.5%，credentials/setup 57.6% |
| 嵌入副本 | 作为源项目审查 | 69 个受审查源文件，逐文件 SHA-256 验证 |

覆盖率表来自最终离线 race 测试。后续新增的独立进程集成测试已经执行真实控制器启动、调和与重启，不将其混入单元覆盖率分母。最终执行日志记录新增验收结果。

独立审查按两条轴并行进行，修复后再次复核：

| Standards：规范／维护性 | Spec：需求／可靠性 |
| --- | --- |
| 生成器所有权、固定输入、独立 checkout、完整 CI、镜像入口、凭据 namespace、快照维护 | 权限覆盖、部分创建、关系原地更新、排序、分页、默认标记、配额、失败收敛 |
| 发现的嵌入模块 vet 缺口已补上；依赖更新 ownership 已写明 | 发现的默认标记丢失和满额 scope 替换问题已修复并有回归测试 |
| 发布 provenance、Core package 安装与实际 CI 执行仍待发布环境验证 | 真实 Logto 和 API Server 调和验收已完成 |

本地原始日志、coverage profile 与生成摘要保存在工作区 `.local/` 以及各项目被忽略的 `coverage.out`；不进入源码或发布产物。
