# Sub2API / New API 接入 SOP

先把 Muse2API 直连跑通，再接中转站。中转站是独立的鉴权、路由、计费和任务系统；填写 Base URL 并不自动证明所有端点兼容。

本次仅按公开主仓库源码核对，没有拿维护者的私有中转站配置当作通用方案，也没有在这两个版本上执行真实账号媒体链路。

## 核对版本与兼容矩阵

| 项目 | 2026-11-18 核对版本 |
| --- | --- |
| Sub2API | [`0.2.23` / `2af3f1e15c98c893a89255a037f551046883b8c0`](https://github.com/Wei-Shaw/sub2api/tree/2af3f1e15c98c893a89255a037f551046883b8c0) |
| New API | [`3a407cc9b064fe79a4ffac4146b2690c465dc929`](https://github.com/QuantumNous/new-api/tree/3a407cc9b064fe79a4ffac4146b2690c465dc929)，以 commit 标识，未据空 VERSION 文件猜发布号 |

| Muse2API 功能 | 直连合同 | Sub2API | New API |
| --- | --- | --- | --- |
| Chat Completions | 本项目离线 fake 验证 | 源码确认支持自定义 OpenAI API-key 上游与强制 Chat Completions；真人链路待验 | 源码确认标准 Chat 路由与 OpenAI 渠道；真人链路待验 |
| 图片生成 Base64 | 本项目离线 fake 验证 | 有图片入口，模型、字段、重试及返回处理需单独验收 | 有图片协议入口，渠道／插件选择及字段需单独验收 |
| JSON 图片编辑 | `reference_images`，非 multipart | 不保证 stock UI/转换器保留本字段 | 不保证 stock UI/转换器保留本字段 |
| 图片相对 URL 下载 | 鉴权 `/v1/images/{id}/content` | 不是可假定透传的标准中转资源路径 | 同左；不能把上游 Key 交给最终用户补链路 |
| 异步视频 create/status/content | 本项目独立合同 | 未提供本项目的通用视频适配；不能套用 Grok/Sora 专用流程 | 当前任务插件机制可扩展，但本候选未提供已验证 Muse provider plugin |
| 媒体多用户资源隔离 | 单用户实例 | 必须由中转站独立验证 | 必须由中转站独立验证 |

“源码确认入口”不等于“已接入通过”。最终至少需要一次 create → 原 ID status → content 下载，并用两个不同调用者检查归属。

## 先准备上游

确认自己的 Muse2API 已完成浏览器授权和加密保存，直连文字可用。若需要图片或视频，分别完成一件明确授权的真实作品并下载验收。未知送达的作品只查原 ID。

中转站只连接业务端口，不能连接管理端口。部署方式可选：

- 同一受控主机：中转站从自己的网络命名空间连接可达的 Muse2API 业务地址。容器内的 `127.0.0.1` 是容器自身，不是宿主机；应按实际网络明确配置。
- 独立主机：使用自己的 HTTPS 域名，例如 `https://media.example.com`。

上游地址允许名单只增加自己的精确主机。Sub2API 当前有 `security.url_allowlist`、`allow_private_hosts` 等策略，部署到私网时按其文档为所需目标配置，不能为接入本项目就关闭所有 URL 校验。[Sub2API URL 安全配置](https://github.com/Wei-Shaw/sub2api/blob/2af3f1e15c98c893a89255a037f551046883b8c0/README.md#L564-L580)

## Sub2API：先接文字

1. 在管理后台新增 **OpenAI → API Key** 类型账号，不选择 OAuth／订阅账号类型。
2. Base URL 填 Muse2API 的业务根地址，例如 `https://media.example.com`。当前构造函数能处理根地址与版本后缀，使用根地址可减少重复 `/v1`。[地址构造源码](https://github.com/Wei-Shaw/sub2api/blob/2af3f1e15c98c893a89255a037f551046883b8c0/backend/internal/service/openai_endpoint_url.go#L8-L27)
3. Key 填本实例的 Muse2API API Key；它只存于中转站的上游凭据配置，不分发给调用者。
4. 将上游协议设置为 **Chat Completions**。该版本的账号 `extra.openai_responses_mode` 支持 `force_chat_completions`；本项目没有 `/v1/responses`，不能保持强制 Responses。[路由判定](https://github.com/Wei-Shaw/sub2api/blob/2af3f1e15c98c893a89255a037f551046883b8c0/backend/internal/pkg/openai_compat/upstream_capability.go#L38-L55)
5. 将 `muse-default` 加入该账号和分组可用模型，创建只允许这组模型的测试调用者 Key。保持模型名原样；不要将上游 `model` 改成未经本项目支持的模型名。
6. 使用 **Sub2API 签发的调用者 Key**，向它自己的 `/v1/chat/completions` 发最小请求，只含 `model/messages/stream`。先同步，再验证完整 SSE。

如果当前 UI 名称或字段与上述源码版本不同，先记录实际版本，再对照账号路由设置。404 `/v1/responses` 是协议配置问题；不要靠换 Muse 账号或重发媒体来解决。

## New API：OpenAI 渠道

1. 新增 **OpenAI** 渠道（该版本常量为 type 1），Base URL 填业务根地址 `https://media.example.com`，不在末尾加 `/v1`。其普通 URL 构造是 Base URL 与 `/v1/...` 请求路径拼接。[渠道类型](https://github.com/QuantumNous/new-api/blob/3a407cc9b064fe79a4ffac4146b2690c465dc929/constant/channel.go#L1-L15)、[地址拼接](https://github.com/QuantumNous/new-api/blob/3a407cc9b064fe79a4ffac4146b2690c465dc929/relay/common/relay_utils.go#L26-L40)
2. Key 填自己的 Muse2API 上游 Key；模型先只启用 `muse-default`，指定测试分组。按该站的模型定价或自用模式要求完成配置，不把不存在的 token 用量当作真实账单。
3. 给测试调用者签发 New API Key，客户端 Base URL 指向 New API 的 `/v1`，请求仍用 `muse-default`。
4. 对照 [Chat 路由](https://github.com/QuantumNous/new-api/blob/3a407cc9b064fe79a4ffac4146b2690c465dc929/router/relay-router.go#L96-L110) 完成最小同步请求，再检查 SSE 是否到完整结束。

若站点对请求注入 `stream_options`、`max_tokens`、sampling 或其他本项目不支持的字段，先关闭相应注入或做显式转换；不能因“OpenAI 兼容”而要求本项目静默吞掉参数。

## 图片接入

文字成功后再启用 `muse-image`。用 `response_format:"b64_json"`、`n:1`、简短 prompt 做一次显式授权测试，检查中转站是否原样保留模型和支持的字段、是否把有效 Base64 交给调用者。

图片编辑使用本项目 JSON `reference_images`；如果中转站只接受或只转发 multipart `image`，需要额外适配，不能标为直接兼容。客户端若默认添加 `size`、`quality` 等未实现字段，应明确移除，不能用中转站注入默认值模拟硬控制。

媒体创建前还要验证：

- 原稳定 `Idempotency-Key` 是否保留；`X-Request-ID` 仅在它缺失时作兼容后备，不能依赖中转站每次新建的 trace ID 防重。
- 502／超时是否触发重试或上游切换；没有可确认的防重合同前，媒体暂不经过该中转站。
- 图片内容没有被错误当成 SSE 或被代理截断，Base64 解码后的实际文件可用。

本项目不会提供伪造 token usage；中转站的计费、最低余额与限额行为须由运营者按实际版本验收。

## 视频与资源下载：当前可用的清晰路线

本项目视频合同是 `POST /v1/videos` 与原 ID 的 GET/status/content。Sub2API 的 Grok 专用视频路由或 New API 的 Sora 等任务插件不能仅靠改模型名自动转为此合同。New API 当前允许插件声明 `openai_video` 或自定义 task 路由，但需要实现完整的提交、状态、内容和用户归属处理。[Task Plugin API v1](https://github.com/QuantumNous/new-api/blob/3a407cc9b064fe79a4ffac4146b2690c465dc929/docs/plugin-api/v1.md#L1-L47)

在专门适配完成前，可采用：

| 用途 | 地址示例 | 调用者凭据 |
| --- | --- | --- |
| 文字中转 | `https://chat.example.com/v1` | 中转站签发的用户 Key |
| 本人媒体直连 | `https://media.example.com/v1` | 自己的 Muse2API Key |

也可以由自己网站的后端持有 Muse2API Key，向媒体实例请求，再由自己的业务鉴权决定用户能读哪个资源。不要把这个上游 Key 写入浏览器前端。

单域名分路径同样可行，但路径必须路由清楚：例如 Chat 交给中转站，Videos 与其 status/content 交给独立 Muse2API；这两个路径仍使用各自的 Key。域名相同不代表凭据和资源归属相同。

## 接入验收记录

记录实际中转站 commit／版本、上游地址的公开标识、模型配置、请求 ID、时间、状态码和结果，不记录 Key、Cookie、会话密钥、邮箱或整个数据库配置。

通过标准：文字同步与完整 SSE 分别成功；图片可解码；视频原 ID 查询和完整下载成功；超时不增加创作；不支持字段返回可解释的错误；两个不同调用者不能查询彼此任务。只要其中一项未验证，该项继续标为未验证。
