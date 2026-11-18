# 候选验证记录

日期：2026-11-18。此记录区分源码／合成测试与真实上游验收。

## 已执行

| 范围 | 结果 | 实际覆盖 |
| --- | --- | --- |
| Go adapter | 19 个回归通过，`go vet` 通过 | fake backend 覆盖双 listener、管理 Origin、一次性码、AES-GCM／重启、Cookie 旋转、鉴权、SSE、幂等、unknown、worker 与下载资源边界；另含 2 个真实 TCP 延迟／缺失请求体检查 |
| 本机脚本与发布门禁 | 14 个 Python 检查通过 | 私密配置不覆盖、literal env、实际二进制空状态启动、真实 loopback 请求、文档链接、发布扫描绕过回归 |
| 便携客户端 | 23 个合成检查通过 | 502 保留原 ID、只读恢复、回执提交证据、完整下载、不覆盖、代理／重定向、输入上限与不重发 |
| 浏览器扩展 | 4 个 Node 检查及 popup 内存 mock 场景通过 | 精确 Muse origin、拒绝隐私窗口、三 Cookie 名、loopback 目标、输入过滤；没有读取真实浏览器数据 |
| Skill | 两份元数据校验通过，独立 forward-test 通过 | 合成视频创建一次再 GET 下载，旧 502 图片仅 GET 恢复；原提交证据不会被后续 completed 覆盖 |
| 构建 | Windows 实际进程 smoke 与 Linux amd64 交叉编译通过 | 启动和编译能力，不代表真实账号或 Linux 运行验收 |

源码候选按明确白名单组装，检查 Git 实际暂存的 blob，而不是只查工作目录。发布门禁拒绝真实环境、运行目录、会话／设备身份、日志、媒体、二进制、软链接或 junction，并检查常见密钥格式、个人路径与非示例账号信息。发布前还要核对最终 commit 和源码 ZIP；扫描通过不能取代人工内容及许可复核。

## 尚未执行

- 在真实 Chrome／Edge 安装扩展、授权真实 Muse 账号及浏览器 CORS 验收。
- 用本候选完成真实上游文字、图片、视频和重启后推理。
- Docker 镜像构建、Compose 运行及真实 Caddy／TLS 部署。
- 真实 Sub2API / New API 文字与媒体链路，以及多个调用者的资源隔离。

以上未验收项不能由 mock、模型列表、ready 或加密文件存在来替代。需要真实验收时，使用使用者自己的私密配置与账号，显式授权一次作品创建并保留原 request-id。CI 不配置上游账号，也不创建真实媒体。

## 复现离线检查

```sh
go test ./... -count=1
go vet ./...
python -B -m unittest discover -s tests -v
python -B -m unittest discover -s skills/muse2api-client/scripts -p test_muse_client.py -v
node --test browser-extension/auth.test.mjs
python -B scripts/audit_release.py
```

最后一项读取当前 Git index；空 index 不会得到通过结果。审计输出只含文件、行号和规则类别，不打印被匹配的凭据。私密附加规则与审核清单应写在源码仓库之外；不要把运行回执或机器私密扫描规则加入 Git。
