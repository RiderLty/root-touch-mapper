# 虚拟手写笔光标 (pen 模式) 使用手册

面向操作者（人或 AI）的完整说明：如何用本工具在 Android 设备上模拟一支
HID 手写笔，实现**虚拟光标指示**（悬停移动，不产生触摸）以及落笔交互。

所有内容均在测试设备 **Lenovo TB322FC, Android 15 (<device-ip>:5555,
adb over TCP, 无需 root, /dev/uhid 组权限)** 上实测验证 (2026-09-23)。

---

## 1. 部署

```bash
GOOS=linux GOARCH=arm64 go build -o root-touch-mapper_arm64 .
adb connect <device-ip>:5555
adb -s <device-ip>:5555 push root-touch-mapper_arm64 /data/local/tmp/rtm
adb -s <device-ip>:5555 shell chmod +x /data/local/tmp/rtm
```

启动 pen 模式（交互式 REPL，每行一条命令）：

```bash
adb -s <device-ip>:5555 shell /data/local/tmp/rtm pen
```

脚本化（推荐，EOF 自动退出）：

```bash
adb -s <device-ip>:5555 shell "printf 'sleep 2000\nh 0.5 0.5\nsleep 1000\nq\n' | /data/local/tmp/rtm pen"
```

> 开头 `sleep 2000` 必要：等 Android input 系统接入虚拟设备（日志出现
> "有进程打开了输入设备" 即就绪）。

---

## 2. 命令一览 (pen 模式)

| 命令 | 作用 |
|---|---|
| `h <x> <y>` | **悬停移动**（纯光标指示，不产生触摸）x/y 为 0~1 浮点 |
| `d <x> <y> [p0~1]` | 按下（默认压感 0.5） |
| `m <x> <y> [p0~1]` | 按下状态移动（拖动） |
| `u` | 抬笔（先 hover 一帧再离场，符合规范状态机） |
| `tap <x> <y>` | 单击（按下 60ms → 抬起） |
| `b` | 切换侧键 (Barrel Switch) 状态 |
| `line <x1> <y1> <x2> <y2> [ms]` | 1000Hz 匀速悬停画线（默认 3000ms）；**1/3 处自动按下，2/3 处自动抬起** |
| `circle` | 诊断：悬停画 3 圈圆（顶部进、发送端顺时针） |
| `diag` | 诊断：沿两条对角线慢速悬停，3 遍 |
| `demo` | 压感渐变弧线 |
| `raw <hex>` | 直接发送原始报文字节（见 §4） |
| `sleep <ms>` | 停顿（脚本化用） |
| `q` | 退出并注销设备 |

`touch` 模式（`./rtm touch`）命令不变：`d/m/u/tap/swipe/demo/raw/sleep/q`。

---

## 3. 已验证的关键行为

| 现象 | 结论 |
|---|---|
| Android 识别 | `goodix_ts` → Sources: **KEYBOARD \| TOUCHSCREEN \| STYLUS**，`IsExternal: false`（I2C 伪装生效） |
| 悬停 | 内核出 `BTN_DIGI`(=BTN_TOOL_PEN) + ABS_X/Y，**无 BTN_TOUCH**；应用收到 ACTION_HOVER_MOVE |
| 悬停指针图标 | 系统在悬停时渲染笔形/圆点光标；**落笔时图标自动隐藏**（笔尖即指针，标准行为，非 bug） |
| 落笔 | `BTN_TOUCH` DOWN + `ABS_PRESSURE`（值 = 压感 × 1023） |
| Chrome 网页 | 悬停触发 `pointermove`，`pointerType=pen, pressure=0`；落笔触发 pointerdown/up，pressure 正确 |
| 点击/拖动 | 与真实触摸等价，应用正常响应 |
| 滑动中抬笔再按下 | 正常断开为两段笔画（画图应用可见两段独立墨迹） |

**坐标系**：与虚拟触屏完全一致（用户 2026-09-23 实测 circle 诊断 = 恒等映射）。
注意：映射与屏幕旋转状态相关，历史实测曾在横屏下观察到 x/y 互换。
换设备/换旋转方向后，先跑一次 `circle`：光标从圆顶部出现且顺时针 = 恒等；
其它起点/转向按下表修正：

| 屏幕上起点 | 转向 | 映射 |
|---|---|---|
| 顶 | 顺时针 | `(x, y)` 恒等 |
| 顶 | 逆时针 | `(x, 1-y)` |
| 左 | 顺时针 | `(y, 1-x)` |
| 左 | 逆时针 | `(y, x)` |
| 底 | 顺时针 | `(1-x, 1-y)` |
| 底 | 逆时针 | `(1-x, y)` |
| 右 | 顺时针 | `(1-y, x)` |
| 右 | 逆时针 | `(1-y, 1-x)` |

---

## 4. HID 报文格式（7 字节，无 Report ID）

描述符见 `pen.go`（Digitizer/Pen + Stylus 物理集合，符合 MS 手写笔规范）。

```
字节0  bit0 Tip Switch | bit1 Barrel | bit2 Invert | bit3 Eraser
       bit4 In-Range  | bit5..7 padding
字节1-2  X   uint16 LE, 0..32767
字节3-4  Y   uint16 LE, 0..32767
字节5-6  Tip Pressure uint16 LE, 0..1023
```

常用测试字节（`raw` 命令直接喂，X/Y 用真实坐标）：

```
10000000000000    悬停 真实(0,0)            [in-range=0x10]
10ff7fff7f0000    悬停 真实(32767,32767)     [0x7FFF = ff 7f LE]
10ff7f00000000    悬停 真实(32767,0)
100000ff7f0000    悬停 真实(0,32767)
11400040000002    落笔 中心(16384) 压感512    [tip+in-range=0x11]
13400040000002    落笔+侧键 中心
10400040000000    悬停 中心
00400040000000    离开感应范围 (in-range=0, 重复最后坐标)
```

## 5. 可扩展的上报参数（当前未实现，需要时加进描述符）

| 参数 | Usage | Android 效果 |
|---|---|---|
| Secondary Barrel Switch | 0x5A | `BUTTON_STYLUS_SECONDARY` 双键笔 |
| X-Tilt / Y-Tilt | 0x3D / 0x3E | `AXIS_TILT` 侧锋笔触（压感之外的第二笔锋维度） |
| Twist | 0x41 | 绕笔轴旋转 |
| Width / Height | 0x48 / 0x49 | `AXIS_TOUCH_MAJOR/MINOR` 接触椭圆（不改光标图标大小） |
| Z Distance | GD页 Z | `AXIS_DISTANCE` 离面高度（不改系统光标大小） |
| Scan Time | 0x56 | 报文时间戳，高回报率防抖 |

系统指针图标是静态位图，任何上报参数都不能改变其大小。

## 6. 给 AI 操作者的注意事项

1. **光标指示用 `h`（悬停），不要用 `d`** —— 按下会产生真实触摸交互且指针图标消失
2. 高回报率移动用 `line`（内部按 1ms/报文时间基准对齐）；手动描点用多条 `h` + `sleep 100`
3. 设备息屏会让一切"看不见"：先 `input keyevent KEYCODE_WAKEUP` + `wm dismiss-keyguard`
   （截图验证时加 `svc power stayon true`，但 WiFi adb 下它不保证有效）
4. 验证事件流（不靠眼睛）：`adb shell timeout 5 getevent -t -l /dev/input/eventN`
   （N 用 `dumpsys input | grep goodix` 找，本次是 event11）
5. 浏览器可视化：`screenshots/cursor.html`（HTTP 任意端口托管后
   `am start -a android.intent.action.VIEW -d http://<host>:<port>/cursor.html`），
   悬停画青色光标圈、按下画墨迹（宽度=压感）
6. `screencap` 抓不到系统指针图标（精灵层），屏幕验证要么人眼看，要么用
   cursor.html / 指针位置 overlay（`settings put system pointer_location 1`，
   用完 `settings put system pointer_location 0`）
7. 一次进程只注册一台虚拟设备；`q` 退出即注销。重复注册同名设备会并存，
   注意先退出旧会话
