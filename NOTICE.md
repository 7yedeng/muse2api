# 来源、致谢与许可边界

核对日期：2026-11-18。

本项目独立编写 HTTP 适配、配置、会话存储、浏览器授权交接、部署示例和操作 Skill。根 LICENSE 适用于本项目贡献者有权许可的原创内容，不重新授权第三方作品，不授予 Muse / Meta 的商标或账号访问权。本项目不是 Meta 官方服务。

本项目不隶属于 Meta，未获得 Meta 对本项目的官方认证或背书。代码许可不授予 Meta 服务自动化、账号共享、额度转售或商业运营的权限；具体服务权益由适用的官方授权和条款决定。个人学习和自用是项目定位，不增设 MIT 的非商业限制，也不构成适用条款的豁免。详见 [项目定位与使用说明](docs/responsible-use.md)。

## 感谢玉米须分享的开源项目

本项目的 Muse 网页协议接入参考并使用了 [CoolkHz/muse-protocol-go](https://github.com/CoolkHz/muse-protocol-go)。感谢玉米须的开源分享，相关发布见[原帖](https://x.com/0xraco/status/2116108328925622385)。代码仓库与分享来源分别保留署名，未据此认定不同平台账号的身份一致。

SDK 固定版本：`573014e7fca56a223b58392ff19cca087e0816d0`，Go pseudo-version `v0.0.0-20261030101003-573014e7fca5`。SDK 通过 Go 模块依赖下载，不在本仓库 vendor 其源码。

- [SDK LICENSE](https://github.com/CoolkHz/muse-protocol-go/blob/573014e7fca56a223b58392ff19cca087e0816d0/LICENSE) 为 MIT，版权人为 `2026 CoolkHz and contributors`。
- [SDK 第三方声明](https://github.com/CoolkHz/muse-protocol-go/blob/573014e7fca56a223b58392ff19cca087e0816d0/THIRD_PARTY_NOTICES.md) 明确保留两条更早参考来源，并表示其 MIT 仅覆盖有权许可的贡献，不证明全部实现独立创作。
- [minhquan130599/MuseAI-API](https://github.com/minhquan130599/MuseAI-API/blob/bf964f7c022f11a3574e2e17a8ec57e7c9b0e596/README.md#L328-L330) 在本次核对时明确尚未选择许可证。
- `its-benjamin/muse2api-http` 在本次 GitHub API 核对返回 404，当前许可无法确认。

因此不能将整条依赖来源链宣称为“全部 MIT 已清权”。外部依赖形式减少本仓库直接分发的第三方内容，不消除 SDK 历史来源的未决问题。计划二进制发行或其他再分发时，应重新核对来源与许可，保留其完整声明。这里不据声明推断发生侵权，也不提供尚未取得的授权保证。

## 其他依赖与研究资料

- SDK 使用的 `golang.org/x/crypto`、`golang.org/x/sys` 采用 BSD-3-Clause；二进制分发需保留相关版权、许可和免责声明。
- 本项目还直接使用 `golang.org/x/sys v0.38.0`（Windows 私密文件权限）和 `golang.org/x/image v0.25.0`（WebP 校验），均采用 Go Authors 的 BSD-3-Clause。许可全文保留在 `LICENSES/`。
- SDK 的 Cacophony Noise 测试向量采用其对应版本的 Unlicense；本项目没有复制这些测试向量。
- [Meta Muse Gadget SDK](https://github.com/facebookincubator/muse-gadget-sdk/tree/6a44074014daad1a623f54e62a169f514741836b) 用于核对官方配对和输入输出边界，许可为 Apache-2.0。本仓库不复制其设备代码、字体、头像或编解码器资源，也不把其配对流程包装成网页登录 OAuth。

使用者自己的配置、API Key、浏览器会话和设备配对文件不属于公开源码，不得随本项目上传或分发。
