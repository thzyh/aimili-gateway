# 本机 Docker 单出口隔离原型验证记录

日期：2026-09-06（Asia/Shanghai）。状态：原型运行中，等待用户手动验收。

## 本阶段范围

本阶段只交付 AimiliVPN 真实单出口数据面：专用 Docker bridge、容器内 `tun0`、一条主 OpenVPN 隧道、本机回环 HTTP/SOCKS5 代理和管理页。

Gateway、3x-ui/Xray、订阅、四出口和协议事务尚未加入本机 Docker；不能把本记录表述为完整 Docker 部署已经完成。

本轮没有连接 `ssh ny`，没有读取或复制生产数据库、配置、证书、令牌或密钥，也没有操作 v2rayN 活动节点、TUN 和系统代理。

## 本地实现

AimiliVPN 新增：

- `deploy/docker-single-exit/Dockerfile`：固定 Python slim 运行环境和容器内 OpenVPN/路由工具。
- `deploy/docker-single-exit/compose.yaml`：专用 bridge、专用 volume、只读根文件系统、资源限制和 Windows 回环端口映射。
- `deploy/docker-single-exit/entrypoint.py`：原子、幂等生成本地 UI 凭据和控制 token，不输出秘密。
- `deploy/docker-single-exit/common.ps1`、`start.ps1`、`verify.ps1`、`show-access.ps1`、`stop.ps1`：安全启停、宿主基线门、脱敏真实出口验证和用户主动凭据查看。
- `tests/test_docker_single_exit.py`：运行时初始化、Compose 有效配置、PowerShell 行为和运行容器集成测试。
- `deploy/docker-single-exit/README.md` 及根 README 入口。

Gateway 保存设计和逐文件计划：

- `docs/superpowers/specs/2026-09-06-local-docker-single-exit-prototype-design.md`
- `docs/superpowers/plans/2026-09-06-local-docker-single-exit-prototype.md`

## TDD 与故障边界

每个新增行为均先观察到预期失败，再做最小实现：

1. entrypoint 缺失时，首次生成、幂等和损坏文件失败关闭测试为 RED；实现后 GREEN。
2. Compose 缺失时，有效权限、端口、网络和 volume 测试为 RED；实现后 GREEN。
3. PowerShell 生命周期脚本缺失时，真实只读行为测试为 RED；实现后 GREEN。
4. Docker Engine 停止时，Windows PowerShell 5.1 把 `docker info` stderr 提升为终止错误；新增失败测试后改用封闭的可用性探测。
5. 原始运行验证通过 PowerShell 向容器 `sh -c` 传递命令替换字符串，发生参数引号重解析；新增 Docker 集成失败测试后改用容器内 Python 返回脱敏状态。
6. `show-access.ps1` 的无 BOM UTF-8 中文格式串在 Windows PowerShell 5.1 下被本地代码页误解码；新增不回显秘密的失败测试后改用 ASCII 标签。
7. 原型运行后，端口检查错误拒绝本项目自己的端口映射；现有全量失败测试驱动精确的 Compose 容器所有权判断，其他进程占用仍会被拒绝。

上述失败均发生在本机脚本或测试边界；没有误报为 VPS、Docker 容器数据面或 v2rayN 故障。

## 最新验证

2026-09-06 本轮最新结果：

- `AIMILI_DOCKER_INTEGRATION=1` 的 AimiliVPN 最终全量测试：174 项通过，0 失败，运行容器集成测试未跳过。
- `docker compose ... config --quiet`：通过。
- `python -m py_compile deploy/docker-single-exit/entrypoint.py`：通过；生成的单个缓存文件已按准确路径清理。
- `git diff --check`：通过。
- 容器运行且 health 为 `healthy`，RestartCount 为 0。
- `Privileged=false`、只读根文件系统、专用 `aimili-single-exit-net`，唯一附加 capability 为 `CAP_NET_ADMIN`，唯一设备为 `/dev/net/tun`。
- 只有一条主 OpenVPN 隧道：`tun0=true`、OpenVPN 进程数为 1、`MULTI_EXIT_SLOTS=0`。
- 经 `127.0.0.1:17928` 的显式代理请求返回有效 IP，且与容器 `eth0` 普通出口不同；具体 IP 未输出。
- Windows 只监听 `127.0.0.1:17928` 和 `127.0.0.1:18787` 的 Docker 端口映射。
- v2rayN PID 为 11532，系统代理保持启用并指向 `127.0.0.1:10808`，现用代理 HTTP 健康检查为 204；启动基线与最终安全门一致。
- Windows 默认路由摘要与启动基线一致。
- 容器采样内存约 51.73 MiB（上限 512 MiB），PIDs 12；本地运行数据约 156,811 字节。
- 本地镜像大小 144,172,411 字节；Docker Desktop 保持运行以供用户验收。

## 用户验收入口

在 AimiliVPN 功能工作树运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\docker-single-exit\show-access.ps1
```

该命令只在用户终端显示本地管理页 URL、用户名和密码。代理测试入口为 `127.0.0.1:17928`，支持 HTTP/SOCKS5；项目脚本不会替用户切换 v2rayN 活动节点。

停止但保留数据使用 `stop.ps1`。只有显式 `stop.ps1 -PurgeData` 才删除准确命名的原型 volume；该数据删除不可恢复。

## Git 状态

AimiliVPN 本地提交：

- `ece31a0`：运行时凭据和入口。
- `096d45e`：镜像与 Compose。
- `9352eda`：安全生命周期脚本。
- `820911f`：Docker 停止状态探测修复。
- `4f46bde`：运行验证与脚本兼容性修复。
- `a49b015`：用户文档。
- `183420e`：复杂度审查移除只供测试调用的生产脚本接口，净减少 20 行。

Gateway 本地文档提交：

- `4dd5c0c`：原型设计。
- `b794414`：实施计划与设计空白修正。

本记录将在 Gateway 产生一个新的本地文档提交。两个仓库均未执行 GitHub push，不能表述为远程已同步。
