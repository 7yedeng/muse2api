# 能力、认证与证据分层

核对日期：2026-11-18。这里区分本项目 HTTP 合同、所依赖社区 SDK 的协议实现，以及官方 Gadget SDK。某一产品具备某项能力，不等于本适配器已经开放相同接口。

## 本项目候选

| 输入或输出 | 当前 HTTP 实现 | 验收要求 |
| --- | --- | --- |
| 纯文字输入／输出 | Chat Completions，同步与 SSE | 检查完整回复与结束事件 |
| 图片理解 | Chat 的图片 data URL | 检查实际语义；不接受远程 URL |
| 文字生成图片 | Images generations | Base64／鉴权下载、实际图片尺寸与内容 |
| 图片编辑／参考图生成 | JSON `reference_images` | 编辑至少一张图，检查是否使用了参考 |
| 文字／图片参考生成视频 | 异步 Videos | 原 ID 查询、完整下载、解码、时长、音轨和内容 |
| PDF、音频、已有 MP4 文件理解 | 未开放 | 不能把未知字段静默当成已理解 |
| 音频／视频作为生成参考 | 未开放 | 图片参考不等同声线或视频条件绑定 |
| 任意文本独立 TTS、固定 `voice_id` | 未开放 | 不以视频包含音轨代替 TTS 接口 |
| 标准 `tools/tool_calls` | 未开放 | 设备命令不是 OpenAI function calling |
| 原生会话复用资源 | 未开放 | `messages` 只是本次请求的文字上下文 |
| 原生 `seconds/size/seed` 输入参数 | 未开放 | 提示词为软要求，以输出测量值为准 |
| 视频取消、删除 | 未开放 | 404 不能证明上游取消成功 |

离线合成测试可以证明鉴权、请求形状、幂等、存储和错误处理；不能证明当前任一账号、地区或上游模型实际可用。本候选没有打包维护者的真实账号或历史生成物来冒充新用户验收。

## 网页会话路线

本项目 HTTP backend 使用固定版本的 [CoolkHz/muse-protocol-go](https://github.com/CoolkHz/muse-protocol-go/tree/573014e7fca56a223b58392ff19cca087e0816d0)。它提供网页会话引导、Noise/Hatch、对话、生成与媒体下载；HTTP 端点、加密存储和配置层由本项目适配。

浏览器扩展是本项目新增的显式会话交接入口：使用者正常登录网页、获取本机一次性码、点击扩展授权。它不是官方 OAuth，也没有在网页账号与 API Key 之间建立永久授权关系。API Key 只保护本实例，不能赋予上游未开放的账号权益。

上游协议可能变化；账号注销、过期、撤销或准入限制都可能要求重新授权。加密保存不等于登录永不过期。

## 官方 Gadget SDK 的另一套能力

本次核对固定版本 [Meta Muse Gadget SDK `6a44074014daad1a623f54e62a169f514741836b`](https://github.com/facebookincubator/muse-gadget-sdk/tree/6a44074014daad1a623f54e62a169f514741836b)。这套 SDK 使用网页 SDK token 与手机 App 的 BLE 设备配对，不能被描述为仅网页登录即可完成授权。[官方前提](https://github.com/facebookincubator/muse-gadget-sdk/blob/6a44074014daad1a623f54e62a169f514741836b/README.md#L41-L52)、[Linux 配对步骤](https://github.com/facebookincubator/muse-gadget-sdk/blob/6a44074014daad1a623f54e62a169f514741836b/linux/README.md#L90-L106)

| 官方 typed 接口 | 已核实范围 | 与本项目的关系 |
| --- | --- | --- |
| `send_chat` | 文字；SID 可用于侧会话 | 本项目未接为另一种 backend |
| `send_image` | JPEG/PNG/GIF/WebP，最多 2 MiB，文字项在前 | 不能直接套用成本项目上传限制 |
| `send_voice` | 最多 2 MiB WAV，可附文字 | 不证明支持任意 MP3 输入 |
| `stream_tts` | 读取 voice-modality 助手消息的 MP3 | 不是任意文本＋固定声线的通用 TTS |
| `subscribe_chat` | 只订阅已有 SID 的事件 | 不等于发送新提示词 |
| 实时语音／视频通话 | 独立通话通道 | 不等于生成视频或 MP4 文件理解 |

相关实现见 [官方 `link_client.py`](https://github.com/facebookincubator/muse-gadget-sdk/blob/6a44074014daad1a623f54e62a169f514741836b/linux/src/musegadget/link_client.py#L378-L462) 与 [订阅、TTS](https://github.com/facebookincubator/muse-gadget-sdk/blob/6a44074014daad1a623f54e62a169f514741836b/linux/src/musegadget/link_client.py#L591-L653)。这些是官方 SDK 代码证据，没有转化为本项目已实现功能。

官方默认持久状态包含设备身份、pairing JSON 与 SDK token，使用受限目录和文件权限；不能将 `0600` JSON 称为自动静态加密。本项目的 AES-GCM 会话存储是独立实现。[官方存储代码](https://github.com/facebookincubator/muse-gadget-sdk/blob/6a44074014daad1a623f54e62a169f514741836b/linux/src/musegadget/config.py#L111-L125)

官方仓库还收录一条社区测试的 headless 迁移路线：在支持 BLE 的机器完成同一设备配对，经受控 SSH 迁移自己的状态文件到服务器，并停掉原活动设备。它不属于本项目浏览器扩展流程，不能把两套凭据混用。[headless 说明](https://github.com/facebookincubator/muse-gadget-sdk/blob/6a44074014daad1a623f54e62a169f514741836b/linux/docs/raspberry-pi-2-headless.md#L118-L137)

## 来源与许可

本项目感谢玉米须分享相关开源项目，并明确依赖 CoolkHz 的 Go SDK。跨平台作者身份关系不在本次源码核验的证明范围。

Go SDK 的 MIT 文件与其第三方声明同时存在：前者不替代更早参考作品的授权。其记录的一个参考项目当前明确未选许可证，另一个当前不可读取；不能从公开可读推断全部代码已获 MIT 授权。固定依赖而不 vendor 只缩小本仓库直接分发范围，不会消除上游历史来源的不确定性。完整说明见 [NOTICE.md](../NOTICE.md)。
