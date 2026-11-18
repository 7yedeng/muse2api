# muse2api

将本人已经获准使用的 Muse 网页会话接到本机 HTTP API，或部署到自己的服务器，再接入兼容客户端与中转站。

这是社区协议适配器，不是 Meta 官方 OpenAI API。项目默认是**单用户实例**：一个 Muse 会话、一组上游配置、一个服务 API Key。配套的浏览器扩展必须由使用者自己安装并点击授权；Skill 不会读取浏览器 Profile、密码或 Cookie。

想让自己的 AI 助手整理并执行环境对应的完整流程，可以直接复制 [给 AI 的完整 SOP 提示词](docs/ai-prompt.md)，填写目标与环境；不要填写凭据。

## 项目定位与官方权益

本项目主要面向个人学习、协议研究和本人创作工作流，由社区独立维护，不隶属于 Meta，也未获得 Meta 对本项目的官方认证或背书。感谢 Meta 团队对 Muse 产品与开发者生态的投入，建议优先使用官方网页、App 和适用的官方开发者路径。

请尊重官方条款、服务成本和账号权益。API 适配不会增加账号原有的准入、订阅或配额，也不提供自动化、共享或转售服务的官方授权；本项目不为绕过限制、账号池轮换规避用量或滥用免费额度提供支持。

MIT 许可适用于本项目有权许可的代码，不能替代 Meta 服务授权。“个人使用”是项目定位，不是免责理由，也不改变 MIT 的代码许可。完整说明与官方信息入口见 [项目定位与使用说明](docs/responsible-use.md)。

## 五步上手

需要 Go（最低版本以 `go.mod` 为准）、Python 3.10+，以及能正常登录并使用 Muse 的 Chrome 或 Edge。以下步骤不会购买、注册或解除上游账号限制。

```sh
git clone https://github.com/7yedeng/muse2api.git
cd muse2api
python scripts/init_config.py
python scripts/run.py
```

1. `init_config.py` 生成仅保存在本机的私密 `.env`，已有文件不会被覆盖。`run.py` 加载配置、构建并启动服务；密钥不会打印到终端。
2. 在自己的浏览器正常登录 [Muse](https://muse.ai/)，完成账号本来需要的条款、地区与验证步骤。
3. 按 [浏览器扩展说明](browser-extension/README.md) 加载扩展。到本机管理入口 `http://127.0.0.1:18689`，或执行 `python scripts/admin.py pair-code`，取得一次性授权码。
4. 在扩展中填写本机管理端口和该授权码，点击授权。扩展只将指定的 Muse 会话 Cookie 交给本机管理端口，服务验证后以 AES-GCM 加密保存。这个流程不是官方 OAuth。
5. 用 `python scripts/admin.py status` 检查 `ready`、`persisted`；按 [API 使用说明](docs/api.md) 为调用者配置自己的 Key，再发一条明确授权的简短文字请求验证上游。`/health` 只证明本进程可访问。

默认入口：

| 用途 | 地址 | 暴露范围 |
| --- | --- | --- |
| 业务 API | `http://127.0.0.1:18688/v1` | 默认仅本机；自有域名通过 TLS 反代此入口 |
| 登录与状态管理 | `http://127.0.0.1:18689` | 始终 loopback；服务器管理走 SSH 隧道 |

完整步骤见 [Windows / Linux 本机安装](docs/local-setup.md)、[自有服务器与域名部署](docs/server-deployment.md)。

## 当前接口和边界

| 功能 | 本项目接口 | 当前范围 |
| --- | --- | --- |
| 文字与图片理解 | `POST /v1/chat/completions` | 文本、图片 data URL；同步与 SSE |
| 图片生成 | `POST /v1/images/generations` | 文字，可附图片 data URL；输出一张图片 |
| 图片编辑 | `POST /v1/images/edits` | JSON `reference_images`，至少一张参考图 |
| 视频生成 | `POST /v1/videos` | 异步任务，返回 ID 后只查原任务 |
| 视频状态与下载 | `GET /v1/videos/{id}`、`GET .../content` | 需要本实例 API Key |
| 图片下载 | `GET /v1/images/{id}/content` | URL 模式的鉴权下载入口 |

图片默认以 `b64_json` 返回，便于标准客户端或中转站处理；选择 URL 模式时，下载地址同样需要鉴权。媒体 POST 必须带 `Idempotency-Key` 或 `X-Request-ID`。本地与服务器都不会因超时、5xx、断线、`unknown` 或重启自动再次提交生成。

这版没有开放 PDF、音频或已有视频文件理解、音视频生成参考、标准 `tools/tool_calls`、任意文本 TTS，以及原生 `seconds/size/seed` 控制。未知请求字段会返回 400。提示词中的时长、画幅和音色是软要求，必须以实际文件验收。图片编辑暂不接受 multipart，远程图片 URL 也不属于当前合同。

`messages` 可以携带文字历史，但本版没有暴露可持续复用的原生会话资源；不同 API 请求不能据此推断共享 Muse 原生 SID。完整字段见 [API 合同](docs/api.md) 与 [能力证据分层](docs/capabilities.md)。

## 三条使用路线

- **自己本机用：** 网页登录 → 显式扩展授权 → 本地端口 → 客户端或 Skill。
- **自己服务器持久运行：** 服务器启动 → SSH-L 连接管理端口 → 在自己的浏览器授权 → 会话加密保存在服务器 → 自有域名仅开放业务 API。
- **接中转站：** 将业务 API 作为自定义 OpenAI 上游。先接文字，再按中转站当前版本分别验收图片和视频。参见 [Sub2API / New API 接入](docs/relay-integration.md)。

中转站签发的用户 Key 与 Muse2API 上游 Key 是不同的凭据。多个调用者共用一个上游 Key 时，不能据此声称媒体资源已实现多租户隔离；媒体任务归属必须由中转站自己的鉴权层验证。

## 配套 Skill

仓库包含两份可独立安装的 Skill：

- [muse2api-local](skills/muse2api-local/SKILL.md)：本机安装、显式登录授权、启动与持久化恢复。
- [muse2api-client](skills/muse2api-client/SKILL.md)：使用调用者自己的 Base URL 和 Key 进行文字、图片及视频调用，保留媒体回执并恢复下载。

将所需的整个 Skill 文件夹复制到自己使用的代理所支持的技能目录，例如 `~/.codex/skills/`。安装 Skill 不会自动授权 Muse 账号；浏览器授权和 API Key 配置仍由使用者完成。[安装及用法](docs/skills.md)。

## 来源、许可与发布状态

本项目参考并使用 [CoolkHz/muse-protocol-go](https://github.com/CoolkHz/muse-protocol-go)，固定依赖版本见 `go.mod`。感谢玉米须的开源分享，[分享原帖](https://x.com/0xraco/status/2116108328925622385)。代码仓库与分享来源分别保留署名，详见第三方声明。

本项目原创适配层的许可、第三方依赖及其已知来源范围分别见 [LICENSE](LICENSE) 和 [NOTICE.md](NOTICE.md)。上游 SDK 的早期参考链仍有许可信息缺口，不能将整条来源链表述为已全部清权。本仓库不 vendor 那些参考项目的源码。

候选验证使用离线 fake backend 和合成测试，不包含维护者的账号、域名部署、会话、运行日志或历史生成物。离线测试通过不等于你的账号已经获得上游能力；发布与真实链路验收记录应分别保留。

已执行的检查与尚未进行的浏览器／上游／Docker 验收分别见[候选验证记录](docs/verification.md)。

## 文档索引

- [项目定位、Meta 官方关系与账号权益](docs/responsible-use.md)
- [给 AI 的完整 SOP 提示词](docs/ai-prompt.md)
- [本机安装与授权](docs/local-setup.md)
- [API、配置与请求示例](docs/api.md)
- [服务器、Docker、TLS 与持久化](docs/server-deployment.md)
- [Sub2API / New API 接入与验收矩阵](docs/relay-integration.md)
- [重新登录、备份和回退](docs/recovery.md)
- [输入输出与能力证据](docs/capabilities.md)
- [Skill 安装与使用](docs/skills.md)
- [候选验证与复现](docs/verification.md)

## 贡献与审核

贡献者通过 Fork、工作分支和 Pull Request 提交修改，由维护者审核后合并。规则、离线检查和敏感文件要求见 [CONTRIBUTING.md](CONTRIBUTING.md)，默认代码所有者见 [CODEOWNERS](.github/CODEOWNERS)。普通贡献者的合并需要有效审批与通过的检查；GitHub 仓库保护设置负责实际执行。所有者保留管理员维护权限，不将其作为 AI 或协作者擅自绕过审核的授权。
