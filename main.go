// root-touch-mapper: UHID 虚拟 HID 设备验证程序 (Android)。
//
// 通过 /dev/uhid 在内核注册虚拟 HID 设备, 逐条命令上报输入报文,
// 用于验证 Android input 系统对各类 HID 设备的识别与处理。
//
// 用法:
//
//	./root-touch-mapper touch   # 虚拟触屏 (默认)
//	./root-touch-mapper pen     # 虚拟手写笔 (压感/侧键/悬停光标)
//
// 代码分层:
//   - uhid.go  /dev/uhid 传输层, 与设备类型无关
//   - touch.go / pen.go   各设备的描述符、报文编码与交互命令
//   - main.go  本文件: 入口分发 + 共享命令循环与工具函数
package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// repl 通用命令循环: 逐行读入并按空白分词, 交给 handle 处理。
// handle 返回 false 时退出。stdin 结束 (EOF) 也退出 —— 便于
// printf '...' | ./root-touch-mapper pen 这样的脚本化测试。
func repl(handle func(fields []string) bool) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		fields := strings.Fields(strings.TrimSpace(sc.Text()))
		if len(fields) == 0 {
			continue
		}
		// sleep <ms>: 公共命令, 脚本化测试时用来控制节奏
		if lower(fields[0]) == "sleep" && len(fields) > 1 {
			if ms, err := strconv.Atoi(fields[1]); err == nil {
				time.Sleep(time.Duration(ms) * time.Millisecond)
				continue
			}
		}
		if !handle(fields) {
			return
		}
	}
}

func lower(s string) string { return strings.ToLower(s) }

// parseNums 把参数解析为浮点数; 出错时打印并返回 nil。
func parseNums(args []string) []float64 {
	nums := make([]float64, 0, len(args))
	for _, a := range args {
		v, err := strconv.ParseFloat(a, 64)
		if err != nil {
			fmt.Printf("参数错误: %q\n", a)
			return nil
		}
		nums = append(nums, v)
	}
	return nums
}

// sendRaw 实现 raw 命令: 直接发送任意原始 HID 报文 (十六进制),
// 便于手工喂测试字节, 不经过任何设备语义封装。
func sendRaw(dev *uhidDev, args []string) {
	if len(args) < 1 {
		fmt.Println("用法: raw <hex>  例如: raw 11400040000002")
		return
	}
	b, err := hex.DecodeString(strings.Join(args, ""))
	if err != nil {
		fmt.Println("hex 解析失败:", err)
		return
	}
	if err := dev.SendReport(b); err != nil {
		fmt.Println("发送失败:", err)
		return
	}
	fmt.Printf("已发送 %d 字节: % X\n", len(b), b)
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	mode := "touch"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}
	switch mode {
	case "touch":
		runTouch()
	case "pen":
		runPen()
	default:
		fmt.Printf("未知设备类型 %q\n用法: %s [touch|pen]\n", mode, os.Args[0])
		os.Exit(2)
	}
}
