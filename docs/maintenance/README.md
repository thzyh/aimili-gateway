# 生产维护记录

本目录记录 Aimili Gateway、AimiliVPN、3x-ui/Xray 在生产服务器上的历史资源、保留理由、可清理边界和回滚要求。这里的记录不包含密码、Cookie、UUID、私钥或完整代理地址。

## 重要文件

- `2026-08-29-history-residuals-maintenance.md`：第 4 主连接、Test 风格订阅及旧聚合清理后的历史残留维护基线。
- `../../scripts/audit-history-remote.sh`：只读审计，输出脱敏结构化清单。
- `../../scripts/cleanup-history-remote.sh`：默认 dry-run；只有 `--apply` 会备份并删除已失去入站引用的孤儿客户端。

## 使用与验证

```bash
sudo bash scripts/audit-history-remote.sh --audit
sudo bash scripts/cleanup-history-remote.sh --dry-run
sudo bash scripts/cleanup-history-remote.sh --apply
sudo bash scripts/audit-history-remote.sh --audit
```

执行 `--apply` 前必须保存最新审计输出。执行后必须核对原 `8443`、三个 `agw-*` 双协议组、四入站 VLESS 订阅、服务状态、内存和 OOM 记录。脚本不会删除非受管入站、AimiliVPN stash、systemd 单元、旧数据库或生产备份；这些内容需要按照维护记录逐项证明无依赖后才可另行处理。

## 限制

- 生产事实以最新一次服务器审计和端到端验证记录为准；本文档不是实时状态接口。
- 不以文件名、数量或创建时间单独判定资源无用。
- 至少保留一个已验证可工作的 Gateway、3x-ui、AimiliVPN 和 Caddy 联合回滚点。
