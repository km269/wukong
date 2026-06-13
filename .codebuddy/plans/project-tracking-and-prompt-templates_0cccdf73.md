---
name: project-tracking-and-prompt-templates
overview: 新增两个功能：1) 自动追踪工作目录+会话恢复（project 子命令）；2) 自定义系统提示词模板目录（加载 .wukong_templates/ 下的 .md 文件注入系统指令）。
---

<xml>
<plan_result>
<req>

## 功能一：自动追踪工作目录 + 会话恢复

### 需求描述

每调用一次 `wukong session`，自动将当前工作目录记录为"项目"，关联 session ID。支持 `wukong project` 和 `wukong projects` 两个子命令来恢复/浏览项目。

### 核心功能

- **自动记录**：每次启动 `wukong session` 时，获取 `os.Getwd()` 作为工作目录，与 UUID sessionID 关联，存入 `~/.config/wukong/projects.json`。
- **项目恢复**：`wukong project` 列出最近项目列表（路径、最后访问时间、最后指令），用户选择后可恢复之前会话（沿用旧 sessionID）或在该目录开启新会话。
- **项目列表**：`wukong projects` 以表格形式展示所有已记录项目，显示路径、最后访问时间、最后指令摘要。
- **数据持久化**：`projects.json` 格式为 `[{path, session_id, last_accessed, last_instruction}]`，按最后访问时间排序，最多保留 50 条。
- **CLI 注册**：在 `internal/cli/root.go` 中注册 `projectCmd` 和 `projectsCmd` 两个子命令。

## 功能二：自定义系统提示词模板目录

### 需求描述

支持从指定目录加载多个 `.md` 提示词模板文件，在构建 System Instruction 时按顺序拼接，替代 / 增强当前硬编码的 `buildSystemInstruction()`。

### 核心功能

- **模板目录**：配置项 `system_prompt_dir`，默认 `~/.config/wukong/prompts/`，自动创建。
- **模板加载**：启动时扫描目录中所有 `.md` 文件，按文件名字典序排序，读取内容拼接为完整系统提示词。
- **模板变量**：支持 `{{.WorkingDir}}`、`{{.ModelName}}`、`{{.ProviderName}}`、`{{.SessionID}}` 等运行时变量替换。
- **优先级**：模板目录有文件时，完全替换硬编码的 `buildBaseInstruction()`；模板目录为空时，回退到硬编码默认值。
- **配置驱动**：在 `config.yaml` 和 `AgentConfig` 中新增 `system_prompt_dir` 字段和默认值。
- **与 TopOfMind 协作**：模板内容先于 TopOfMind 指令注入，最后是 Memory/Session/Recall 上下文。
</req>

<tech>

## 技术栈

- Go 1.26 + tRPC-Agent-Go v1.10.0
- 项目选型：纯 Go 标准库实现，不引入额外依赖
- 配置驱动：Viper + YAML + `mapstructure` tags

## 实现方案

### 功能一：项目管理

**新增文件**：`internal/project/manager.go`

**设计要点**：

1. `ProjectManager` 结构体持有 `[]ProjectRecord` 切片 + `sync.RWMutex` + 文件路径
2. `ProjectRecord` 结构体包含 `Path`, `SessionID`, `LastAccessed`, `LastInstruction`
3. 原子写入：`save()` 方法使用先写临时文件 + `os.Rename` 确保写入安全
4. 上限保护：`maxProjects = 50`，超出时自动删除最旧条目
5. `TrackProject(dir, sessionID, instruction)` 方法被 `bootstrapSession` 调用
6. `ListProjects()` 返回最近项目列表，供 `project` / `projects` 子命令消费
7. `runSession` 在开始前调用 `TrackProject`，在用户输入第一句话时更新 `LastInstruction`

**新增文件**：`internal/cli/project.go`

- `newProjectsCmd()` — `wukong projects` 表格列出所有项目
- `newProjectCmd()` — `wukong project` 交互式选择项目，支持"沿用旧 sessionID"或"新会话"

### 功能二：提示词模板

**新增文件**：`internal/agent/prompt_template.go`

**设计要点**：

1. `PromptTemplateManager` 负责加载 `.md` 模板文件
2. `LoadTemplates(dir string) (string, error)` 扫描目录、读取文件、拼接内容、返回完整提示词
3. 变量替换：`os.Expand()` 风格，支持 `{{.WorkingDir}}`, `{{.ModelName}}`, `{{.ProviderName}}`, `{{.SessionID}}`
4. 缓存机制：仅在启动时加载一次，会话期间不变
5. 空目录回退：模板目录无文件时，返回空字符串，调用方使用硬编码默认值

**修改文件**：`internal/agent/loop.go` 和 `internal/agent/workflow.go`

- `buildSystemInstruction()` 和 `buildBaseInstruction()` 优先从模板加载
- 模板为空时回退到硬编码默认值
- 模板内容放在最前面，后续拼接 TopOfMind + Memory + SessionRecall

### 配置变更

`internal/config/config.go`：

- `WukongConfig` 新增 `ProjectDir string` 字段（`mapstructure:"project_dir"`），默认 `~/.config/wukong/projects.json`
- `AgentConfig` 新增 `SystemPromptDir string` 字段（`mapstructure:"system_prompt_dir"`），默认 `~/.config/wukong/prompts/`

`config.yaml`：

- 新增 `project_dir` 和 `agent.system_prompt_dir` 配置项

### 架构设计

```
新增命令层:
  wukong project          → cli/project.go → project.Manager.ListProjects() → 交互选择
  wukong projects         → cli/project.go → project.Manager.ListProjects() → 表格输出

修改会话层:
  session.go::runSession()
    → projectMgr.TrackProject(workingDir, sessionID, "")  // 会话开始时记录
    → bootstrapSession(...)
    → startupSummary 增加 [Project: /path/to/dir] 显示

修改引擎层:
  loop.go::buildSystemInstruction()
    → promptTemplateMgr.LoadTemplates(dir)  // 优先从文件加载
    → 回退到硬编码默认值
    → 拼接 TopOfMind + Memory + SessionRecall
```

### 关键设计决策

1. **工作目录记录时机**：在 `bootstrapSession()` 之前记录，确保即使后续初始化失败，项目记录也不会丢失。
2. **模板与默认值关系**：模板目录有文件时完全替换默认值（而非追加），确保用户完全控制提示词内容。
3. **项目文件路径**：使用 `config.ResolvePath` 统一路径解析，支持相对路径展开。
4. **线程安全**：`ProjectManager` 使用 `sync.RWMutex` 保护并发读写。
5. **向后兼容**：无 `--project-dir` 时使用默认路径，无模板目录时回退硬编码，不改变现有行为。

### 目录结构

```
wukong/
├── internal/
│   ├── project/
│   │   └── manager.go        # [NEW] 项目管理器：Track/List/Resume
│   ├── agent/
│   │   ├── prompt_template.go # [NEW] 提示词模板管理器：Load/Render
│   │   ├── loop.go            # [MODIFY] buildSystemInstruction 优先模板加载
│   │   └── workflow.go        # [MODIFY] buildBaseInstruction 优先模板加载
│   ├── cli/
│   │   ├── project.go         # [NEW] wukong project 和 projects 子命令
│   │   ├── root.go            # [MODIFY] 注册 project/projects 子命令
│   │   └── session.go         # [MODIFY] TrackProject 调用 + 显示工作目录
│   └── config/
│       └── config.go          # [MODIFY] ProjectDir + SystemPromptDir 字段和默认值
├── config.yaml                # [MODIFY] project_dir + system_prompt_dir 配置项
└── ~/.config/wukong/
    ├── projects.json          # [NEW] 运行时生成的项目记录
    └── prompts/               # [NEW] 用户创建的模板目录
        ├── base.md
        └── coding.md
```

</tech>

<todolist>
<item id="p1-config" deps="">在 config.go 中新增 ProjectDir 和 SystemPromptDir 字段及默认值，更新 config.yaml 配置项</item>
<item id="p2-project-manager" deps="p1-config">创建 internal/project/manager.go：实现 ProjectRecord 结构体、TrackProject、ListProjects、原子写入/读取 projects.json</item>
<item id="p3-prompt-template" deps="p1-config">创建 internal/agent/prompt_template.go：实现 LoadTemplates 扫描 .md 文件、变量替换 {{.WorkingDir}} 等、空目录回退逻辑</item>
<item id="p4-cli-project" deps="p2-project-manager">创建 internal/cli/project.go：实现 wukong project（交互恢复/新会话）和 wukong projects（表格展示）子命令，在 root.go 中注册</item>
<item id="p5-integrate-session" deps="p2-project-manager">修改 session.go：bootstrapSession 前调用 TrackProject 记录工作目录、startupSummary 显示项目路径、首次用户消息更新 LastInstruction</item>
<item id="p6-integrate-instruction" deps="p3-prompt-template">修改 loop.go 和 workflow.go：buildSystemInstruction 和 buildBaseInstruction 优先从模板加载，回退硬编码默认值</item>
<item id="p7-build-test" deps="p6-integrate-instruction">编译验证并创建默认提示词模板示例文件</item>
</todolist>
</plan_result>
</xml>