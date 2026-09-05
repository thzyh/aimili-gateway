# Aimili Gateway 当前状态交接

日期：2026-09-05（Asia/Shanghai）

状态：当前权威入口

## 1. 使用与优先级

继续 Aimili Gateway 任务时先读本文，再按任务范围读取对应设计、计划、运行手册和验证记录。历史文档保留其发生时的事实，不以批量改写方式伪装成当前状态。

发生冲突时按以下顺序判断：

1. 本轮最新只读检查得到的代码、Git 和生产事实；
2. 本文；
3. 最新专题验证记录；
4. 历史交接、实施计划和设计快照。

任何生产写入前仍须重新核对目标文件、服务、数据库和回滚条件。本文不是跳过部署安全门的授权。

## 2. 当前本地路径

| 项目 | 当前路径 | 分支 |
| --- | --- | --- |
| Aimili Gateway | `D:\CodexProject\Github\aimili-gateway\.worktrees\main-switch-protocol-modes` | `feat/main-switch-protocol-modes` |
| AimiliVPN | `D:\CodexProject\Github\aimili-vpngate\.worktrees\main-switch-protocol-modes` | `feat/main-switch-protocol-modes` |
| 3x-ui 补丁与部署 | `D:\CodexProject\Github\aimili-3xui-deploy\.worktrees\main-switch-protocol-modes` | `feat/main-switch-protocol-modes` |
| 历史简化部署资料 | `D:\CodexProject\Github\aimili-3xui-simple-deploy` | 历史资料 |

旧任务输入曾使用 `D:\CodexProject\Github\aimili-gateway.worktrees\main-switch-protocol-modes`。该同级目录当前不存在，且仓库文档没有使用它；正确结构是仓库内部的 `.worktrees\main-switch-protocol-modes`。

## 3. 当前 Git 检查点

本节来自 2026-09-05 本地只读检查。ahead 数量包含本次交接文档提交，基于当前本地远程跟踪引用；本次文档整理没有重新 `fetch`，不得把它表述为 GitHub 当前在线状态。

| 仓库 | 最新业务代码基线 | 工作区 | 本地相对跟踪分支 |
| --- | --- | --- | --- |
| Aimili Gateway | `43b63a4` | 已跟踪文件干净；保留既有未跟踪构建、缓存和 `.deploy-assets/` | ahead 5，其中 4 个业务代码提交、1 个本文档提交 |
| AimiliVPN | `88be2fb` | 干净 | ahead 2 |
| 3x-ui 补丁与部署 | `5dbe6f0` | 干净；该功能分支没有远程 | 不适用 |

Gateway 尚未推送的四个最新业务代码提交：

- `ffe1a36 fix: restore managed gateway resources`
- `8c84fdb fix: reclaim x-ui auto-tagged gateway inbound`
- `de0bdab fix: preserve and recover gateway subscription`
- `43b63a4 fix: persist dismissed refresh notices`

AimiliVPN 尚未推送的两个最新提交：

- `d662e85 fix: raise isolated proxy connection capacity`
- `88be2fb fix: normalize managed slot country names`

不得改写 AimiliVPN 已有提交历史。推送前重新运行受影响验证、`git diff --check` 和 `git fetch` 后比较。

## 4. 当前生产摘要

2026-09-05 01:50 UTC 的最新只读检查确认：

- `aimili-gateway`、`aimilivpn`、`x-ui`、`caddy` 四项服务均为 `active`；
- Gateway 数据库 `PRAGMA quick_check=ok`；
- 稳态为 4 个 OpenVPN、1 个 Xray；
- 四条协议状态均为 `ready`；主连接当前为 XHTTP/REALITY，三个普通出口合计为两个 TCP/Vision 和一个 Hysteria2/QUIC/TLS；本次没有执行会改变协议的操作；
- 根分区使用率 51%，可用 `4,559,163,392` 字节；
- `/var/backups/aimili-gateway` 顶层只保留 `20260905-refresh-notice` 一套回滚资产；
- 用户已人工反馈当前 v2rayN 测试没有问题。

本次文档整理没有重新执行真实四出口流量、订阅导入、SOCKS5H 或协议切换测试；不得用上述只读摘要替代专题验收记录。

## 5. 已完成的最新修复

- 普通替换失败、候选不可达与 pending 槽位恢复不再伪造无用的 `repair_required`；真正运行态不确定时仍保留该安全状态和恢复入口。
- 3x-ui Gateway 受管资源恢复、自动标签资源认领和订阅关联恢复已完成并部署。
- 节点国家名称已规范化，重复越南等同义国家显示已修复。
- 刷新结果通知关闭后，同一结果在页面刷新时不再反复出现；新一轮刷新结果仍会显示。
- v2rayN 服务端订阅与数据面根因修复后，用户已确认当前测试正常；没有采用客户端补丁替代服务端修复。
- VPS 历史备份与临时资产已经按绝对路径清理，当前只保留一套最近回滚资产。

## 6. 当前未实现或未执行

- Gateway 的“外部静态资源免重启发布”与“后端一键安全升级”目前只是架构建议，尚未设计、编码或部署。
- Gateway 与 AimiliVPN 最新本地提交尚未推送 GitHub。
- 本次文档整理没有创建新备份、删除文件、部署服务或修改生产数据库。

## 7. 前端与后端发布边界

- 纯 UI 免重启：内嵌前端保底，外部静态资源按版本目录发布，校验后原子切换 `current`，失败切回上一版。
- 后端一键升级：下载已签名的预编译 Gateway 二进制，由权限封闭的 root helper 原子替换，短暂重启 Gateway，并执行健康门和单一回滚。
- 两者合称“两层发布机制”。第一层是第二层的一部分，而不是两个互斥方案。
- 在该机制正式实现前，当前 `go:embed` 架构下的任何 UI 修改仍须重新构建并部署 Gateway 二进制。
