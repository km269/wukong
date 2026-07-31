# Wukong 部署运维指南

> 本指南提供 Wukong 的安装、配置、部署、监控和运维的完整说明。

---

## 目录

1. [安装方式](#1-安装方式)
2. [配置详解](#2-配置详解)
3. [部署方案](#3-部署方案)
4. [监控告警](#4-监控告警)
5. [性能调优](#5-性能调优)
6. [故障排查](#6-故障排查)
7. [备份恢复](#7-备份恢复)
8. [安全加固](#8-安全加固)

---

## 1. 安装方式

### 1.1 预编译二进制

**Linux/macOS**:
```bash
# 下载最新版本
wget https://github.com/km269/wukong/releases/latest/download/wukong-linux-amd64
chmod +x wukong-linux-amd64
sudo mv wukong-linux-amd64 /usr/local/bin/wukong

# 验证安装
wukong version
```

### 1.2 从源码构建

```bash
# 克隆仓库
git clone https://github.com/km269/wukong.git
cd wukong

# 构建
go build -o wukong ./cmd/wukong
```

### 1.3 Docker 安装

```bash
# 拉取镜像
docker pull km269/wukong:latest

# 运行容器
docker run -d \
  --name wukong \
  -p 9090:9090 \
  -v ~/.wukong:/root/.wukong \
  km269/wukong:latest \
  wukong server
```

### 1.4 Homebrew (macOS)

```bash
brew tap km269/tap
brew install wukong
```

---

## 2. 配置详解

### 2.1 配置文件位置

配置文件加载优先级：
1. CLI 标志 (`--config`)
2. 环境变量 (`WUKONG_*`)
3. `./config.yaml`
4. `~/.config/wukong/config.yaml`
5. `/etc/wukong/config.yaml`
6. 内置默认值

### 2.2 核心配置

```yaml
# 全局设置
log_level: "info"
default_provider: "openai"

# Provider 配置
providers:
  - name: "openai"
    type: "openai"
    api_key: "${OPENAI_API_KEY}"
    model: "gpt-4o"

# Agent 配置
agent:
  max_llm_calls: 50
  temperature: 0.7
  streaming: true

# 安全配置
security:
  permission_mode: "smart"
  block_dangerous_commands: true

# 记忆配置
memory:
  auto_extract: true
  max_memories: 1000
```

### 2.3 环境变量

```bash
# 日志级别
export WUKONG_LOG_LEVEL=debug

# API Keys
export OPENAI_API_KEY=sk-...
export DEEPSEEK_API_KEY=sk-...
```

---

## 3. 部署方案

### 3.1 单机部署

**Systemd 服务**:
```bash
# 1. 创建用户
sudo useradd -r -s /bin/false wukong

# 2. 安装二进制
sudo cp wukong /usr/local/bin/

# 3. 配置文件
sudo mkdir -p /etc/wukong
sudo cp config.yaml /etc/wukong/

# 4. 创建服务
sudo cat > /etc/systemd/system/wukong.service <<EOF
[Unit]
Description=Wukong AI Agent Platform

[Service]
Type=simple
User=wukong
ExecStart=/usr/local/bin/wukong server
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

# 5. 启动服务
sudo systemctl enable wukong
sudo systemctl start wukong
```

### 3.2 Docker Compose 部署

```yaml
version: '3.8'

services:
  wukong:
    image: km269/wukong:latest
    container_name: wukong
    restart: unless-stopped
    ports:
      - "9090:9090"
      - "9091:9091"
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - wukong-data:/root/.wukong
    environment:
      - WUKONG_LOG_LEVEL=info
      - OPENAI_API_KEY=${OPENAI_API_KEY}

volumes:
  wukong-data:
```

### 3.3 Kubernetes 部署

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: wukong
spec:
  replicas: 3
  selector:
    matchLabels:
      app: wukong
  template:
    spec:
      containers:
      - name: wukong
        image: km269/wukong:1.0.0
        ports:
        - containerPort: 9090
        resources:
          limits:
            cpu: "4"
            memory: "8Gi"
```

---

## 4. 监控告警

### 4.1 Prometheus 集成

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'wukong'
    static_configs:
      - targets: ['wukong:9091']
```

### 4.2 关键指标

- `wukong_llm_calls_total`: LLM 调用次数
- `wukong_tool_calls_total`: 工具调用次数
- `wukong_request_duration_seconds`: 请求延迟
- `wukong_errors_total`: 错误率

### 4.3 告警规则

```yaml
groups:
  - name: wukong
    rules:
      - alert: HighErrorRate
        expr: rate(wukong_errors_total[5m]) > 0.05
        for: 5m
        labels:
          severity: warning
```

---

## 5. 性能调优

### 5.1 数据库优化

```yaml
# 启用 WAL 模式
session:
  db_path: "wukong.db"
  wal_mode: true
```

### 5.2 并发调优

```yaml
agent:
  parallel_tools: true
  max_llm_calls: 100

browser:
  workers: 8
```

### 5.3 内存优化

```bash
# 设置内存限制
export GOMEMLIMIT=8GiB
export GOMAXPROCS=4
```

---

## 6. 故障排查

### 6.1 常见问题

**无法连接 LLM Provider**:
```bash
# 检查网络
curl -I https://api.openai.com

# 验证配置
wukong config validate

# 查看日志
journalctl -u wukong -f
```

**内存不足**:
```bash
# 检查内存使用
wukong stats

# 减少并发
# 修改配置: browser.workers: 2
```

### 6.2 诊断工具

```bash
# 健康检查
wukong health

# 配置验证
wukong config validate

# 性能分析
wukong profile cpu --duration 30s
```

---

## 7. 备份恢复

### 7.1 数据备份

```bash
#!/bin/bash
# 备份数据库
cp /var/lib/wukong/wukong.db /backup/wukong_$(date +%Y%m%d).db

# 备份配置
cp /etc/wukong/config.yaml /backup/config_$(date +%Y%m%d).yaml
```

### 7.2 数据恢复

```bash
# 停止服务
sudo systemctl stop wukong

# 恢复数据
cp /backup/wukong_20260730.db /var/lib/wukong/wukong.db

# 启动服务
sudo systemctl start wukong
```

---

## 8. 安全加固

### 8.1 系统安全

```bash
# 创建专用用户
sudo useradd -r -s /bin/false wukong

# 限制文件访问
sudo chmod 700 /var/lib/wukong
sudo chmod 600 /etc/wukong/config.yaml

# 防火墙配置
sudo ufw allow 9090/tcp
sudo ufw enable
```

### 8.2 API 安全

```yaml
# 使用环境变量管理 API Key
providers:
  - name: "openai"
    api_key: "${OPENAI_API_KEY}"
```

---

## 运维最佳实践

### 日常检查清单

**每日**:
- 检查服务状态
- 查看错误日志
- 检查磁盘空间

**每周**:
- 检查备份完整性
- 查看性能指标
- 清理临时文件

**每月**:
- 更新依赖
- 审查安全配置
- 性能调优

### 故障恢复流程

1. 检查服务状态
2. 查看日志定位问题
3. 尝试重启服务
4. 必要时恢复备份
5. 记录问题和解决方案

---

**相关文档**:
- [系统概述](../SYSTEM_OVERVIEW.md)
- [技术架构](ARCHITECTURE.md)
- [开发者指南](DEVELOPER_GUIDE.md)
