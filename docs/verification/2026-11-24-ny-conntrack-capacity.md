# ny 连接跟踪表容量与高水位记录

2026-11-24 15:58 和 16:45（北京时间），ny 的内核两次报告 `nf_conntrack: table full, dropping packet`，SSH 和 HTTPS 同时受影响。该 VPS 的内核连接跟踪上限原为 4,096 条，重启前没有保留逐类流量快照，因此无法确定当时占满连接表的流量构成。

本次在线把 `net.netfilter.nf_conntrack_max` 提到 16,384。写入前后同一 SSH 会话保持连通，内核报告的条目数都是 1,251，Gateway、AimiliVPN、3x-ui、Caddy、SSH 进程 PID 分别保持 777、779、817、781、1116。公网健康接口返回 HTTP 200。规则、桶数、超时和现有连接保持原样。现场 `nf_conntrack` slab 单对象为 256 字节，因此 12,288 个额外对象的基础空间约 3 MiB，实际内核分配还包括扩展和对齐开销。

上限保存在 `/etc/sysctl.d/zz-aimili-conntrack.conf`。`/etc/modules-load.d/aimili-conntrack.conf` 使 conntrack 模块在启动阶段先于 `systemd-sysctl` 加载。已检查本机的 systemd 依赖顺序，运行中的内核已读回新值。此轮不重启来验证持久化。

[采样脚本](../../deploy/vps/conntrack_watch.py)部署到 ny 的 `/usr/local/sbin/aimili-conntrack-watch`。现有 cron 每分钟执行一次，单次限时 8 秒。25% 时开始记录连接方向和主要端口，70% 及以上或跟踪丢包计数增加时以 warning 记录，85% 和 95% 上行穿越再记录。快照只保留统计，没有 IP 地址、凭据或数据包内容。采样时连接可继续变化，分类总数也可能少于 sysctl 的瞬时分配数。

取证时可运行 `sudo journalctl -t aimili-conntrack-watch --since today`，或用 `sudo /usr/local/sbin/aimili-conntrack-watch --snapshot` 一次性取样。告警写入 ny 本地 journal。VPS 完全失联时从供应商控制台读日志。此项配置只部署在 ny，仓库没有改动安装器的默认配置。
