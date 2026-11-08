# Android app list helper（固定制品）

`applist.dex` 是设备服务用于只读应用发现的小型 `app_process` 助手。它在当前
用户下通过 `PackageManager.queryIntentActivities(MAIN/LAUNCHER)` 读取可启动应用，
按本地化标签排序，并以每行 `package<TAB>base64(UTF-8 label)` 输出。

- 入口：`com.mobileuse.applist.AppList`
- 远端路径：`/data/local/tmp/mobile-use-applist.dex`
- SHA-256：`63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8`
- 源码：`src/com/mobileuse/applist/AppList.java`（与本制品同提交）
- 构建：`scripts/build-applist.ps1`（本机 Android SDK/JDK；需要 `ANDROID_HOME`）
- 运行身份：`shell`；不需要 APK，也不依赖控制 App 的包可见性声明
- 标签是用户可见内容，服务不得把标签或包名写入诊断日志

本制品不随网络下载：部署前由 `scripts/deploy.ps1` 推送到服务目录，服务启动后
按固定哈希校验并按需懒推送。替换制品必须同时更新哈希、源码与本说明。
