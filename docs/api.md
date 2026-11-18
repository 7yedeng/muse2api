# API 与调用配置

本文描述本项目兼容层，不能作为 Meta 官方 API 合同。业务根路径默认 `http://127.0.0.1:18688/v1`。除 `GET /health` 外，业务请求需要 `Authorization: Bearer <自己的 Muse2API Key>`；Key 不放在 URL、提示词或前端代码里。

## 为调用者设置环境

服务端 `.env` 由启动脚本读取，不会自动导出到其他终端。调用者只需要 Base URL 和 API Key，不需要会话加密密钥。

PowerShell，在自己的终端输入 Key：

```powershell
$env:MUSE2API_BASE_URL='http://127.0.0.1:18688/v1'
$museKey=Read-Host '输入自己的 Muse2API Key' -AsSecureString
$env:MUSE2API_API_KEY=[System.Net.NetworkCredential]::new('', $museKey).Password
```

Linux / macOS shell：

```sh
export MUSE2API_BASE_URL='http://127.0.0.1:18688/v1'
read -r -s -p '输入自己的 Muse2API Key: ' MUSE2API_API_KEY
printf '\n'
export MUSE2API_API_KEY
```

远程地址使用自己的 HTTPS 域名，例如 `https://media.example.com/v1`。便携客户端只允许 HTTPS 远程端点或 HTTP loopback；它不会跟随重定向并转发 Authorization。

## 端点

| 方法与路径 | 请求 | 返回 |
| --- | --- | --- |
| `GET /health` | 无鉴权 | `{status:"ok"}`，仅进程健康 |
| `GET /v1/models` | Bearer | 三个别名：`muse-default`、`muse-image`、`muse-video` |
| `POST /v1/chat/completions` | Chat JSON | Chat Completion 或 SSE |
| `POST /v1/images/generations` | 媒体 JSON、请求 ID | 图片 ID 与 `b64_json` 或鉴权 URL |
| `POST /v1/images/edits` | 同上，至少一张参考图 | 同上 |
| `GET /v1/images/{id}` | Bearer | 原图片任务状态，供断线后只读恢复 |
| `GET /v1/images/{id}/content` | Bearer | 已登记的图片文件 |
| `POST /v1/videos` | 媒体 JSON、请求 ID | HTTP 202，视频任务对象 |
| `GET /v1/videos/{id}` | Bearer | 原任务状态 |
| `GET /v1/videos/{id}/content` | Bearer | 已完成视频文件 |

当前没有 Responses API、文件上传端点、删除或视频取消端点。图片创建仍同步；图片状态查询用于恢复原资源，没有另设 `POST /images/.../async`。

## 请求与媒体大小限制

以下是本项目当前兼容层的限制，不代表 Muse 官方网页或其他 SDK 的额度：

- 图片使用 PNG、JPEG、GIF 或 WebP 的 Base64 data URL；单图解码后的原始文件最多 **10 MiB**，每次请求最多 **4 张**，图片原始文件合计最多 **24 MiB**。Chat 图片与图片／视频参考图均受这些限制。
- 完整 UTF-8 JSON 请求体最多 **32 MiB**，包含提示词、所有字段和 Base64 膨胀后的图片数据。因此原始图片合计恰好 24 MiB 时，也可能因编码与 JSON 开销超限；应缩小图片并留出余量。
- 服务端还校验图片格式与尺寸，每张图片最多 **64,000,000 像素**。便携客户端预检文件大小、数量、合计和 JSON 体积，像素与实际图像解码仍以服务端校验为准；预检失败时不创建生成回执或发送 POST。
- 当前固定 SDK 的单媒体取回上限为 **512 MiB**。本项目的图片内联 `b64_json` 仅接受原始文件不超过 **32 MiB**；便携客户端的单次 JSON 响应和完整内容下载各限制为 **64 MiB**。这些是三个不同限制，不能互相替代。已有原资源 ID 时可通过鉴权内容 GET 下载；文件超过便携客户端上限时，改用具备完整传输校验的下载工具读取原 ID，不重新生成。这些上限不表示已支持音频、PDF 或已有视频输入。

## 文字与图片理解

最小文字请求：

```json
{
  "model": "muse-default",
  "messages": [{"role": "user", "content": "只回复 OK"}],
  "stream": false
}
```

`content` 可以是字符串，或由 `text` 与 `image_url` 项组成的数组：

```json
{
  "model": "muse-default",
  "messages": [{
    "role": "user",
    "content": [
      {"type": "text", "text": "描述这张图片"},
      {"type": "image_url", "image_url": {"url": "data:image/png;base64,<图片数据>"}}
    ]
  }],
  "stream": false
}
```

上述 `<图片数据>` 必须替换为实际 Base64。为避免手工拼接，可调用配套客户端：

```sh
python skills/muse2api-client/scripts/muse_client.py models
python skills/muse2api-client/scripts/muse_client.py chat '只回复 OK'
python skills/muse2api-client/scripts/muse_client.py chat '描述这张图片' --image ./reference.png
```

设置 `stream:true` 时返回 SSE，消费者应完整读到结束标记。HTTP 200 或收到一个 chunk 均不代表整段回复完成。当前便携客户端的 `chat` 使用同步模式；需要 SSE 的应用可直接请求 HTTP 接口。

本版没有暴露原生会话 ID。提供多个 `messages` 是文字历史重放，不能据此宣称原生会话、人物声线或生成任务之间已经绑定。未实现的 `tools`、音频、PDF、已有视频及 sampling 字段会被拒绝，不会被静默忽略。

## 图片生成与编辑

图片生成请求：

```json
{
  "model": "muse-image",
  "prompt": "一只黄色纸船放在白纸上，俯视",
  "n": 1,
  "response_format": "b64_json"
}
```

需要参考图时增加 `reference_images:["data:image/png;base64,<图片数据>"]`。`/v1/images/edits` 使用同一 JSON 合同，要求至少一张参考图。当前不接受 multipart 文件或远程 URL。

`n` 只能为 1。`response_format` 为 `b64_json` 或 `url`，默认前者。典型响应：

```json
{
  "id": "image-<服务生成的标识>",
  "created": 0,
  "data": [{"b64_json": "<图片数据>"}]
}
```

URL 模式返回相对业务路径 `/v1/images/{id}/content`，下载需要同一实例的有效 API Key；不要把上游 Key 发给中转站最终用户来修补下载链路。

```sh
python skills/muse2api-client/scripts/muse_client.py image '一只黄色纸船放在白纸上，俯视' --request-id demo-image-001
python skills/muse2api-client/scripts/muse_client.py edit '保持主体，把背景改为浅蓝色' --reference ./reference.png --request-id demo-edit-001
```

便携客户端的 `edit` 子命令调用编辑端点，`image --reference` 调用带参考图的生成端点。客户端将 Base64 保存成实际文件，只打印资源 ID、路径、大小和摘要，不向聊天输出整段 Base64。若同步图片请求断开或返回未知送达，先从回执取得原图片 ID，再用 `status` 和 `download` 只读恢复；错误响应可能在顶层提供这个 ID。

## 视频：创建一次，持续查询原 ID

```json
{
  "model": "muse-video",
  "prompt": "生成一个简短视频：一只纸船在平静水面漂过，固定镜头",
  "n": 1
}
```

可以附相同格式的 `reference_images`。返回 HTTP 202 与 `id`、`object:"video"`、`model`、`status`、`created_at`、`progress`。状态为 `queued`、`in_progress`、`completed`、`failed` 或 `unknown`；完成后可返回实际 `seconds/width/height` 和下载路径。这些返回测量值不是输入控制参数。

```sh
python skills/muse2api-client/scripts/muse_client.py video '生成一个简短视频：纸船在平静水面漂过' --request-id demo-video-001
python skills/muse2api-client/scripts/muse_client.py status video-实际返回的ID
python skills/muse2api-client/scripts/muse_client.py wait video-实际返回的ID --timeout 900
python skills/muse2api-client/scripts/muse_client.py download video-实际返回的ID --output ./paper-boat.mp4
```

把示例资源 ID 替换为真实响应值。生成并发为 1；不要为同一作品并发创建多个任务。提示词写“3 秒”“竖屏”或“保持同一声音”属于软要求，完成后用实际文件检查尺寸、时长、音轨和内容。

## 幂等、未知送达与下载

图片和视频 POST 应显式设置稳定的 `Idempotency-Key`；服务在它缺失时才使用 `X-Request-ID` 作兼容后备。两个头可以不同，追踪用的 `X-Request-ID` 不改变已提供的幂等键。同一个幂等键与同一个规范请求体映射到原资源，不同请求体复用同一幂等键返回 409。不要依赖中转站每次自动新生成的追踪 ID 防止重复制作。

便携客户端在 POST 前写本地回执，默认目录为使用者自己的 `~/.muse2api-client/`。可用全局参数 `--state-dir` 显式指定其他目录，参数放在子命令前。回执保存请求 ID、Base URL、请求体摘要与资源 ID，不保存 Key 或提示词原文。

```sh
python skills/muse2api-client/scripts/muse_client.py receipt demo-video-001
```

超时、断线、5xx、`unknown`、`failed` 或等待中断均不自动重新 POST；不要换 request-id、Key、Base URL 或清空回执来绕过未知状态。有资源 ID 时继续查询或下载原资源；没有 ID 时保留回执交给该实例维护者定位。重启会将未完成的持久任务标为 `unknown`，不会自动重建生成。

`status`／`wait` 会仅在自己的 `state-dir/receipts` 内，按精确 Base URL 和资源 ID 更新已有回执，记录上游状态与观测时间。完整下载校验成功后，当前 `state` 更新为 `completed`，并追加交付路径、长度与摘要；原提交状态和错误仍保留在 `submission_state`、`submission_error`、`submission_http_status` 等字段。`submission_evidence_captured` 标记原提交证据已经一次性固定；原 POST 未返回状态时，`submission_status` 保留为 `null`，不会由后续 GET 的 `completed` 回填。查询成功与下载交付分别记录，`delivery_verified` 只有完成内容校验后才为 true。没有匹配回执时不伪造创建记录，也不会跟随回执符号链接。

下载只接受完整 HTTP 200 且无 `Content-Range`，核对长度并输出 SHA-256。失败保留 `.part-*`，不发布最终文件；恢复下载仅重试 GET。客户端使用同目录原子发布，目标文件存在时拒绝覆盖；文件系统不支持原子发布时保留临时文件并报错，可换到支持的目录后下载同一资源 ID。

## 错误

错误响应为：

```json
{"error":{"message":"固定错误说明","type":"错误分类","code":"错误分类","delivery_unknown":true}}
```

错误文案不回传上游 Cookie、令牌、完整请求或账号标识。`delivery_unknown:true` 表示不能据此再次创建；`false` 也不是客户端自动补发许可。媒体下载、原任务查询和新的生成是不同操作。
