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

type CommandResult struct {
	Type  string // 响应类型：Simple String(+), Error(-), Integer(:), Bulk String($)
	Value string
}
type client struct {
	inTransaction bool
	commandQueue  [][]string
}

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

func writeRESP(connection net.Conn, result CommandResult) {
	switch result.Type {
	case "+", "-", ":":
		connection.Write([]byte(result.Type + result.Value + "\r\n"))
	case "$":
		if result.Value == "" {
			connection.Write([]byte("$-1\r\n"))
		} else {
			// Bulk Strings
			connection.Write([]byte("$" + strconv.Itoa(len(result.Value)) + "\r\n" + result.Value + "\r\n"))
		}
	}
}

func processCommand(messages []string) CommandResult {
	switch strings.ToUpper(messages[0]) {
	case "PING":
		return CommandResult{Type: "+", Value: "PONG"}
	case "COMMAND":
		return CommandResult{Type: "+", Value: "OK"}
	case "ECHO":
		return CommandResult{Type: "+", Value: messages[1]}
	case "SET":
		if len(messages) < 3 {
			return CommandResult{Type: "-", Value: "ERR wrong number of arguments for 'set' command"}
		}
		key := messages[1]
		value := messages[2]
		expiryTime := int64(0)
		if len(messages) > 3 && strings.ToUpper(messages[3]) == "PX" {
			if len(messages) != 5 {
				return CommandResult{Type: "-", Value: "ERR wrong number of arguments for 'set' command"}
			}
			expiry, err := strconv.Atoi(messages[4])
			if err != nil {
				return CommandResult{Type: "-", Value: "ERR invalid expiry time"}
			}
			expiryTime = time.Now().UnixMilli() + int64(expiry)
		}
		mu.Lock()
		defer mu.Unlock()
		kvStore[key] = entry{value: value, expiration: expiryTime}
		// mu.Unlock()
		return CommandResult{Type: "+", Value: "OK"}
	case "GET":
		if len(messages) != 2 {
			return CommandResult{Type: "-", Value: "ERR wrong number of arguments for 'get' command"}
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
			return CommandResult{Type: "$", Value: ""}
		} else {
			return CommandResult{Type: "$", Value: entry.value}
		}
	case "INCR":
		if len(messages) != 2 {
			return CommandResult{Type: "-", Value: "ERR wrong number of arguments for 'incr' command"}
		}
		key := messages[1]
		mu.Lock()
		defer mu.Unlock()

		entry_val, ok := kvStore[key]
		if ok && entry_val.expiration > 0 && entry_val.expiration < time.Now().UnixMilli() {
			delete(kvStore, key)
			ok = false
		}
		if ok {
			value, err := strconv.Atoi(entry_val.value)
			if err != nil {
				return CommandResult{Type: "-", Value: "ERR value is not an integer or out of range"}
			}
			entry_val.value = strconv.Itoa(1 + value)
			kvStore[key] = entry_val
		} else {
			kvStore[key] = entry{value: "1", expiration: int64(0)}
		}
		return CommandResult{Type: ":", Value: kvStore[key].value}
	}
	return CommandResult{Type: "-", Value: "ERR unknown command"}
}

func executeQueuedCommands(clientState *client, connection net.Conn) {
	// 写入数组长度
	connection.Write([]byte("*" + strconv.Itoa(len(clientState.commandQueue)) + "\r\n"))

	// 执行所有队列中的命令并收集结果
	for _, cmd := range clientState.commandQueue {
		result := processCommand(cmd)
		writeRESP(connection, result)
	}
}

func handleConnection(connection net.Conn) {
	defer connection.Close()
	// 创建一个大小为1024字节的缓冲区（buffer），用于临时存储从连接中读取的数据。
	buf := make([]byte, 1024)
	// 用于存储客户端的状态，包括是否在事务中以及事务中的命令队列。
	clientState := client{
		inTransaction: false,
		commandQueue:  make([][]string, 0),
	}

	for {
		// 从连接中读取数据，存储到缓冲区中，并返回读取的字节数。
		dataLength, err := connection.Read(buf)
		if err != nil {
			if err.Error() == "EOF" {
				fmt.Println("Connection closed")
				break
			} else {
				fmt.Println("Error reading:", err.Error())
				break
			}
		}
		// 在 Redis CLI 中直接按回车不会触发这个错误，因为 Redis CLI 是一个高级客户端，它会过滤掉空输入。
		// 实际上，这段代码中检查 dataLength == 0 可能是多余的，因为：
		// TCP 连接中读取到 0 字节通常意味着连接已经关闭
		// 上面的 err 检查已经能处理连接关闭的情况（EOF）
		// if dataLength == 0 {
		// 	fmt.Println("No data read")
		// 	break
		// }
		// 将缓冲区中的数据转换为字符串，并根据换行符分割成多个消息。
		// messages := strings.Split(string(buf), "\r\n")
		messages, err := parseRESP(buf[:dataLength])
		if err != nil {
			// fmt.Println("Error parsing RESP:", err.Error())
			writeRESP(connection, CommandResult{Type: "-", Value: "ERR " + err.Error()})
			continue
		}
		if clientState.inTransaction && strings.ToUpper(messages[0]) != "EXEC" && strings.ToUpper(messages[0]) != "DISCARD" {
			clientState.commandQueue = append(clientState.commandQueue, messages)
			fmt.Println("Queued messages:", clientState.commandQueue)
			// connection.Write([]byte("+QUEUED\r\n"))
			writeRESP(connection, CommandResult{Type: "+", Value: "QUEUED"})
			continue
		}
		fmt.Println("Received messages:", messages)
		if strings.ToUpper(messages[0]) == "MULTI" {
			if clientState.inTransaction {
				writeRESP(connection, CommandResult{Type: "-", Value: "ERR MULTI calls can not be nested"})
			} else {
				clientState.inTransaction = true
				writeRESP(connection, CommandResult{Type: "+", Value: "OK"})
			}
			continue
		}
		if strings.ToUpper(messages[0]) == "EXEC" {
			if !clientState.inTransaction {
				writeRESP(connection, CommandResult{Type: "-", Value: "ERR EXEC without MULTI"})
			} else {
				executeQueuedCommands(&clientState, connection)
				clientState.inTransaction = false
				clientState.commandQueue = make([][]string, 0)
			}
			continue
		}

		if strings.ToUpper(messages[0]) == "DISCARD" {
			if !clientState.inTransaction {
				writeRESP(connection, CommandResult{Type: "-", Value: "ERR DISCARD without MULTI"})
			} else {
				clientState.inTransaction = false
				clientState.commandQueue = make([][]string, 0)
				writeRESP(connection, CommandResult{Type: "+", Value: "OK"})
			}
			continue
		}
		result := processCommand(messages)
		writeRESP(connection, result)
	}
}

// docker run --rm -it redis redis-cli -h host.docker.internal -p 6379
