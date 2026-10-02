// 虚拟手写笔设备 —— Digitizer / Pen。
//
// 描述符按 Microsoft 手写笔规范 (Required HID Top-Level Collections /
// pen-sample-report-descriptors) 与 AOSP stylus 指南裁剪:
//
//	必选: X/Y (Generic Desktop 页), Tip Switch(0x42), In-Range(0x32),
//	      Barrel Switch(0x44)
//	建议加上: Tip Pressure(0x30, Windows 归一化到 0~1024, Android 用它出
//	          压感笔锋), Eraser(0x45), Invert(0x3C)
//	In-Range 语义: 悬停与落笔都置 1; 离开感应范围时清 0 并重复最后坐标。
//
// 只有一个顶层集合、一种报文, 所以不需要 Report ID。
// 内核 hid-generic 会把手写笔集合映射为 BTN_TOOL_PEN / ABS_X / ABS_Y /
// ABS_PRESSURE / BTN_TOUCH / BTN_STYLUS, Android InputReader 识别为
// SOURCE_STYLUS (toolType = STYLUS), 备忘录/画图类应用可拿到压感。
//
// 报文 7 字节 (无 Report ID):
//
//	[0]     bit0 Tip Switch | bit1 Barrel | bit2 Invert | bit3 Eraser
//	        bit4 In-Range | bit5..7 padding
//	[1:3]   X  uint16 LE, 逻辑区间 0..32767
//	[3:5]   Y  uint16 LE, 逻辑区间 0..32767
//	[5:7]   Tip Pressure uint16 LE, 逻辑区间 0..1023
//
// 测试字节 (屏幕中心 X=16384=0x4000, Y 同):
//
//	10 40 00 40 00 00 00   悬停中心 (in-range, 无 tip, pressure=0)
//	11 40 00 40 00 00 02   落笔中心 (tip + in-range, pressure=512)
//	13 40 00 40 00 00 02   落笔 + 侧键 (barrel)
//	00 40 00 40 00 00 00   离开感应范围 (清 in-range, 重复最后坐标)
//
// 运行后可用 `raw 11400040000002` 直接喂这些字节。
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"time"
)

var penReportDesc = []byte{
	0x05, 0x0D, // Usage Page (Digitizer)
	0x09, 0x02, // Usage (Pen)
	0xA1, 0x01, // Collection (Application)
	0x09, 0x20, //   Usage (Stylus)
	0xA1, 0x00, //   Collection (Physical)
	0x09, 0x42, //     Usage (Tip Switch)
	0x09, 0x44, //     Usage (Barrel Switch)
	0x09, 0x3C, //     Usage (Invert)
	0x09, 0x45, //     Usage (Eraser)
	0x15, 0x00, //     Logical Minimum (0)
	0x25, 0x01, //     Logical Maximum (1)
	0x75, 0x01, //     Report Size (1)
	0x95, 0x04, //     Report Count (4)
	0x81, 0x02, //     Input (Data,Var,Abs)
	0x09, 0x32, //     Usage (In Range)
	0x95, 0x01, //     Report Count (1)
	0x81, 0x02, //     Input (Data,Var,Abs)
	0x95, 0x03, //     Report Count (3)
	0x81, 0x03, //     Input (Const,Var,Abs) - padding
	0x05, 0x01, //     Usage Page (Generic Desktop)
	0x09, 0x30, //     Usage (X)
	0x15, 0x00, //     Logical Minimum (0)
	0x26, 0xFF, 0x7F, // Logical Maximum (32767)
	0x75, 0x10, //     Report Size (16)
	0x95, 0x01, //     Report Count (1)
	0x81, 0x02, //     Input (Data,Var,Abs)
	0x09, 0x31, //     Usage (Y)
	0x81, 0x02, //     Input (Data,Var,Abs)
	0x05, 0x0D, //     Usage Page (Digitizer)
	0x09, 0x30, //     Usage (Tip Pressure)
	0x15, 0x00, //     Logical Minimum (0)
	0x26, 0xFF, 0x03, // Logical Maximum (1023)
	0x75, 0x10, //     Report Size (16)
	0x95, 0x01, //     Report Count (1)
	0x81, 0x02, //     Input (Data,Var,Abs)
	0xC0, //   End Collection (Physical)
	0xC0, // End Collection (Application)
}

const penReportSize = 7

const (
	penLogicalMax   = 32767 // X/Y 逻辑最大值
	penPressureMax  = 1023  // Tip Pressure 逻辑最大值 (与 AOSP 10bit 一致)
	penDefaultPress = 512   // 默认落笔压感, 约半压
)

// penReport 一份手写笔输入报文的字段。编码后即 7 字节报文。
type penReport struct {
	tip      bool
	barrel   bool
	invert   bool
	eraser   bool
	inRange  bool
	x, y     uint16 // 0..penLogicalMax
	pressure uint16 // 0..penPressureMax
}

func (p penReport) encode() []byte {
	b := make([]byte, penReportSize)
	if p.tip {
		b[0] |= 1 << 0
	}
	if p.barrel {
		b[0] |= 1 << 1
	}
	if p.invert {
		b[0] |= 1 << 2
	}
	if p.eraser {
		b[0] |= 1 << 3
	}
	if p.inRange {
		b[0] |= 1 << 4
	}
	binary.LittleEndian.PutUint16(b[1:3], p.x)
	binary.LittleEndian.PutUint16(b[3:5], p.y)
	binary.LittleEndian.PutUint16(b[5:7], p.pressure)
	return b
}

// scalePen 把 0~1 归一化坐标映射到 16bit 逻辑区间。
func scalePen(v float64) uint16 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return uint16(v * float64(penLogicalMax))
}

// scalePressure 把 0~1 归一化压感映射到 0..1023。
func scalePressure(v float64) uint16 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return uint16(v * float64(penPressureMax))
}

type penCtl struct {
	dev       *uhidDev
	inContact bool // 笔尖是否接触表面
	barrel    bool
	// 最后一次上报的位置, 抬笔/离场时按规范重复
	lastX, lastY uint16
}

func (p *penCtl) report(tip, inRange bool, x, y, pressure uint16) {
	p.lastX, p.lastY = x, y
	r := penReport{tip: tip, barrel: p.barrel, inRange: inRange, x: x, y: y, pressure: pressure}
	if err := p.dev.SendReport(r.encode()); err != nil {
		fmt.Println("发送失败:", err)
	}
}

func (p *penCtl) down(x, y float64, pressure uint16) {
	p.inContact = true
	p.report(true, true, scalePen(x), scalePen(y), pressure)
}

func (p *penCtl) move(x, y float64, pressure uint16) {
	if !p.inContact {
		return
	}
	p.report(true, true, scalePen(x), scalePen(y), pressure)
}

// hover 悬停: in-range=1 但 tip=0, pressure=0。画图应用里表现为笔尖跟随光标。
func (p *penCtl) hover(x, y float64) {
	p.report(false, true, scalePen(x), scalePen(y), 0)
}

func (p *penCtl) up() {
	if !p.inContact {
		return
	}
	p.inContact = false
	// Windows 手写笔状态机: tip 抬起后先进入 hover (pressure=0),
	// 再离开感应范围 (in-range=0, 重复最后坐标)。
	p.report(false, true, p.lastX, p.lastY, 0)
	p.report(false, false, p.lastX, p.lastY, 0)
}

func (p *penCtl) toggleBarrel() {
	p.barrel = !p.barrel
	fmt.Println("barrel switch:", map[bool]string{false: "松开", true: "按下"}[p.barrel])
}

func (p *penCtl) tap(x, y float64) {
	p.down(x, y, penDefaultPress)
	time.Sleep(60 * time.Millisecond)
	p.up()
	time.Sleep(30 * time.Millisecond)
}

// demo 悬停进场 → 一条压感渐变的弧线 → 抬笔离场。
func (p *penCtl) demo() {
	fmt.Println("demo: hover (0.5,0.4) -> 弧线 (0.3,0.6)->(0.7,0.6) 压感 0.1~0.9 -> up")
	p.hover(0.5, 0.4)
	time.Sleep(300 * time.Millisecond)
	p.down(0.3, 0.6, scalePressure(0.1))
	time.Sleep(30 * time.Millisecond)
	steps := 30
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		x := 0.3 + 0.4*f
		y := 0.6 - 0.2*(4*f*(1-f)) // 抛物线拱起
		pressure := 0.1 + 0.8*f
		if f > 0.8 {
			pressure = 0.9 - 1.5*(f-0.8) // 收笔前泄压
		}
		p.move(x, y, scalePressure(pressure))
		time.Sleep(12 * time.Millisecond)
	}
	p.up()
	fmt.Println("demo 完成")
}

// diag 坐标系诊断: 悬停沿 (0,0)->(1,1) 再 (1,0)->(0,1) 两条对角线缓慢移动,
// 重复 3 遍。盯着屏幕看两条线各走哪个对角、什么方向, 即可确定 Android
// 对虚拟笔坐标施加的旋转/翻转 (见 README 旋转说明)。
func (p *penCtl) diag() {
	for rep := 1; rep <= 3; rep++ {
		fmt.Printf("diag: 第 %d 遍 (0,0)->(1,1) ...\n", rep)
		for i := 0; i <= 50; i++ {
			f := float64(i) / 50
			p.hover(f, f)
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(2 * time.Second)
		fmt.Printf("diag: 第 %d 遍 (1,0)->(0,1) ...\n", rep)
		for i := 0; i <= 50; i++ {
			f := float64(i) / 50
			p.hover(1-f, f)
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(2500 * time.Millisecond)
	}
}

// circle 坐标系诊断(可见版): 悬停沿屏幕中心一圈圈缓慢画圆, 重复 3 圈。
// 从圆的起止点和旋转方向即可读出 Android 对虚拟笔坐标施加的旋转/翻转。
func (p *penCtl) circle() {
	for rep := 1; rep <= 3; rep++ {
		fmt.Printf("circle: 第 %d 圈 (从圆顶部顺时针) ...\n", rep)
		for i := 0; i <= 36; i++ {
			a := float64(i) * 10 * 3.14159265 / 180
			p.hover(0.5+0.35*math.Sin(a), 0.5+0.35*math.Cos(a))
			time.Sleep(90 * time.Millisecond)
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

// line 悬停沿直线 (x1,y1)->(x2,y2) 匀速移动。默认 3000ms 跑完,
// 以 1000Hz (1ms 一份报文) 均匀上报; 时间基准对齐, 不依赖 sleep 精度。
// 途中 1/3 处按下 (tip=1, 默认压感), 2/3 处抬起 (tip=0), 其余段悬停。
func (p *penCtl) line(x1, y1, x2, y2 float64, durationMs int) {
	if durationMs <= 0 {
		durationMs = 3000
	}
	start := time.Now()
	for i := 0; i <= durationMs; i++ {
		deadline := start.Add(time.Duration(i) * time.Millisecond)
		for time.Now().Before(deadline) {
			time.Sleep(200 * time.Microsecond)
		}
		f := float64(i) / float64(durationMs)
		x := scalePen(x1 + (x2-x1)*f)
		y := scalePen(y1 + (y2-y1)*f)
		switch {
		case f >= 2.0/3.0: // 抬起后的悬停段
			p.report(false, true, x, y, 0)
		case f >= 1.0/3.0: // 按下段
			p.report(true, true, x, y, penDefaultPress)
		default: // 起始悬停段
			p.report(false, true, x, y, 0)
		}
	}
}

// runPen 启动虚拟手写笔并进入交互命令。
func runPen() {
	fmt.Println("root-touch-mapper: UHID 虚拟手写笔验证")

	dev, err := OpenUHID("goodix_ts", "i2c/1-005d", 0, 0, penReportDesc)
	if err != nil {
		log.Fatalf("%v", err)
	}
	go dev.ReadEvents()

	ctl := &penCtl{dev: dev}
	fmt.Println("等待 Android input 系统接入 (见 [uhid] 日志)... 输入命令, q 退出")
	fmt.Println("命令: d/m <x> <y> [pressure0~1] | h <x> <y> (悬停) | b (侧键) | u |")
	fmt.Println("      tap <x> <y> | demo | diag | circle | line <x1> <y1> <x2> <y2> [ms] | raw <hex> | q")
	fmt.Println("      如 raw 11400040000002 = 落笔中心, 压感 512")

	repl(func(fields []string) bool {
		cmd := lower(fields[0])
		if cmd == "raw" { // raw 的参数是十六进制, 不做浮点解析
			sendRaw(dev, fields[1:])
			return true
		}
		args := parseNums(fields[1:])
		switch cmd {
		case "d", "m":
			if len(args) < 2 {
				fmt.Printf("用法: %s <x> <y> [pressure0~1]\n", cmd)
			} else {
				pr := uint16(penDefaultPress)
				if len(args) >= 3 {
					pr = scalePressure(args[2])
				}
				if cmd == "d" {
					ctl.down(args[0], args[1], pr)
				} else {
					ctl.move(args[0], args[1], pr)
				}
			}
		case "h":
			if len(args) < 2 {
				fmt.Println("用法: h <x> <y>")
			} else {
				ctl.hover(args[0], args[1])
			}
		case "b":
			ctl.toggleBarrel()
		case "u":
			ctl.up()
		case "tap":
			if len(args) < 2 {
				fmt.Println("用法: tap <x> <y>")
			} else {
				ctl.tap(args[0], args[1])
			}
		case "demo":
			ctl.demo()
		case "diag":
			ctl.diag()
		case "circle":
			ctl.circle()
		case "line":
			if len(args) < 4 {
				fmt.Println("用法: line <x1> <y1> <x2> <y2> [毫秒, 默认3000]")
			} else {
				ms := 3000
				if len(args) >= 5 {
					ms = int(args[4])
				}
				ctl.line(args[0], args[1], args[2], args[3], ms)
			}
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
