# UHID 数位板光标（tablet / Puck）工作原理与系统行为报告

日期：2026-10-03　设备：Lenovo TB322FC（Android 15，竖屏 1904×3040）
对应实现：`tablet.go`（`./root-touch-mapper tablet`，别名 `puck`）

---

## 1. 目标

在 Android 上虚拟出一个**绝对定位 + 系统光标**的输入设备：坐标用 0~1 归一化值直接指定屏幕位置（不是相对移动），同时系统显示标准鼠标指针，支持悬停、左/中/右键点击与拖动。这是触摸映射类工具（画板、远控指针）需要的核心能力。

## 2. 工作原理

### 2.1 协议选型

普通 HID 相对鼠标（`relmouse`）走 `REL_X/REL_Y`，没有绝对映射语义；`pen`（Digitizer/Pen）悬停虽有光标但语义是手写笔。最终选型是 **Digitizer 页的 Puck（光电十字板鼠标）工具**：数位板厂商为 2D 定位设计的经典协议，天然就是"绝对坐标 + 按键 + 系统光标"。

### 2.2 HID 描述符（`tabletReportDesc`）

一个 UHID 设备的描述符里放了两个顶层 Application Collection：

| Collection | Page/Usage | 提供的能力 |
|---|---|---|
| Mouse | Generic Desktop / Mouse (0x01/0x02) | `BTN_LEFT/RIGHT/MIDDLE`（1 字节按键位图） |
| Digitizer/Puck | Digitizer / Pointer (0x0D/0x01)，内层 Puck usage (0x21) | `Tip Switch` + `In Range`（1 字节状态位）、`ABS_X` 0..20518（u16）、`ABS_Y` 0..32767（u16） |

两个集合各配一个字段：Mouse 集合让 Android 把设备当鼠标类工具（产生按键点击语义）；Digitizer/Puck 集合提供绝对轴和工具类型（`toolType=MOUSE`）。

**关键经验（踩坑结论）**：Puck 集合必须**同时声明 `Tip Switch` 和 `In Range`**。早期版本只声明 In Range，会被 Android 归入 JOYSTICK 类或不产生光标。加上 Tip Switch 后才正确落入 Pointer 路径。

**坐标范围是按显示宽高比定制的**：Pointer 模式保持数位板宽高比。若 X、Y 都用 0..32767，竖屏下 X 轴会超出显示区域。因此 X 逻辑最大值取 `20518 = 1904/3040 × 32767`，Y 取 32767。换设备或旋转屏幕后必须按显示宽高重新计算。

### 2.3 报文格式（6 字节）

```
[0]     buttons 位图: bit0=LEFT, bit1=RIGHT, bit2=MIDDLE
[1]     bit0=Tip Switch, bit1=In Range
[2:4]   X  uint16 LE (0..20518)
[4:6]   Y  uint16 LE (0..32767)
```

发送逻辑（`tabletCtl`）：

- `move`：置 In Range、更新坐标，不带按键 —— 悬停移动；
- `press/release`：置位/清位按键；有按键按下期间 Tip Switch 保持置位（拖动不丢键）；
- `leave`：清 In Range，指针"离场"，光标消失；
- `stream`：定时器驱动按指定频率（≤2000Hz）连续插值发送，用于测试高频轨迹。

### 2.4 数据通路（Linux/Android 全链路）

```
rtm 进程
  └─ 写 /dev/uhid: UHID_CREATE2 (伪装 phys="i2c/absolute-tablet", bus=0x0018)
       └─ 内核 uhid → hid-generic → hid-input 解析描述符
            └─ 生成 /dev/input/eventX ("absolute_tablet_puck Mouse")
UHID_INPUT2 写入报文 → 内核解码为 evdev 事件:
  EV_KEY BTN_TOOL_MOUSE DOWN  +  EV_ABS ABS_X/ABS_Y  +  EV_SYN SYN_REPORT
       └─ Android InputReader EventHub 读取
            └─ Touch Input Mapper (mode - POINTER)
                 └─ MousePointerControllers → 系统光标精灵 + 按键点击/拖动
```

伪装点：`name/phys/bus` 由我们自填，内核原样采用；phys 用 `i2c/...` 前缀让 Android 把它当内置外设而非外部 USB 设备。物理层标识与其他模式错开，避免被 EventHub 合并进同一 InputDevice。

## 3. 系统行为表现（dumpsys input 实测）

设备注册后（并发测试中抓取，指针正处于 stream 移动中）：

- **EventHub 层**：`absolute_tablet_puck Mouse`，`Classes: TOUCH`，路径 `/dev/input/event10`，
  `SysfsDevicePath: /sys/devices/virtual/misc/uhid/0018:0000:0000.0137`，
  `KeyState (pressed): BTN_TOOL_MOUSE`，`AbsState: ABS_X/ABS_Y`。
- **InputReader 层**：`Sources: MOUSE`，`Touch Input Mapper (mode - POINTER)`，`DeviceType: POINTER`，`toolType=MOUSE`。
  - 运动范围 `X: 0..1903.9`、`Y: 0..3040.5`、`PRESSURE: 0..1`；
  - 视口 `INTERNAL: 1904×3040`；缩放因子 `0.0928`（= 1904/20518）；
  - `Last Cooked Touch: x=571.1, y=1824.3` —— 与 stream 逻辑坐标 (0.30, 0.60) 精确对应，证明绝对映射正确；
  - pointer 模式未开启 `isHovering` 区分，In Range 即光标在场。
- **事件层（getevent 实测）**：250~500Hz 连续 stream 单次抓取约 12000 条事件，全部为
  `BTN_TOOL_MOUSE` + `ABS_X` + `ABS_Y` + `SYN_REPORT` 序列，无丢包、无乱序。
- **高频能力**：`stream` 最高 2000Hz；实测 250~500Hz 稳定，光标跟随无明显迟滞。

### 3.1 与其他模式的对比

| 模式 | 坐标 | 系统光标 | 真实触摸 | 结论 |
|---|---|---|---|---|
| `touch` | 绝对 | 无（DIRECT） | 有 | 虚拟手指 |
| `pen` | 绝对 | 笔光标（悬停） | 落笔后是笔迹 | 手写笔语义 |
| `tablet`（本报告） | 绝对 | 鼠标光标 | 无 | **指针/映射首选** |
| `drawtablet` | 绝对 | 光标（Pen tool 路径） | 无 | 备选，语义为 STYLUS |
| `composite` | — | — | — | 失败：单 UHID 复合描述符下 Android 只建 DIRECT mapper，忽略 Puck 集合 |
| `relmouse` | 相对 | 鼠标光标 | 无 | 仅作基线对照 |

## 4. 旋转自适应（2026-10-03 补充）

**问题**：横屏下坐标错位。四点截图探针（屏幕 3040×1904）实测定位到规律：InputReader 的 Pointer 模式对绝对指针采用 **cover（填充裁切）语义**——等比缩放，系数 = `max(屏宽÷板宽, 屏高÷板高)`，左上角对齐（无居中偏移），另一轴溢出部分不可达。旧描述符 X:Y = 20518:32767 是竖屏形状，横屏沿用时系数取 max(0.148, 0.058)=0.148，Y 被放大 2.55 倍溢出——y=0.25 落在屏幕 63.7% 高度、y=0.5 压底边、y>0.5 完全出屏。

该规则已用正方形区间描述符（32767×32767，类似 pen）在横竖屏下双向验证：横屏时 Y 溢出贴底边（fy = 1.597×y_raw，y>0.626 出屏）、X 完整；竖屏时 X 溢出贴右边缘（fx = 1.597×x_raw，x>0.626 出屏）、Y 完整。**是裁切不是压扁**——两轴永远保持同一缩放系数。方向感知描述符之所以两个方向都完美，是因为板子宽高比与屏幕当前方向一致，cover 退化为 1:1 精确映射。

**修复**：描述符长短轴按显示方向互换（竖屏 X=20518/Y=32767，横屏 X=32767/Y=20518，比例始终匹配面板 1904×3040），并内置旋转监视（700ms 轮询 `dumpsys display` 的 `mCurrentOrientation`），变化时自动销毁重建 UHID 设备。

**验证**（横屏四点探针，像素级分析光标精灵位置）：

| 目标 (x, y) | 修复后落点 (fx, fy) |
|---|---|
| (0.25, 0.25) | (0.2498, 0.2497) |
| (0.75, 0.25) | (0.7498, 0.2497) |
| (0.5, 0.5) | (0.4998, 0.4997) |
| (0.1, 0.9) | (0.0996, 0.8996) |

全量程 1:1 映射，误差 <0.1%。

**游戏实测**：横屏游戏内，右侧屏幕区域 `drag`（按下-滑动-松开）可控制视角——Puck 光标的按键拖拽被游戏接受。**数位板协议在本设备上可完整充当"绝对鼠标"**：绝对光标 + 悬停 + 点击 + 拖拽。

## 4a. 负面结论：KVM 式绝对鼠标（absmouse）

实现并实测了 KVM/IP-KVM 风格的"绝对鼠标"（`absmouse`：GDMouse 集合 + 绝对 X/Y，无 Digitizer 集合，分别试过 I2C/USB bus）：

- 内核层完全正常：hid-generic 正确生成 evdev 节点（`BTN_MOUSE/RIGHT/MIDDLE + ABS_X/Y`），getevent 抓到全部报文；
- 但 **Android EventHub/InputReader 从不注册该设备**（logcat 无 `New device`，dumpsys 无踪迹，屏幕无光标）——"按键+ABS"既无 REL_X/Y 不算相对鼠标，又无 BTN_TOUCH/BTN_TOOL_* 不算触摸，能力集分类落空被直接丢弃；
- 该结论与传输层无关（UHID/uinput/真实 USB 同理），换 bus 类型（USB 0x03）同样被忽略。KVM 厂商宣称的安卓支持实为相对鼠标模式；
- iPadOS 亦无原生绝对鼠标 HID 通路（指针栈仅相对鼠标/触摸板/AssistiveTouch，WebKit 无 Pointer Lock）。

## 5. 并发工作状态（触摸 + 指针同时运行）

推荐架构是两个独立 UHID 进程并行：`rtm tablet`（光标）+ `rtm touch`（真实触摸）。

2026-10-03 并发实测（tablet stream 8s/500Hz 与 touch 按下拖动 3s 时间重叠）：

- 指针侧：注册正常、事件流完整（~12000 条），dumpsys 中 mapper 状态健康，触摸期间与触摸释放后光标均持续可控；
- 触摸侧：并发轮次中曾观察到 evdev 层零事件的异常，但 logcat 证实设备注册正常（`classes=TOUCH | TOUCH_MT`，`sources=TOUCHSCREEN`，mode DIRECT）；后续单独运行时事件正常（30 条）。异常是否与并发相关待进一步对照实验；
- `composite`（单设备复用）方案已排除，见上表。

## 5. 已知限制

1. 逻辑范围按当前设备面板 1904×3040 定制（`tabletLogicalMaxShort/Long`）；换设备需按新面板宽高比重算。旋转已自动适应（§4）。
2. Puck 语义无压感、无倾斜；需要压感手写笔走 `pen`/`drawtablet`。
3. 需要 `/dev/uhid` 访问权限（root，或 shell 用户在 `uhid` 组——本设备属后者）。
4. 单实例单设备：一个进程一个 UHID，双功能需双进程（composite 已验证不可行）。

## 6. 结论

`tablet` 模式在 Android 15 上打通了完整的"绝对坐标 + 系统鼠标光标"通路：HID Puck 协议 → hid-generic → evdev → Touch Input Mapper (POINTER) → MousePointerControllers。绝对映射精度、事件频率、光标行为均验证通过，**并经游戏实测可作为"绝对鼠标"控制视角拖拽**；旋转自适应已内置。适合作为触摸映射工具的指针通道；真实触摸由独立 `touch` 进程承担，二者并行。KVM 式绝对鼠标在 Android/iPadOS 上均不可行（§4a）。
