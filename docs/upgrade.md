# 网页更新受阻时的升级路径

页面给出的版本、组件和原因来自服务器对已签名发布清单及本机状态的检查。更新器发现不能安全安装时会在停止服务前结束事务。先保留更新事务编号及现有备份；请勿在有数据的 VPS 上重跑首次部署脚本作为通用修复办法。

## platform-unsupported

**原因：** 操作系统或 CPU 架构与签名包声明的目标不符。运行 `uname -m`、`cat /etc/os-release` 核对。当前正式包针对 Ubuntu 24.04、x86_64。升级路径是等待维护者发布匹配平台的签名包；必须换 VPS 时，先备份并按专用迁移流程搬运数据和凭据，不能在新 VPS 上伪造旧机 UUID。

## unsupported-layout

**原因：** 当前数据库或出口服务位置与统一布局不同。运行 `systemctl show aimilivpn -p ExecStart`、`ls -ld /var/lib/aimili-gateway/aimili-egress` 核对。保留配置、数据库、3x-ui 数据和密钥备份，让维护者提供针对该布局的签名迁移包；经测试后先迁移布局，再回到网页检测更新。不要直接运行空白 VPS 安装命令覆盖有数据的服务器。

## database-too-new

**原因：** 当前数据库版本高于目标包允许的最高版本，通常是尝试安装旧包。运行 `sqlite3 /var/lib/aimili-gateway/aimili-gateway.db 'select max(version) from schema_migrations;'` 核对。选择明确支持该模式的更新版本；若要回退，应使用与目标程序同一事务保存的数据库快照，停服后恢复，不能只换程序文件。

## database-too-old

**原因：** 发布包没有声明从当前数据库版本连续迁移的能力。维护者需发布包含缺失迁移及副本验证的签名过渡版本。保留现有数据库，先安装该过渡版本，验证后再检测目标版本。

## disk-full

**原因：** 下载、数据库快照和回滚空间不足。运行 `df -h /var/lib/aimili-gateway` 检查，释放经确认可删除的非生产文件或扩容磁盘，再重新检测。不要删除当前数据库、密钥、出口配置和最近一次事务备份。

## updater-upgrade-required

**原因：** 目标包需要较新的更新引擎，但签名清单缺少可验证的引擎资产，或现有引擎无法识别更新协议。等待维护者发布兼容现有更新器的过渡版本；先通过页面安装过渡版本，再重新检测目标版本。不要从任意 URL 手工下载 Python 文件覆盖 root 更新器。

## updater-too-new

**原因：** 当前更新引擎版本超过目标包的兼容范围。选择较新且支持当前引擎的正式发布；确需回退时，使用该事务已有的程序与数据库联合备份，不单独降级更新器。

## updater-engine-failed

**原因：** 已验签的新更新引擎无法正常运行，固定引导器已把指针切回上一引擎。查看 `journalctl -u aimili-project-update-install.service -n 100 --no-pager` 与该事务结果，保留 `/usr/local/lib/aimili-gateway/update-engines/current.json` 和事务资产。维护者需修复引擎、使用新版本号重新签发；同一坏引擎摘要会被阻止再次安装。若页面显示 `repair_required`，说明生产修改可能已开始，先按 `rollback-failed` 核对快照与服务。

## unsupported-component

**原因：** 发布包要求修改页面显示的组件，当前引擎没有对应的受限执行器。例如现有契约不能直接替换 Caddy、OpenVPN、systemd 或 Xray 的运行配置。维护者应先发布可通过当前网页安装的引擎过渡版本，再发布包含该组件预检、快照、核验和回滚规则的正式包；不要关闭兼容检查。

## release-incomplete

**原因：** 签名清单要求的资产在正式发布中不存在或摘要不匹配。保留现状，等待维护者用新版本号发布完整签名资产；不要在原 tag 下替换不可变发布文件。

## service-unhealthy

**原因：** 更新前的 Gateway、AimiliVPN、3x-ui、Caddy 或数据库预检不通过。运行 `systemctl --failed`、`systemctl status aimili-gateway aimilivpn x-ui caddy --no-pager`，再看对应服务在本次操作后的日志。先恢复原服务及数据库健康，再重新检测；更新器不会把旧故障算成升级成功。

## database-invalid

**原因：** 升级前后 SQLite `quick_check` 未通过；若已执行修改，更新器会尝试恢复同一事务快照。保留数据库、`-wal`/`-shm` 文件及事务备份，先核对页面终态，再按 `rollback-failed` 或数据库快照恢复流程处理。不要只替换二进制继续启动。

## identity-changed

**原因：** 安装后的连接、UUID、端口或订阅身份摘要不同于升级前；更新器已尝试回滚。保留事务备份并核对 Gateway 与 3x-ui 数据库，确认回滚后的身份与客户端持久化配置一致。若显示 `repair_required`，按 `rollback-failed` 处理。

## manifest-incompatible

**原因：** 已签名清单缺少平台、数据库、组件或更新器兼容数据。维护者应发布包含完整兼容说明的新签名版本。当前版本保持运行，不能通过网页强行忽略字段。

## invalid-signature

**原因：** 发布清单无法通过 VPS 内置公钥验证。停止操作并核对发布来源与公钥轮换公告。只有带新旧双签名的过渡版本，或经过核实的离线恢复流程，才能安全更换信任根。

## invalid-payload

**原因：** 下载内容与签名清单中的 SHA256 不一致，或压缩包结构无效。等待维护者按新版本号重新发布完整资产，再点击检测；不要关闭摘要或路径检查。

## download-failed

**原因：** VPS 无法完整获取固定 GitHub Release 资产。运行 `curl -I https://github.com/thzyh/aimili-gateway/releases` 检查连通性。网络恢复后重新检测；无网络环境须由维护者提供同一签名清单对应的离线资产。

## release-missing

**原因：** 固定仓库未提供可识别的正式版本。等待维护者完成签名发布，不能改为抓取未发布的 main 源码直接覆盖运行服务。

## rollback-failed

**原因：** 自动恢复后的服务、数据库或连接身份未能确认，后续安装已锁定。保留 `/var/lib/aimili-gateway-project-update/transactions` 与最近备份，运行 `systemctl status aimili-gateway aimilivpn x-ui caddy --no-pager`，请维护者按同一事务快照核对并恢复；不要重复点击更新。

## interrupted

**原因：** 服务器或更新服务在事务中途停止。刷新页面继续查询原事务；确认已自动回滚及服务恢复后才再次检测。如果显示需要修复，按上一节处理。

## version-invalid

**原因：** 目标版本不是更高的正式版本，或包内程序版本与签名清单不一致。刷新版本列表并选择新的正式发布；包内不一致需要维护者重新签发新版本。

## operation-failed

**原因：** 未归类的安装错误。保留事务编号，查看 `journalctl -u aimili-project-update-install.service -n 100 --no-pager` 和服务器上的终态结果，交给维护者核对第一处失败。若已开始修改，优先确认自动回滚，不能直接重试。
