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
	// connection, err := l.Accept()
	// if err != nil {
	// 	fmt.Println("Error accepting connection: ", err.Error())
	// 	os.Exit(1)
	// }
	// fmt.Println("Accepted a connection!")
	// buf := make([]byte, 1024)

	for {
		connection, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			os.Exit(1)
		}
		fmt.Println("Accepted a connection!")
		go handleConnection(connection)
		// 	buf := make([]byte, 1024)
		// 	dataLength, err := connection.Read(buf)
		// 	if err != nil {
		// 		if err.Error() == "EOF" {
		// 			fmt.Println("Connection closed")
		// 			break
		// 		}
		// 		fmt.Println("Error reading:", err.Error())
		// 		break
		// 	}
		// 	if dataLength == 0 {
		// 		fmt.Println("No data read")
		// 		break
		// 	}
		// 	messages := strings.Split(string(buf), "\r\n")
		// 	fmt.Println("Received messages:", messages)
		// 	for _, message := range messages {
		// 		switch message {
		// 		case "PING":
		// 			connection.Write([]byte("+PONG\r\n"))
		// 		default:
		// 		}
		// 	}
	}
}

func handleConnection(connection net.Conn) {
	defer connection.Close()
	buf := make([]byte, 1024)
	for {
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
