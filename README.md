# 掌弦 · PalmCadence

给 Palm webOS 做的多音源播放器。在同一界面切换 QQ 音乐、网易云音乐、酷我、酷狗和 Apple Music，搜索歌曲和歌手。

## 音源

| 音源 | 0.1.0 实测 |
| --- | --- |
| 网易云音乐 | 搜索，播放平台返回的音频 |
| QQ 音乐、酷我、酷狗 | 搜索，打开原曲页面。测试曲目未返回可播地址 |
| Apple Music | 搜索，曲目详情页试听，打开官方曲目页面 |

## 启动

需要 Node.js 22 或更新版本。

```sh
npm ci
npm start
```

打开 `http://127.0.0.1:8787` 就能在电脑上试用。

## 装到 webOS

界面覆盖 Pre、Pre 2、Pre 3、Pixi、Veer 的三种尺寸（320×400、320×480、480×800），客户端以 webOS 1.4.5 为兼容目标。

用 [Palm SDK](https://sdk.webosarchive.org/) 打包和安装。真机安装前开启开发者模式。

```sh
palm-package --use-v1-format web
palm-install org.palmcadence.app.player_0.1.0_all.ipk
```

真机和电脑连同一个 Wi-Fi，在电脑运行 `HOST=0.0.0.0 npm start`，点 App 右上角「连接」，填 `http://电脑的局域网地址:8787`。使用默认 QEMU 用户模式网络的模拟器可填 `http://10.0.2.2:8787`。

系统音源请求由电脑上的服务完成，老 webOS 通过 HTTP 接收搜索结果和音频。Apple 曲目搜索默认使用美区目录，可用 `APPLE_STOREFRONT` 切换。

## 参考

[AlgerMusicPlayer](https://github.com/algerkong/algermusicplayer) · [Folia](https://github.com/chthollyphile/folia-major) · [见音](https://github.com/qianqianhhh2/jianyin) · [Meting](https://github.com/metowolf/Meting)

代码采用 [MIT](LICENSE) 许可。iTunes 徽标由 Apple 提供。
