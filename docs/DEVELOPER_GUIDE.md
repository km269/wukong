# Wukong 开发者指南

> 本指南帮助开发者快速上手 Wukong 开发，包括环境搭建、代码结构、开发流程和最佳实践。

---

## 目录

1. [开发环境搭建](#1-开发环境搭建)
2. [项目结构](#2-项目结构)
3. [核心模块开发](#3-核心模块开发)
4. [扩展开发](#4-扩展开发)
5. [测试指南](#5-测试指南)
6. [调试技巧](#6-调试技巧)
7. [贡献指南](#7-贡献指南)

---

## 1. 开发环境搭建

### 1.1 系统要求

- Go 1.26+
- Git
- Make (可选)

### 1.2 安装步骤

```bash
# 克隆仓库
git clone https://github.com/km269/wukong.git
cd wukong

# 安装依赖
go mod download

# 构建项目
go build -o wukong ./cmd/wukong

# 运行测试
go test ./...

# 运行项目
./wukong version
```

### 1.3 IDE 配置

**VS Code 推荐扩展**:
- Go
- gopls
- Go Nightly

**配置 settings.json**:
```json
{
  "go.useLanguageServer": true,
  "gopls": {
    "ui.semanticAnalysis": true,
    "ui.completion.usePlaceholders": true
  }
}
```

---

## 2. 项目结构

```
wukong/
├── cmd/                    # 入口命令
│   └── wukong/            # 主程序入口
├── internal/              # 内部实现
│   ├── agent/             # Agent 核心
│   │   ├── loop.go        # CoreLoop
│   │   ├── workflow.go    # 编排系统
│   │   └── context.go     # 上下文管理
│   ├── cortex/            # 记忆系统
│   │   ├── store.go       # CortexStore
│   │   ├── memoryflow.go  # MemoryFlow
│   │   └── graphflow.go   # GraphFlow
│   ├── evolution/         # 进化引擎
│   ├── security/          # 安全系统
│   ├── apps/              # 应用管理
│   ├── extension/         # 扩展系统
│   └── browser/           # 浏览器引擎
├── pkg/                   # 公共包
│   ├── sandbox/           # 沙箱
│   ├── cli/               # CLI 工具
│   └── utils/             # 工具函数
├── docs/                  # 文档
├── configs/               # 配置示例
└── scripts/               # 脚本
```

---

## 3. 核心模块开发

### 3.1 添加新工具

在 `internal/tools/` 创建新工具:

```go
package tools

import "context"

type MyTool struct {
    name string
}

func NewMyTool() *MyTool {
    return &MyTool{name: "my_tool"}
}

func (t *MyTool) Name() string {
    return t.name
}

func (t *MyTool) Description() string {
    return "My custom tool description"
}

func (t *MyTool) Execute(ctx context.Context, input string) (string, error) {
    // 实现工具逻辑
    return "result", nil
}
```

### 3.2 添加新 Provider

在 `internal/provider/` 添加新 Provider:

```go
package provider

type MyProvider struct {
    apiKey  string
    baseURL string
}

func NewMyProvider(cfg *Config) *MyProvider {
    return &MyProvider{
        apiKey:  cfg.APIKey,
        baseURL: cfg.BaseURL,
    }
}

func (p *MyProvider) Call(ctx context.Context, req *Request) (*Response, error) {
    // 实现 API 调用
    return &Response{}, nil
}
```

---

## 4. 扩展开发

### 4.1 创建扩展

```go
package extension

type MyExtension struct {
    name string
}

func NewMyExtension() *MyExtension {
    return &MyExtension{name: "my_extension"}
}

func (e *MyExtension) Name() string {
    return e.name
}

func (e *MyExtension) Initialize(ctx context.Context) error {
    // 初始化逻辑
    return nil
}

func (e *MyExtension) Tools() []tool.Tool {
    // 返回工具列表
    return nil
}
```

### 4.2 注册扩展

在配置文件中添加:

```yaml
extensions:
  - name: "my_extension"
    type: "external"
    transport: "stdio"
    command: "/path/to/my_extension"
    enabled: true
```

---

## 5. 测试指南

### 5.1 单元测试

```go
package mypackage

import "testing"

func TestMyFunction(t *testing.T) {
    tests := []struct {
        name string
        input string
        want string
    }{
        {"case1", "input1", "output1"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := MyFunction(tt.input)
            if got != tt.want {
                t.Errorf("got %q, want %q", got, tt.want)
            }
        })
    }
}
```

### 5.2 运行测试

```bash
# 运行所有测试
go test ./...

# 运行特定包
go test ./internal/agent

# 运行单个测试
go test -run TestMyFunction ./internal/agent

# 查看覆盖率
go test -cover ./...

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## 6. 调试技巧

### 6.1 日志调试

```go
import "log"

log.Printf("Debug: %+v", myVar)
```

### 6.2 Delve 调试器

```bash
# 安装 Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# 调试程序
dlv debug ./cmd/wukong -- session

# 常用命令
# break main.main
# continue
# next
# step
# print <变量>
```

### 6.3 性能分析

```bash
# CPU 分析
go test -cpuprofile=cpu.prof -bench=.

# 内存分析
go test -memprofile=mem.prof -bench=.

# 查看分析
go tool pprof cpu.prof
```

---

## 7. 贡献指南

### 7.1 贡献流程

1. Fork 项目
2. 创建分支: `git checkout -b feature/my-feature`
3. 提交代码: `git commit -m "Add my feature"`
4. 推送分支: `git push origin feature/my-feature`
5. 创建 Pull Request

### 7.2 代码规范

- 遵循 Go 代码规范
- 使用 gofmt 格式化代码
- 添加必要注释
- 编写测试用例

### 7.3 提交信息格式

```
type(scope): subject

body

footer
```

类型: feat, fix, docs, style, refactor, test, chore

---

## 开发工作流

```
开发 → 测试 → 代码审查 → 合并 → 发布
  ↓       ↓        ↓        ↓       ↓
编写代码  单元测试   PR Review  CI/CD   版本发布
```

---

**相关文档**:
- [系统概述](../SYSTEM_OVERVIEW.md)
- [技术架构](ARCHITECTURE.md)
- [API 参考](API_REFERENCE.md)
