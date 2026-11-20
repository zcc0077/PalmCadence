# 掌弦 · PalmCadence

给 Palm webOS 做的多音源播放器。搜歌、切换平台，也可以边浏览边播放。缩成卡片或切换应用时会保留播放状态。

点应用右上角的「设置」，也可以从 webOS 左上角的应用菜单进入。可设置播放顺序、定时停止、启动音源、Apple Music 地区和深浅配色。

支持 Pre、Pre 2、Pre 3、Pixi、Veer 的三种屏幕尺寸。TouchPad 横竖屏使用双栏，搜索结果和当前曲目同时显示。

## 界面截图

按对应分辨率在浏览器截取。

| 320×400 · Veer / Pixi | 320×480 · Pre / Pre 2 | 480×800 · Pre 3 |
| --- | --- | --- |
| <img src="screenshots/320x400.png" width="180" alt="Veer 和 Pixi 上的搜索与播放界面"> | <img src="screenshots/320x480.png" width="180" alt="Pre 和 Pre 2 上的搜索与播放界面"> | <img src="screenshots/480x800.png" width="240" alt="Pre 3 上的搜索与播放界面"> |

| 768×1024 · TouchPad 竖屏 | 1024×768 · TouchPad 横屏 |
| --- | --- |
| <img src="screenshots/768x1024.png" width="320" alt="TouchPad 竖屏双栏界面"> | <img src="screenshots/1024x768.png" width="420" alt="TouchPad 横屏双栏界面"> |

| Pre 3 · 浅色设置 | TouchPad · 深色设置 |
| --- | --- |
| <img src="screenshots/settings-480x800.png" width="240" alt="Pre 3 上的浅色设置和底部播放控件"> | <img src="screenshots/settings-1024x768.png" width="420" alt="TouchPad 横屏双栏设置"> |

## 音源

| 音源 | 支持情况 |
| --- | --- |
| 网易云音乐、酷我、酷狗 | 搜索，播放平台提供的游客音源 |
| QQ 音乐 | 搜索，打开原曲页面。测试曲目未返回游客音源 |
| Apple Music | 搜索，播放 iTunes 提供的试听，可切换地区 |

## 安装

把 `org.palmcadence.app.player_0.3.0_all.ipk` 通过 [Preware](https://github.com/webos-internals/preware) 安装到 webOS。IPK 内含 ARM 和模拟器版本，以及处理 HTTPS 的本机组件。设备联网后即可搜索和播放，无需在电脑上启动服务。

## 从源码打包

构建需要 Go、Python 3、`curl`、`patch` 和 `ar`。

```sh
./scripts/package-webos.sh
```

IPK 输出到 `dist/`。打包脚本使用 Go 1.26.11，并加入 Linux 2.6.26 所需的系统调用兼容层。

## 参考

[AlgerMusicPlayer](https://github.com/algerkong/algermusicplayer) · [Folia](https://github.com/chthollyphile/folia-major) · [见音](https://github.com/qianqianhhh2/jianyin) · [Meting](https://github.com/metowolf/Meting)

[MIT](LICENSE) · iTunes 徽标由 Apple 提供。
