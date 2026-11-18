# 调用参考

将 `scripts/muse_client.py` 解析为本 Skill 自身目录下的脚本。下面用仓库相对路径演示；安装为 Skill 后不要求用户保留原仓库。

配置仅用调用者自己的 `MUSE2API_BASE_URL` 与 `MUSE2API_API_KEY`。默认 Base URL 为本机 `http://127.0.0.1:18688/v1`；远程必须 HTTPS。客户端不会跟随重定向转发 Key。

## 文字与图片

```sh
python scripts/muse_client.py models
python scripts/muse_client.py chat '只回复 OK'
python scripts/muse_client.py chat '描述这张图片' --image ./reference.png
python scripts/muse_client.py image '纸船放在白纸上，俯视' --request-id paper-image-001
python scripts/muse_client.py edit '保持主体，把背景改为浅蓝色' --reference ./reference.png --request-id paper-edit-001
```

图片生成默认要求 `b64_json`，客户端将内容保存为文件并输出路径、字节数和 SHA-256；不要将整段 Base64 放到聊天。图片与视频的 `reference_images` 由用户指定的本地图片生成 data URL，不抓取其他目录。

Chat 的 `--image` 和生成命令的 `--reference` 均最多 4 张图片，每张原始文件不超过 10 MiB，原始文件合计不超过 24 MiB；支持 PNG、JPEG、GIF、WebP。完整 UTF-8 JSON 请求最多 32 MiB，Base64 膨胀与提示词也计入，因此应给原始图片合计限制留出编码余量。客户端在生成回执创建前检查这些限制。服务端另校验实际图片解码与每张最多 64,000,000 像素；客户端不把文件扩展名预检当成完整图像验收。

当前固定 SDK 取回单个媒体文件最多 512 MiB；服务端图片内联 Base64 仅接受原始文件最多 32 MiB；本客户端完整下载与单次 JSON 响应各限制为 64 MiB。已经有原资源 ID 时可通过内容 GET 取回文件，超过本客户端上限时使用具有完整传输校验的下载工具读取同一资源，不因响应太大重新生成。这些是不同层的限制，不能推断支持音频、PDF 或已有视频输入。

## 视频

```sh
python scripts/muse_client.py video '生成一个简短视频，纸船在平静水面漂过' --request-id paper-video-001
python scripts/muse_client.py status video-实际ID
python scripts/muse_client.py wait video-实际ID --timeout 900
python scripts/muse_client.py download video-实际ID --output ./paper-boat.mp4
```

将资源 ID 换成创建响应里的实际值。图片状态同样可用 `status image-实际ID`；图片内容可用 `download image-实际ID --output ./picture.png`。等待仅轮询原任务；结束状态未知或失败时停止创建动作。

## 回执与下载

```sh
python scripts/muse_client.py receipt paper-video-001
python scripts/muse_client.py --state-dir ./private-client-state receipt paper-video-001
```

`--state-dir` 是全局参数，放在子命令前。默认回执与图片输出在使用者自己的 `~/.muse2api-client/`。回执不保存 Key 或提示词原文；仍属于使用者运行状态，不进入公开仓库。

客户端在 POST 前独占创建回执。重复执行同一创建命令会拒绝再次提交；换 Key 也不能绕过同端点和 request-id 的本地回执。服务器返回带资源 ID 的错误时，先保留该 ID 再进行只读查询。

`status`／`wait` 将最新上游状态和观测时间写回精确匹配的已有回执；完整下载后写入交付路径、长度和摘要，当前 `state` 为 `completed`。原 POST 的未知状态或 502 仍保留在 `submission_*` 字段，并用 `submission_evidence_captured` 一次性固定；原 POST 没有状态时 `submission_status` 为 `null`，后续 GET 状态不会变成原提交证据。后续完成不会允许再次创建。`delivery_verified` 区分“上游报告已完成”和“内容已完整取回”。只在本 Skill 指定的 state 目录操作，不递归搜索用户目录，不跟随回执符号链接；没有匹配回执也不创建一条假的提交记录。

媒体下载只接受完整 HTTP 200 且没有 `Content-Range`，校验长度并输出 SHA-256。`.part-*` 不等于完整交付，失败不发布最终文件；恢复下载只发 GET。目标文件存在时拒绝覆盖，避免误写用户文件。文件系统不支持原子发布时，改用支持的目录下载同一资源。

成功后报告实际文件路径、尺寸／时长／音轨等已经执行的检查。若未解码或未实听，明确保留该验收缺口。不得使用旧示例素材当作本次生成结果。
