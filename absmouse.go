// 虚拟绝对鼠标 (KVM/IP-KVM 风格)。
// 普通 Generic Desktop / Mouse 应用集合, 但 X/Y 声明为绝对轴 (Data,Var,Abs)
// —— JetKVM/NanoKVM 等远程控制硬件在 Android 上显示绝对光标采用的方式,
// 不依赖 Digitizer/Puck 集合, 描述符比 tablet 更简单。
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"time"
)

var absMouseReportDesc = []byte{
	0x05, 0x01, // Usage Page (Generic Desktop)
	0x09, 0x02, // Usage (Mouse)
	0xA1, 0x01, // Collection (Application)
	0x09, 0x01, //   Usage (Pointer)
	0xA1, 0x00, //   Collection (Physical)
	0x05, 0x09, //     Usage Page (Button)
	0x19, 0x01, 0x29, 0x03, //     Buttons 1..3
	0x15, 0x00, 0x25, 0x01, 0x95, 0x03, 0x75, 0x01, 0x81, 0x02,
	0x95, 0x01, 0x75, 0x05, 0x81, 0x03, //     5 bit padding
	0x05, 0x01, //     Usage Page (Generic Desktop)
	0x09, 0x30, //     X
	0x09, 0x31, //     Y
	0x15, 0x00,       //     Logical Min (0)
	0x26, 0xFF, 0x7F, //     Logical Max (32767)
	0x75, 0x10, 0x95, 0x02, 0x81, 0x02, //     2 x uint16 absolute
	0xC0, 0xC0,
}

// 与 tablet 相同的宽高比处理: Pointer 模式保持数位板宽高比, 竖屏
// 1904x3040 下 X 范围取 0..20518, 否则 X 轴超出显示区域。
const (
	absMouseReportSize  = 5
	absMouseLogicalMaxX = tabletLogicalMaxShort
	absMouseLogicalMaxY = tabletLogicalMaxLong
)

type absMouseCtl struct {
	dev     *uhidDev
	x, y    uint16
	buttons uint8
}

// report 发送 5 字节报文: [0]按键位图 [1:3]X [3:5]Y。
// 无按键时即悬停移动; 绝对鼠标没有 In Range 概念, 报文本身就代表指针在场。
func (a *absMouseCtl) report() error {
	b := make([]byte, absMouseReportSize)
	b[0] = a.buttons
	binary.LittleEndian.PutUint16(b[1:3], a.x)
	binary.LittleEndian.PutUint16(b[3:5], a.y)
	return a.dev.SendReport(b)
}

func (a *absMouseCtl) move(x, y float64) {
	a.x, a.y = scaleTabletX(x), scaleTabletY(y)
	a.report()
}

func (a *absMouseCtl) press(button uint8, x, y float64) {
	a.x, a.y = scaleTabletX(x), scaleTabletY(y)
	a.buttons |= button
	a.report()
}

func (a *absMouseCtl) release(button uint8) {
	a.buttons &^= button
	a.report()
}

func (a *absMouseCtl) leave() {
	a.buttons = 0
	a.report()
}

func (a *absMouseCtl) click(button uint8, x, y float64) {
	a.press(button, x, y)
	time.Sleep(60 * time.Millisecond)
	a.release(button)
	time.Sleep(30 * time.Millisecond)
}

func (a *absMouseCtl) drag(button uint8, x1, y1, x2, y2 float64, steps int) {
	if steps <= 0 {
		steps = 30
	}
	a.press(button, x1, y1)
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		a.move(x1+(x2-x1)*f, y1+(y2-y1)*f)
		time.Sleep(10 * time.Millisecond)
	}
	a.release(button)
}

// streamLine 与 tabletCtl.streamLine 相同: 定频连续绝对坐标, 验证高频轨迹。
func (a *absMouseCtl) streamLine(x1, y1, x2, y2 float64, durationMs, hz int) {
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
	a.move(x1, y1)
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
		a.move(x1+(x2-x1)*f, y1+(y2-y1)*f)
	}
	a.move(x2, y2)
}

func runAbsMouse() {
	fmt.Println("root-touch-mapper: UHID 绝对鼠标 (KVM 风格 ABS Mouse)")
	dev, err := OpenUHIDBus("kvm_absolute_mouse", "usb/kvm-absolute-mouse", 0, 0, busUSB, absMouseReportDesc)
	if err != nil {
		log.Fatalf("%v", err)
	}
	go dev.ReadEvents()
	ctl := &absMouseCtl{dev: dev, x: absMouseLogicalMaxX / 2, y: absMouseLogicalMaxY / 2}
	fmt.Println("等待 Android input 系统接入 (见 [uhid] 日志)... 输入命令, q 退出")
	fmt.Println("命令: m/move <x> <y> | d/down <x> <y> [button] | u/up [button] | click <x> <y> [button] | drag <x1> <y1> <x2> <y2> [steps] | stream <x1> <y1> <x2> <y2> <ms> [hz] | q")
	repl(func(fields []string) bool {
		cmd := lower(fields[0])
		args, ok := tabletNums(fields, 2)
		switch cmd {
		case "m", "move":
			if !ok {
				fmt.Println("用法: move <x> <y>")
			} else {
				ctl.move(args[0], args[1])
			}
		case "d", "down":
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
			ctl.release(tabletLeft)
		case "click":
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
			dargs, dok := tabletNums(fields, 4)
			if !dok {
				fmt.Println("用法: drag <x1> <y1> <x2> <y2> [steps]")
			} else {
				n := 30
				if len(dargs) >= 5 {
					n = int(dargs[4])
				}
				ctl.drag(tabletLeft, dargs[0], dargs[1], dargs[2], dargs[3], n)
			}
		case "stream":
			sargs := parseNums(fields[1:])
			if len(sargs) < 4 {
				fmt.Println("用法: stream <x1> <y1> <x2> <y2> <duration_ms> [hz]")
			} else {
				durationMs := 8000
				if len(sargs) >= 5 {
					durationMs = int(sargs[4])
				}
				hz := 1000
				if len(sargs) >= 6 {
					hz = int(sargs[5])
				}
				ctl.streamLine(sargs[0], sargs[1], sargs[2], sargs[3], durationMs, hz)
			}
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
