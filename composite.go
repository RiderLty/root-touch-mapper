package main

// 复合 UHID 实验：同一个 HID 设备同时提供触摸屏和绝对 Puck 指针。
// Report ID 1 是触摸屏，Report ID 2 是 tabletReportDesc 的完整报文。

import (
	"encoding/binary"
	"fmt"
	"log"
	"time"
)

var compositeReportDesc = func() []byte {
	// Report ID 是全局项，放在每个顶层应用集合的开始位置。
	touch := append([]byte{}, touchReportDesc...)
	touch = append([]byte{0x85, 0x01}, touch...)
	tablet := append([]byte{}, tabletReportDesc...)
	tablet = append([]byte{0x85, 0x02}, tablet...)
	return append(touch, tablet...)
}()

const (
	compositeTouchReportID  = 1
	compositeTabletReportID = 2
)

type compositeCtl struct {
	dev         *uhidDev
	touchID     uint8
	touchActive bool
	x, y        uint16
	buttons     uint8
	tip         bool
	inRange     bool
}

func (c *compositeCtl) pointerReport() error {
	b := make([]byte, 1+tabletReportSize)
	b[0] = compositeTabletReportID
	b[1] = c.buttons
	if c.tip {
		b[2] |= 1
	}
	if c.inRange {
		b[2] |= 1 << 1
	}
	binary.LittleEndian.PutUint16(b[3:5], c.x)
	binary.LittleEndian.PutUint16(b[5:7], c.y)
	return c.dev.SendReport(b)
}

func (c *compositeCtl) pointerMove(x, y float64) {
	c.x, c.y = scaleTabletX(x), scaleTabletY(y)
	c.inRange = true
	if err := c.pointerReport(); err != nil {
		fmt.Println("指针发送失败:", err)
	}
}

func (c *compositeCtl) pointerDown(x, y float64) {
	c.x, c.y = scaleTabletX(x), scaleTabletY(y)
	c.inRange, c.tip, c.buttons = true, true, tabletLeft
	if err := c.pointerReport(); err != nil {
		fmt.Println("指针发送失败:", err)
	}
}

func (c *compositeCtl) pointerUp() {
	c.buttons, c.tip = 0, false
	if err := c.pointerReport(); err != nil {
		fmt.Println("指针发送失败:", err)
	}
}

func (c *compositeCtl) touchReport(tip bool, x, y float64) error {
	b := make([]byte, 1+touchReportSize)
	b[0] = compositeTouchReportID
	if tip {
		b[1] = 1
	}
	b[2] = c.touchID
	binary.LittleEndian.PutUint32(b[3:7], scaleTouch(x))
	binary.LittleEndian.PutUint32(b[7:11], scaleTouch(y))
	if tip {
		b[11] = 1
	}
	return c.dev.SendReport(b)
}

func (c *compositeCtl) touchDown(x, y float64) {
	c.touchID++
	c.touchActive = true
	if err := c.touchReport(true, x, y); err != nil {
		fmt.Println("触摸发送失败:", err)
	}
}

func (c *compositeCtl) touchUp() {
	if !c.touchActive {
		return
	}
	c.touchActive = false
	if err := c.touchReport(false, 0, 0); err != nil {
		fmt.Println("触摸发送失败:", err)
	}
}

func runComposite() {
	fmt.Println("root-touch-mapper: UHID 复合触摸屏 + 绝对 Puck 指针")
	dev, err := OpenUHID("composite_touch_pointer", "i2c/composite-touch-pointer", 0, 0, compositeReportDesc)
	if err != nil {
		log.Fatalf("复合设备创建失败: %v", err)
	}
	go dev.ReadEvents()
	c := &compositeCtl{dev: dev, x: tabletLogicalMaxShort / 2, y: tabletLogicalMaxLong / 2}
	fmt.Println("命令: pm <x> <y> (指针) | pd/pu (指针按键) | td <x> <y>/tu (触摸) | both <x> <y> | q")
	repl(func(fields []string) bool {
		cmd := lower(fields[0])
		args := parseNums(fields[1:])
		switch cmd {
		case "pm", "pointer", "move":
			if len(args) >= 2 {
				c.pointerMove(args[0], args[1])
			}
		case "pd":
			if len(args) >= 2 {
				c.pointerDown(args[0], args[1])
			}
		case "pu":
			c.pointerUp()
		case "td", "touchdown":
			if len(args) >= 2 {
				c.touchDown(args[0], args[1])
			}
		case "tu", "touchup":
			c.touchUp()
		case "both":
			if len(args) >= 2 {
				c.pointerMove(args[0], args[1])
				c.touchDown(args[0], args[1])
				time.Sleep(200 * time.Millisecond)
				c.touchUp()
			}
		case "q", "quit", "exit":
			c.touchUp()
			c.pointerUp()
			dev.Close()
			return false
		default:
			fmt.Println("未知命令:", cmd)
		}
		return true
	})
}
