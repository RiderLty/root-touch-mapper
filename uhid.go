// /dev/uhid 传输层 —— 与具体 HID 设备类型无关。
//
// 这里只负责两件事:
//  1. 通过 UHID_CREATE2 用任意 report descriptor 在内核注册虚拟 HID 设备;
//  2. 通过 UHID_INPUT2 发送任意已编码好的 HID 输入报文。
//
// 具体设备 (触屏 / 手写笔) 的描述符与报文编码分别在 touch.go / pen.go。
//
// 内核接口定义见 include/uapi/linux/uhid.h (UHID_CREATE2 / UHID_INPUT2)。
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
)

const (
	uhidDataMax = 4096

	UHID_DESTROY          = 1
	UHID_START            = 2
	UHID_STOP             = 3
	UHID_OPEN             = 4
	UHID_CLOSE            = 5
	UHID_OUTPUT           = 6
	UHID_GET_REPORT       = 9
	UHID_GET_REPORT_REPLY = 10
	UHID_CREATE2          = 11
	UHID_INPUT2           = 12

	busUSB = 0x03
	// 伪装成内置 I2C 设备, 让 Android EventHub 的 isExternal() 判定为 false
	busI2C = 0x18
)

// struct uhid_event 的大小: __u32 type + union (最大成员 uhid_create2_req 4372 字节)
const uhidEventSize = 4 + 128 + 64 + 64 + 2 + 2 + 4 + 4 + 4 + 4 + uhidDataMax

type uhidDev struct {
	f *os.File
	// 复用的 UHID_INPUT2 写缓冲 (6 字节头 + 报文本体)
	in2buf []byte
}

// OpenUHIDBus 与 OpenUHID 相同, 但允许指定 bus 类型 (如 busUSB 模拟外接设备)。
func OpenUHIDBus(name, phys string, vendor, product uint32, bus uint16, reportDesc []byte) (*uhidDev, error) {
	f, err := os.OpenFile("/dev/uhid", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("打开 /dev/uhid 失败 (需要 root / 内核需启用 CONFIG_UHID): %w", err)
	}

	buf := make([]byte, uhidEventSize)
	binary.LittleEndian.PutUint32(buf[0:4], UHID_CREATE2)
	off := 4
	copy(buf[off:], name) // name[128]
	off += 128            // phys[64]
	copy(buf[off:], phys)
	off += 64 // uniq[64]
	off += 64
	binary.LittleEndian.PutUint16(buf[off:], uint16(len(reportDesc)))
	off += 2
	binary.LittleEndian.PutUint16(buf[off:], bus)
	off += 2
	binary.LittleEndian.PutUint32(buf[off:], vendor)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], product)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], 0) // version: 非零会像 bcdDevice 一样泄露 USB 来源
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], 0) // country
	off += 4
	copy(buf[off:], reportDesc)

	if _, err := f.Write(buf); err != nil {
		f.Close()
		return nil, fmt.Errorf("UHID_CREATE2 写入失败: %w", err)
	}
	return &uhidDev{f: f}, nil
}

// ReadEvents 循环读取内核事件 (UHID_START/OPEN/CLOSE 等), 需在独立 goroutine 中运行。
func (d *uhidDev) ReadEvents() {
	buf := make([]byte, uhidEventSize)
	for {
		n, err := d.f.Read(buf)
		if err != nil {
			log.Printf("[uhid] 读事件退出: %v", err)
			return
		}
		if n < 4 {
			continue
		}
		typ := binary.LittleEndian.Uint32(buf[0:4])
		switch typ {
		case UHID_START:
			log.Printf("[uhid] 设备已启动 (HID 层绑定成功)")
		case UHID_OPEN:
			log.Printf("[uhid] 有进程打开了输入设备 (Android input 系统已接入)")
		case UHID_CLOSE:
			log.Printf("[uhid] 输入设备被关闭")
		case UHID_STOP:
			log.Printf("[uhid] 设备已停止")
		case UHID_OUTPUT:
			log.Printf("[uhid] 收到 OUTPUT 事件 (LED 等, 忽略)")
		case UHID_GET_REPORT:
			if n >= 12 {
				// 立即回一个错误, 避免驱动请求 feature report 时等待 5s 超时。
				// 验证阶段没有可回的 feature report。
				id := binary.LittleEndian.Uint32(buf[4:8])
				reply := make([]byte, 12)
				binary.LittleEndian.PutUint32(reply[0:4], UHID_GET_REPORT_REPLY)
				binary.LittleEndian.PutUint32(reply[4:8], id)
				binary.LittleEndian.PutUint16(reply[8:10], 1) // err = EPERM, size = 0
				if _, err := d.f.Write(reply); err != nil {
					log.Printf("[uhid] 回复 GET_REPORT 失败: %v", err)
				}
			}
		default:
			log.Printf("[uhid] 事件 type=%d len=%d", typ, n)
		}
	}
}

// SendReport 发送一份已编码好的 HID 输入报文 (UHID_INPUT2)。
// 报文的字节布局由各设备实现文件自己定义, 这里不做任何解释。
func (d *uhidDev) SendReport(report []byte) error {
	if cap(d.in2buf) < 6+len(report) {
		d.in2buf = make([]byte, 6+len(report))
	}
	b := d.in2buf[:6+len(report)]
	binary.LittleEndian.PutUint32(b[0:4], UHID_INPUT2)
	binary.LittleEndian.PutUint16(b[4:6], uint16(len(report)))
	copy(b[6:], report)
	_, err := d.f.Write(b)
	return err
}

func (d *uhidDev) Close() error {
	return d.f.Close()
}

// OpenUHID 用 I2C bus 伪装成内置设备的默认注册入口。
func OpenUHID(name, phys string, vendor, product uint32, reportDesc []byte) (*uhidDev, error) {
	return OpenUHIDBus(name, phys, vendor, product, busI2C, reportDesc)
}
