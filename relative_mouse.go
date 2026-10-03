// 普通 HID 相对鼠标基线，用来和 tablet/Puck 的绝对坐标实验对比。
package main

import (
	"fmt"
	"log"
	"strconv"
)

var relativeMouseReportDesc = []byte{
	0x05, 0x01, 0x09, 0x02, 0xA1, 0x01, 0x09, 0x01, 0xA1, 0x00,
	0x05, 0x09, 0x19, 0x01, 0x29, 0x03, 0x15, 0x00, 0x25, 0x01,
	0x95, 0x03, 0x75, 0x01, 0x81, 0x02, 0x95, 0x01, 0x75, 0x05, 0x81, 0x03,
	0x05, 0x01, 0x09, 0x30, 0x09, 0x31, 0x15, 0x81, 0x25, 0x7F,
	0x75, 0x08, 0x95, 0x02, 0x81, 0x06, // relative X/Y
	0x09, 0x38, 0x15, 0x81, 0x25, 0x7F, 0x75, 0x08, 0x95, 0x01, 0x81, 0x06,
	0xC0, 0xC0,
}

type relativeMouseCtl struct {
	dev     *uhidDev
	buttons uint8
}

func (m *relativeMouseCtl) report(dx, dy, wheel int8) {
	b := make([]byte, 4)
	b[0] = m.buttons
	b[1] = byte(dx)
	b[2] = byte(dy)
	b[3] = byte(wheel)
	if err := m.dev.SendReport(b); err != nil {
		fmt.Println("发送失败:", err)
	}
}

func runRelativeMouse() {
	fmt.Println("root-touch-mapper: UHID 普通相对鼠标基线")
	dev, err := OpenUHID("relative_mouse", "usb/1-1", 0, 0, relativeMouseReportDesc)
	if err != nil {
		log.Fatalf("%v", err)
	}
	go dev.ReadEvents()
	ctl := &relativeMouseCtl{dev: dev}
	fmt.Println("命令: move <dx> <dy> | click | raw <hex> | q")
	repl(func(fields []string) bool {
		cmd := lower(fields[0])
		if cmd == "raw" {
			sendRaw(dev, fields[1:])
			return true
		}
		switch cmd {
		case "move", "m":
			if len(fields) < 3 {
				fmt.Println("用法: move <dx> <dy>")
				break
			}
			dx, e1 := strconv.Atoi(fields[1])
			dy, e2 := strconv.Atoi(fields[2])
			if e1 != nil || e2 != nil {
				fmt.Println("参数错误")
				break
			}
			if dx < -127 {
				dx = -127
			}
			if dx > 127 {
				dx = 127
			}
			if dy < -127 {
				dy = -127
			}
			if dy > 127 {
				dy = 127
			}
			ctl.report(int8(dx), int8(dy), 0)
		case "click":
			ctl.buttons = 1
			ctl.report(0, 0, 0)
			ctl.buttons = 0
			ctl.report(0, 0, 0)
		case "q", "quit", "exit":
			ctl.buttons = 0
			ctl.report(0, 0, 0)
			dev.Close()
			return false
		default:
			fmt.Println("未知命令:", cmd)
		}
		return true
	})
}
