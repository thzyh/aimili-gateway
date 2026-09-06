# 本机原生部署资产

这些脚本在 VM 内执行，属于 Aimili Gateway 本机原生 systemd 部署的受限基础层。

- `deployment.json`：当前部署清单。数量是期望值，不是永久上限。
- `guest-network-preflight.sh`：只读验证路由、上游 TCP、DNS、HTTPS 和 UFW 出站。
- `stage.sh`：把已经校验的构建资产原子放入受限 staging run 目录。
- `backup.sh`：为单个组件创建带文件清单的唯一备份。
- `rollback.sh`：从明确 run ID 恢复单个组件，并保留一个精确的 `.previous` 目录。
- `verify-native.sh --json`：读取清单和脱敏验收证据，统一验证 systemd、TUN、路由、监听、真实出口、订阅覆盖、协议隔离与宿主不变性。

`verify-native.sh` 的 `--evidence` JSON 使用 `schemaVersion: 1`，只保存可公开比较的逻辑出口名（`main`、`slot-N`）、公网协议/端口、mixed 端口、订阅是否误含 mixed 的布尔值，以及宿主进程号列表和代理/默认路由的 SHA-256 摘要。不得写入订阅 URL、认证材料、UUID、节点 IP 或随机后台路径。验证结果只返回布尔判定和数量，不回显输入证据。

脚本不负责连接 ny，不接受生产路径作为隐式目标，也不安装 Docker。调用方必须传入绝对路径和显式 run ID；所有目录在执行前都要做路径包含校验。
