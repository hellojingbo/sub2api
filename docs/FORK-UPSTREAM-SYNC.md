# Fork 上游同步记录

## v0.2.25

- 合入上游 `v0.2.25`（`348997267d756eab5729bb6b78a275a15d18796a`），fork 版本更新为 `0.2.25-dev`。
- 采集列表按页返回请求摘要，默认每页 20 条。详情按 ID 读取完整记录，旧会话继续可读。
- WebSocket 透传合入上游的重复字段检查，沿用 fork 的 Codex 请求身份和采集逻辑。管理服务接入上游新增的平台配额依赖。

## v0.2.23

- 合入上游 `v0.2.23`（`5af9afb588c4b014b3df9db7d4073af1f8ae01b6`），相对 v0.2.21 新增 219 个提交；合并前 fork 为 `70b6f6216`。保留已有提交历史，版本更新为 `0.2.23-dev`，Go 工具链升级到 1.27.4。
- 上游标签内 VERSION 为上一版 0.2.22；fork 合并提交通过显式 VERSION 值声明 0.2.23-dev。
- 原生 TUI / exec / Desktop UA 保留、Desktop 独立 build、账号与会话关系、代理时区、zstd 及手动请求采集沿用现有设计。
- 会话缓存键采用上游新规则：对比 body session_id 和客户端原始 session-id。临时分叉保留父会话缓存键，同时保留自己的 body 会话身份；回归同时验证连字符 session-id，避免只比较两个缺失的旧 session_id 头。
- 合并上游 additional_tools 声明 UUID 与引用映射，同时保留原生调用 ID 的保护条件。相应用例按真实 TUI 请求验证声明归一化及原生 message/reasoning ID 保留，兼容路径的默认清理规则继续由既有用例覆盖。
- 纳入流式空闲超时与首输出切换预算、Ultrafast 分组和渠道计价等上游更新。新增 3 项迁移：分组 Ultrafast 开关、分组用量汇总重建状态/失效函数、渠道 Ultrafast 价格倍率。
- 部署仍校验镜像/二进制版本、ELF 与执行权限、健康、前端资源、数据挂载及迁移器 TrimSpace 后的 SQL 校验和；部署前备份数据库、Redis、应用数据与配置，并保留旧镜像。

## 2026-11-16：原生 Codex UA 修复

- 对照官方 `rust-v0.169.0` 的 TUI / exec 初始化和 UA 构造逻辑，原生 HTTP、透传 HTTP、WebSocket 保留完整合法官方 UA 的客户端名、引擎版本、系统、终端和尾标，并从 UA 配对 originator。
- Linux 采集值 `codex-tui/0.169.0 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.169.0)` 不再被改成 codex_cli_rs，也不再丢失 TUI 尾标。
- Desktop 采集值 `Codex Desktop/0.169.0-alpha.11.1 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.1111.21308)` 完整保留。未来客户端携带的新版本和 build 同样沿用；该采集值仅作为缺失 UA 时的 Desktop 回退模板。
- 真实官方 UA 优先确定客户端身份；工作区路径描述执行环境，不能把 Linux TUI 或 macOS Desktop 改装成 Windows 客户端。缺失完整 UA 时再参考 body 元数据、header 元数据及 originator 选择回退。
- 只有 originator 或不完整 UA 时，codex-tui / codex_exec 分别生成自己的回退 UA 和尾标；无法识别的客户端使用库级 codex_cli_rs 回退。回退 CLI 基线更新到 0.169.0 / Ubuntu 24.4.0，仍支持版本设置及自动同步。
- 显式 ForceCodexCLI 保持最高优先级；账号级完整 UA 覆盖次之。账号 CLI 指纹继续跟随后台版本设置，保留 TUI/exec 身份；账号 Desktop 覆盖保留其独立引擎版本和 app build。
- 原生推理的全部身份分支均不添加 legacy Version 头；后台鉴权、模型探测等无具体客户端的兼容路径继续使用原有规范身份机制。
- 增加两条真实采集值、后续 Desktop build、TUI/exec、跨系统工作区、缺失/非法 UA、显式覆盖及 HTTP/WS 一致性回归。此次修复范围为 UA 与 originator 配对，账号隔离、请求压缩及其他请求处理保持原有策略。
- 验证：Go 1.27.3 服务层 unit 测试通过（全进程堆快照用例独立通过）；UA 定向、OpenAI 身份与请求压缩回归通过；首 token 耗时用例首轮出现 1ms 边界波动，连续复测 3 次及最终整包执行均通过；Linux amd64 含前端 embed 构建通过并核验 ELF 文件头。本次未部署或进行真实上游调用。

## v0.2.21

- 上游：`Wei-Shaw/sub2api` 的 `v0.2.21` 标签，提交 `10ba8f9b4`。
- 合并前 fork：`hellojingbo/sub2api` 的 `33fdfa6d1`，基于上游 `b0a02a330`（0.2.17）。
- 合并方式：merge，保留 fork 原有 7 个提交及其历史；纳入上游新增的 309 个提交。
- Fork 版本：`0.2.21-dev`。上游标签内的 VERSION 文件仍为 `0.2.20`，其构建脚本通过精确标签识别发布版本；fork 合并提交不处于该标签，因此显式更新版本文件。

### 保留的请求设计

- 根据客户端选择 Ubuntu CLI、Windows CLI 或 Windows Desktop 的 Codex UA、originator 和元数据；原生推理请求不发送 legacy Version 头。
- 使用连字符形式的 session-id、thread-id、x-client-request-id；账号间隔离，保留根会话三者相等、子线程独立及 turn/window 关联，维持 UUIDv7 形态。
- body client_metadata 为主要来源，WebSocket 头作为回退；保持 header/body 身份一致及原有 JSON 字段顺序策略。
- 原生 Codex 请求不补入默认 instructions；保留合法工具调用 ID 及配对关系，兼容路径继续执行必要的协议修正。
- 按有效代理地区改写请求中已有的 environment_context 和 Codex 客户端时间上下文；缺失时不主动注入。
- 原生 OAuth Responses HTTP 请求继续使用 zstd，已有编码先解码后重新编码，并更新可重放请求体和长度。
- 保留手动开始、结束、删除的 Redis 请求采集及管理页面，记录上下游可读请求体和实际 wire body。
- 保留服务容器默认 Asia/Seoul 时区及预构建二进制的可执行权限修正。

### 合并适配

- 采用上游新增的 TUI → CLI 成对身份归一化，继续使用 fork 的客户端 persona 选择。
- 保留上游 #7978：OAuth 上游仍以 SSE 请求，下游 stream=false 返回单个 JSON；compact 继续非流式。
- 上游透传入口放宽 instructions 校验后，显式排除官方 Codex 客户端的默认 instructions 注入，维持 fork 原生请求设计。
- 新增官方 Desktop 透传回归，联合验证流式/非流式响应、无默认 instructions、无 Version/conversation_id 及根会话三头关系。
- 默认 instructions 的上游用例改为验证兼容客户端，官方客户端由原生请求回归覆盖；合法工具历史另经 HTTP/WS 共用的兼容处理函数验证。
- Ollama 旧回调用例使用明确不同的短冷却时间戳，目录缓存用例为每次转发附加唯一序号，避免低精度时钟令代次或缓存地址恰好相同；生产逻辑未改动。

### 验证

- 使用 Go 1.27.3、pnpm 9.15.9。
- Codex 身份、会话、时区、合法工具历史、zstd 和 Redis 手动采集定向回归通过。
- 前端 384 个测试文件、3,110 项测试通过；i18n、vue-tsc、ESLint、Vite 生产构建通过。
- Linux amd64 静态构建（含 embed 前端）通过，并校验 ELF 文件头。
- integration 标签下的全包编译通过；本机 Docker 未启动，未执行真实 PostgreSQL/Redis 容器集成测试。
- 完整单元测试中，Windows 无符号链接权限的 `TestCgroupMemoryLimit/cgroup_path_cannot_escape_the_mount` 单独记录为环境限制；SQL 耗时断言首轮受负载影响，连续复测 3 次及 repository 全量复测通过。
- `TestInflightEstimate_AccountMappingNoDBAndBoundedMemory` 使用全进程堆快照，整包运行时出现一次超过 8 MiB 阈值的波动；保持该用例不变，独立执行并连续通过 3 次，服务层其他测试单独执行。

最终后端 61 个有测试的包通过验证：其余 60 个包整包通过，service 除内存快照用例外整包通过，内存用例独立通过；仅跳过上述 Windows 符号链接权限用例。CI 中的测试用例和执行范围未禁用。

真实上游请求和生产部署需分别验证。
