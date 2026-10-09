# multica-notify

自托管 [Multica](https://github.com/multica-ai/multica) 的事件通知桥——接收签名的插件 hook 事件，扇出到 Apprise、ntfy 或任意 webhook。

[![CI](https://github.com/bg9ezn/multica-notify/actions/workflows/ci.yml/badge.svg)](./actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8)](./go.mod)

> **非官方社区项目**——与 Multica AI / Index Labs 无隶属关系。完全基于 Multica 已发布的插件契约（`plugincontract`）构建。

English documentation: [README.md](./README.md)

## 为什么需要它

Multica 的收件箱只覆盖站内通知，聊天集成是对话式的——两者都不会把「issue X 待审核」「task Y 失败」主动推到你的手机或群聊。这个桥用官方插件机制补上这一环：

```
Multica（插件 hook，HMAC 签名 POST）
   │  issue.status_changed / task.completed / task.failed / 每日心跳
   ▼
multica-notify 桥（Go 单二进制）
   ├─ 验签     （HMAC-SHA256，±5 分钟窗口，常量时间比较，防重放）
   ├─ 过滤     （只报你关心的状态；跳过重试中的失败）
   ├─ 去抖     （同一 issue 快速翻转只发一条最新态）
   ├─ 去重     （delivery_id 日志，重启后仍然有效）
   └─ 扇出     （通道间独立失败、互不影响）
        ├─ apprise  → apprise-api → 企微 / 钉钉 / 飞书 / Telegram / 邮件 / …
        ├─ ntfy     → 手机直推（走你自己的服务器）
        └─ webhook  → 任何能收 JSON 的地方
```

桥刻意只说这三种协议。渠道细节（企微机器人 key、钉钉加签……）全部留在 Apprise——增加渠道永远不需要改这个仓库的代码。

## 快速开始

```bash
# 1. 构建（Go 1.22+）
make build

# 2. 最小配置（完整注释版见 deploy/examples/config.example.yaml）
cat > config.yaml <<'EOF'
listen: "127.0.0.1:9097"
idempotency_journal: data/journal.jsonl
channels:
  - name: phone
    type: ntfy
    options:
      server: http://127.0.0.1:8086   # 自托管 ntfy
      topic: multica
EOF

# 3. 启动（先跑通 HTTP；生产必须 HTTPS，见下文）
MULTICA_NOTIFY_SIGNING_SECRET=whsec_... ./bin/multica-notify -config config.yaml

# 4. 不动 Multica 就能发测试投递
go run ./cmd/mocksender -url http://127.0.0.1:9097/hooks/issue-status \
  -secret whsec_... -number 42 -title "试试" -status in_review
```

`/healthz` 返回 `ok`，供探针使用。

## 接入自托管 Multica

三个服务端前置条件，全部由运营者控制：

1. **开启插件功能**（默认关闭）：让 `MULTICA_FEATURE_FLAGS_FILE` 指向包含 `plugins_v1: {default: true}` 的 YAML 文件，重启后端；
2. **放行内网 hook 来源**：Multica 默认拒绝拨私网地址（SSRF 守卫）且强制 HTTPS。用 [`deploy/tls/gen-certs.sh`](./deploy/tls/gen-certs.sh) 生成本地 CA 和服务证书，在后端 env 设置：
   ```dotenv
   MULTICA_PLUGIN_DEV_ORIGINS=https://<桥host>:9097
   MULTICA_PLUGIN_DEV_CA=/path/to/ca.crt
   ```
3. **发布并安装插件**：上传 bundle（`POST /plugins/packages`，管理员），在工作区安装（预览 → 同意 → 安装）。使用 [`manifest/multica.plugin.json`](./manifest/multica.plugin.json)，把 `BRIDGE_HOST_PLACEHOLDER` 换成你的桥地址。轮换插件令牌时会一次性显示签名密钥——写入 `/etc/multica-notify/env` 的 `MULTICA_NOTIFY_SIGNING_SECRET`。

完整操作手册（含 flag 文件格式与 API 调用）见 [`docs/deploy-selfhost.md`](./docs/deploy-selfhost.md)。

## 配置

见 [`deploy/examples/config.example.yaml`](./deploy/examples/config.example.yaml)，每个字段都有注释。要点：

| 字段 | 默认 | 含义 |
|---|---|---|
| `enabled` | `true` | **总开关**。`false` = 全局静音：投递照常接收并记账，但抑制全部扇出（SIGHUP 热生效） |
| `listen` | `:9097` | hook 服务地址 |
| `tls` | — | 证书/私钥；生产必填（Multica 只接受 https transport） |
| `filters.issue_statuses` | `[in_review, done]` | 哪些 issue 状态触发通知；空 = 全部 |
| `filters.on_task_failed` | `true` | 终态任务失败时通知 |
| `filters.skip_retrying_tasks` | `true` | 丢弃 Multica 标记 `retry_pending` 的中间失败 |
| `debounce.window` | `30s` | 同一 issue 的快速翻转合并为最新态 |
| `idempotency_journal` | `data/journal.jsonl` | 投递 id 日志（至多一次语义） |
| `channels[]` | — | `apprise` / `ntfy` / `webhook` |

各通道选项见示例配置；未知选项被忽略，未知通道类型启动即报错并列出可用类型。

## 安全模型

hook 契约的四条纪律，全部实现在 [`internal/hookserver/verify.go`](./internal/hookserver/verify.go)：

1. 对**原始字节**验签，先验后解析；
2. 时间戳超出 **±5 分钟**窗口即拒绝；
3. **常量时间**比较签名；
4. 窗口内记忆已接受的签名——补上宿主无法替你补上的重放缺口。

桥只持有签名密钥，只拨出你配置的通道，只存投递 id。issue 标题会随通知发出——请据此选择通道（自托管 ntfy 可让内容不出内网）。

## 开发

```bash
make help             # 列出全部目标
make clean build test # 一条命令：清理、构建、lint、单元测试（含 race）
make test-integration # docker compose 拉真 ntfy + 签名投递的集成测试
make package          # linux/arm64 + amd64 产物到 dist/ + sha256
```

目录（依赖单向；接口只在 `channel` 边界）：

```
cmd/{multica-notify,mocksender}
internal/config      YAML 加载/校验/热加载
internal/hookserver  HTTPS + 验签 + hook 路由 + 发送池
internal/event       解码、过滤、去抖、journal
internal/message     事件 → 标题/正文模板
internal/channel     Channel 接口 + 注册表 + apprise/ntfy/webhook
internal/httpx       共享的带重试 JSON POST
```

单元测试覆盖拒绝矩阵、去抖语义、journal 重启恢复、每个适配器对 httptest 接收端的行为；集成测试对真实 ntfy 跑完整管线（含重放拒绝、apprise 宕机时的通道隔离）。

## 部署

- [`deploy/systemd/multica-notify.service`](./deploy/systemd/multica-notify.service) — 加固过的 systemd 单元
- [`deploy/compose/deploy.yml`](./deploy/compose/deploy.yml) — 可选的 apprise-api + ntfy（有 Docker 的机器）
- [`deploy/tls/gen-certs.sh`](./deploy/tls/gen-certs.sh) — 内网来源所需的本地 CA + 服务证书
- [`docs/deploy-selfhost.md`](./docs/deploy-selfhost.md) — 端到端操作手册

## 发布产物

每个 tag 产出每个平台一个静态链接二进制 + `sha256sums.txt`。命名规则：`multica-notify-<版本>-<系统>-<架构>[.exe]`。

| 产物 | 适用 |
|---|---|
| `...-linux-amd64` | 64 位 x86 Linux（服务器、PC、NAS） |
| `...-linux-arm64` | 64 位 ARM Linux——**树莓派 3/4/5**、云上 ARM 实例 |
| `...-linux-armv6` | 32 位 ARM Linux——树莓派 Zero/1、32 位系统的 Pi 2/3（一个包覆盖 ARMv6 至 ARMv8 32 位模式） |
| `...-linux-loong64` | 龙芯（LoongArch64）Linux |
| `...-linux-riscv64` | RISC-V Linux（昉·星光 2、Lichee Pi 4A 等） |
| `...-windows-amd64.exe` | 64 位 Windows |
| `...-windows-arm64.exe` | Windows on ARM |
| `...-darwin-amd64` | Intel Mac |
| `...-darwin-arm64` | Apple Silicon Mac |

运行前校验：`sha256sum -c sha256sums.txt`。二进制为静态链接（CGO 关闭）——下载、`chmod +x`、直接运行。darwin 构建未签名：首次运行需 `xattr -d com.apple.quarantine <binary>`。

## 相关项目

| 项目 | 关系 |
|---|---|
| [Multica](https://github.com/multica-ai/multica) | 本桥所扩展的自托管智能体平台。这里实现的 hook 契约即 Multica 已发布的插件契约；参见上游 [RFC #1964](https://github.com/multica-ai/multica/issues/1964)（外发 webhook）与官方 `triage-notify` 示例——本项目的签名处理以该示例为基准。 |
| [Apprise](https://github.com/caronc/apprise) / [apprise-api](https://github.com/caronc/apprise-api) | 128+ 服务的扇出层（企微、钉钉、飞书、Telegram、邮件……）。桥走 apprise-api 的 HTTP 协议。（[Shoutrrr](https://github.com/containrrr/shoutrrr) 是本生态中的 Go 库对应物——仅有库/CLI 形态、无 HTTP API，因此不作为本桥的集成路径。） |
| [ntfy](https://github.com/binwiederhier/ntfy) | 自托管 HTTP 手机推送——推荐的个人通道。同类可选：[Gotify](https://github.com/gotify/server)。 |

## 版本规则

SemVer（`v主.次.补`），`v` 前缀打 tag；每个 tag 自动触发全平台（Makefile 的 `PLATFORMS`）产物发布。

- **补丁位（PATCH）**——向后兼容的 bug 修复、测试、文档、构建修正；不新增配置面、不改行为。
- **次版本（MINOR）**——新功能或新配置面（通道、开关、平台）；0.x 阶段的常态。
- **主版本（MAJOR）**——破坏性变更（配置结构、hook 契约用法）；1.0 之前预计不会出现。

## 许可证

[Apache-2.0](./LICENSE)。欢迎贡献——提交请加签署（`git commit -s`），CI 强制 DCO。
