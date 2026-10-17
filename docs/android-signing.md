# Android 发布签名

正式 APK 必须不可调试，并使用固定签名；依据 [Android 应用签名说明](https://developer.android.com/studio/publish/app-signing)。私钥丢失或更换会影响后续覆盖升级。当前正式密钥尚未配置；v0.4.0 按用户要求先发布 Windows，再单独附加临时签名的 Android 测试版，不承诺后续覆盖升级。

`packaging/release-targets.json` 明确选择 Android 发布类型：`signed` 必须具备固定密钥，缺失则失败；`preview` 生成一次性 CI 密钥，产物命名为 `MIXED-API-Android-preview.apk`，并在 Release 中说明升级限制。两种类型均执行 APK 签名校验、单元测试、Lint 和模拟器验收；不回退到 Debug 构建。临时私钥不保存在仓库、构建附件或 GitHub Secrets 中。

仓库维护者在 GitHub Actions 配置以下值。密钥只用于非 PR 构建，勿提交到仓库、日志或 Release。

| 设置 | 类型 | 内容 |
| --- | --- | --- |
| `MIXED_API_ANDROID_KEYSTORE_B64` | Secret | 私有 PKCS12 文件的 base64，密钥别名为 `mixed-api` |
| `MIXED_API_ANDROID_KEY_PASSWORD` | Secret | PKCS12 与密钥的相同密码 |
| `MIXED_API_ANDROID_CERT_SHA256` | Variable | 从该密钥的公开证书独立计算的 SHA-256，64 位十六进制，不含冒号 |

维护者必须保留私钥及密码的独立私密备份；GitHub Secret 不提供明文取回。不要每次发布生成新密钥，也不要把 PR 测试密钥设置成正式密钥。旧的 `MIXED_API_ANDROID_DEBUG_KEYSTORE_B64` 不再使用。

本机构建使用环境变量 `MIXED_ANDROID_KEYSTORE`（绝对路径）及 `MIXED_ANDROID_KEY_PASSWORD`，执行 `gradle -p android :app:assembleRelease`。默认版本号来自 VERSION，versionCode 来自 Actions run number；`MIXED_ANDROID_VERSION_CODE` 仅用于指定验收基线的版本号。构建前后都不能输出密钥或密码。

PR 包采用该次运行的临时签名，包括同仓库 PR；不会获得正式密钥。`signed` 模式的非 PR 构建缺少固定密钥、密码或证书指纹时直接失败。`scripts/check-android-apk.py` 检查实际 APK 的应用 ID、版本号、不可调试标记、完整签名和证书指纹，不能只看 Gradle 参数。

CI 使用同一源码生成两个版本号不同的不可调试包：先安装较低版本、写入合成账号，再覆盖安装目标包，检查配置及 Android Keystore 账号仍可读取；之后验证通知停止、重启、注销和目标包全新安装。只有临时模拟器使用 root 读取自己的测试配置；用户设备和应用运行不需要 root。此检查证明当前实现的覆盖升级行为，不证明历史版本迁移、真实账号或实体手机已验收。

`android-update-test` 仅为验收材料，不进入 Release；发布先收集通过门禁的 `*-release` 资产，再单独上传明确标注的 Android 测试版并更新 SHA256SUMS。新签名无法替代旧 v0.3.1 的随机 Debug 私钥，旧包切换限制见[使用指引](quickstart-zh.md)。
