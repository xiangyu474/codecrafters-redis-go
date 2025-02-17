package main

import (
	"fmt"
	"time"
)

func main() {
	// 获取当前时间
	currentTime := time.Now()
	fmt.Println("Current Time:", currentTime)

	// 获取当前时间的 Unix 时间戳（毫秒）
	currentUnixMilli := time.Now().UnixMilli()
	fmt.Println("Current Unix Milli:", currentUnixMilli)
}
