package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type entry struct {
	value      string
	expiration int64
}

var (
	kvStore = make(map[string]entry)
	mu      sync.Mutex
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

// parseRESP parses Redis Serialization Protocol (RESP) formatted byte data into string commands.
// It supports RESP Arrays of Bulk Strings format only.
//
// The function takes a byte slice containing RESP formatted data and returns:
// - []string: A slice containing the parsed commands
// - error: An error if the parsing fails due to invalid format
//
// Format expected:
// *<number of arguments>\r\n
// $<number of bytes of argument 1>\r\n
// <argument data>\r\n
// ...
//
// Example input:
// "*2\r\n$4\r\nECHO\r\n$5\r\nhello\r\n"
// Returns: ["ECHO", "hello"], nil
func parseRESP(data []byte) ([]string, error) {

	tokens := strings.Split(strings.TrimSpace(string(data)), "\r\n")
	var commands []string
	i := 0
	for i < len(tokens) {
		if tokens[i] == "" {
			i++
			continue
		}
		switch tokens[i][0] {
		case '*':
			// string to int
			arrayLen, err := strconv.Atoi(tokens[i][1:])
			if err != nil {
				return nil, err
			}
			i++
			commands = make([]string, 0, arrayLen)
			for j := 0; j < arrayLen; j++ {
				if i >= len(tokens) || tokens[i][0] != '$' {
					return nil, fmt.Errorf("invalid bulk string header")
				}
				strLen, err := strconv.Atoi(tokens[i][1:])
				if err != nil {
					return nil, err
				}
				i++
				if i >= len(tokens) || len(tokens[i]) != strLen {
					return nil, fmt.Errorf("invalid string length")
				}
				commands = append(commands, tokens[i])
				i++
			}
		default:
			return nil, fmt.Errorf("unsupported RESP type: %s", tokens[i])
		}
	}
	return commands, nil
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
		// messages := strings.Split(string(buf), "\r\n")
		messages, err := parseRESP(buf[:dataLength])
		if err != nil {
			fmt.Println("Error parsing RESP:", err.Error())
			break
		}
		fmt.Println("Received messages:", messages)
		switch strings.ToUpper(messages[0]) {
		case "PING":
			connection.Write([]byte("+PONG\r\n"))
		case "COMMAND":
			connection.Write([]byte("+OK\r\n"))
		case "ECHO":
			connection.Write([]byte("+" + messages[1] + "\r\n"))
		case "SET":
			if len(messages) < 3 {
				connection.Write([]byte("-ERR wrong number of arguments for 'set' command\r\n"))
				break
			}
			key := messages[1]
			value := messages[2]
			expiryTime := int64(0)
			if len(messages) > 3 && strings.ToUpper(messages[3]) == "PX" {
				if len(messages) != 5 {
					connection.Write([]byte("-ERR wrong number of arguments for 'set' command\r\n"))
					break
				}
				expiry, err := strconv.Atoi(messages[4])
				if err != nil {
					connection.Write([]byte("-ERR invalid expiry time\r\n"))
					break
				}
				expiryTime = time.Now().UnixMilli() + int64(expiry)
			}
			mu.Lock()
			kvStore[key] = entry{value: value, expiration: expiryTime}
			mu.Unlock()
			connection.Write([]byte("+OK\r\n"))
		case "GET":
			if len(messages) != 2 {
				connection.Write([]byte("-ERR wrong number of arguments for 'get' command\r\n"))
				continue
			}
			key := messages[1]
			mu.Lock()
			entry, ok := kvStore[key]
			if ok && entry.expiration > 0 && entry.expiration < time.Now().UnixMilli() {
				delete(kvStore, key)
				ok = false
			}
			mu.Unlock()
			if !ok {
				connection.Write([]byte("$-1\r\n"))
			} else {
				connection.Write([]byte("$" + strconv.Itoa(len(entry.value)) + "\r\n" + entry.value + "\r\n"))
			}
		case "INCR":
			if len(messages) != 2 {
				connection.Write([]byte("-ERR wrong number of arguments for 'incr' command\r\n"))
				continue
			}
			key := messages[1]
			mu.Lock()
			entry_val, ok := kvStore[key]
			if ok && entry_val.expiration > 0 && entry_val.expiration < time.Now().UnixMilli() {
				delete(kvStore, key)
				ok = false
			}
			if ok {
				value, err := strconv.Atoi(entry_val.value)
				if err != nil {
					connection.Write([]byte("-ERR value is not an integer or out of range\r\n"))
				}
				entry_val.value = strconv.Itoa(1 + value)
				kvStore[key] = entry_val
			} else {
				kvStore[key] = entry{value: "1", expiration: int64(0)}
			}
			mu.Unlock()
			connection.Write([]byte(":" + entry_val.value + "\r\n"))
		default:
		}
	}
}

// docker run --rm -it redis redis-cli -h host.docker.internal -p 6379
