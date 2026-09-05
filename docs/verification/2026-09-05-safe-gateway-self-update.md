# Gateway 安全更新验证记录

日期：2026-09-05（Asia/Shanghai）。状态：最新代码与 UI 已部署；后端升级/回滚演练被生产会话索引异常阻断，尚未完成。

## 本地实现与验证

逐文件计划：`docs/superpowers/plans/2026-09-05-safe-gateway-self-update.md`。

代码提交为 `2f87fce`、`4ac4787`、`b0dcc39`。实现签名与版本绑定、固定 systemd 安装入口、隔离 fetcher、原子锁、跨 UID 请求文件、持久安装记录、失败恢复、唯一 previous、中文可关闭通知和明确禁用状态。未增加依赖或改变 DB schema、协议、端口。

- `4ac4787`：主线程完整 Go `-race -count=1`、全包 vet、前端 60/60 与发布构建通过。
- `b0dcc39`：受影响 Gateway/UI installer 三包 race、vet、Linux 完整测试通过；前端未变。
- Linux 测试覆盖真实 UID/GID、umask0077、并发原子锁、拒绝非指定 systemd 的 root 执行、Gateway 五个中断点、UI 指针恢复及清理链。
- 最终三项范围复审 PASS：候选显式0755；UI 半切换恢复原 previous；不确定 journal/指针错误保留 repair_required 和诊断资产。
- 复杂度审查移除旧的重复、不可达 CLI 执行入口。
- 上述本地结果不等于生产全部升级路径已验收。

## 实际生产部署

本轮通过 SSH 上传 root 私有资产，核对归档及逐文件 SHA-256；签名资产由本机持久 Ed25519 私钥生成并校验。私钥不入仓库、不上传、不输出。

1. updater 二进制、固定 root helper、systemd units、专用目录与公钥已安装。安装峰值内存约54.6 MB；没有重启数据面。
2. bootstrap 首次被0700目录安全门拒绝：解包恢复了755目录权限。确认二进制/PID/previous均未改变后，仅修正准确上传目录权限再执行。
3. Gateway 已部署为 `v1.0.0`，绑定源码 `b0dcc395f0fb89a2571be539f343b63c4edd29a8`。Gateway PID由961658变为989591；AimiliVPN/x-ui/Caddy PID始终为916096/916107/916125。
4. 受限 installer 正确拒绝同版本更新，返回 `version_not_newer`。
5. `v1.0.1` dry-run成功；Gateway二进制、配置、DB摘要及四服务PID全部不变。
6. 真实 `v1.0.1` install在 `preflight_probe_failed` 退出，未停止Gateway、未替换二进制、未建立切换journal。当前仍是v1.0.0，不能声称后端升级/回滚演练完成。
7. 新UI通过同一个受限installer发布成功，随后经固定helper回退并恢复，四服务PID全程不变。公网页面manifest及三个静态资源摘要全部匹配。

当前外部UI：

- current：`80e689af1a5b8003bd3a3bb68e40106bf05c245d07b5215bdf6fb68926f355bf`
- previous：`af05c9f205d7a51078ddf75e86c2df0443350d476074d7d974025d7550f790aa`
- 内嵌UI保留为兜底。

## 已定位的生产阻断

相同Go只读诊断在受限unit和普通root环境都报告Gateway会话索引条目数异常，因此不是namespace权限问题。Python SQLite 3.45.1的quick_check返回ok，但完整integrity_check同时确认：

- `sessions_expiry_idx` 缺少条目；
- `sqlite_autoindex_sessions_1` 缺少条目；
- 会话表429行，索引418项。

项目既有revoke-sessions只修改撤销时间，不能修复索引，因此没有执行。

在VPS root私有SQLite副本中重建这两个索引后：

- integrity_check由失败变为ok；
- 表和索引计数均为429；
- 所有表的数据摘要不变；
- 生产DB未写入。

用户明确禁止直接编辑生产Gateway DB。生产索引重建需要新增、限定授权，不能通过换入口规避该边界。建议授权范围仅为：保留一个一致性DB恢复副本，短暂停Gateway控制面，在事务中重建这两个索引，验证数据行摘要及完整integrity_check，再继续既定升级/回滚演练。不修改密码、业务记录、节点、端口或其他服务。索引异常形成时间与历史原因尚未确定。

## 最终生产检查

2026-09-05 14:06:43 UTC：

| 逻辑出口 | 国家 | 公网协议 | 状态 |
| --- | --- | --- | --- |
| 主连接 | LA | XHTTP/REALITY | active，egressOk |
| 出口1 | VN | TCP/Vision | up，egressOk |
| 出口2 | KR | TCP/Vision | up，egressOk |
| 出口3 | JP | Hysteria2 | up，egressOk |

- 四服务active，重启计数0；4 OpenVPN、1 Xray。
- 主公网/mixed端口8443/31000；三个槽位20000/30000、20001/30001、20002/30002；八个监听存在。
- 四协议ready；3 VLESS、1 Hysteria、4 mixed；订阅alias数4。
- x-ui配置指纹仍为 `07481c53a2103075105da30c4a0f62534e5c0faa4e6d259749cdd5ec273d2fd3`；非受管入站0，指纹不变。
- x-ui DB quick_check=ok；Gateway DB可读，但integrity_check失败，不能称数据库健康。
- 没有当前忙事务；三条多日前遗留HTTP操作由保守新鲜度规则区分，未直接改写。
- SOCKS5H来源限制仍关闭，applyStatus=applied；本轮未修改或重新进行SOCKS5H流量验收。
- 未操作v2rayN活动代理、TUN或系统代理；未重做用户原节点替换/协议切换操作，也没有以静态资源验证冒充浏览器点击验收。

## 存储与清理

最终根分区51%，可用 `4,603,858,944` 字节（约4.29 GiB）。

删除前均验证准确绝对路径、属主与进程打开引用为0：

| 本轮临时资产 | 移除分配空间 |
| --- | ---: |
| 第一批Linux fixture目录 | 16,343,040字节 |
| 第二批namespace/DAC fixture目录 | 38,965,248字节 |
| 正式上传目录 | 70,946,816字节 |
| 只读诊断程序与私有DB实验副本 | 11,862,016字节 |
| 合计 | 138,117,120字节 |

这些均为本轮生成的临时资产，可通过本地构建或重新制作只读副本恢复；目录本身已删除。诊断临时unit已移除。UI installer只保留current/previous。

新建的唯一Gateway previous二进制保留。既有 `/var/backups/aimili-gateway/20260905-external-ui`（约17.64 MB）暂不删除，因为后端回滚演练仍未通过；未以清理为由删除尚需使用的回滚资产。没有再次删除早已不存在的历史Go缓存目录。

## 禁用状态、Git与下一步

- `allowGatewayInstall=false` 已恢复，fetcherUid已恢复专用用户UID。
- fetcher enable marker不存在；网页updateEnabled仍为默认false；没有验证过的HTTPS发布源/catalog，因此网页一键下载未验收。
- 两个path units恢复监听；request=0、staging=0、active journal不存在；6个终态结果保留在有界目录。
- 2026-09-05本轮fetch后，代码分支比origin领先33个提交；文档提交另增加一个。尚未推送GitHub，未合并main。
- 代码已提交到本地Git；既有未跟踪部署资产、缓存、Windows产物保留。当前文档更新会单独提交。
- 下一步需要上述会话索引限定修复授权；完成后才能继续v1.0.1安装、Gateway回滚/恢复与旧回滚资产清理。
