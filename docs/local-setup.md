# 本机安装与浏览器授权

适用 Windows、Linux 和可构建该 Go 模块的本机环境。本项目不包含账号注册、购买、邀请码或地区限制处理。先在官方网页正常完成本人账号的准入要求。

本机端口提供协议适配，生成和推理由 Muse 上游完成，需要运行本服务的机器能够访问上游网络。

## 准备与启动

安装 Git、与 `go.mod` 兼容的 Go、Python 3.10+。在自己选择的工作目录执行：

```sh
git clone https://github.com/7yedeng/muse2api.git
cd muse2api
python scripts/init_config.py
python scripts/run.py --check
python scripts/run.py
```

Linux 的 Python 命令可能是 `python3`。`--check` 只检查配置结构，不登录上游，也不证明推理可用。首次 `run.py` 会构建本机二进制，再启动 `serve`。按 Ctrl+C 停止该前台实例；不要批量结束其他 Python 或 Go 进程。

配置生成器拒绝覆盖已有文件。`.env` 中的 API Key、AES-GCM 密钥均在使用者本机生成，不会打印；不要把这份文件、加密会话或运行目录提交 GitHub。

如只需要构建：

```sh
python scripts/run.py --build-only
```

也可手工执行 `go build -o bin/muse2api ./cmd/muse2api`。手工运行前必须自行将私密配置注入环境，Go 程序不会自动读取 `.env`。

## 授权现有网页登录会话

1. 使用 Chrome 或 Edge 的普通窗口正常登录 [Muse](https://muse.ai/)，并确认该账号在官网实际可用。当前扩展只接受精确的 `https://muse.ai` 页面，拒绝无痕窗口；不将此包声明为 Firefox 通用扩展。
2. 按 [扩展安装说明](../browser-extension/README.md) 手动加载本项目扩展。核对它的权限范围和目标端口；不需要整个 Profile 或密码导出。
3. 保持本机服务运行。打开 `http://127.0.0.1:18689`，或在另一个终端执行 `python scripts/admin.py pair-code`，取得本次一次性码。
4. 在扩展中填写管理端口，粘贴一次性码，明确点击授权。扩展只提交 `hatch_sess`、`hatch_native_auth_device`、`hatch_vml` 这三个允许的 Muse 会话 Cookie；不会读取其他网站。
5. 执行 `python scripts/admin.py status`。成功后应看到 `ready:true` 与 `persisted:true`，再按 [API 文档](api.md) 配置调用者自己的 API Key，提交一条简短文字请求验证上游。

一次性码有效期 120 秒，使用后即消费，失败也需要新码。它不是 API Key。授权失败时先确认官网账号状态、所选浏览器账号及当前一次性码；不要反复导出整个浏览器或将 Cookie 发到聊天里。

这是一条用户明确同意的社区会话交接流程，不是 Meta 官方 OAuth。浏览器中已登录、扩展安装完成、本机状态 ready、真实推理成功是四个不同检查点。

## 端口与持久化

默认业务端口 `18688`，管理端口 `18689`。如果端口被占用，选择新的本机地址并相应修改私密配置与扩展目标端口；不能只改扩展而让它指向另一个不明服务。

会话以 AES-GCM 密文保存在配置的运行目录。进程重启使用相同 `MUSE2API_SESSION_KEY` 与数据目录恢复；密钥错误、损坏密文会明确失败，不会自动退回明文保存。持久化只解决本机保存，不保证上游会话永不过期。

重新授权需要当前没有文字或媒体创作在途。状态显示忙碌时先等待原调用结束，或按 [恢复文档](recovery.md) 保存未完成任务状态；不要为了登录覆盖另一个正在使用的会话。

## 官方 Gadget SDK 是另一条路线

Meta 官方 Gadget SDK 当前使用网页申请 SDK token、手机 Muse App 与 BLE 配对设备；其设备 access/refresh token 与本项目网页会话是不同凭据。本项目没有把那套配对实现为可切换 HTTP backend。若要研究官方设备路线，先阅读 [能力与认证证据](capabilities.md)，不要把 SDK token 粘贴到本项目 Cookie 或 API Key 字段。
