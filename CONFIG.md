# Wukong 配置说明文档

> 完整配置文件参考: `config.yaml` | 配置加载器: `internal/config/config.go`

---

## 目录

1. [配置加载机制](#1-配置加载机制)
2. [配置文件位置](#2-配置文件位置)
3. [配置项参考](#3-配置项参考)
4. [环境变量覆盖](#4-环境变量覆盖)
5. [CLI 参数覆盖](#5-cli-参数覆盖)
6. [常见配置场景](#6-常见配置场景)

---

## 1. 配置加载机制

Wukong 使用 **Viper** 进行配置管理，配置优先级从高到低为：

```
优先级 1 (最高): CLI 参数           --provider, --model, --temperature, etc.
优先级 2:        环境变量            WUKONG_ 前缀
优先级 3:        YAML 配置文件       config.yaml
优先级 4 (最低):  内置默认值          setDefaults() in config.go
```

### 加载流程

```
NewLoader(configPath)
  ├── 注册默认值 (setDefaults)
  ├── 读取 YAML 文件 (按搜索路径)
  ├── 合并环境变量 (WUKONG_ prefix)
  └── Load()
      ├── Unmarshal → WukongConfig
      ├── 展开 ${ENV_VAR} 引用
      └── 缓存结果
```

---

## 2. 配置文件位置

配置文件搜索顺序（优先使用第一个找到的）：

| 优先级 | 路径 | 说明 |
|--------|------|------|
| 1 | `--config` CLI 参数指定的路径 | 命令行指定 |
| 2 | `./config.yaml` | 当前工作目录 |
| 3 | `~/.config/wukong/config.yaml` | 用户配置目录 |
| 4 | `/etc/wukong/config.yaml` | 系统级配置 |

**推荐做法**：将 `config.yaml` 放在 `~/.config/wukong/` 目录下，API Key 使用环境变量引用。

---

## 3. 配置项参考

### 3.1 Providers（LLM 提供商）

```yaml
default_provider: openai          # 默认使用的提供商名称

providers:
  - name: openai                  # 唯一标识符
    type: openai                  # 提供商类型
    base_url: https://api.openai.com/v1
    api_key: ${OPENAI_API_KEY}    # 支持环境变量展开
    model: gpt-4o                 # 默认模型
```

**支持的提供商类型**：

| type | base_url (默认) | 说明 |
|------|----------------|------|
| `openai` | `https://api.openai.com/v1` | OpenAI 及兼容 API |
| `anthropic` | `https://api.anthropic.com/v1` | Anthropic Claude |
| `google` | `https://generativelanguage.googleapis.com/v1beta/openai` | Google Gemini |
| `deepseek` | `https://api.deepseek.com/v1` | DeepSeek |
| `ollama` | `http://localhost:11434/v1` | 本地 Ollama |
| `lmstudio` | (需手动指定) | 本地 LM Studio |

### 3.2 Agent（代理行为）

```yaml
agent:
  max_llm_calls: 50               # 每次运行最大 LLM 调用次数 (0=无限制)
  max_tool_iterations: 30         # 最大工具调用迭代次数
  parallel_tools: true            # 并行执行独立工具调用
  streaming: true                 # 启用实时流式输出
  max_run_duration: 300s          # 单次运行最大时长
  temperature: 0.7                # 模型采样温度 (0.0-2.0)
  max_tokens: 4096                # 每次 LLM 调用最大输出 token
  tool_retry_enabled: true        # 启用工具调用失败重试
  tool_retry_max_attempts: 3      # 最大重试次数
  tool_retry_initial_wait: 1s     # 首次重试等待时间
  tool_retry_backoff_factor: 2.0  # 退避倍数 (指数退避)
  enable_post_tool_prompt: true   # 工具结果后注入提示
```

### 3.3 Extensions（扩展工具）

```yaml
extensions:
  # 内置扩展
  - name: developer               # 文件读写、命令执行、代码搜索
    type: builtin
    enabled: true

  - name: computer_controller     # Web 抓取、文件缓存、浏览器自动化
    type: builtin
    enabled: true

  - name: memory                  # 长期记忆管理
    type: builtin
    enabled: true

  - name: auto_visualiser         # 图表/流程图/表格生成
    type: builtin
    enabled: true

  - name: tutorial                # 交互式教程
    type: builtin
    enabled: true

  - name: top_of_mind             # 持久化指令注入
    type: builtin
    enabled: true

  - name: code_mode               # JavaScript 沙箱执行
    type: builtin
    enabled: true

  - name: apps                    # 自定义 HTML 应用
    type: builtin
    enabled: true

  # 外部 MCP 服务器
  # - name: filesystem
  #   type: external
  #   transport: stdio            # stdio | sse | streamable
  #   command: npx
  #   args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
  #   enabled: false
```

**内置扩展功能一览**：

| 扩展 | 提供的工具 | 依赖 |
|------|-----------|------|
| `developer` | file_read, file_write, file_replace, command_execute, code_search, directory_list | rg (可选) |
| `computer_controller` | web_fetch, file_cache, browser_navigate, browser_extract, browser_screenshot | browser.enabled |
| `memory` | memory_add, memory_search, memory_update, memory_delete, memory_load, memory_clear | 无 |
| `auto_visualiser` | visualiser_chart, visualiser_diagram, visualiser_table | visualiser.enabled |
| `tutorial` | tutorial_start, tutorial_list, tutorial_step | tutorial.enabled |
| `top_of_mind` | (注入系统指令，非工具) | top_of_mind.enabled |
| `code_mode` | (执行 JS 代码，非独立工具) | code_mode.enabled |
| `apps` | (创建/管理 HTML 应用，非独立工具) | apps.enabled |

### 3.4 Storage（存储配置）

```yaml
session:
  backend: sqlite                 # sqlite | memory
  db_path: wukong.db              # SQLite 数据库文件
  event_limit: 500                # 每个会话最大事件数
  ttl: 0h                         # 会话过期时间 (0=不过期)
  enable_summary: true            # 启用自动摘要
  summary_trigger: 50             # 触发摘要的事件数阈值

memory:
  backend: sqlite                 # sqlite | memory
  db_path: wukong.db
  max_memories: 100               # 每用户最大记忆数
  auto_extract: true              # 自动从对话提取记忆

todo:
  backend: sqlite                 # sqlite | memory
  db_path: wukong.db

recall:
  enabled: true                   # 启用跨会话搜索
  backend: sqlite                 # sqlite | memory
  db_path: wukong.db
  max_results: 10                 # 最大搜索结果数
  max_messages_per_session: 200   # 每会话最大存储消息数
```

**关于 DatabasePool**：所有 SQLite 后端默认共享同一个 `wukong.db` 文件，通过连接池 (`internal/util/database.go`) 管理。各模块可配置独立的 `db_path` 使用不同文件。

### 3.5 Security（安全配置）

```yaml
security:
  malware_scan_enabled: true      # 扫描外部扩展恶意代码
  default_timeout: 30s            # 工具默认超时
  max_timeout: 300s               # 工具最大超时
  block_dangerous_commands: true  # 拦截危险命令
  blocked_commands:               # 危险命令模式列表
    - "rm -rf /"
    - "dd if=/dev/zero"
    - "mkfs."
    - "> /dev/sda"
    - "fork bomb"
  permission_mode: smart          # auto | smart | manual | chat_only
  allowlist: []                   # 工具白名单 (空=全部允许)
  # denylist:                     # 工具黑名单
  #   - "unsafe_tool"
```

**权限模式说明**：

| 模式 | 行为 | 适用场景 |
|------|------|---------|
| `auto` | 所有工具自动执行 | 完全信任的沙箱环境 |
| `smart` | 高风险操作需审批 | **推荐**，日常使用 |
| `manual` | 所有工具调用需审批 | 高安全要求环境 |
| `chat_only` | 禁止所有工具 | 纯对话场景 |

### 3.6 Context Management（上下文管理）

```yaml
revision:
  enabled: true                   # 启用自动上下文管理
  revision_provider: ""           # 专用摘要模型提供商 (空=默认)
  revision_model: ""              # 专用摘要模型 (空=默认模型)
  max_command_output: 8000        # 命令输出最大字节数
  enable_semantic_search: false   # 启用语义搜索 (实验性)
  search_strategy: include_all    # include_all | semantic
  max_context_tokens: 64000       # 上下文窗口软限制
  trim_ratio: 0.3                 # 超出限制时的裁剪比例
```

### 3.7 Browser（浏览器自动化）

```yaml
browser:
  enabled: true
  browser_type: chromium          # chromium | firefox | webkit
  headless: true                  # 无头模式
  cache_dir: .wukong_cache        # 文件缓存目录
  max_download_size: 104857600    # 最大下载大小 (100MB)
  timeout: 60s                    # 请求超时
```

### 3.8 Visualiser（可视化）

```yaml
visualiser:
  enabled: true
  output_dir: .wukong_visuals     # 输出目录
  max_width: 1200                 # 最大图表宽度
  max_height: 800                 # 最大图表高度
```

### 3.9 Tutorial（教程）

```yaml
tutorial:
  enabled: true
  language: zh                    # zh | en
```

### 3.10 Top of Mind（持久化指令）

```yaml
top_of_mind:
  enabled: true
  instruction_file: .wukong_instructions.md  # 指令文件路径
  max_length: 2000                # 最大指令长度 (字符)
```

**使用方式**：在 `.wukong_instructions.md` 中写入持久化指令，这些指令会在每次对话时自动注入到 system prompt 中。

### 3.11 Code Mode（代码执行）

```yaml
code_mode:
  enabled: true
  timeout: 10s                    # 单次执行超时
  max_memory_mb: 128              # JS 引擎内存限制 (MB)
```

### 3.12 Apps（HTML 应用）

```yaml
apps:
  enabled: true
  app_dir: .wukong_apps           # 应用存储目录
```

### 3.13 Summon（子代理委派）

```yaml
summon:
  enabled: true
  skills_dir: .wukong_skills      # Skill recipe 目录
  max_concurrent: 5               # 最大并发子代理数
  # a2a_remotes:                  # 远程 A2A 代理
  #   - name: code-reviewer
  #     description: "Reviews code for bugs and style issues"
  #     server_url: http://localhost:9090
  #     auth_type: api_key        # jwt | api_key | oauth2
  #     api_key: ${REMOTE_API_KEY}
```

### 3.14 Skill（Agent 技能系统）

```yaml
skill:
  enabled: true
  skills_dir: .wukong_agent_skills  # SKILL.md 文件目录
  auto_load: true                 # 启动时自动加载
  max_skills: 20                  # 最大加载技能数
```

> **注意**：Skill 系统使用 `SKILL.md` 文件（目录格式），与 Summon 的 `.md` recipe 文件是独立的两个系统。

### 3.15 Workflow（工作流编排）

```yaml
workflow:
  mode: single                    # single | chain | parallel | cycle | graph
  max_iterations: 10              # cycle/graph 模式最大迭代次数
  # sub_agents:                   # 自定义子代理
  #   - name: planner
  #     instruction: "You are a planning specialist..."
  #     allowed_tools: ["file_read", "search_code"]
  #   - name: executor
  #     instruction: "You execute the plan..."
  #     all_tools: true
```

### 3.16 A2A Server（A2A 服务端）

```yaml
a2a_server:
  enabled: false                  # 启用 A2A 服务
  address: ":9090"                # 监听地址
  agent_name: wukong              # A2A 代理名称
  agent_description: "Wukong AI Agent - A2A service endpoint"
```

### 3.17 Telemetry（遥测）

```yaml
telemetry:
  enabled: false                  # 启用分布式追踪
  exporter_type: console          # grpc | http | console
  endpoint: localhost:4317        # OTLP 收集器地址
  service_name: wukong
  service_version: "1.0.0"
  environment: development        # development | staging | production
  sample_rate: 1.0                # 采样率 (0.0-1.0)
```

---

## 4. 环境变量覆盖

所有配置项都可以通过环境变量覆盖，格式为 `WUKONG_<SECTION>_<KEY>`：

```bash
# 覆盖默认提供商
export WUKONG_DEFAULT_PROVIDER=deepseek

# 覆盖代理配置
export WUKONG_AGENT_TEMPERATURE=0.3
export WUKONG_AGENT_MAX_TOKENS=8192
export WUKONG_AGENT_MAX_LLM_CALLS=100

# 覆盖安全配置
export WUKONG_SECURITY_PERMISSION_MODE=manual

# 覆盖遥测配置
export WUKONG_TELEMETRY_ENABLED=true
export WUKONG_TELEMETRY_EXPORTER_TYPE=grpc
```

**注意**：环境变量优先级低于 CLI 参数，高于 YAML 配置文件。

---

## 5. CLI 参数覆盖

```bash
# 指定配置文件
wukong session --config /path/to/custom.yaml

# 指定提供商
wukong session --provider deepseek

# 指定模型
wukong session --model gpt-4o-mini

# 恢复之前的会话
wukong session --session-id abc12345

# 调整生成参数
wukong session --temperature 0.3 --max-tokens 8192

# 禁用流式输出
wukong session --no-stream
```

---

## 6. 常见配置场景

### 6.1 使用本地 Ollama

```yaml
default_provider: ollama

providers:
  - name: ollama
    type: ollama
    base_url: http://localhost:11434/v1
    api_key: ollama
    model: llama3.1:8b
```

### 6.2 使用 DeepSeek

```yaml
default_provider: deepseek

providers:
  - name: deepseek
    type: deepseek
    base_url: https://api.deepseek.com/v1
    api_key: ${DEEPSEEK_API_KEY}
    model: deepseek-chat
```

### 6.3 高安全模式（所有工具需审批）

```yaml
security:
  permission_mode: manual
  block_dangerous_commands: true
```

### 6.4 纯对话模式（禁用所有工具）

```yaml
security:
  permission_mode: chat_only

extensions:
  - name: developer
    type: builtin
    enabled: false
  # ... 禁用其他扩展
```

### 6.5 仅允许特定工具

```yaml
security:
  permission_mode: smart
  allowlist:
    - "file_read"
    - "file_write"
    - "search_code"
    - "memory_search"
    - "memory_add"
```

### 6.6 启用 A2A 服务

```yaml
a2a_server:
  enabled: true
  address: ":9090"
  agent_name: wukong

summon:
  enabled: true
  a2a_remotes:
    - name: specialist
      description: "Specialized agent for data analysis"
      server_url: http://other-host:9090
      auth_type: api_key
      api_key: ${REMOTE_API_KEY}
```

### 6.7 使用纯内存存储（不持久化）

```yaml
session:
  backend: memory

memory:
  backend: memory

todo:
  backend: memory

recall:
  backend: memory
```

### 6.8 生产环境部署

```yaml
agent:
  max_run_duration: 600s
  max_llm_calls: 100

security:
  permission_mode: smart
  block_dangerous_commands: true

telemetry:
  enabled: true
  exporter_type: grpc
  endpoint: otel-collector:4317
  environment: production
  sample_rate: 0.1               # 10% 采样

a2a_server:
  enabled: true
  address: ":9090"
```
