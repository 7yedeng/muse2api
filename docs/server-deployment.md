# 自有服务器、域名与持久化

推荐先在本机完成一次安装和授权流程，再部署自己的服务器。服务器不需要复制维护者的环境，也不接收他人的会话。本文不修改已有中转站服务；新实例先使用独立路径、端口与数据目录。

## 路线选择

| 路线 | 适用环境 | 管理方式 |
| --- | --- | --- |
| Go 二进制 + systemd | Linux 服务器 | SSH-L 到仅 loopback 的管理端口 |
| Compose host network | Linux Docker 主机 | 同上；不映射容器端口 |
| 本机原生运行 | Windows / macOS / Linux | 本机浏览器与本机管理端口 |

当前 Compose 使用 Linux host networking。不要把它当作 Windows / macOS Docker Desktop 的通用端口映射方案：容器内部的 `127.0.0.1` 管理监听不能靠普通 `ports` 映射访问。

## Linux systemd

在服务器取得源码并构建。创建专门服务用户与持久目录，命令中的路径是通用部署示例：

```sh
go build -o bin/muse2api ./cmd/muse2api
sudo useradd --system --user-group --home /var/lib/muse2api --shell /usr/sbin/nologin muse2api
sudo install -d -m 700 -o muse2api -g muse2api /var/lib/muse2api
sudo install -d -m 750 /etc/muse2api
sudo install -m 755 bin/muse2api /usr/local/bin/muse2api
sudo python3 scripts/init_config.py --output /etc/muse2api/muse2api.env --data-dir /var/lib/muse2api
sudo install -m 644 deploy/muse2api.service /etc/systemd/system/muse2api.service
sudo systemctl daemon-reload
sudo systemctl enable --now muse2api
```

若服务用户已经存在，复用它并核对目录所有者，不要重复创建。检查私密环境文件权限为 `0600`，持久目录由服务用户持有并保持 `0700`。systemd 的 `EnvironmentFile` 由服务管理器读取，密钥不写进公开 unit 或命令行。

```sh
sudo systemctl status muse2api --no-pager
curl --fail http://127.0.0.1:18688/health
```

不要将服务日志或环境内容直接上传工单。状态与日志检查不能代替一次明确授权的实际推理。

## 通过自己的浏览器给服务器授权

在自己的电脑建立 SSH 隧道，保持终端运行：

```sh
ssh -N -L 18689:127.0.0.1:18689 user@your-server
```

本机 `18689` 必须空闲。此后本机访问 `http://127.0.0.1:18689` 实际抵达服务器管理入口。用服务器生成的一次性码，在自己已登录 Muse 的浏览器扩展中明确授权。会话将保存在服务器配置的数据目录，而不是本机那份运行目录。

若本机已占用该端口，可将隧道改为 `-L 28689:127.0.0.1:18689`，扩展填写 `28689`，管理命令使用 `python scripts/admin.py pair-code --port 28689` 或 `status --port 28689`。不要因此开放服务器公网管理端口。

服务器授权后依次核对：管理状态 ready、persisted；停止隧道；在服务器重启进程；重新通过隧道检查恢复状态；最后使用自己的业务 Key 发一次简短文字请求。每一步的结果分别记录。

不要将管理端口、防火墙或 Caddy 路由开放到公网来代替 SSH-L。浏览器扩展的目标仍是本机 loopback，它不直接把会话发往任意域名。

## Linux Compose

从项目根目录生成本机私密配置后启动：

```sh
python3 scripts/init_config.py
export MUSE2API_UID="$(id -u)"
export MUSE2API_GID="$(id -g)"
docker compose -f deploy/compose.yaml up -d --build
docker compose -f deploy/compose.yaml ps
```

Compose 配置使用 host network，业务与管理 listener 保持 loopback。以同一部署用户生成配置并启动；上面的 UID/GID 显式使容器与宿主机 `0700` 运行目录属主一致。后续执行 `up` 也保留这两个值；已有目录若由其他用户持有，先核对并修正属主，不要改成 `0777`。运行目录和私密配置通过明确路径传入，镜像不会包含 `.env`、会话或运行媒体。授权方式仍是上面的 SSH-L。

升级时保留同一份私密配置与数据目录。不要执行带 `-v` 的清理来删除自己的会话或任务；不要将整个运行目录当成镜像构建上下文。

## 自有域名与 TLS

将自己的 `media.example.com` 指向该服务器，并使用仓库 [Caddy 示例](../deploy/Caddyfile)。Caddy 只转发列出的业务路由到 `127.0.0.1:18688`，管理端口独立存在且没有公开路由。

部署前替换示例域名，核对 DNS、证书与端口。公网通常只需要 HTTPS 443；`18688` 与 `18689` 保持不可从互联网直接访问。对外客户端 Base URL 为：

```text
https://media.example.com/v1
```

在浏览器地址后加端口只改变连接目标，不会完成鉴权。自己的网页应用应由后端保管 Key 并请求 API；不要把 Muse2API Key 或会话密钥内嵌到公开 JavaScript。

若同一域名还承载中转站，必须明确每个路径的归属与凭据，见 [中转站接入](relay-integration.md)。最简单的组织是文字中转与媒体服务使用两个子域名和各自的 Key。

## 上线验收

先在本机或受控网络验证，再开放域名：

1. 无 Key 的 `/v1/models` 被拒绝，正确 Key 可列模型。
2. 公网任何管理路径均不可达；不要只测 `/setup`，本项目管理路由在另一 listener。
3. 授权成功、加密存储成功、重启恢复成功分别成立；真实推理另验。
4. 一张图或一段视频只创建一次，查询并下载原资源；校验文件尺寸、时长、音轨及 SHA-256。
5. 网络中断／5xx／重启 unknown 不会再次提交生成。
6. 接入多用户中转时，使用两个不同调用者验证媒体读取归属。一个共享上游 Key 不提供这项隔离证明。

本仓库的离线测试不能替代上面针对你自己服务器、账号和中转站版本的验收。
