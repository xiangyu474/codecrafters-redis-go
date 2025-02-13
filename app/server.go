package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

func main() {
	fmt.Println("Logs from your program will appear here!")

	l, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Println("Failed to bind to port 6379")
		os.Exit(1)
	}
	fmt.Println("Listening on 6379")

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			continue // 遇到错误，不退出程序，而是继续监听
		}
		fmt.Println("Accepted a connection!")

		// 🚀 使用 goroutine 处理每个连接，确保服务器不会被阻塞
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		msg, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println("Client disconnected.")
				return
			}
			fmt.Println("Error reading from connection: ", err.Error())
			return
		}

		// ✨ 解决空消息问题
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}

		fmt.Println("Received: ", msg)

		_, err = conn.Write([]byte("+PONG\r\n"))
		if err != nil {
			fmt.Println("Error writing to connection: ", err.Error())
			return
		}
	}
}
