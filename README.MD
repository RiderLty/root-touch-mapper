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

## 数位板 / 绝对光标实验结论

新增 `tablet`（也可用 `puck`）模式，使用 Digitizer/Puck + 绝对 `ABS_X/Y` + `In Range`，并新增 `relmouse` 作为标准相对鼠标对照。

在 Lenovo TB322FC / Android 15 实测：

- `pen`：Android `Sources: KEYBOARD | TOUCHSCREEN | STYLUS`，悬停产生系统笔光标；绝对坐标有效。
- `tablet`：关键是 Puck 集合必须同时声明 `Tip Switch` 和 `In Range`。最终描述符被 Android 合并为 `Sources: KEYBOARD | TOUCHSCREEN | MOUSE | STYLUS`、`Touch Input Mapper (mode - POINTER)`，并创建标准 `MousePointerControllers`；`ABS_X/Y` 仍是绝对坐标。仅声明 In Range 的早期版本会落入 JOYSTICK 或不产生光标。
- Pointer 模式会保持数位板宽高比；当前设备竖屏 1904×3040，因此实现把 X 逻辑范围设为 0..20518、Y 设为 0..32767，避免 X 轴超出显示区域。换屏幕方向或设备时需要按显示宽高重新计算 X 范围。
- `relmouse`：标准相对鼠标协议，用于验证 Android 鼠标光标路径；它需要相对 `REL_X/Y`，不具备数位板绝对映射语义。

因此当前设备上已经验证两种“绝对值 + 系统光标”协议：`pen` 悬停和 `tablet` Puck。最终的指示器方案可以优先使用 `tablet`（光标工具语义为 MOUSE，坐标绝对），另一个 `touch` 进程发送 `d/m/u` 执行真正触摸。两台 UHID 设备可以并行存在；虽然名称和物理位置相同，Android 会按 HID 描述符拆成独立 EventHub 设备。

新增 `composite` 实验：同一 UHID 描述符同时放入触摸屏 Report ID 1 和 Puck Report ID 2。Android 只创建 `TOUCHSCREEN` 的 DIRECT mapper，忽略 Puck 的 MOUSE/POINTER 集合，不能用一个复合设备同时获得独立系统光标。

两个独立设备并行验证通过：触摸屏按住期间，Puck 仍可移动；触摸释放后，Puck 指针状态仍保持。推荐架构仍是分别运行 `touch` 和 `tablet`。

网页验证：打开 <https://riderlty.github.io/touch-tester/>，`pen h` 应观察到悬停/指针事件，`touch d/m/u` 应观察到触摸事件。系统光标精灵不一定会被网页截图捕获，可同时使用 `getevent -lt` 和 `dumpsys input`。

推荐的并行运行方式（两个 UHID 进程分别占一个终端）：

```sh
# 终端 1：绝对数位板光标
adb shell /data/local/tmp/rtm tablet

# 终端 2：真正触摸
adb shell /data/local/tmp/rtm touch
```

在终端 1 输入 `move 0.8 0.2`，网页会收到 `pointermove`（鼠标工具）；
在终端 2 输入 `tap 0.5 0.5`，网页会收到独立的触摸 `pointerdown/up`。
`dumpsys input` 中前者应显示 `Sources: MOUSE` 和 `Touch Input Mapper
(mode - POINTER)`，后者应显示 `TOUCHSCREEN`。
