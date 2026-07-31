# Wukong 技术实现详解

> 本文档详细说明 Wukong 各子系统的技术实现细节，包括核心代码、数据结构、算法设计和性能优化。

---

## 目录

1. [CoreLoop 中央编排引擎实现](#1-coreloop-中央编排引擎实现)
2. [多 Agent 编排系统实现](#2-多-agent-编排系统实现)
3. [记忆系统实现](#3-记忆系统实现)
4. [Evolution 进化引擎实现](#4-evolution-进化引擎实现)
5. [安全防御系统实现](#5-安全防御系统实现)
6. [网站克隆引擎实现](#6-网站克隆引擎实现)
7. [应用管理系统实现](#7-应用管理系统实现)
8. [扩展系统实现](#8-扩展系统实现)
9. [浏览器引擎实现](#9-浏览器引擎实现)
10. [ANP/ARD 协议实现](#10-anpard-协议实现)
11. [配置系统实现](#11-配置系统实现)
12. [数据存储实现](#12-数据存储实现)

---

## 1. CoreLoop 中央编排引擎实现

### 1.1 核心数据结构

**CoreLoop 结构体** (`internal/agent/loop.go`):

```go
type CoreLoop struct {
    agent          agent.Agent           // Agent 实例
    runner         runner.Runner         // Runner 执行器
    sessionService session.Service       // 会话服务
    memoryService  memory.Service        // 记忆服务
    factory        *provider.Factory     // Provider 工厂
    cfg            *config.WukongConfig  // 配置
    contextMgr     *ContextManager       // 上下文管理器
    security       *security.Guard       // 安全检查器
    recallStore    *recall.Store         // 召回存储
    cortexStore    *cortex.CortexStore   // CortexDB 存储
    memoryFlow     *cortex.MemoryFlowService  // MemoryFlow 服务
    graphFlow      *cortex.GraphFlowService    // GraphFlow 服务
    
    mu     sync.RWMutex
    closed bool
    bgWg   sync.WaitGroup  // 后台 goroutine 等待组
    runWg  sync.WaitGroup  // 运行中任务等待组
}
```

### 1.2 四阶段执行流程

#### Phase 1: Prepare — 上下文准备

```go
func (l *CoreLoop) prepareContext(
    ctx context.Context,
    userID, sessionID string,
    message model.Message,
) (model.Message, error) {
    // 1. 转录用户消息到 MemoryFlow
    if l.memoryFlow != nil {
        content := extractMessageContent(message)
        if err := l.memoryFlow.IngestTurn(ctx,
            sessionID, userID, "user", content); err != nil {
            util.Logger.Warn("memoryflow ingest failed", "error", err)
        }
    }
    
    // 2. 唤醒 3 层上下文 (Identity → Recall → Session)
    var wakeCtx string
    if l.memoryFlow != nil {
        identity := "You are Wukong, a helpful AI agent."
        wakeCtx, _ = l.memoryFlow.WakeUp(ctx,
            identity, content, sessionID, userID)
        
        if wakeCtx != "" {
            message = model.Message{
                Role:    "user",
                Content: fmt.Sprintf("%s\n\n[User message]\n%s",
                    wakeCtx, content),
            }
        }
    }
    
    // 3. 语义召回 (CortexStore 或 Recall)
    // 4. 注入持久记忆 (tRPC Memory)
    // ... 详细实现见完整代码
    
    return message, nil
}
```

#### Phase 2: Execute — 执行

```go
func (l *CoreLoop) execute(
    ctx context.Context,
    userID, sessionID string,
    message model.Message,
) (<-chan *event.Event, error) {
    // 创建 OpenTelemetry 追踪
    tracer := otel.Tracer("wukong/agent")
    ctx, span := tracer.Start(ctx, "agent.Run",
        trace.WithAttributes(
            attribute.String("user_id", userID),
            attribute.String("session_id", sessionID),
        ),
    )
    defer span.End()
    
    // 运行时选项
    runOpts := []agent.RunOption{}
    if l.cfg.Agent.JSONRepairEnabled {
        runOpts = append(runOpts,
            agent.WithToolCallArgumentsJSONRepairEnabled(true))
    }
    
    // 执行 Agent
    events, err := l.runner.Run(ctx, userID, sessionID, message, runOpts...)
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
        return nil, err
    }
    
    return events, nil
}
```

### 1.3 优雅关闭序列

```go
func (l *CoreLoop) Close() error {
    l.mu.Lock()
    if l.closed {
        l.mu.Unlock()
        return nil
    }
    l.closed = true
    l.mu.Unlock()
    
    var errs []error
    
    // 1. 等待运行中的任务
    l.runWg.Wait()
    
    // 2. 等待后台 goroutine
    l.bgWg.Wait()
    
    // 3. 关闭 Runner
    if err := l.runner.Close(); err != nil {
        errs = append(errs, err)
    }
    
    // 4. 关闭 Evolution 引擎
    // 5. 关闭 GraphFlow
    // 6. 刷新遥测数据
    // 7. 关闭数据库连接池 (最后)
    
    if len(errs) > 0 {
        return fmt.Errorf("close errors: %w", errors.Join(errs...))
    }
    return nil
}
```

---

## 2. 多 Agent 编排系统实现

### 2.1 WorkflowBuilder 工厂模式

```go
func (b *WorkflowBuilder) Build(
    ctx context.Context,
    wfCfg *OrchestrationConfig,
) (agent.Agent, error) {
    switch wfCfg.Mode {
    case WorkflowSingle:
        return b.buildSingleAgent()
    case WorkflowChain:
        return b.buildChainAgent(wfCfg)
    case WorkflowParallel:
        return b.buildParallelAgent(wfCfg)
    case WorkflowCycle:
        return b.buildCycleAgent(wfCfg)
    case WorkflowGraph:
        return b.buildGraphAgent(wfCfg)
    default:
        return nil, fmt.Errorf("unsupported mode: %s", wfCfg.Mode)
    }
}
```

### 2.2 编排模式实现

**Chain 编排** (规划者 → 执行者 → 审查者):

```go
func (b *WorkflowBuilder) buildChainAgent(wfCfg *OrchestrationConfig) (agent.Agent, error) {
    subAgents := []agent.Agent{
        b.createSpecializedAgent("planner", "You are a planning specialist..."),
        b.createSpecializedAgent("executor", "You are an execution specialist..."),
        b.createSpecializedAgent("reviewer", "You are a quality reviewer..."),
    }
    
    return chainagent.New("wukong-chain",
        chainagent.WithSubAgents(subAgents),
    ), nil
}
```

**Parallel 编排** (多视角并发分析):

```go
func (b *WorkflowBuilder) buildParallelAgent(wfCfg *OrchestrationConfig) (agent.Agent, error) {
    subAgents := []agent.Agent{
        b.createSpecializedAgent("code-analyzer", "Technical perspective..."),
        b.createSpecializedAgent("doc-analyzer", "Documentation perspective..."),
        b.createSpecializedAgent("test-analyzer", "Testing perspective..."),
    }
    
    return parallelagent.New("wukong-parallel",
        parallelagent.WithSubAgents(subAgents),
    ), nil
}
```

**Graph 编排** (条件路由 DAG):

```go
func (b *WorkflowBuilder) buildGraphAgent(wfCfg *OrchestrationConfig) (agent.Agent, error) {
    // 构建状态图
    sg := graph.NewStateGraph(schema)
    
    // 添加节点
    sg.AddAgentNode("analyze")
    sg.AddAgentNode("code")
    sg.AddAgentNode("search")
    sg.AddAgentNode("answer")
    sg.AddAgentNode("review")
    
    // 定义条件路由
    sg.AddConditionalEdges("analyze", routingFunc, pathMap)
    
    // 汇聚到 review
    sg.AddEdge("code", "review")
    sg.AddEdge("search", "review")
    sg.AddEdge("answer", "review")
    
    compiledGraph, _ := sg.Compile()
    
    return graphagent.New("wukong-graph", compiledGraph), nil
}
```

---

## 3. 记忆系统实现

### 3.1 MemoryFlow 实现

**IngestTurn - 对话转录**:

```go
func (m *MemoryFlowService) IngestTurn(
    ctx context.Context,
    sessionID string,
    userID string,
    role string,
    content string,
) error {
    _, err := m.flow.IngestTranscript(ctx,
        memoryflow.IngestTranscriptRequest{
            Transcript: memoryflow.Transcript{
                SessionID: sessionID,
                UserID:    userID,
                Source:    "chat",
                Turns: []memoryflow.TranscriptTurn{
                    {Role: role, Content: content},
                },
            },
            Scope:     cortexdb.MemoryScopeSession,
            Namespace: m.cfg.Namespace,
        },
    )
    return err
}
```

**WakeUp - 3 层上下文唤醒**:

```go
func (m *MemoryFlowService) WakeUp(
    ctx context.Context,
    identity string,
    query string,
    sessionID string,
    userID string,
) (string, error) {
    resp, err := m.flow.WakeUpLayers(ctx,
        memoryflow.WakeUpLayersRequest{
            Identity: identity,
            Recall: memoryflow.RecallRequest{
                Query:     query,
                SessionID: sessionID,
                UserID:    userID,
            },
        },
    )
    
    // 格式化 3 层: Identity → Recall → Session
    var builder strings.Builder
    for _, layer := range resp.Layers {
        if layer.Text != "" {
            fmt.Fprintf(&builder, "## %s\n%s\n\n", layer.Title, layer.Text)
        }
    }
    return builder.String(), nil
}
```

### 3.2 CortexStore 双索引实现

```go
type CortexStore struct {
    db          *cortexdb.DB  // HNSW 向量索引
    lexical     *lexicalStore // FTS5 全文索引
    vectorCache *VectorCache  // 向量缓存
}

func (s *CortexStore) Search(query, userID string, limit int) ([]recall.SearchResult, error) {
    if s.db != nil && s.embedder != nil {
        return s.searchVector(query, userID, limit)
    }
    return s.lexical.search(query, userID, limit)
}
```

### 3.3 记忆去重算法

```go
func isMemoryDuplicated(memoryText, wakeCtx string) bool {
    // 30 字符滑动窗口，60% 重叠阈值
    windowSize := 30
    overlapThreshold := 0.6
    
    memoryLower := strings.ToLower(memoryText)
    wakeLower := strings.ToLower(wakeCtx)
    
    overlapCount := 0
    totalWindows := 0
    
    for i := 0; i <= len(memoryLower)-windowSize; i++ {
        window := memoryLower[i : i+windowSize]
        totalWindows++
        if strings.Contains(wakeLower, window) {
            overlapCount++
        }
    }
    
    return float64(overlapCount)/float64(totalWindows) >= overlapThreshold
}
```
