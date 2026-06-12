// Package builtin provides the Tutorial built-in extension.
// It provides interactive tutorial capabilities.
package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// TutorialToolSet provides interactive tutorial tools.
type TutorialToolSet struct {
	tools  []tool.Tool
	cfg    *config.WukongConfig
	inited bool
	closed bool
}

// NewTutorialToolSet creates the tutorial tool set.
func NewTutorialToolSet(cfg *config.WukongConfig) *TutorialToolSet {
	ts := &TutorialToolSet{cfg: cfg}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.startTutorial,
			function.WithName("tutorial_start"),
			function.WithDescription(
				"Start an interactive tutorial on a given "+
					"topic. Returns the tutorial content and steps.",
			),
		),
		function.NewFunctionTool(
			ts.listTutorials,
			function.WithName("tutorial_list"),
			function.WithDescription(
				"List all available tutorials.",
			),
		),
		function.NewFunctionTool(
			ts.getTutorialStep,
			function.WithName("tutorial_step"),
			function.WithDescription(
				"Get a specific step of a tutorial.",
			),
		),
	}
	return ts
}

// Tools returns the tutorial tools.
func (ts *TutorialToolSet) Tools(ctx context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *TutorialToolSet) Name() string {
	return "tutorial"
}

// Init initializes the tool set.
func (ts *TutorialToolSet) Init(ctx context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *TutorialToolSet) Close() error {
	ts.closed = true
	return nil
}

// TutorialStartReq is the input for starting a tutorial.
type TutorialStartReq struct {
	Topic string `json:"topic" jsonschema:"description=Tutorial topic: git, docker, go, python, rust, testing, etc."`
}

// TutorialStartRsp is the output for starting a tutorial.
type TutorialStartRsp struct {
	Success     bool     `json:"success"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Steps       []string `json:"steps,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// Built-in tutorials
var builtinTutorials = map[string]TutorialStartRsp{
	"git": {
		Success:     true,
		Title:       "Git 基础教程",
		Description: "学习 Git 版本控制的基础操作",
		Steps: []string{
			"1. 初始化仓库: git init",
			"2. 添加文件: git add <file>",
			"3. 提交更改: git commit -m 'message'",
			"4. 查看状态: git status",
			"5. 查看日志: git log",
			"6. 创建分支: git branch <name>",
			"7. 切换分支: git checkout <name>",
			"8. 合并分支: git merge <branch>",
		},
	},
	"docker": {
		Success:     true,
		Title:       "Docker 入门教程",
		Description: "学习 Docker 容器化基础",
		Steps: []string{
			"1. 拉取镜像: docker pull <image>",
			"2. 运行容器: docker run <image>",
			"3. 查看容器: docker ps",
			"4. 构建镜像: docker build -t <name> .",
			"5. Dockerfile 基础: FROM, RUN, CMD, COPY",
			"6. 数据卷: docker volume",
			"7. 网络: docker network",
			"8. Compose: docker-compose up",
		},
	},
	"go": {
		Success:     true,
		Title:       "Go 语言入门教程",
		Description: "学习 Go 编程语言基础",
		Steps: []string{
			"1. 安装 Go: go version",
			"2. 创建模块: go mod init",
			"3. Hello World: package main, func main()",
			"4. 变量与类型: var, const, := ",
			"5. 函数: func name(params) returns",
			"6. 结构体与接口: type, struct, interface",
			"7. 错误处理: if err != nil",
			"8. 并发: go, chan, select",
		},
	},
}

func (ts *TutorialToolSet) startTutorial(
	ctx context.Context, req TutorialStartReq,
) (TutorialStartRsp, error) {
	tutorial, ok := builtinTutorials[req.Topic]
	if !ok {
		// Try to find tutorial file on disk
		tutorialDir := ".wukong_tutorials"
		filePath := filepath.Join(
			tutorialDir, req.Topic+".md",
		)
		data, err := os.ReadFile(filePath)
		if err != nil {
			// Generate a generic tutorial
			return TutorialStartRsp{
				Success:     true,
				Title:       fmt.Sprintf("%s 教程", req.Topic),
				Description: fmt.Sprintf("关于 %s 的交互式教程", req.Topic),
				Steps: []string{
					fmt.Sprintf("1. 了解 %s 的基础概念", req.Topic),
					fmt.Sprintf("2. 设置 %s 开发环境", req.Topic),
					fmt.Sprintf("3. %s 核心功能实践", req.Topic),
					fmt.Sprintf("4. %s 进阶技巧", req.Topic),
					"5. 实战项目练习",
				},
			}, nil
		}
		return TutorialStartRsp{
			Success:     true,
			Title:       req.Topic,
			Description: "从文件加载的教程",
			Steps: []string{string(data)},
		}, nil
	}
	return tutorial, nil
}

// TutorialListReq is the input for listing tutorials.
type TutorialListReq struct{}

// TutorialListRsp is the output for listing tutorials.
type TutorialListRsp struct {
	Success   bool     `json:"success"`
	Tutorials []string `json:"tutorials,omitempty"`
	Count     int      `json:"count"`
}

func (ts *TutorialToolSet) listTutorials(
	ctx context.Context, req TutorialListReq,
) (TutorialListRsp, error) {
	topics := make([]string, 0, len(builtinTutorials))
	for topic := range builtinTutorials {
		topics = append(topics, topic)
	}
	return TutorialListRsp{
		Success:   true,
		Tutorials: topics,
		Count:     len(topics),
	}, nil
}

// TutorialStepReq is the input for getting a step.
type TutorialStepReq struct {
	Topic string `json:"topic" jsonschema:"description=Tutorial topic"`
	Step  int    `json:"step" jsonschema:"description=Step number (1-based)"`
}

// TutorialStepRsp is the output for getting a step.
type TutorialStepRsp struct {
	Success bool   `json:"success"`
	Content string `json:"content,omitempty"`
	Total   int    `json:"total,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *TutorialToolSet) getTutorialStep(
	ctx context.Context, req TutorialStepReq,
) (TutorialStepRsp, error) {
	tutorial, ok := builtinTutorials[req.Topic]
	if !ok {
		return TutorialStepRsp{
			Success: false,
			Error:   fmt.Sprintf("tutorial %q not found", req.Topic),
		}, nil
	}
	if req.Step < 1 || req.Step > len(tutorial.Steps) {
		return TutorialStepRsp{
			Success: false,
			Error: fmt.Sprintf(
				"step %d out of range (1-%d)",
				req.Step, len(tutorial.Steps),
			),
		}, nil
	}
	return TutorialStepRsp{
		Success: true,
		Content: tutorial.Steps[req.Step-1],
		Total:   len(tutorial.Steps),
	}, nil
}
