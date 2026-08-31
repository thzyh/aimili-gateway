# 运行节点稳定排序、切换故障修复与两层节点池验收记录

日期：2026-08-31

状态：本地验证通过，生产阶梯部署待执行

## 已确认故障边界

切换后的第一处明确失败发生在 AimiliVPN 本地代理/进程资源层：systemd 任务预算为 48，但旧代理允许 256 个线程式连接；Xray 本机重试耗尽任务后，OpenVPN 子进程已创建而日志线程无法启动，异常路径未回收子进程，遗留进程继续占用槽位 TUN，最终导致 AimiliVPN、Gateway 与 Xray 状态分叉。

本轮没有把订阅、UUID、REALITY、XHTTP、Hysteria2 参数或 Xray 全局重载误判为第一根因。

## 本地实现

- 本地代理默认全局 24、每监听实例 6；线程启动失败会关闭客户端并释放两级许可。
- OpenVPN 日志线程或策略路由失败会终止并等待子进程；同槽位重拨前只回收带精确 `AIMILI_SLOT` 标记的未登记受管进程。
- 有效缓存上限为 30，支持运行/事务硬保护、国家锚点、手动刷新保护、旧健康优先、住宅优先和机房回退。
- `pool_metadata.json` 只保存安全统计与保护轮次；损坏时保留 `nodes.json`。
- Gateway 在协议事务写入前验证槽位身份、`up` 状态、真实出口和 mixed；失败时不调用协议 helper。
- 页面运行分区固定为主连接、出口1、出口2、出口3，显示公网/mixed 端口，并拆分现有国家筛选与官方国家补充。

## 最新本地验证

| 验证 | 结果 |
| --- | --- |
| AimiliVPN `python -m unittest discover -s tests -v` | 127/127 通过 |
| AimiliVPN `py_compile` | 通过 |
| Gateway Vitest | 6 个文件、28/28 通过 |
| Gateway 前端生产构建 | 通过 |
| Gateway `go test ./... -race -count=1` | 通过，退出码 0 |
| Gateway `go vet ./...` | 通过，退出码 0；全局 module stat cache 只读提示不影响结果 |
| Gateway 两个二进制构建 | 通过 |
| 两仓库 `git diff --check` | 通过 |

## 尚未宣称完成的生产层

- `ssh ny` Stage 0–5 尚未在本记录中标记通过。
- 尚未通过生产 Gateway UI、正式 check/repair、v2rayN 7.24.4 四条节点和 300 秒资源观察验收。
- 未在生产复验前，不得声称延迟 `-1` 已完成端到端修复。

生产部署只上传不含生产事务配置、密码、Cookie、UUID、Auth、私钥、随机后台路径和订阅链接的修复资产；不得增加运行出口、公网入站或 mixed 数量，不得触碰非 Gateway 受管 3x-ui 资源。
