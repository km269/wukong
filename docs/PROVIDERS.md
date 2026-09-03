# Wukong Provider 能力矩阵

> 全部 LLM Provider 的接入方式、传输协议、配置面与原生特性覆盖一览。
> 对应路线图 P1-5（见 [Yao 对比与优化路线图](YAO_COMPARISON_AND_ROADMAP.md)）。
> 最后更新：2026-09-03

---

## 1. 总览

Wukong 遵循**框架组装**哲学：LLM 接入层构建于 trpc-agent-go 之上。
当前框架（v1.10.0 / v1.11.2）仅提供 OpenAI 协议原生实现（含
hunyuan / deepseek / qwen 三个 Variant 特化），因此 Wukong 的全部
非 ACP provider 均经 **OpenAI 兼容层**接入——这是各家已广泛提供
`/v1/chat/completions` 兼容端点后的主流做法。

| 类型 | 默认 BaseURL | 传输协议 | 框架特化 |
|------|--------------|----------|----------|
| `openai` | `https://api.openai.com/v1` | OpenAI Chat Completions | VariantOpenAI |
| `anthropic` | `https://api.anthropic.com/v1` | OpenAI 兼容层 | — |
| `google` / `gemini` | `https://generativelanguage.googleapis.com/v1beta/openai` | OpenAI 兼容层（Gemini 官方兼容端点） | — |
| `deepseek` | `https://api.deepseek.com/v1` | OpenAI 兼容层 | **VariantDeepSeek**（reasoning content 处理） |
| `ollama` | `http://localhost:11434/v1` | OpenAI 兼容层 | — |
| `lmstudio` | `http://localhost:1234/v1` | OpenAI 兼容层 | — |
| `vllm` | `http://localhost:8000/v1` | OpenAI 兼容层 | — |
| `acp` | — | Agent Client Protocol | 独立实现（`internal/provider/acp.go`） |

> `gemini` 是 `google` 的别名（两者路由与默认值完全一致）。
> OpenAI 兼容云服务（SiliconFlow / OpenRouter / Groq / Moonshot /
> 智谱等）用 `type: openai` + 自定义 `base_url` 接入。

---

## 2. 经兼容层可用的能力

所有 provider（acp 除外）共享同一配置面：

| 能力 | 说明 |
|------|------|
| Chat + 流式（SSE） | 完整支持 |
| Tool calling（function calling） | 完整支持，含并行工具调用 |
| Multimodal（vision） | 经消息 ContentParts 支持（取决于后端模型） |
| Token budget | `context_window` + 框架 token tailoring（精确裁剪防 400） |
| Reasoning content | DeepSeek 系自动启用 VariantDeepSeek；`WithReasoningContentBackfill` 可经逃生舱开启 |
| 重试/超时 | agent 层统一（tool_retry / tool_call_timeout） |

### 配置逃生舱（本版本新增）

当兼容端点暴露 provider 特有的协议细节时，无需改源码即可透传：

```yaml
providers:
  - name: claude-proxy
    type: anthropic
    base_url: https://my-anthropic-proxy/v1
    api_key: ${ANTHROPIC_API_KEY}
    model: claude-sonnet-4
    extra_headers:              # 注入任意请求头
      anthropic-version: "2023-06-01"
    extra_fields:               # 合并进请求体根字段
      thinking: {type: disabled}
```

- `extra_headers`（map[string]string）→ 每个请求的 HTTP 头
- `extra_fields`（map）→ chat-completion 请求体根级 JSON 字段

---

## 3. 原生特性缺失清单（兼容层的边界）

| Provider 原生特性 | 状态 | 原因 |
|------|------|------|
| Anthropic tool blocks / 细粒度 tool 结果 | ❌ | 依赖原生 Messages API |
| Anthropic prompt caching（cache_control） | ❌ | 同上 |
| Anthropic extended thinking 原生块 | 部分 | 兼容端点暴露了基础 reasoning 字段；原生块结构不可达 |
| Gemini grounding（Google Search） | ❌ | 需原生 generateContent |
| Gemini 原生 safety_settings | 可经 `extra_fields` 透传 | 视兼容端点支持度 |
| OpenAI Batch / Files API | 框架支持（batch.go），Wukong 未暴露 | 非当前场景 |

---

## 4. 协议升级路径

- trpc-agent-go **v1.11.2 仍无** anthropic / gemini 原生 model 包
  （model/ 下仅 openai + huggingface/hunyuan 特化）。自研协议客户端
  违背"框架组装"哲学且需独立维护流式/工具块/缓存语义——**不做**。
- 当上游提供原生包后，切换成本约为 `internal/provider/factory.go`
  中两处 switch 各一行路由（`case "anthropic": return anthropic.New(...)`），
  不涉及任何调用方变更——这是 Factory 抽象的设计目标。
- 升级决策点：跟踪 trpc-agent-go 发布日志，出现
  `model/anthropic` 或 `model/gemini` 包时重新评估。

---

## 5. 快速配置示例

```yaml
default_provider: deepseek

providers:
  - name: deepseek            # 国产首选：框架 VariantDeepSeek 特化
    type: deepseek
    api_key: ${DEEPSEEK_API_KEY}
    model: deepseek-chat
    context_window: 64000

  - name: local               # 本地推理：务必设置 context_window
    type: ollama
    base_url: http://localhost:11434/v1
    model: qwen3:27b
    context_window: 32768

  - name: gemini              # google 的别名
    type: gemini
    api_key: ${GEMINI_API_KEY}
    model: gemini-2.0-flash
```

校验配置：`wukong config validate`。上下文窗口设置不当是本地模型
400 报错的首要原因——`ollama` / `vllm` / `lmstudio` 未设置时会收到警告。

---

> **最后更新**: 2026-09-03
