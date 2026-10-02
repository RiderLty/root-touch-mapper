// 虚拟触屏设备 —— 单指 Win8 触屏。
//
// 描述符与 hid_touch/hid_touch.ino 完全一致 (Digitizer / Touch Screen:
// Tip Switch + Contact ID + 32bit X/Y + Contact Count),
// Android 经 hid-multitouch 驱动识别为内置触摸屏。
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"time"
)

// 与 hid_touch/hid_touch.ino 的 report_descriptor 完全一致。
// 该描述符符合 Win8 触屏规范, Android 可直接识别。
var touchReportDesc = []byte{
	0x05, 0x0D, // Usage Page (Digitizer)
	0x09, 0x04, // Usage (Touch Screen)
	0xA1, 0x01, // Collection (Application)
	// Finger 1
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

// HID 输入报文, 11 字节, 与 .ino 的 SERIAL_BUFFER_SIZE 报文一致:
// [0] tip switch(1bit) + 7bit padding
// [1] contact identifier
// [2:6] X  uint32 LE
// [6:10] Y uint32 LE
// [10] contact count
const touchReportSize = 11

// 与 u_input_interface.go / hid_touch.ino 一致: X/Y 的 32bit 逻辑最大值
const touchLogicalMax = 0x7ffffffe

// scaleTouch 把 0~1 归一化坐标映射到描述符声明的 32bit 逻辑区间。
func scaleTouch(v float64) uint32 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return uint32(v * float64(touchLogicalMax))
}

type touchCtl struct {
	dev  *uhidDev
	id   uint8 // 当前 contact identifier, hid-multitouch 用它做 slot 跟踪
	down bool
}

func (t *touchCtl) send(tip bool, contactID uint8, x, y uint32) error {
	b := make([]byte, touchReportSize)
	if tip {
		b[0] = 1
	}
	b[1] = contactID
	binary.LittleEndian.PutUint32(b[2:6], x)
	binary.LittleEndian.PutUint32(b[6:10], y)
	if tip {
		b[10] = 1
	}
	return t.dev.SendReport(b)
}

func (t *touchCtl) press(x, y float64) {
	t.id++
	t.down = true
	t.send(true, t.id, scaleTouch(x), scaleTouch(y))
}

func (t *touchCtl) move(x, y float64) {
	if !t.down {
		return
	}
	t.send(true, t.id, scaleTouch(x), scaleTouch(y))
}

func (t *touchCtl) up() {
	if !t.down {
		return
	}
	t.down = false
	// 抬起: tip switch=0, 保持上一次坐标, contact count=0
	t.send(false, t.id, 0, 0)
}

func (t *touchCtl) tap(x, y float64) {
	t.press(x, y)
	time.Sleep(60 * time.Millisecond)
	t.up()
	time.Sleep(30 * time.Millisecond)
}

func (t *touchCtl) swipe(x1, y1, x2, y2 float64, steps int) {
	if steps <= 0 {
		steps = 20
	}
	t.press(x1, y1)
	time.Sleep(30 * time.Millisecond)
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		t.move(x1+(x2-x1)*f, y1+(y2-y1)*f)
		time.Sleep(10 * time.Millisecond)
	}
	t.up()
	time.Sleep(30 * time.Millisecond)
}

func (t *touchCtl) demo() {
	fmt.Println("demo: tap (0.5,0.5) tap (0.3,0.7) swipe (0.8,0.8)->(0.2,0.2)")
	t.tap(0.5, 0.5)
	time.Sleep(500 * time.Millisecond)
	t.tap(0.3, 0.7)
	time.Sleep(500 * time.Millisecond)
	t.swipe(0.8, 0.8, 0.2, 0.2, 25)
	fmt.Println("demo 完成")
}

// runTouch 启动虚拟触屏并进入交互命令。
func runTouch() {
	fmt.Println("root-touch-mapper: UHID 虚拟触屏验证")

	dev, err := OpenUHID("goodix_ts", "i2c/1-005d", 0, 0, touchReportDesc)
	if err != nil {
		log.Fatalf("%v", err)
	}
	go dev.ReadEvents()

	ctl := &touchCtl{dev: dev}
	fmt.Println("等待 Android input 系统接入 (见 [uhid] 日志)... 输入命令或 demo, q 退出")
	fmt.Println("命令: d/m <x> <y> | u | tap <x> <y> | swipe <x1> <y1> <x2> <y2> [steps] | demo | raw <hex> | q")

	repl(func(fields []string) bool {
		cmd := lower(fields[0])
		if cmd == "raw" { // raw 的参数是十六进制, 不做浮点解析
			sendRaw(dev, fields[1:])
			return true
		}
		args := parseNums(fields[1:])
		switch cmd {
		case "d":
			if len(args) < 2 {
				fmt.Println("用法: d <x> <y>")
			} else {
				ctl.press(args[0], args[1])
			}
		case "m":
			if len(args) < 2 {
				fmt.Println("用法: m <x> <y>")
			} else {
				ctl.move(args[0], args[1])
			}
		case "u":
			ctl.up()
		case "tap":
			if len(args) < 2 {
				fmt.Println("用法: tap <x> <y>")
			} else {
				ctl.tap(args[0], args[1])
			}
		case "swipe":
			if len(args) < 4 {
				fmt.Println("用法: swipe <x1> <y1> <x2> <y2> [steps]")
			} else {
				steps := 20
				if len(args) >= 5 {
					steps = int(args[4])
				}
				ctl.swipe(args[0], args[1], args[2], args[3], steps)
			}
		case "demo":
			ctl.demo()
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
