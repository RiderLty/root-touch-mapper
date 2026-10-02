# 用 UHID 让 Android 触摸指示器显示"空心环"

> 面向需要复现此功能的实现者。结论均在某 Android 15（SDK 35）真机上实测得到，非纯理论推导。

## TL;DR

Android 的"显示点按操作反馈"指示器有两种图标：**实心圆盘**和**空心环**。
选哪个由这次触摸的**压力值是否大于 0** 决定。

默认的 HID 触摸屏描述符（本仓库 `uhid.go` 里那份）**没有压力字段**，Android 拿不到压力轴，
于是触摸期间固定按 1.0 处理 —— 因此**只能出实心圆盘，永远出不了环**。

要让环出现，需要两件事：

1. 在报告描述符里**加一个 Tip Pressure 字段**（`Digitizer` page, usage `0x30`）；
2. 报文里把**压力报 0**（同时 tip switch 仍为 1）。

---

## 原理

Android 的 `TouchSpotController::setSpots()` 按压力选图标：

```cpp
const SpriteIcon& icon = c.getAxisValue(AMOTION_EVENT_AXIS_PRESSURE) > 0
        ? mResources.spotTouch   // 实心圆盘
        : mResources.spotHover;  // 空心环
```

- `spotTouch` → 实心半透明白色圆盘（带淡蓝描边）
- `spotHover` → 空心环

压力从哪来：`TouchInputMapper` 把 evdev 的压力轴归一化后填进 `PointerCoords.pressure`。
设备**没有**压力轴时，Android 在触摸期间直接给 1.0；有压力轴时，就用真实值。
所以 `pressure == 0` 只有在设备**能报压力**、并且**确实报了 0** 时才可能出现。

⚠️ 注意语义：压力为 0 的指针在 Android 眼里是 **hover，不是 touch**。
这不只是图标变了 —— 事件根本不会送到应用（下面实测部分有证据）。
如果你的目的是"用一个可见信号表示这次触摸没有生效"，这正好适用；
如果你只是想要个装饰性的环，那就得接受触摸同时失效。

---

## 改法

### 1. 描述符：插入 Tip Pressure 字段

插入位置在 **Contact Identifier 之后、切到 Generic Desktop page 之前**
（此时仍在 `Digitizer` page `0x0D` 上，`0x30` 才是 Tip Pressure）：

```c
0x09, 0x30,        // Usage (Tip Pressure)
0x15, 0x00,        // Logical Minimum (0)
0x26, 0xFF, 0x00,  // Logical Maximum (255)
0x75, 0x08,        // Report Size (8)
0x95, 0x01,        // Report Count (1)
0x81, 0x02,        // Input (Data,Var,Abs)
```

`Logical Maximum` 必须显式声明且大于 0（Android 要用它做归一化的分母）。

### 2. 报文：11 字节 → 12 字节

新增的 1 字节插在 contact identifier 之后，后面全部往后挪：

| 偏移 | 原（11 字节） | 新（12 字节） |
|---|---|---|
| `[0]` | tip switch(1bit) + 7bit padding | 同左 |
| `[1]` | contact identifier | contact identifier |
| `[2]` | — | **tip pressure (0..255)** ← 新增 |
| `[3:7]` | X uint32 LE | X uint32 LE |
| `[7:11]` | Y uint32 LE | Y uint32 LE |
| `[11]` | contact count | contact count |

**报 0 得环，报 >0 得实心圆盘。** 阈值就是严格大于 0。

### 3. 完整描述符（可直接替换）

```c
var touchReportDesc = []byte{
    0x05, 0x0D, // Usage Page (Digitizer)
    0x09, 0x04, // Usage (Touch Screen)
    0xA1, 0x01, // Collection (Application)
    0x09, 0x22, //   Usage (Finger)
    0xA1, 0x02, //   Collection (Logical)
    0x09, 0x42, //     Usage (Tip Switch)
    0x15, 0x00, //     Logical Minimum (0)
    0x25, 0x10, //     Logical Maximum (1)
    0x75, 0x01, //     Report Size (1)
    0x95, 0x01, //     Report Count (1)
    0x81, 0x02, //     Input (Data,Var,Abs)
    0x95, 0x07, //     Report Count (7)
    0x81, 0x03, //     Input (Const,Var,Abs) - padding
    0x09, 0x51, //     Usage (Contact Identifier)
    0x75, 0x08, //     Report Size (8)
    0x95, 0x01, //     Report Count (1)
    0x81, 0x02, //     Input (Data,Var,Abs)
    // ↓↓↓ 新增部分 ↓↓↓
    0x09, 0x30, //     Usage (Tip Pressure)
    0x15, 0x00, //     Logical Minimum (0)
    0x26, 0xFF, 0x00, // Logical Maximum (255)
    0x75, 0x08, //     Report Size (8)
    0x95, 0x01, //     Report Count (1)
    0x81, 0x02, //     Input (Data,Var,Abs)
    // ↑↑↑ 新增部分 ↑↑↑
    0x05, 0x01, //     Usage Page (Generic Desktop)
    0x09, 0x30, //     Usage (X)
    0x15, 0x00, //     Logical Minimum (0)
    0x27, 0xFE, 0xFF, 0xFF, 0x7F, // Logical Maximum (0x7ffffffe)
    0x75, 0x20, //     Report Size (32)
    0x95, 0x01, //     Report Count (1)
    0x81, 0x02, //     Input (Data,Var,Abs)
    0x09, 0x31, //     Usage (Y)
    0x15, 0x00, //     Logical Minimum (0)
    0x27, 0xFE, 0xFF, 0xFF, 0x7F, // Logical Maximum (0x7ffffffe)
    0x75, 0x20, //     Report Size (32)
    0x95, 0x01, //     Report Count (1)
    0x81, 0x02, //     Input (Data,Var,Abs)
    0xC0,       //   End Collection (Finger)
    0x05, 0x0D, // Usage Page (Digitizer)
    0x09, 0x54, // Usage (Contact Count)
    0x25, 0x02, // Logical Maximum (2)
    0x75, 0x08, // Report Size (8)
    0x95, 0x01, // Report Count (1)
    0x81, 0x02, // Input (Data,Var,Abs)
    0xC0, // End Collection (Application)
}
```

对应 `touchReportSize` 由 `11` 改为 `12`，`sendReport` 的 X/Y 偏移由 `[2:6]/[6:10]` 改为
`[3:7]/[7:11]`，contact count 由 `r[10]` 改为 `r[11]`，并在 `r[2]` 写压力。

### 4. 内核侧会看到什么

加入后 evdev 节点上会多出：

```
ABS_PRESSURE : value 0, min 0, max 255
```

注意：**是 `ABS_PRESSURE` 而不是 `ABS_MT_PRESSURE`**（这份描述符下 hid-multitouch 的实际行为）。
不要照搬"MT 设备应该有 ABS_MT_PRESSURE"的直觉去检查。

---

## 实测验证

环境：Android 15 / SDK 35 平板，`show_touches=1`，前台是一个 Chrome 触控测试页
（页面自身会画出触点并显示"当前触控点"计数，可同时观察系统指示器和应用侧）。

| 报文压力 | Android 指示器 | 页面计数 | 应用是否收到触摸 |
|---|---|---|---|
| `prs=0` | **空心环** | **0 当前触控点** | **否** |
| `prs=1` | 实心圆盘 | 画出触点 | 是 |
| `prs=255` | 实心圆盘 | 画出触点 | 是 |

内核侧同步确认：`prs=0` 时 `ABS_PRESSURE` 的 value 为 0。

**这张表最重要的结论是第 3 列**：环出现时应用一个事件都收不到。
所以环是一个诚实的"触摸未送达"信号，而不是单纯的视觉差异。

---

## 坑

1. **`pointer_location` 覆盖层顶栏的 `Prs:` 读数不可信。**
   实测 `prs=0` 和 `prs=255` 两种情况它都显示 `1.0`。
   不要用它来测压力，**以指示器本身的形状为准**。

2. **用差分截图（按住 vs 不按，两图相减）来确认指示器存在，容易误判。**
   如果触摸同时改变了 UI（比如列表项高亮），差分图会被 UI 变化淹没，看起来像"指示器没渲染"。
   正确做法是**按住不放、直接裁剪触摸点附近的区域**看。

3. **长按会触发 UI 行为。** 网页会被选中/弹菜单，桌面会进编辑模式，把后续截图带偏。
   做这类可视化验证时，单次按住控制在几秒内，并在每步之后确认前台窗口没变。

4. **别用 `input swipe`/`input tap` 做对照实验。**
   `input` 命令走 `InputManager.injectInputEvent`，**绕过 InputReader**，
   因此根本不会触发这个指示器。它没法作为"真实触摸会怎样"的基线。

5. **加了压力字段后，压力 0 的触摸不送达应用。**
   如果你的上层逻辑依赖触摸真的生效，报 0 就等于主动丢弃这次触摸。
   要只在需要指示时用 0，正常操作时报 >0。

---

## 怎么自测

不需要真的去数像素，最快的验证路径：

```sh
# 1. 打开"显示点按操作反馈"（开发者选项），确认 show_touches=1
adb shell settings put system show_touches 1

# 2. 跑你的程序，在同一个位置按住不放，分别报压力 0 和 >0
#    3. 直接截屏裁剪触摸点附近，比较两次的形状
adb exec-out screencap -p > shot.png
```

触摸点的**屏幕坐标**要自己算清楚（见下面"相关但独立的问题"第 1 条，
如果设备是横屏，坐标会被旋转，别用归一化坐标直接乘宽高）。

---

## 相关但独立的问题（同一轮调查中实测，与本功能无依赖，但会咬人）

### 1. 横屏下归一化坐标被旋转 90°

Android 会按当前显示旋转对原始触摸坐标做变换。虚拟 UHID 设备没有"自然朝向"，
Android 就按显示器的自然朝向解释。实测在 ROTATION_90（横屏）下：

```
screen_X = y_发送        screen_Y = 1 − x_发送
反解:   发送 x = 1 − Y，发送 y = X
```

竖屏（ROTATION_0）下是 1:1 正确的。
**陷阱**：`(0.5, 0.5)` 是旋转的不动点，所以只测屏幕中心的冒烟测试**一定会通过**，
完全掩盖这个 bug。必须测屏幕外的点。

### 2. 按住不放时更换 Contact ID 会永久卡住一个触点

hid-multitouch 用 HID 的 Contact Identifier 做 slot 映射。若在 tip switch 仍为 1 时
改变 Contact ID，会**新开一个 slot**，而旧 slot 永远不会被释放：

```
press id=1 → ABS_MT_TRACKING_ID 0
press id=2 → ABS_MT_SLOT 1, ABS_MT_TRACKING_ID 1   ← slot 0 从未释放
release id=2 → 只释放 slot 1
BTN_TOUCH 保持 DOWN，直到 UHID 设备被销毁才变 UP
```

结果是手指"粘"在屏幕上，后续触摸行为异常。
**规则：每次 press 用一个新 ID，且必须配对一次 release。**

---

## 变更应该落在哪

本仓库原实现刻意只做单指、不做旋转映射（见 `README.MD` 的"已知限制"）。
上面第 1、2 条是原实现的真实缺陷；压力/指示器是新增能力。
如果要把环作为正式功能引入，建议做成开关，不要改变默认的 11 字节报文行为。
