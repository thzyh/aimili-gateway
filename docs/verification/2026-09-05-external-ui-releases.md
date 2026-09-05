# 外部 UI 免重启发布生产验证

日期：2026-09-05（Asia/Shanghai）

## 范围

本阶段只启用 Gateway 外部 UI 读取、Ed25519 签名发布、离线 root 安装器以及 `current`/`previous` 原子切换。未增加网页更新入口，未部署后端下载器或后端二进制自更新，也未修改 AimiliVPN、x-ui/Xray、Caddy、协议、端口、数据库 schema 或 SOCKS5H 安全策略。

## 本地实现与验证

- `internal/webassets` 每次请求只解析一次 `current`，仅接受 UI 根下的 `releases/<64位SHA-256>`，外部版本损坏、越界、API 不兼容或入口资产缺失时回退内嵌 UI。
- 缺失静态文件返回 404，只有无扩展名前端路由使用 SPA fallback；index/manifest 不缓存，哈希资产 immutable。
- `internal/releaseverify` 对 manifest 原始字节执行 Ed25519 分离签名校验，并验证 SHA-256、字节数和文件闭集。
- `internal/uirelease` 拒绝软链接、硬链接、路径穿越和额外 staging 文件；原子切换 current/previous，只保留两个外部版本。
- Linux 专用测试在 `umask 0077` 下验证 release 根和所有 assets 目录显式为 `0755`。
- 前端全量：7 个测试文件、50 项测试通过；生产构建通过。
- Go 全包：`go test ./... -race -count=1` 通过；`go vet ./...` 通过；Gateway、admin、release-tool、installer 四个二进制构建通过；`git diff --check` 通过。
- Linux 真实软链接测试和权限测试通过交叉编译测试二进制在 ny VPS `/tmp` 独立执行，未读取生产数据库或配置。

## 首次失败、根因和恢复

首次 Stage A 安装在 UI HTTP 门失败后自动恢复了旧 Gateway 二进制、unit 和 config 内容。随后发现回滚脚本使用 `install -m 0600` 恢复 config 时丢失原属主，Gateway 因无权读取配置进入重启循环；数据面期间仍为 OpenVPN 4、Xray 1，AimiliVPN、x-ui 和 Caddy 未停止。

本轮立即把单个 config 恢复为 `aimili-gateway:aimili-gateway 0600` 并重启 Gateway，四服务、主连接、三槽和两个数据库随后全部恢复正常。TDD 固定了回滚必须保留 config mode/ownership/timestamps。

UI HTTP 门失败的明确根因是 root installer 的 `os.MkdirTemp` 生成 release 目录为 `0700`，rename 后未改权限；非 root Gateway 无法进入目录，因而安全回退内嵌 UI，`/manifest.json` 返回不可用。Linux RED 测试稳定得到 `0700`，最小修复后 release 根、assets 和嵌套目录均为 `0755`，Linux GREEN 通过。

## 生产首次启用结果

- 第二次 staging 使用新绝对路径，上传后逐文件 SHA-256、普通文件闭集和无软链接检查通过。
- Gateway 首次启用成功，Gateway PID 从 `926467` 变为 `961658`；AimiliVPN、x-ui、Caddy PID 保持 `916096`、`916107`、`916125`。
- 外部 UI 配置已生效；UI 根、releases、正式版本和 assets 目录均为 `root:root 0755`。
- `/manifest.json` 从 Gateway 回环地址返回正式签名版本；内嵌 UI 仍编译在 Gateway 中作为兜底。
- 四服务 active；Gateway DB 与 x-ui DB `PRAGMA quick_check=ok`；主连接 active/egress 正常；三槽均为 up/egress 正常；OpenVPN 4、Xray 1；4 条协议状态 ready；受管入站数量仍为 8。

## 免重启切换和回滚

使用第二个只增加 HTML 识别注释的签名 UI 执行生产切换：

1. installer 返回 `success`，current 指向测试版，previous 指向正式版；
2. Gateway 回环 HTTP 返回测试版；
3. 执行 `ui-rollback` 后 current 恢复正式版，previous 指向测试版；
4. Gateway PID 在安装、读取和回滚全过程保持 `961658`；AimiliVPN、x-ui、Caddy PID、OpenVPN/Xray 数量也完全不变。

最终只保留 current、previous 和内嵌 UI，满足日常纯 UI 发布/回滚不重启服务的目标。

## 存储与回滚资产

删除前逐项解析真实绝对路径、统计大小并确认打开进程数为 0。删除内容：

- 两个 Stage A 上传目录；
- 一个 UI 切换测试 staging；
- 两个 Linux 临时测试二进制；
- 已被新回滚替代的 `/var/backups/aimili-gateway/20260905-refresh-notice`。

合计释放约 93.45 MB。上述临时目录和旧备份目录本身不可恢复，但正式 current/previous UI、内嵌 UI、当前运行文件和新的 `/var/backups/aimili-gateway/20260905-external-ui` 均保留。最终根分区使用率 51%，可用 `4,554,670,080` 字节；生产只保留一个约 17.64 MB 的 Gateway 回滚目录。

## 剩余边界

- 阶段 B/C/D 的低权限 fetcher、无网络 root spool、后端 dry-run、网页更新入口和 `control-plane-only` 后端替换尚未实现或部署。
- 本记录不等于后端一键升级完成，也不替代用户对后续网页操作路径的验收。
