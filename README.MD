# root-touch-mapper

UHID 虚拟触屏验证程序 (Go, 运行在 Android, 需要 root)。

参考 `../go-touch-mapper`:

- `hid_touch/hid_touch.ino` — HID 报告描述符与 11 字节触摸报文格式 (单指, Tip Switch + Contact ID + 32bit X/Y + Contact Count), 本程序原样复用;
- `u_input_interface.go` — 原项目用 `/dev/uinput` (evdev 事件) 实现虚拟触屏, 本程序改用内核 UHID: 向 `/dev/uhid` 写 `UHID_CREATE2` 注册一个 HID 设备, 写 `UHID_INPUT2` 上报报文。内核会把设备交给 `hid-multitouch` 驱动, 生成真正的 `/dev/input/eventX`, Android 将其识别为一个 USB 触摸屏。

## 构建

```sh
GOOS=android GOARCH=arm64 go build -o root-touch-mapper_arm64 .
```

## 运行 (adb)

```sh
adb push root-touch-mapper_arm64 /data/local/tmp/
adb shell
su
chmod +x /data/local/tmp/root-touch-mapper_arm64
/data/local/tmp/root-touch-mapper_arm64
```

进入交互后输入 `demo` (两次点击 + 一条滑动), 或手动控制:

```
d <x> <y>                       按下   (0~1 归一化坐标, 屏幕比例位置)
m <x> <y>                       移动
u                               抬起
tap <x> <y>                     单击
swipe <x1> <y1> <x2> <y2> [步数]  滑动
q                               退出
```

启动日志出现 `[uhid] 设备已启动` / `[uhid] 有进程打开了输入设备` 即表示 Android input 系统已接入虚拟设备。

## 验证要点

- `getevent -l` 应出现一个新的 eventX, 名为 "UHID Virtual Touch", 带 ABS_MT_POSITION_X/Y (逻辑范围 0..0x7ffffffe)。
- 坐标用 0~1 归一化, 由程序缩放到逻辑区间; Android InputReader 会把逻辑区间映射到当前 display。

## 已知限制 (验证阶段, 刻意未实现)

- 单指 (与 ESP32 固件一致); 无多指、无分辨率动态调整。
- 坐标不做旋转/映射 (`rotateAbsoluteXY` 等逻辑未搬过来)。
- 需要 root 且内核启用 `CONFIG_UHID` (大部分 Android 内核有); SELinux 若拒绝访问 `/dev/uhid`, 在 Magisk su 环境通常为 permissive 或 `uhid_device` 规则放行, 失败时先看 `dmesg | grep uhid` 与 `logcat -b audit`。

## 手写笔模式 (2026-09 新增)

`./root-touch-mapper pen` 注册虚拟 HID 手写笔 (Digitizer/Pen), 支持悬停光标
(不产生触摸)、压感落笔、侧键、橡皮擦。完整使用手册与已验证行为见
**[STYLUS_CURSOR.md](STYLUS_CURSOR.md)**。

```bash
adb shell "printf 'sleep 2000\nh 0.5 0.5\nsleep 1000\nq\n' | /data/local/tmp/rtm pen"
```
