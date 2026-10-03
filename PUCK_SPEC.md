# USB 数位板 = Android 绝对鼠标 · 移植规格

日期：2026-10-03　验证平台：Lenovo TB322FC（Android 15，面板 1904×3040）
定位：一个**独立的 tiny USB 设备**（ESP32/RP2040/SAMD 等），烧录本文的 HID 描述符后，
**插上 Android 设备即成为绝对定位的鼠标**——系统出现标准光标，可悬停、点击、拖拽
（已实测可控制游戏视角）。全程**不需要与上位机通讯**：不需要分辨率、不需要方向、
不需要任何配置，描述符固定不变。

参考实现：本仓库 `tablet.go` / `uhid.go`（UHID 传输验证，与真实 USB 走同一内核
hid-generic 驱动栈）；研究数据：`TABLET_REPORT.md`。

---

## 1. 工作原理（两层）

```
┌─ 协议层 ──────────────────────────────────────────────┐
│ HID 数位板: Digitizer/Puck 工具 + 绝对 X/Y + 按键       │
│ Android 识别为 Sources: MOUSE → Touch Input Mapper      │
│ (mode - POINTER) → MousePointerControllers → 系统光标   │
├─ 映射层 ──────────────────────────────────────────────┤
│ Android 对绝对指针做 cover(填充裁切)映射:               │
│   scale = max(屏宽/32767, 屏高/32767)  ← 等比, 两轴共用  │
│   screen = raw × scale                 ← 左上角对齐      │
│   溢出轴超出屏幕的部分不可达(贴边), 不是压扁             │
└───────────────────────────────────────────────────────┘
```

描述符固定为正方形逻辑区间 32767×32767（§3），于是 cover 缩放系数在任何方向下都是
`长边/32767`：**屏幕的长边方向恰好被完整覆盖，短边方向只能用到坐标区的
短边/长边（本设备 62.6%）**。这就是"方形 + 裁切"适配方式的全部原理。

## 2. 关键规则（不可省略，踩坑结论）

1. **Puck 集合必须同时声明 `Tip Switch (0x42)` 和 `In Range (0x32)`**——只声明 In Range
   会被 Android 归入 JOYSTICK 或不产生光标；
2. **必须有 Mouse 按钮集合**，否则没有点击语义；
3. 不要 Report ID；X/Y 为无符号 16 位、小端序；
4. 拖动全程保持 Tip Switch 置位（见 §4），否则丢键。

## 3. HID 报告描述符（最终版，96 字节，固定不变）

```c
// ---- Mouse 集合: 提供左/中/右键 (34 字节) ----
0x05, 0x01,       // Usage Page (Generic Desktop)
0x09, 0x02,       // Usage (Mouse)
0xA1, 0x01,       // Collection (Application)
0x09, 0x01,       //   Usage (Pointer)
0xA1, 0x00,       //   Collection (Physical)
0x05, 0x09,       //     Usage Page (Button)
0x19, 0x01, 0x29, 0x03,                       // Buttons 1..3
0x15, 0x00, 0x25, 0x01,
0x95, 0x03, 0x75, 0x01, 0x81, 0x02,           // 3 bit (Data,Var,Abs)
0x95, 0x01, 0x75, 0x05, 0x81, 0x03,           // 5 bit padding (Const)
0xC0, 0xC0,
// ---- Digitizer/Puck 集合: 绝对坐标 + 工具状态 (62 字节) ----
0x05, 0x0D,       // Usage Page (Digitizer)
0x09, 0x01,       // Usage (Pointer)   [顶层 Application]
0xA1, 0x01,       // Collection (Application)
0x09, 0x21,       //   Usage (Puck)    [内层物理集合]
0xA1, 0x00,       //   Collection (Physical)
0x09, 0x42,       //     Usage (Tip Switch)   <-- 必须声明!
0x09, 0x32,       //     Usage (In Range)     <-- 必须声明!
0x15, 0x00, 0x25, 0x01,
0x95, 0x02, 0x75, 0x01, 0x81, 0x02,           // 2 bit (Data,Var,Abs)
0x95, 0x01, 0x75, 0x06, 0x81, 0x03,           // 6 bit padding
0x05, 0x01,       //     Usage Page (Generic Desktop)
0x09, 0x30,       //     Usage (X)
0x16, 0x00, 0x00, //     Logical Min (0)
0x26, 0xFF, 0x7F, //     Logical Max = 32767
0x75, 0x10, 0x95, 0x01, 0x81, 0x02,           // X: uint16 (Data,Var,Abs)
0x09, 0x31,       //     Usage (Y)
0x16, 0x00, 0x00,
0x26, 0xFF, 0x7F, //     Logical Max = 32767
0x75, 0x10, 0x95, 0x01, 0x81, 0x02,           // Y: uint16 (Data,Var,Abs)
0xC0, 0xC0,
```

平铺字节数组（可直接烧录）：

```c
const uint8_t report_descriptor[96] = {
    0x05, 0x01, 0x09, 0x02, 0xA1, 0x01, 0x09, 0x01, 0xA1, 0x00,
    0x05, 0x09, 0x19, 0x01, 0x29, 0x03, 0x15, 0x00, 0x25, 0x01,
    0x95, 0x03, 0x75, 0x01, 0x81, 0x02, 0x95, 0x01, 0x75, 0x05,
    0x81, 0x03, 0xC0, 0xC0,
    0x05, 0x0D, 0x09, 0x01, 0xA1, 0x01, 0x09, 0x21, 0xA1, 0x00,
    0x09, 0x42, 0x09, 0x32, 0x15, 0x00, 0x25, 0x01, 0x95, 0x02,
    0x75, 0x01, 0x81, 0x02, 0x95, 0x01, 0x75, 0x06, 0x81, 0x03,
    0x05, 0x01, 0x09, 0x30, 0x16, 0x00, 0x00, 0x26, 0xFF, 0x7F,
    0x75, 0x10, 0x95, 0x01, 0x81, 0x02, 0x09, 0x31, 0x16, 0x00,
    0x00, 0x26, 0xFF, 0x7F, 0x75, 0x10, 0x95, 0x01, 0x81, 0x02,
    0xC0, 0xC0,
};
```

## 4. 输入报文格式（6 字节，无 Report ID）

| 字节 | 内容 |
|---|---|
| [0] | 按键位图：bit0=左键，bit1=右键，bit2=中键，bit3..7=0 |
| [1] | bit0=Tip Switch，bit1=In Range，bit2..7=0 |
| [2:4] | X，uint16 小端，0..32767 |
| [4:6] | Y，uint16 小端，0..32767 |

行为语义：

- **悬停移动**：`In Range=1, Tip Switch=0, buttons=0`，更新 X/Y —— 仅移动光标，不产生点击；
- **按下**：置按键位 + `Tip Switch=1`（In Range 保持 1）；
- **拖动**：保持按键位与 Tip Switch 不变，逐帧更新 X/Y；
- **抬起**：清按键位与 Tip Switch，保持 In Range=1 与最后坐标；
- **离场（可选）**：清 In Range，光标从屏幕消失；
- 状态变化即发送一帧；无状态变化不必发送。

## 5. 坐标映射与可视区（使用者必读）

固定正方形区间 + cover 裁切，产生如下**固有行为**（已双向实测验证）：

| 手机方向 | 完整覆盖的轴 | 被裁切的轴 | 可视区 |
|---|---|---|---|
| 竖屏（1904×3040） | Y：0..32767 → 全高 | X：x>0.626 贴右边缘出不去 | X 0..0.626 × Y 0..1 |
| 横屏（3040×1904） | X：0..32767 → 全宽 | Y：y>0.626 贴底边出不去 | X 0..1 × Y 0..0.626 |

实测数据（像素级定位光标，误差 <0.1%）：

- 横屏：(0.25,0.25)→屏幕 (0.25, 0.399)，(0.5,0.5)→(0.5, 0.798)，(0.5,0.7)→出屏——
  `fy = 1.597×y_raw`，**裁切不是压扁**（两轴缩放系数恒等）；
- 竖屏：(0.25,0.25)→(0.399, 0.25)，(0.75,0.25)→贴死右边缘——`fx = 1.597×x_raw`。

对使用者的含义：

1. **板子与手机同向握持**。竖屏时板子右侧约 37% 是死区（坐标发过去光标贴在屏幕右缘），
   横屏时死区转到板子底部——这与真实数位板接非同比例显示器的行为一致；
2. **物理设计建议**：若把面板的有效感应区做成 0.626（短边/长边，即目标屏比例）的矩形、
   长边方向对齐 32767 全程，则有效区内恰好 1:1 精确映射、无死区；
3. 分辨率余量充足：短边方向全屏仍有 32767×0.626 ≈ 20522 个计数，远超屏幕像素密度需求；
4. 若死区不可接受且允许少量通讯，上位机把贴边轴预乘 0.626 即可全屏可达（可选，非必需）。

## 6. 推荐发送节奏（实测手感参数）

- 悬停移动：单帧即达，无需插值；
- 拖动（视角控制/画线）：**先悬停到起点 → 50ms → 按下 → 插值移动 → 抬起**；
- 插值：smoothstep `e = f²(3−2f)`（起停平滑），40 步 × 8ms（125Hz）已流畅，最高 500Hz 稳定；
- 点击：按下保持 60ms → 抬起 → 30ms。

## 7. USB 移植要点（TinyUSB 类设备）

- 类别：HID Generic Input（**不需要** boot protocol 子类）；仅 Input 报文，无 Output/Feature；
- 端点：Interrupt IN，间隔 1ms；125~500Hz 报告率已验证；
- VID/PID 与字符串任意——Android 的设备分类基于**能力集**（Tip Switch/In Range 映射出
  BTN_TOUCH/BTN_TOOL_MOUSE → TOUCH 类 → POINTER 模式），与总线、身份、字符串无关；
- 设备上电枚举后 Android 自动完成注册（<1s），无需任何 App、驱动或配置；
- 固件只需：读坐标/按键 → 按 §4 组包 → 发 Interrupt IN 报文。

## 8. Android 验证清单（移植后逐项确认）

```sh
# 1. 设备出现, 能力集正确 (BTN_MOUSE/BTN_RIGHT/MIDDLE, ABS_X/Y, 以及
#    Tip Switch/In Range 映射出的 BTN_TOUCH/BTN_TOOL_MOUSE)
adb shell getevent -pl

# 2. Android 注册为鼠标 (关键! 应有 "New device: ... classes=TOUCH",
#    随后 InputReader "Device added ... sources=MOUSE")
adb shell logcat -c && 触碰设备 && adb shell "logcat -d | grep -E 'New device|Device added'"

# 3. mapper 状态 (应见 Touch Input Mapper (mode - POINTER), DeviceType: POINTER)
adb shell dumpsys input | grep -A20 <设备名>

# 4. 事件流 (悬停移动应产生 EV_KEY BTN_TOOL_MOUSE + EV_ABS ABS_X/ABS_Y + SYN_REPORT)
adb shell getevent -t -l
```

屏幕位置验证：光标精灵会被 `screencap` 截到（黑底白十字），配深色网页逐点截图定位最可靠；
本设备的 `getevent` 只接受单个 device 参数。

## 9. 参考实现

- 描述符、报文、悬停/点击/拖动/流式轨迹的完整 Go 实现：本仓库 `tablet.go`（`tabletsq`
  实验模式即本文的固定正方形方案，已双向验证）、`uhid.go`（UHID 传输层）；
- 研究过程与全部实测数据（含方向匹配描述符、cover 规则推导）：`TABLET_REPORT.md`。
