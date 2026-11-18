---
name: muse2api-client
description: 使用调用者自己的 Muse2API Base URL 和 Key 完成文字、图片理解、图片生成编辑及视频任务查询下载；适用于已授权 API 调用，不管理服务器或上游登录会话。
---

# Muse2API 调用

从调用者已配置的 `MUSE2API_BASE_URL` 和 `MUSE2API_API_KEY` 使用其自己的端点。缺少凭据时让用户在本机环境或其密钥管理方式配置，不要求将 Key 发到聊天，不搜索浏览器、SSH、原维护者文件或整个用户目录。

按 [调用参考](references/usage.md) 选择所需操作。优先用本 Skill 的 `scripts/muse_client.py`；它是标准库客户端，不依赖仓库的绝对路径。先确认目标端点和用户本次任务，`models` 只读，媒体生成每个作品只创建一次。

图片／视频必须显式给稳定 request-id。创建前保存本地回执；超时、断线、5xx、`unknown`、`failed` 或等待中断都不自动重新 POST，不通过换编号、Key、Base URL 或清空回执绕过未知结果。已有资源 ID 只查、等或下载原资源；没有 ID 保留回执交给实例维护者定位。

只使用用户明确提供的参考图片。当前合同为文字与图片 data URL；未开放的 PDF、音频、已有视频、tools 或 `seconds/size/seed` 不得伪装成支持。提示词要求必须用实际输出验收。

下载核对完整 HTTP 200、长度和摘要；失败仅恢复 GET。报告实际文件与技术检查，不把音轨存在、ASR 文本或同一提示词当作音色一致性。本文不包含网站后台管理、账号开通、服务器登录或 Cookie 导入权限。
