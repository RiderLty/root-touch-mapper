// 虚拟数位板光标工具（Digitizer/Puck）。
// 数位板工具使用 Digitizer 页的 Puck usage、Tip Switch、绝对 X/Y 和
// In Range 状态，因此与普通相对鼠标不同，可作为带系统光标的绝对定位工具。
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var tabletReportDescTemplate = []byte{
	// Mouse application: provides BTN_LEFT/RIGHT/MIDDLE.
	0x05, 0x01, 0x09, 0x02, 0xA1, 0x01,
	0x09, 0x01, 0xA1, 0x00,
	0x05, 0x09, 0x19, 0x01, 0x29, 0x03,
	0x15, 0x00, 0x25, 0x01, 0x95, 0x03, 0x75, 0x01, 0x81, 0x02,
	0x95, 0x01, 0x75, 0x05, 0x81, 0x03,
	0xC0, 0xC0,
	// Digitizer/Puck application: provides BTN_TOOL_MOUSE, In Range and ABS_X/Y.
	0x05, 0x0D, 0x09, 0x01, 0xA1, 0x01,
	0x09, 0x21, 0xA1, 0x00,
	0x09, 0x42, 0x09, 0x32, 0x15, 0x00, 0x25, 0x01, 0x95, 0x02, 0x75, 0x01, 0x81, 0x02,
	0x95, 0x01, 0x75, 0x06, 0x81, 0x03,
	0x05, 0x01, 0x09, 0x30, // X
	0x16, 0x00, 0x00, 0x26, 0x26, 0x50, 0x75, 0x10, 0x95, 0x01, 0x81, 0x02, // 0..20518
	0x09, 0x31, // Y
	0x16, 0x00, 0x00, 0x26, 0xFF, 0x7F, 0x75, 0x10, 0x95, 0x01, 0x81, 0x02, // 0..32767
	0xC0, 0xC0,
}

// tabletDescriptor 返回按方向回填了 X/Y 逻辑上限的 Puck 描述符。
// 模板中 X/Y 的 Logical Max 分别紧跟在 usage 0x30/0x31 的 Logical Min 之后。
func tabletDescriptor(xmax, ymax int) []byte {
	desc := append([]byte{}, tabletReportDescTemplate...)
	if i := bytes.Index(desc, []byte{0x09, 0x30, 0x16, 0x00, 0x00, 0x26}); i >= 0 {
		binary.LittleEndian.PutUint16(desc[i+6:], uint16(xmax))
	}
	if i := bytes.Index(desc, []byte{0x09, 0x31, 0x16, 0x00, 0x00, 0x26}); i >= 0 {
		binary.LittleEndian.PutUint16(desc[i+6:], uint16(ymax))
	}
	return desc
}

// tabletReportDesc: 竖屏默认描述符, 供 composite 实验引用。
var tabletReportDesc = tabletDescriptor(tabletRanges(0))

const (
	tabletReportSize = 6
	// 长短轴逻辑范围: InputReader 的 Pointer 模式按"屏宽/设备X宽"做统一缩放
	// (宽填充、高裁剪), 因此描述符的 X:Y 比例必须与当前显示方向一致:
	// 竖屏 1904x3040 用 X=20518/Y=32767, 横屏 3040x1904 用 X=32767/Y=20518,
	// 否则非宽轴会溢出屏幕 (实测横屏沿用竖屏范围时 y>0.5 全部出屏)。
	tabletLogicalMaxLong  = 32767
	tabletLogicalMaxShort = 20518 // 32767 * 1904/3040, 当前设备面板宽高比
	tabletLeft            = 1 << 0
	tabletRight           = 1 << 1
	tabletMiddle          = 1 << 2
)

// tabletRanges 按显示旋转角返回当前应使用的 X/Y 逻辑上限。
// rot 为 dumpsys display 的 mCurrentOrientation (0/2 竖屏, 1/3 横屏)。
func tabletRanges(rot int) (xmax, ymax int) {
	if rot%2 == 0 {
		return tabletLogicalMaxShort, tabletLogicalMaxLong
	}
	return tabletLogicalMaxLong, tabletLogicalMaxShort
}

// currentRotation 读取当前显示旋转角; 失败时返回 0 (竖屏)。
func currentRotation() int {
	out, err := exec.Command("dumpsys", "display").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "mCurrentOrientation="); i >= 0 {
			if v, err := strconv.Atoi(strings.TrimSpace(line[i+len("mCurrentOrientation="):])[:1]); err == nil {
				return v
			}
		}
	}
	return 0
}

type tabletCtl struct {
	mu      sync.Mutex
	dev     *uhidDev
	xmax    int // 当前方向的 X 逻辑上限
	ymax    int // 当前方向的 Y 逻辑上限
	x, y    uint16
	buttons uint8
	inRange bool
	tip     bool
}

// scaleTabletX/Y: 竖屏常数缩放, 仅供 composite 实验使用;
// tablet 模式用 ctl 自身的方向感知缩放 (t.scaleX/t.scaleY)。
func scaleTabletX(v float64) uint16 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	xmax, _ := tabletRanges(0)
	return uint16(v * float64(xmax))
}

func scaleTabletY(v float64) uint16 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	_, ymax := tabletRanges(0)
	return uint16(v * float64(ymax))
}

func (t *tabletCtl) scaleX(v float64) uint16 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return uint16(v * float64(t.xmax))
}

func (t *tabletCtl) scaleY(v float64) uint16 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return uint16(v * float64(t.ymax))
}

// swapDev 在旋转变化后更换 UHID 设备与逻辑范围; 旧设备先 leave 再关闭。
func (t *tabletCtl) swapDev(dev *uhidDev, xmax, ymax int) {
	t.mu.Lock()
	old := t.dev
	t.dev = dev
	t.xmax, t.ymax = xmax, ymax
	t.inRange, t.tip = false, false
	t.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

func (t *tabletCtl) report() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dev == nil {
		return fmt.Errorf("设备未就绪")
	}
	b := make([]byte, tabletReportSize)
	b[0] = t.buttons
	if t.tip {
		b[1] |= 1 << 0
	}
	if t.inRange {
		b[1] |= 1 << 1
	}
	binary.LittleEndian.PutUint16(b[2:4], t.x)
	binary.LittleEndian.PutUint16(b[4:6], t.y)
	return t.dev.SendReport(b)
}
func (t *tabletCtl) move(x, y float64) {
	t.mu.Lock()
	t.x, t.y = t.scaleX(x), t.scaleY(y)
	t.inRange = true
	t.mu.Unlock()
	// Preserve Tip Switch during a button drag; hover moves start with it clear.
	t.report()
}
func (t *tabletCtl) press(button uint8, x, y float64) {
	t.mu.Lock()
	t.x, t.y = t.scaleX(x), t.scaleY(y)
	t.inRange = true
	t.tip = true
	t.buttons |= button
	t.mu.Unlock()
	t.report()
}
func (t *tabletCtl) release(button uint8) {
	t.mu.Lock()
	t.buttons &^= button
	if t.buttons == 0 {
		t.tip = false
	}
	t.mu.Unlock()
	t.report()
}
func (t *tabletCtl) releaseAll() {
	t.mu.Lock()
	changed := t.buttons != 0
	t.buttons = 0
	t.mu.Unlock()
	if changed {
		t.report()
	}
}
func (t *tabletCtl) leave() { t.releaseAll(); t.inRange = false; t.tip = false; t.report() }
func (t *tabletCtl) click(button uint8, x, y float64) {
	t.press(button, x, y)
	time.Sleep(60 * time.Millisecond)
	t.release(button)
	time.Sleep(30 * time.Millisecond)
}
func (t *tabletCtl) drag(button uint8, x1, y1, x2, y2 float64, steps int) {
	if steps <= 0 {
		steps = 40
	}
	// 先悬停到起点, 让光标先就位, 再落笔 —— 避免按下与移动在同一报文里
	// 被合并成一次跳跃。
	t.move(x1, y1)
	time.Sleep(50 * time.Millisecond)
	t.press(button, x1, y1)
	// smoothstep 缓动: f*f*(3-2f), 起步与收尾平滑, 中段匀速。
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		e := f * f * (3 - 2*f)
		t.move(x1+(x2-x1)*e, y1+(y2-y1)*e)
		time.Sleep(8 * time.Millisecond)
	}
	t.release(button)
}

// streamLine 连续以指定频率发送慢速绝对坐标，用于验证 Android 在触摸并发时
// 是否仍处理 Puck 指针。频率通常取 1000Hz，durationMs 控制轨迹总时长。
func (t *tabletCtl) streamLine(x1, y1, x2, y2 float64, durationMs, hz int) {
	if durationMs <= 0 {
		durationMs = 8000
	}
	if hz <= 0 {
		hz = 1000
	}
	if hz > 2000 {
		hz = 2000
	}
	t0 := time.Now()
	t.move(x1, y1)
	ticker := time.NewTicker(time.Second / time.Duration(hz))
	defer ticker.Stop()
	for {
		if time.Since(t0) >= time.Duration(durationMs)*time.Millisecond {
			break
		}
		<-ticker.C
		ms := int(time.Since(t0).Milliseconds())
		if ms > durationMs {
			ms = durationMs
		}
		f := float64(ms) / float64(durationMs)
		t.move(x1+(x2-x1)*f, y1+(y2-y1)*f)
	}
	t.move(x2, y2)
}
func tabletButton(s string) uint8 {
	switch lower(s) {
	case "r", "right", "2":
		return tabletRight
	case "m", "middle", "3":
		return tabletMiddle
	default:
		return tabletLeft
	}
}

func tabletNums(fields []string, n int) ([]float64, bool) {
	if len(fields) < n+1 {
		return nil, false
	}
	args := parseNums(fields[1 : n+1])
	return args, args != nil
}
func (t *tabletCtl) demo() {
	fmt.Println("demo: 数位板光标沿四角移动 -> 左键点击 -> 拖动")
	for _, p := range [][2]float64{{.1, .1}, {.9, .1}, {.9, .9}, {.1, .9}, {.5, .5}} {
		t.move(p[0], p[1])
		time.Sleep(250 * time.Millisecond)
	}
	t.click(tabletLeft, .5, .5)
	t.drag(tabletLeft, .5, .5, .85, .85, 30)
	fmt.Println("demo 完成")
}

func runTablet(square bool) {
	fmt.Println("root-touch-mapper: UHID 虚拟数位板光标 (Digitizer/Puck)")
	// 使用独立 phys，避免与 touch/pen 的同一 EventHub InputDevice 合并；
	// 这样数位板指针和真正触摸各自拥有独立 mapper。
	// 描述符按启动时的显示方向生成; 之后监听旋转并自动重建设备。
	// square=true: 实验模式, 强制正方形逻辑区间 (32767x32767, 类似 pen),
	// 用于验证 Pointer 模式的宽填充裁剪行为, 不做旋转自适应。
	rot := currentRotation()
	xmax, ymax := tabletRanges(rot)
	if square {
		fmt.Println("实验模式: 正方形逻辑区间 (预期 Y 轴溢出被裁剪)")
		xmax, ymax = tabletLogicalMaxLong, tabletLogicalMaxLong
		rot = -1 // 禁用旋转自适应
	}
	dev, err := OpenUHID("absolute_tablet_puck", "i2c/absolute-tablet", 0, 0, tabletDescriptor(xmax, ymax))
	if err != nil {
		log.Fatalf("%v", err)
	}
	go dev.ReadEvents()
	ctl := &tabletCtl{dev: dev, xmax: xmax, ymax: ymax, x: uint16(xmax/2), y: uint16(ymax/2)}
	go func() {
		last := rot
		for {
			if last < 0 { // square 实验模式: 不做旋转自适应
				return
			}
			time.Sleep(700 * time.Millisecond)
			r := currentRotation()
			if r == last {
				continue
			}
			last = r
			nx, ny := tabletRanges(r)
			nd, err := OpenUHID("absolute_tablet_puck", "i2c/absolute-tablet", 0, 0, tabletDescriptor(nx, ny))
			if err != nil {
				fmt.Println("旋转后重建设备失败:", err)
				continue
			}
			go nd.ReadEvents()
			fmt.Printf("检测到旋转 -> rot=%d, 重建 UHID (X max=%d, Y max=%d)\n", r, nx, ny)
			ctl.swapDev(nd, nx, ny)
		}
	}()
	fmt.Println("等待 Android input 系统接入 (见 [uhid] 日志)... 输入命令, q 退出")
	fmt.Println("命令: m/move/h/hover <x> <y> | d/down <x> <y> [left|right|middle] | u/up [button] | leave")
	fmt.Println("      click <x> <y> [button] | drag <x1> <y1> <x2> <y2> [steps] [button] | stream <x1> <y1> <x2> <y2> <ms> [hz] | demo | raw <hex> | q")
	repl(func(fields []string) bool {
		cmd := lower(fields[0])
		if cmd == "raw" {
			sendRaw(dev, fields[1:])
			return true
		}
		if cmd == "leave" || cmd == "out" {
			ctl.leave()
			return true
		}
		switch cmd {
		case "m", "move", "h", "hover":
			args, ok := tabletNums(fields, 2)
			if !ok {
				fmt.Println("用法: move <x> <y>")
			} else {
				ctl.move(args[0], args[1])
			}
		case "d", "down":
			args, ok := tabletNums(fields, 2)
			if !ok {
				fmt.Println("用法: down <x> <y> [button]")
			} else {
				b := uint8(tabletLeft)
				if len(fields) >= 4 {
					b = tabletButton(fields[3])
				}
				ctl.press(b, args[0], args[1])
			}
		case "u", "up":
			b := ctl.buttons
			if len(fields) >= 2 {
				b = tabletButton(fields[1])
			}
			if b == 0 {
				ctl.releaseAll()
			} else {
				ctl.release(b)
			}
		case "click":
			args, ok := tabletNums(fields, 2)
			if !ok {
				fmt.Println("用法: click <x> <y> [button]")
			} else {
				b := uint8(tabletLeft)
				if len(fields) >= 4 {
					b = tabletButton(fields[3])
				}
				ctl.click(b, args[0], args[1])
			}
		case "drag":
			args, ok := tabletNums(fields, 4)
			if !ok {
				fmt.Println("用法: drag <x1> <y1> <x2> <y2> [steps] [button]")
			} else {
				n := 30
				if len(args) >= 5 {
					n = int(args[4])
				}
				b := uint8(tabletLeft)
				if len(fields) >= 7 {
					b = tabletButton(fields[6])
				}
				ctl.drag(b, args[0], args[1], args[2], args[3], n)
			}
		case "stream", "line":
			args := parseNums(fields[1:])
			if len(args) < 4 {
				fmt.Println("用法: stream <x1> <y1> <x2> <y2> <duration_ms> [hz]")
			} else {
				durationMs := 8000
				if len(args) >= 5 {
					durationMs = int(args[4])
				}
				hz := 1000
				if len(args) >= 6 {
					hz = int(args[5])
				}
				ctl.streamLine(args[0], args[1], args[2], args[3], durationMs, hz)
			}
		case "demo":
			ctl.demo()
		case "q", "quit", "exit":
			ctl.leave()
			dev.Close()
			return false
		default:
			fmt.Println("未知命令:", cmd)
		}
		return true
	})
}

// runDrawingTablet 使用 Digitizer/Pen 描述符，但把设备声明为独立的
// POINTER 设备。Android 会将它组合成 SOURCE_MOUSE|SOURCE_STYLUS，走
// drawing-tablet 光标控制器；这是数位板厂商通常使用的绝对光标路径。
func runDrawingTablet() {
	fmt.Println("root-touch-mapper: UHID 绝对数位板光标 (Pen tool / drawing tablet)")
	dev, err := OpenUHID("drawing_tablet_pen", "i2c/drawing-tablet", 0, 0, penReportDesc)
	if err != nil {
		log.Fatalf("%v", err)
	}
	go dev.ReadEvents()
	ctl := &penCtl{dev: dev}
	fmt.Println("等待 Android input 系统接入 (见 [uhid] 日志)... 输入 h/d/m/u, q 退出")
	fmt.Println("命令: h <x> <y> (悬停) | d <x> <y> [pressure] | m <x> <y> [pressure] | u | raw <hex> | q")
	repl(func(fields []string) bool {
		cmd := lower(fields[0])
		if cmd == "raw" {
			sendRaw(dev, fields[1:])
			return true
		}
		args := parseNums(fields[1:])
		switch cmd {
		case "h":
			if len(args) >= 2 {
				ctl.hover(args[0], args[1])
			} else {
				fmt.Println("用法: h <x> <y>")
			}
		case "d", "m":
			if len(args) < 2 {
				fmt.Printf("用法: %s <x> <y> [pressure0~1]\n", cmd)
				break
			}
			pressure := uint16(penDefaultPress)
			if len(args) >= 3 {
				pressure = scalePressure(args[2])
			}
			if cmd == "d" {
				ctl.down(args[0], args[1], pressure)
			} else {
				ctl.move(args[0], args[1], pressure)
			}
		case "u":
			ctl.up()
		case "q", "quit", "exit":
			ctl.up()
			dev.Close()
			return false
		default:
			fmt.Println("未知命令:", cmd)
		}
		return true
	})
}
