# TUI/CUI 优化方案（OPTIMIZATION_PLAN）

> 状态：Phase 1+2 已完成并通过回归（2026-08-30）。Phase 3 已排期（P3.1 → P3.5），**P3.1、P3.2、P3.3、P3.4、P3.5 全部完成 ✓**。Phase 4 已完成 ✓（2026-08-30）。后续排期已登记（六·五节：A 测试缺口续补 / B 零测试模块补齐 / C 功能增强），**A、B、C 全部完成 ✓（2026-08-30）**。
> 关联文档：`docs/CLI_TUI.md`（架构基线）、`docs/REFACTOR_NOTES.md`（配置重构记录）。

## 一、现状快照

| 层 | 入口 | 技术 | 特点 |
|---|---|---|---|
| TUI | `wukong session` | Bubble Tea v1.3.10（Elm 架构） | 流式渲染、并发工具面板、三主题、命令菜单 |
| CUI-REPL | `wukong run -d` | bufio stdin | 极简 REPL，仅 5 命令 |
| CUI-选择器 | `wukong project` | bufio.Scanner | 编号选择 + `r/n/q` 二级操作，不启动 session |
| CUI-向导 | `wukong configure` | bufio.Reader | 5 步向导，每次从头新建配置 |
| CUI-单发 | `wukong run` | loop.Run(Stream) | 一次性输出 |

核心文件：`internal/cli/tui/{model,view,update}.go`、`internal/cli/{run,project,configure,session}.go`

## 二、问题清单（按优先级）

### P1 正确性风险（Bug 级）

1. **工具结果错误路由**（update.go）：`tool.response` 带 content 但无 tool_calls 时，强行挂到 `lastToolName`（最近启动的工具）。并发工具场景下，先完成的工具结果会错误分配给后启动的工具。
2. **tool.response 无 content 被静默丢弃**：`choice.Message.Content == ""` 时走 `continue`，工具结果丢失（不渲染、不落 audit）。
3. **requestExit/cleanup 语义冲突**（model.go）：两者共享同一 `sync.Once`——先 `requestExit` 再 `cleanup` 时 cleanup 完全跳过；退出路径清理语义被掩盖。
4. **Ctrl+C 后断链**（model.go Update）：流式期间 Ctrl+C 置 `cancelled=true` 后 `return m, nil`，**没有返回 `readStreamEvent` cmd**，streamCh 从此不再被消费——IsEnd 永远到不了，界面卡在 "Cancelling..."，用户必须再按一次 Ctrl+C 强退。

### P2 可交互性缺陷

5. **Modal 固定 60×14**（view.go RenderModal）：内容溢出无滚动，仅支持 ↑/↓/Enter/Esc，小终端上越界。
6. **工具面板无滚动**：工具数量超过可视区时，Tab/Shift+Tab 只能选中显示区外不可见的工具。
7. **/help 硬编码**（model.go）：内置扩展列表写死（developer/computer_controller/memory/...），新增扩展后必然过时。
8. **TUI 无法恢复 session**：`/new` 只开新会话，无 `/projects`、`/resume`——而 session 服务端支持按 sessionID 恢复上下文。
9. **project 选择器只打印命令**（project.go）：选完项目不启动 session，用户得手动复制粘贴。

### P3 性能与体验（后续排期）

10. glamour 增量渲染：assistant 每 token 触发整条消息重渲染。
11. 截断丢失关键信息：结果是截断 500 字符，运行日志类结果无法展开查看。
12. configure 向导不可增量修改。
13. CUI REPL 弱：无行编辑、无历史、无流式工具渲染。
14. CUI 输出可能泄露 secrets。

### P4 测试缺口（后续排期）

15. 无 modal 键盘路径测试。
16. 无 sendMessage 异步流测试（无 fake loop.Run）。
17. 无 tool.response 路由测试、无 CUI REPL 测试。

## 三、Phase 1：正确性修复（已完成 ✓）

| 项 | 修改 | 文件 |
|---|---|---|
| 1.1 | 工具结果路由改为 **FIFO 队列**：update.go 维护 `resultQueue []string`（按工具启动顺序入队），tool.response 到达时队首出队绑定；TUI 侧 toolCallResultMsg 改为**顺序匹配**首个 Name 匹配且 running 的条目（从最旧到最新） | `tui/update.go`、`tui/model.go` |
| 1.2 | tool.response 无 content 也发送 `ToolResult{Result: "(no content)"}`，不再静默丢弃 | `tui/update.go` |
| 1.3 | 抽取私有 `stopStream()`（sync.Once 保护 cancel+wait）；`requestExit = stopStream + quitRequested + tea.Quit`；`cleanup = stopStream`。两函数不再互相干扰 | `tui/model.go` |
| 1.4 | 流式期间 Ctrl+C 分支改为 `return m, readStreamEvent(m.streamCh)` 继续消费，IsEnd 到达后自然收尾（不再要求第二次 Ctrl+C）；streamEndMsg 幂等收尾保留 | `tui/model.go` |

## 四、Phase 2：可交互性增强（已完成 ✓）

| 项 | 修改 | 文件 |
|---|---|---|
| 2.1 | Modal 自适应尺寸：打开时按 items/content 行数计算高度，`maxH = max(6, m.height-8)`、`w = max(40, min(60, m.width-8))`；modalState 增加 `Scroll` 视口偏移，↑/↓ 移动选中并滚动视口（可视行数 = height-2） | `tui/model.go`、`tui/view.go` |
| 2.2 | 工具面板滚动窗口：`toolScrollStart` 字段，渲染子窗口 `[start, start+maxVisible)`，selected 越界时窗口自动跟随；`maxVisible = max(2, viewport.Height/4)` | `tui/model.go` |
| 2.3 | /help 动态生成：Built-in 扩展列表从 `m.cfg.Extensions`（enabled）实时生成；平台前缀静态但注明 | `tui/model.go` |
| 2.4 | TUI 会话恢复：`/projects` 列出项目（直接 import `project` 包，接口用真实类型 `[]project.ProjectRecord`），`/resume <sessionID>` 切换 `m.sessionID` 并清屏重置（框架按 sessionID 自动恢复上下文） | `tui/model.go` |
| 2.5 | project 选择器直达 session：`[r]ecover` 分支 `os.Chdir(selected.Path)` 后直接调用抽取出的 `startInteractiveSession`（复用 runSession 完整 bootstrap/信号/清理链路），不再只打印命令 | `project.go`、`session.go` |

### 实现备注

- project 包（`internal/project`）只依赖 config/util，无循环依赖，TUI 直接 import 真实类型；`projectMgr any` 字段通过 `projectLister` / `instructionUpdater` 小型接口断言，替换原先的本地镜像类型（断言恒失败缺陷已修复）。
- `/resume <sessionID>` 只切换 sessionID 并清空本地 UI 缓存（messages/toolCalls/auditLog/cached*），上下文由服务端按 sessionID 恢复，不重新 bootstrap。
- 2.5 实现时需注意：agent 内部路径基于 `os.Getwd()` 计算（recipe.go），故 recover 前必须 chdir 到项目目录。

## 五、Phase 3：性能与体验（已排期，按 P3.1 → P3.5 顺序执行）

> 排期原则：先做零风险/纯增量项（3.1、3.2），再做涉及新状态与按键的项（3.3、3.4），最后做安全项（3.5）。每项完成跑一遍"六、回归验证"。

### P3.1 glamour 尾部增量渲染缓存（已完成 ✓ 2026-08-30）
- **现状**：`streamingDeltaMsg` 每 token 触发 `updateViewport()`（去抖后）→ `renderAssistantMessage()` → `m.mdRenderer.Render(content)` 整条 markdown 重渲（glamour 开销大）。
- **方案（落地版）**：
  1. 新增 `renderStreamedMarkdown(content)` 增量入口（仅 `m.streaming && m.currentStream == content` 时走增量，否则回退全量 `renderAssistantMessage`）。
  2. 安全分割点 `lastSafeSplit`：从后往前找空行，仅当**前缀与尾块 fence 数均为偶数**（无未闭合代码块）才返回分割偏移；否则返回 0（全量渲染）。
  3. 每帧只重渲 `content[split:]` 尾块（`renderMarkdownBody`），与缓存的 `streamCacheRendered` 前缀拼接；`fenceCount` 支持缩进 ```/~~~。
  4. 兜底：`safeIncrementalTail` 拒绝空尾块/奇数 fence；内容 < 2KB（`markdownStreamFlushThreshold`）直接全量；每 8 帧（`markdownStreamReconcileEvery`）强制全量重渲自校正；前缀失配即失效重渲。
  5. 缓存失效点全覆盖：`streamEndMsg`、`streamingErrorMsg`、`sendMessage`、`/new`、`/clear`、`/resume`、`/theme`。
  6. 去抖放宽 16ms → 50ms（`streamDebounce`）。
- **验收结果**：build/vet/test 全绿；新增 `fenceCount`/`safeIncrementalTail`/`lastSafeSplit`/`resetStreamCache` 单测（覆盖空行分界、fence 奇偶、开后门内空行、前缀奇数 fence 等边界）。长流式渲染仅重渲尾块，前缀复用。
- **文件**：`tui/model.go`（渲染与缓存状态）、`tui/update.go`（sendMessage 失效）、`tui/model_test.go`（单测）。

### P3.2 工具结果 `[+]` 展开（已完成 ✓ 2026-08-30）
- **现状**：`RenderToolCallResult` 已支持 `Collapsed` 折叠状态（Tab 切换），且折叠时显示隐藏字符数——**功能已具备**。缺失的是：
  - collapsed 默认值统一（`toolCallEntry.Collapsed` 初始值在 `toolCallStartedMsg` 中未显式置为 true）。
  - 展开后 Result 完整显示（现在无论折叠与否都 500 字符截断，展开态截断无提示）。
- **方案（落地版）**：
  1. `toolCallStartMsg` 创建条目时 `Collapsed: true`（默认折叠，仅显示 header + 字符数提示）。
  2. **Tab/Shift+Tab 改为纯导航**（不再切换折叠）；**Enter 切换**选中工具（仅已完成的结果可展开，running/无结果不响应，且不会吞掉提交键 Ctrl+D）。
  3. 展开态渲染用 `toolCallResultStyle.Width(maxWidth)` 对全长结果换行（去掉 `len>500 -> ...` 硬截断），超长内容由工具窗口（viewport 1/4 高）内滚动查看；折叠提示文案改为 "press Enter to expand"。
  4. 工具窗口 footer 提示随选中项状态变化：选中可展开项时显示 "Enter to expand/collapse"。
- **验收结果**：build/vet/test 全绿；新增 12 项单测（默认折叠、Enter 双向切换、多工具选中切换不动他者、Tab/Shift+Tab 环绕导航、running 状态不折叠、展开不截断（1000 字符全保留）、折叠字符提示等）。
- **文件**：`tui/model.go`（toolCallStartMsg 默认折叠、Tab/Enter 按键、footer 提示）、`tui/view.go`（RenderToolCallResult 加 maxWidth 参数、去截断改换行）、`tui/model_test.go`（单测）。

### P3.3 configure 增量编辑模式（已完成 ✓ 2026-08-30）
- **现状**：`configure.go` 的 5 步骤顺序向导，`readLine` 用 `bufio.Reader`，每次从 `config.Defaults()` 全新开始，无法修改已有配置文件；且写回用 `yaml.Marshal`（camelCase 字段名）与加载用的 `mapstructure` snake_case tag 不一致，`mcp_port`/`context_window`/`timeout` 等字段写回后读不回来。
- **方案（落地版）**：
  1. **config 层**：新增 `MarshalYAML`（按 mapstructure tag 反射序列化，snake_case 键名，time.Duration 输出字符串形式）与 `LoadUnresolved`（加载不展开 `${ENV}` secret，避免 edit 写回时把展开的明文密钥持久化）；`Load()` 重构复用内部 `loadUnmarshaled`。
  2. **向导**：目标路径已存在文件（或 `--edit` flag）时以磁盘配置为基线（`baseConfig`），各提示显示 `[当前值]`，Enter 保留原值；新增 `--edit` flag 强制增量模式（无文件时回退 defaults）。
  3. **provider 编辑**：已有 provider 逐个走查字段（Type/BaseURL/APIKey/Model），Enter 保留；新增 provider 检测重名跳过；extensions 保留非 builtin 条目（不外删），builtin 显示当前 enabled 状态。
- **验收结果**：build/vet/test 全绿；新增测试：`MarshalYAML` round-trip（snake_case 键、duration 字符串、nil 切片）、写后 NewLoader 读回 lossless（mcp_port/context_window 等关键字段）、`LoadUnresolved` 保留 `${ENV}` 引用、`baseConfig` 存在/缺失/损坏文件三态、promptValue 默认值语义。
- **文件**：`config/marshal.go`（新）、`config/config.go`（LoadUnresolved + loadUnmarshaled）、`config/config_test.go`、`cli/configure.go`（重构）、`cli/configure_test.go`（新）。

### P3.4 CUI REPL 行编辑/历史/流式工具渲染（已完成 ✓ 2026-08-30）
- **现状**：`runDialogue`（run.go）用 `bufio.Reader.ReadString('\n')`，无行编辑、无历史、无流式状态；`runOneShot` 的 `streamToStdout` 只打印 delta content，工具调用过程不可见。
- **方案（落地版）**（分两小步，可独立提交）：
  1. **行编辑/历史**（`cli/repl.go` 新增）：`editLine` 在 TTY 下用 `golang.org/x/term.MakeRaw` 进入原始模式逐字节读，自研单行编辑器——Enter 提交、Backspace、↑/↓ 历史导航（环形 `history`，去重 + 上限 100）、←/→/Home/End、Ctrl+A/E/U/K、Ctrl+R 增量历史搜索、Ctrl+D 提交 EOF；非 TTY（管道/测试）自动回退 `readPlainLine` 纯读取。
  2. **流式工具渲染**：`streamToStdout`（目前为 `streamToStdoutWith(evt, out)` 可注入 writer）识别 Message/Delta 中的 `ToolCalls` 打印 `◉ tool_name (args 摘要)`（dim 色），识别 `Role=="tool" || Object=="tool.response"` 且 ToolName 非空打印 `✔`/`✖` 结果行（首行 80 字符截断）；`--no-stream` 分支不变。
- **验收结果**：build/vet/test 全绿；新增 `cli/repl.go`（编辑器/历史/渲染）与 `cli/repl_test.go`（20 项单测：历史去重截断、插入/退格/删除、多字节 rune 光标、argsSummary 截断、渲染输出、流式判别、管道读、非 TTY 回退）；`runDialogue` 接入 `hist + editLine`，执行前 `hist.add`，`/exit`、`/session`、`/clear`、`/help` 行为不变。
- **文件**：`cli/repl.go`（新）、`cli/repl_test.go`（新）、`cli/run.go`（接入编辑器和历史）。

### P3.5 输出 secret 脱敏（CUI + TUI）（已完成 ✓ 2026-08-30）
- **现状**：全库无统一的输出脱敏函数；config 的 `expandSecrets`（config.go:389）负责展开 `${ENV}`，但展开后值可能出现在对话输出/工具结果中（如 API key、token）。
- **方案（落地版）**：
  1. 新增 `internal/util/redact.go`：`func RedactSecrets(text string, secrets []string) string` —— 忽略长度 < 2 的 secret、去重、只保留实际出现在 text 中的 secret，按长度降序稳定排序后逐字符前缀匹配替换为 `[REDACTED]`（最长匹配优先，避免子串互相遮蔽）；空输入/无匹配时返回原串。
  2. `config.WukongConfig.SecretValues()` 收集脱敏集合：providers[].api_key、extensions[].env 中键名含 key/secret/token/password/credential 的值、summon a2a_remotes 的 api_key/jwt_secret/oauth_client_secret、AGUI/ACPServer/MCPServer 的 security.auth 的 api_key/jwt_secret、gateway feishu 的 AppSecret/EncryptKey/VerificationToken（仅 Enabled 时）。
  3. TUI：Model 增加 `secrets` 字段（NewModel 从 `cfg.SecretValues()` 注入）；`streamingDeltaMsg` 渲染入口 `renderAssistantMessage` 先脱敏；工具结果与工具参数在 update.go 写入 streamCh / resultQueue 前脱敏（同时覆盖屏幕渲染、消息存储与 audit 落盘）。
  4. CUI：`streamToStdoutWith(evt, out, secrets)` 三参签名，工具 args / 工具结果 content / delta content 三处先脱敏；`runOneShot` 流式回调包闭包传 `cfg.SecretValues()`，非流式 `fmt.Println(util.RedactSecrets(response, ...))`。
- **验收结果**：build/vet/test 全绿；`go test ./internal/util/` 新增脱敏单测（无匹配、单个/多个、空 secret 忽略、单字符忽略、最长匹配优先、嵌套子串、相邻句柄、unicode、多行、重复 secret、全量复制无分配）；cli/config 包回归通过。配置中的 api_key 不再出现在任何终端输出。
- **文件**：`internal/util/redact.go`（新）、`internal/util/redact_test.go`（新）、`config/config.go`（SecretValues）、`tui/model.go`、`tui/update.go`、`cli/repl.go`、`cli/run.go`。

## 六、Phase 4：测试补齐（已完成 ✓ 2026-08-30）

### 4.1 modal 键盘路径测试（已完成 ✓）
- 用现有测试模式（包内 `&Model{...}` 直接构造）覆盖 ↑/↓ 导航与边界钳位、Enter 执行选中命令（/clear 清空、/new 换 session）、Esc 关闭不执行、PgUp/PgDn 视口滚动与钳位、选中项跟随滚动窗口、非导航键被 modal 拦截、RenderModal 滚动提示/越界钳位/Nil 安全。
- **验收结果**：走查定位 2 处既有缺陷并修正——导航循环终值（`testCommandsItems-2` 应为 `-3`）与 streaming 标志断言反转；18 项单测全绿。
- **文件**：`tui/model_test.go`（追加）。

### 4.2 fake `loop.Run` 异步流测试（已完成 ✓）
- **前置改造**：`Model.loop` 由 `*agent.CoreLoop` 具体类型改为 `loopRunner` 小接口（update.go 定义，`Run(ctx, userID, sessionID, message) (<-chan *event.Event, error)`），测试注入 `fakeLoop` 返回预置事件 channel，驱动 `sendMessage → readStreamEvent → Update` 全链路。
- 覆盖：delta 累积与 streaming 标志复位、toolCallStartedMsg/toolCallResultMsg 路由与 audit、错误事件、`loop.Run` 返回 error（DeadlineExceeded → friendlyError）、NoToolResult 降级分支、空结果 "(no content)"、delta 与工具事件交错流序。
- **验收结果**：7 项单测全绿（含视口初始化防 panic 的 `newStreamModel` 构造）。
- **文件**：`tui/fake_loop_test.go`（新）、`tui/model.go`、`tui/update.go`（loopRunner 注入）。

### 4.3 tool.response FIFO 路由单测（已完成 ✓）
- 直接对 resultQueue 语义做顺序断言：先开先回、后开先回（队首仍按启动序绑定）、结果早到绑定最旧未决调用、同名工具并发去重（First running 匹配）、secrets 脱敏（参数/结果/存储/audit 均无明文）。
- **验收结果**：5 项单测全绿。
- **文件**：`tui/fake_loop_test.go`（追加）。

### 4.4 CUI REPL 测试（已完成 ✓）
- `runDialogue` 的命令分支用 `os.Pipe` 接管 stdin/stdout 制造非 TTY（回退 `readPlainLine`）路径：/exit、/quit 别名、EOF 退出、/session、/clear（ANSI 清屏）、/help、空行继续；writer goroutine 内关闭 inW 保证无死锁。
- `streamToStdout` redact 集成：工具 args/结果/delta 三处脱敏、nil secrets 原样通过。
- **验收结果**：10 项单测全绿。
- **文件**：`cli/repl_test.go`（追加）。

## 六·五、后续排期（已完成 ✓）2026-08-30

> 原计划 P1–P4 全部交付后新增。按"测试缺口续补 → 零测试模块补齐 → 功能增强"三类排期，先做纯函数/可注入的低风险项，再动需要接口化改动的项；每项完成跑一遍"七、回归验证"。

### A 测试缺口续补（Phase 4 未覆盖的行为）

- **A1 TUI 命令分支补齐（已完成 ✓ 2026-08-30）**：新增 9 项测试——`/projects` 三分支（无 manager 降级提示 / 空列表 / 列表渲染含 SessionID 截断 8 位与 resume 提示）、`/resume` 无参 usage、`/resume <sid>` 全量状态复位（sessionID/messages/toolCalls/auditLog/stream/status）、`/resume ` 尾随空格归一到 usage（`sid==""` 分支属防御性死代码，测试锁定实际行为）、`/model <name>` 切换默认 provider 的 Model + modelName + 确认消息、`/model` 无 provider 降级、`/exts` 空配置、`/audit` 空日志。用 `fakeProjectLister` 注入 `projectMgr`。23 项 TestHandleCommand 全绿。
- **A2 Ctrl+C 取消全链路（已完成 ✓ 2026-08-30）**：现有测试只覆盖 `stopStream`/`requestExit` 幂等，**无一条用例向 `Update` 发 `tea.KeyMsg{Type: tea.KeyCtrlC}`**（model.go L404-425）。复用 fake_loop_test.go 的 `pumpStreamWith` 在流中注入 Ctrl+C，断言 streaming 中 Ctrl+C → `cancelled=true` + "Cancelling..." 状态 + 取消提示消息 + 返回 `readStreamEvent` 保持消费 + `streamEndMsg` 后收尾为 "Cancelled"。**测试暴露并修复 2 处缺陷**：① `streamEndMsg` 中 `m.cancelled=false` 在状态判定前复位，"Cancelled" 状态永不显示（死代码）；② streaming 中第二次 Ctrl+C 被 streaming 分支拦截，界面提示 "Press Ctrl+C again to force-quit" 但实际不退出。新增 5 项测试全绿。
- **A3 streamingErrorMsg/streamEndMsg 守卫分支（已完成 ✓ 2026-08-30）**：补 2 项测试——① `TestStreamError_SettlesStateAndContinuesStream`（错误事件→system 消息 + streaming 复位 + "Error occurred" 状态 + 继续消费流、错误后 late delta 不回灌历史）；② `TestStreamError_EmptyContentAfterErrorConsumed`（错误后 currentStream 清空、stale 不落入 assistant 历史）。守卫分支（streamEndMsg 非流式 no-op）由 A2 的 `TestStreamEnd_Guard_NoOpWhenNotStreaming` 覆盖。
- **A4 runOneShot（已完成 ✓ 2026-08-30）**：先做小重构——新增 `runner` 接口（`RunStream` 签名）+ `coreRunner{*agent.CoreLoop}` 适配（run.go L174-207），`runOneShot` 参数由 `*agent.CoreLoop` 改为 `runner`，调用点包 `coreRunner{loop}`（与 TUI 的 `loopRunner` 同一模式）。新增 3 项测试（repl_test.go）：流式路径渲染事件 + 脱敏（secret 在事件中替换为 `[REDACTED]`）+ 参数透传（userID/sessionID/msg），非流式路径打印脱敏响应且不注册 onEvent，loop 错误透传。`captureStdout` 用 os.Pipe 接管 stdout。
- **A5 session 启动决策逻辑（已完成 ✓ 2026-08-30）**：静态分析发现 session.go L129-160 内联实现了与 run.go `resolveUserID` 相同的 userID 解析逻辑（重复实现）→ 删除重复代码统一复用 `resolveUserID`；另将 `sessionID = uuid.New().String()`、`workingDir = os.Getwd()` 提取为 `resolveSessionID`/`resolveWorkingDir` 纯函数（run.go）两处共用。新增 session_test.go 4 项测试：resolveUserID 优先级（USER→DOMAIN\USERNAME→USERNAME→回退非空）、SYSTEM 用户名被拒绝、UUID v4 格式正则、Getwd 一致性。行为与原有实现完全对齐。

### B 零测试模块补齐（基础单测，全包当前 [no test files]）

- **B1 internal/cors（51 行，P0，已完成 ✓ 2026-08-30）**：新增 cors_test.go——`isLocalhostOrigin` 27 例表驱动（localhost/127.0.0.1/[::1]、带端口、https、空值、大小写不敏感、非 localhost 拒绝、子域前缀/后缀注入（`localhost.evil.com`/`localhostevil.com`）、ftp 协议、缺 scheme、尾斜杠、空白环绕）；`SetLocalhostOnly` httptest 三项（localhost 全头设置、远程 Origin 全头省略、缺失 Origin 全头省略）。
- **B2 internal/eval（331 行，P0，已完成 ✓ 2026-08-30）**：新增 eval_test.go——`toolTrajectoryScore` 10 例表驱动（空期望恒过/顺序无关/子串/大小写/部分匹配/重复期望独立计分）+ `patternMatchScore` 5 例 + `minLengthScore` 6 例（比例分/阈值边界）+ `notEmptyScore` 3 例 + `extractUserMessage` 5 例（首个 user turn/仅 assistant/空）。`fakeRunner` 实现 runner.Runner 注入：`Run` 全链路（参数透传 eval-user/eval-tc-1、工具调用收集、流式 delta 拼接、4 指标同时达阈值）、无 user 消息报错、runner 错误降级为 fail、流内错误事件跳过、未知指标默认 1.0 通过、Summary 格式（通过率 2/3/PASS/FAIL/错误行/指标行）与空结果无 panic、SaveResults 嵌套目录 + JSON 反序列化回读、LoadEvalSet 完整字段/缺文件/坏 JSON。
- **B3 internal/apps/mcpapps（1510 行，P0，已完成 ✓ 2026-08-30）**：新增 resource_test.go + host_test.go + manager_test.go + bridge_test.go 共 69 项测试全绿。**测试暴露并修复 1 处真实缺陷**：`BuildCSPHeaders` 对 `nil` resource 直接解引用 `resource.Meta` 会 panic，而 `GetCSPHeaders`（资源不存在时传 nil）必触发——补 nil 守卫。覆盖：resource（`NewUIResource` 默认值、`Validate` 7 例三分支、`hasUIScheme` 8 例、链式 setter + nil Meta 自动初始化、CSPFromConfig/GenerateDefaultCSP、ToJSON/FromJSON round-trip、`NewResourceContent(FromBlob)`、`ResourceContent.Validate` 5 例）；host（默认全 none CSP 头、四类域名拼接、资源级 CSP 覆盖 host、注册/查询/列表、`RegisterContent` 未注册资源报错、工具注册、joinDomains/joinDirectives）；manager（escapeJS 10 例全转义、joinSandboxAttrs、`RegisterAppAsMCPResource` 成功/未找到/description 回退（真实 apps.Manager + t.TempDir）、`RegisterToolForApp` Meta 含 ResourceURI + [model, app]、SetCSP/SetPermissions、GetCSPHeaders 带头/回退、`GenerateSandboxedViewHTML` 成功含 escapeJS 内容/资源不存在/内容不存在）；bridge（SetOnSendCallback 捕获 + HandleMessage 回放驱动 Request 成功/Error 响应、send 失败清理 pendingReqs、marshal 失败、Notify 无 ID、无回调入队 GetQueuedMessages、坏 JSON/无效消息/未知响应 ID、入站 request 路由（成功/无 handler -32601/内部错误 -32603/JSONRPCError 透传）、通知路由、MessageHandler 触发、Initialize 成功解析 hostContext + 重复初始化报错 + 错误传播、SendMessage/ReportSize、parseID 7 例、JSONRPCError.Error 格式）。
- **B4 internal/artifact（70 行，P1，已完成 ✓ 2026-08-30）**：新增 factory_test.go 共 10 项测试全绿——`NewService` 全分支：inmemory（显式 + 空 backend 默认）、cos 缺 bucketURL 报错、缺凭据报错、凭据来自 config、凭据来自 COS_SECRETID/COS_SECRETKEY 环境变量回退、config 优先于 env、混合来源（ID 走 env + Key 走 config）、部分凭据拒绝、未知 backend（s3）报 `unsupported artifact backend`、零值 config 默认 inmemory。均用 `t.Setenv` 隔离环境变量。
- **B5 internal/knowledge（531 行，P1，已完成 ✓ 2026-08-30）**：新增 knowledge_test.go 共 17 项测试全绿（ok 1.095s）——`resolveEmbedderCredentials` 三优先级 6 子表（显式 provider → cortex 启用且 EmbeddingBaseURL 非空 → 默认 provider）、`collectSources`（真实临时目录 4 源 + 空配置）、`isExportableExt` 9 允许 + 8 拒绝、`sanitizeFilename` 5 例、`sanitizeConceptID`（含跨平台反斜杠）、`gracefulSearchTool` 3 项（fake `tool.CallableTool` 注入：正常结果透传、"no relevant documents found" 转 web search 提示、非相关错误透传）、`Manager` nil 接收者方法、`NewManager(disabled)` 返回 nil；`ExportBundle`/`exportDir` 真实 t.TempDir 全链路（kb 用 `knowledge.New()` 伪造非 nil：FromLocalDir 生成 guide.md/index.md/log.md 并跳过 png、WithSourceURLs 生成 urls/ 文件、跳过不支持扩展名、解析文本文件）。**测试暴露并修复 1 处真实缺陷**：`sanitizeFilename` 未替换反斜杠，Windows 路径（如 `C:/temp\file.txt`）会产出含 `\` 的非法文件名——补 `strings.ReplaceAll(s, "\\", "_")`。
- **B6 internal/skill（670 行，P1，已完成 ✓ 2026-08-30）**：新增 skill_test.go 共 25 项测试全绿——`calculateQualityScore` 11 例表驱动（float 容差比较）；`captureEvolutionTrace` 全键注入/类型容错/零值默认/nil 守护（agent.Invocation SetState）；`SkillsDir` 默认与自定义（t.Setenv）；`Initialize` 禁用分支与索引（含 `type: skill` frontmatter 技能）；`GetSkill` 未初始化报错；`Refresh` 重建索引；`CreateSkillAgent` 未初始化报错；OKF 全链路：`ExportSkillsAsOKF`（含未初始化）、`EnsureAllOKFCompliant`、`ImportOKFSkills`（lb 解析/缺 bundle 报错/刷新仓库）、`EnsureOKFType` 8 子用例（已有 type 不变/无 frontmatter 加前缀/type 缺失或空或非字符串补丁/malformed 或 yaml 不可解析跳过/CRLF 补丁）+ 缺文件跳过。
- **B7 internal/observability（67 行，P1，已完成 ✓ 2026-08-30）**：新增 observability_test.go 共 8 项测试全绿——`StartLangfuse` disabled 分支不触碰环境/启用分支把 config 写入 env/无 host 时 `LANGFUSE_INSECURE=true`/已有 env 优先/缺凭据报错且 cleanup=nil；`setIfEmpty` 3 子用例（均用 t.Setenv + Cleanup 恢复）。
- **B8 internal/apps/server（310 行，P1，已完成 ✓ 2026-08-30）**：新增 server_test.go 共 14 项测试全绿——handleRequest 文件/嵌套文件/根 index/无 index 目录列表/父链接与转义（Windows `filepath.Dir` 差异放宽）/嵌套 index/目录遍历拒绝（Windows 上 `filepath.Clean` 会把 `..` 弹回根内，403 与 404 均为安全结果）/404/查询串忽略；`exists`；生命周期：固定端口/自动端口/上下文取消停止/双重启动拒绝/双重 Stop 幂等；DefaultConfig。**测试暴露并修复 2 处真实缺陷**：① 自动选端口时 `Port()` 返回 0——`Start` 写回实际端口；② `StartAndWait` 竞态（running 置位先于 ListenAndServe）——重构为 `net.Listen` 先建立监听器再 `http.Server.Serve(ln)`，监听器就绪才置位 running。
- **B9 internal/topofmind（P1，已完成 ✓ 2026-08-30）**：补齐原仅 5 项的测试缺口，新增 6 项（共 11 项全绿，含 -race）：ModTime 变更触发重载、重载按 MaxLength 截断、文件删除回退缓存、缺文件不 panic、空 InstructionFile 短路、20 goroutine × 并发读写竞态检测。
- **P2 延后（需外部服务/大 mock）**：skill `CreateSkillAgent`/`Initialize`、knowledge `NewManager` 启用分支与 `Import/ExportBundle` 全链路、observability `StartLangfuse` 启用分支。

### C 功能增强（已完成 ✓ 2026-08-30）

- **C1 TUI 多会话 Tab（已完成 ✓ 2026-08-30）**：`/sessions` 打开会话面板 modal——"[+ New session]（→ `/new`）/ 各会话 ID+更新时间 / [— Back]"，Enter 选中即有 ID 行走 `/resume <sessionID>` 切换（复用 /resume 语义：切换 `sessionID` + 清空 UI 状态，server-side session store 恢复上下文），Backspace 对选中会话调 `DeleteSession` 并刷新面板，删至空自动关闭。TUI 通过 `any` 字段 + `sessionLister` 小接口（ListSessions/DeleteSession）解耦 `wksession.SessionService`（AppName `wukong-app`）；`cli/session.go` 的 `BootstrapState` 新增 `SessionSvc` 字段经 `StartTUI` 注入。新增 7 项测试（无 manager 降级 / 打开面板 / Enter resume / Enter new / Back / 删除单条 / 删空关闭），命令菜单 `/sessions` 条目及相关 RenderModal 测试同步更新。
- **C2 CUI REPL 历史落盘（已完成 ✓ 2026-08-30）**：`newHistoryWithFile(100, historyFilePath(cfg))` 将解析历史持久化到 `~/.wukong/history`——启动加载（缺文件/坏文件回退空历史）、退出保存（含 `/history-clear` 清空时删除落盘文件）、上限 100 循环裁剪跨进程保留。新增 9 项测试（load/save/裁剪/清空删除/路径解析等）。
- **C3 `/projects` 直达选择（已完成 ✓ 2026-08-30）**：`/projects` 由纯文本列表改为 ModalProjects 选择 modal（路径 + 8 位截断 SessionID + 指令摘要，Enter 提示），选中后通过 `projectMgr.ListProjects()` 重新查询完整 record 取完整 SessionID，复用 `/resume <sessionID>` 直达对应会话；空列表仍降级提示。原 3 项 `/projects` 测试改写为 modal 断言，新增 1 项选择恢复测试（验证完整 SessionID 拼接）。

## 七、回归验证

每阶段完成执行：

```
go build ./...
go vet ./...
go test ./internal/cli/... -short
```