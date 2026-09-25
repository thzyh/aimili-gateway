# VPS 项目更新发布

`build-release.ps1` 生成完整签名包。首次升级旧 `v0.2.13-vps` 时，使用一次 `-BridgeRelease`，发布标签为 `v0.2.14-vps`，清单契约仍为 1。此后不再发布新的普通 `vX.Y.Z-vps` 完整项目版本；默认构建使用 `project-vX.Y.Z-vps` 标签、契约 2。旧更新器只看到过渡版，新的更新器会同时识别两类标签。

每次发布前先完成受影响模块测试、Linux 构建与签名资产检查，确认仓库提交就是包中 `gatewayCommit`。构建输出目录必须为空，私钥仅由发布机提供且不进入仓库。示例：

```powershell
./deploy/vps/build-release.ps1 -Tag v0.2.14-vps -BridgeRelease -Output D:\release-v0.2.14
```

发布时上传 `manifest.json`、`manifest.sig`、`aimili-vps-package.tar.gz` 和 `project-update-engine.py`。清单中定制 3x-ui 的资产来源固定为 `v0.2.5-vps`；构建器本地复制该二进制计算摘要，新 Release 不重复上传。发布前检查签名、所有摘要、包内版本与提交，并确认被引用的 3x-ui 旧 Release 资产仍处于 `uploaded` 且摘要相同；发布后再次从 GitHub 下载清单验签，不以本地文件替代外网验证。发布资产不可原地替换；失败时用新版本号重新签发。

签名清单声明平台、统一布局、数据库模式范围、可用空间、更新引擎版本和每个运行组件的动作。当前自动更新执行器可替换 Gateway、aimili-egress、3x-ui 二进制和辅助脚本。Caddy、Xray、OpenVPN、systemd 的运行配置仅允许 `preserve`；需要更改时必须先发布具备该组件预检、备份、验证和回滚能力的新签名引擎。发布包未完整、环境不兼容或服务预检失败时，网页应在停服前显示原因及 [升级路径](../../docs/upgrade.md)。

修改 `project_update.py` 的执行能力或更新契约时，必须递增源码中的 `UPDATER_VERSION`。构建器会把它写成签名清单的最低/目标引擎版本，旧引擎据此先切换到新槽；若版本号未递增，既有服务器会保留旧引擎，不能声称支持新的组件动作。

旧 `v0.2.13-vps` 引擎只读取 GitHub API 最近 100 个 Release。长期保留旧机过渡能力时，维护者需在过渡版滑出这 100 条之前发布新的契约 1 过渡版；该过渡版自身仍不得进行旧引擎无法预检的组件修改。新部署从带双槽引导器的正式包开始，不受旧引擎的这项限制。
