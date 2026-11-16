# Fork 上游同步记录

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
