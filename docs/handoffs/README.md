# 任务交接文档

本目录保存 Aimili Gateway 跨任务窗口继续开发时使用的精简交接资料。交接文档只记录当前有效目标、已验证状态、约束、风险和下一任务入口，不替代正式设计、实施计划或验证记录。

## 当前入口

- `2026-08-29-main-switch-protocol-modes-handoff.md`：主连接安全切换与每出口独立协议模式交接。

## 使用方法

新任务应先完整阅读对应交接文档，再重新只读核对本地仓库与生产环境。实际状态与交接快照冲突时，以新任务的最新检查结果为准。

## 验证与限制

- 提交前执行 `git diff --check`。
- 交接文档不得包含密码、Cookie、UUID、私钥、随机后台路径或完整订阅链接。
- 正式设计、实施计划和生产验收分别存放在 `docs/superpowers/specs`、`docs/superpowers/plans` 和 `docs/verification`。
