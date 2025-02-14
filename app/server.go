package main

import (
	"fmt"
	"net"
	"os"
	"strings"
)

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Println("Logs from your program will appear here!")
	l, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Println("Failed to bind to port 6379")
		os.Exit(1)
	}
	fmt.Println("Listening on 6379")

	for {
		connection, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			os.Exit(1)
		}
		fmt.Println("Accepted a connection!")
		go handleConnection(connection)
	}
}

func handleConnection(connection net.Conn) {
	defer connection.Close()
	// 创建一个大小为1024字节的缓冲区（buffer），用于临时存储从连接中读取的数据。
	buf := make([]byte, 1024)
	for {
		// 从连接中读取数据，存储到缓冲区中，并返回读取的字节数。
		dataLength, err := connection.Read(buf)
		if err != nil {
			if err.Error() == "EOF" {
				fmt.Println("Connection closed")
				break
			}
			fmt.Println("Error reading:", err.Error())
			break
		}
		if dataLength == 0 {
			fmt.Println("No data read")
			break
		}
		// 将缓冲区中的数据转换为字符串，并根据换行符分割成多个消息。
		messages := strings.Split(string(buf), "\r\n")
		fmt.Println("Received messages:", messages)
		connection.Write([]byte("+PONG\r\n"))
		// for _, message := range messages {
		// 	switch message {
		// 	case "PING":
		// 		connection.Write([]byte("+PONG\r\n"))
		// 	default:
		// 	}
		// }
	}
}

// docker run --rm -it redis redis-cli -h host.docker.internal -p 6379
