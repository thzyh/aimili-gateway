# 项目更新器 v2：过渡发布核验

## 本轮已完成

- 本地提交 `3344a08d5a326cb6006cfe18e4c17e83619aa0ad` 已推送至 `origin/main`。公开 Release `v0.2.14-vps` 指向同一提交，属于契约 1 过渡版。
- 远端 `manifest.json`、`manifest.sig`、`aimili-vps-package.tar.gz`、`project-update-engine.py` 均为 `uploaded`；远端清单与签名重新下载后通过 Ed25519 验证，包与引擎摘要与签名清单一致。3x-ui 清单指向既有 `v0.2.5-vps` 资产，其远端 SHA256 与本地清单一致。
- 包内 Linux Gateway 执行 `version --json` 报告 `v0.2.14-vps` 及上述提交。归档包含引导器、更新引擎和首次部署入口。公开 Release 后，新引擎实际查询 GitHub API 并验签，得到 `v0.2.14-vps`。
- Go 全包测试通过；前端 87 项测试与生产构建通过；Python 更新器 10 项测试通过，包含双槽切换、坏引擎回退、过渡版接管和阻止项/升级路径。WSL Ubuntu 的 Python 测试通过，OpenSSL 1.1 不支持 `-rawin`，因此该环境跳过签名测试；Windows Git OpenSSL 3 上真实签名测试通过。安装 Shell 与 PowerShell 构建脚本语法检查通过。
- jjs 只读核对仍为 `v0.2.13-vps`，Ubuntu 24.04/x86_64，Gateway、AimiliVPN、x-ui、Caddy 均 active。本轮没有向 jjs 提交检测或安装请求，正式网页验收留给用户。

## 尚未验证与发布纪律

- 尚未在真实 VPS 上执行 `v0.2.13-vps → v0.2.14-vps` apply，故不能宣称旧 worker 在生产中接管双槽的全过程已通过。bj 当前 Gateway 版本为 `dev` 且没有项目更新入口，不能直接作为该路径的对照环境。
- Caddy、Xray、OpenVPN 和 systemd 配置替换仍在当前执行器的阻止矩阵内；发布包如要求修改这些组件，页面必须给出组件、阻止原因及[升级路径](../upgrade.md)，不得强行执行。
- 新项目发布应使用 `project-vX.Y.Z-vps` 标签。旧 worker 只读取 GitHub 最近 100 个 Release，须在过渡版滑出列表前发布新的安全过渡版；不能依赖旧机自动理解未来的签名契约。
