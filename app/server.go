package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type entry struct {
	value      string
	expiration int64
}

type config struct {
	dir        string
	dbfilename string
}

var (
	kvStore = make(map[string]entry)
	mu      sync.Mutex
	cfg     = config{
		dir:        "/tmp",
		dbfilename: "dump.rdb",
	}
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

	// 1. Parse command line arguments
	dir := flag.String("dir", "/tmp", "Redis data directory")
	dbfilename := flag.String("dbfilename", "dump.rdb", "Redis data file name")
	flag.Parse()
	// 2. Set the data directory and file name in the config struct
	if *dir != "" {
		cfg.dir = *dir
	}
	if *dbfilename != "" {
		cfg.dbfilename = *dbfilename
	}
	fmt.Printf("dir: %s, dbfilename: %s\r\n", cfg.dir, cfg.dbfilename)

	// 3. Load the RDB file
	err := loadRDBFile()
	if err != nil {
		fmt.Println("Failed to load RDB file:", err)
		os.Exit(1)
	}
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
	case "*":
		connection.Write([]byte("*" + result.Value))
	}
}

func processCommand(messages []string) CommandResult {
	switch strings.ToUpper(messages[0]) {
	case "CONFIG":
		if len(messages) < 2 {
			return CommandResult{Type: "-", Value: "ERR wrong number of arguments for 'config' command"}
		}
		if strings.ToUpper(messages[1]) == "GET" {
			if len(messages) != 3 {
				return CommandResult{Type: "-", Value: "ERR wrong number of arguments for 'config get' command"}
			}
			switch strings.ToUpper(messages[2]) {
			case "DIR":
				return CommandResult{
					Type:  "*",
					Value: fmt.Sprintf("2\r\n$3\r\ndir\r\n$%d\r\n%s\r\n", len(cfg.dir), cfg.dir),
				}
			case "DBFILENAME":
				return CommandResult{
					Type:  "*",
					Value: fmt.Sprintf("2\r\n$10\r\ndbfilename\r\n$%d\r\n%s\r\n", len(cfg.dbfilename), cfg.dbfilename),
				}
			default:
			}
		}
	case "KEYS":
		if len(messages) != 2 {
			return CommandResult{Type: "-", Value: "ERR wrong number of arguments for 'keys' command"}
		}
		mu.Lock()
		defer mu.Unlock()
		resp := fmt.Sprintf("%d\r\n", len(kvStore))
		for key := range kvStore {
			resp += fmt.Sprintf("$%d\r\n%s\r\n", len(key), key)
		}
		return CommandResult{
			Type:  "*",
			Value: resp,
		}
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

func readStringEncoded(reader *bufio.Reader) (string, error) {
	length, err := readSizeEncoded(reader)
	if err != nil {
		return "", err
	}
	data := make([]byte, length)
	_, err = io.ReadFull(reader, data)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readSizeEncoded(reader *bufio.Reader) (int, error) {
	firstByte, err := reader.ReadByte()
	if err != nil {
		return 0, err
	}

	fmt.Printf("First byte: 0x%X\n", firstByte) // 调试信息
	// 调试，打印原始数据
	fmt.Printf("firstByte: %b\n", firstByte)

	if firstByte>>6 == 0 {
		// 00xxxxxx: 6-bit encoding
		// 如果第一个字节的高 2 位是 00，表示这是一个 6-bit 编码，直接返回这个数值即可。
		fmt.Println("firstByte>>6 == 0")
		return int(firstByte), nil
	}

	if firstByte>>6 == 1 {
		// 01xxxxxx: 14-bit encoding
		fmt.Println("firstByte>>6 == 1")
		secondByte, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		// firstByte & 0x3F 这部分的作用是保留 firstByte 的低 6 位，去掉高 2 位。
		// 左移 8 位（<< 8）表示把 firstByte & 0x3F 这个数值扩大 256 倍，相当于把它放到高 8 位。
		// | 按位或 运算是把 secondByte 拼接到右边，这样就形成一个完整的 14-bit 数值
		return int(firstByte&0x3F)<<8 | int(secondByte), nil
	}

	if firstByte>>6 == 2 {
		// 10xxxxxx: 32-bit encoding
		fmt.Printf("firstByte == 0x80")
		var data uint32
		err := binary.Read(reader, binary.BigEndian, &data)
		if err != nil {
			return 0, err
		}
		return int(data), nil
	}

	// // 11xxxxxx: 特殊编码格式
	// if firstByte>>6 == 3 {
	// 	fmt.Println("firstByte>>6 == 3")
	// 	secondByte, err := reader.ReadByte()
	// 	if err != nil {
	// 		return 0, err
	// 	}
	// 	fmt.Printf("Unexpected encoding: firstByte=0x%X, secondByte=0x%X\n", firstByte, secondByte)
	// 	return 0, fmt.Errorf("unknown encoding format: 0x%X", firstByte)
	// }

	return 0, fmt.Errorf("unknown encoding format: 0x%X", firstByte)
}

func loadRDBFile() error {
	filePath := filepath.Join(cfg.dir, cfg.dbfilename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("RDB file does not exist, starting with empty database")
			return nil
		}
		return fmt.Errorf("failed to read RDB file: %w", err)
	}

	// 将字节切片转换为带有缓冲功能的 bufio.Reader，以便使用 readStringEncoded 和 readSizeEncoded 函数读取数据。
	reader := bufio.NewReader(bytes.NewReader(data))

	header := make([]byte, 9)
	_, err = io.ReadFull(reader, header)
	if err != nil {
		return fmt.Errorf("failed to read RDB header: %w", err)
	}
	// 打印解析出来的header redis
	fmt.Printf("header: %s\n", header)

	var expireTime int64 = 0 // 记录当前 key 的过期时间

	for {
		b, err := reader.ReadByte()
		if err != nil {
			return fmt.Errorf("failed to read next byte: %w", err)
		}
		fmt.Printf("b: 0x%X\n", b)
		switch b {
		case 0xFA:
			metaKey, err := readStringEncoded(reader)
			if err != nil {
				return fmt.Errorf("failed to read meta key: %w", err)
			}
			fmt.Printf("Meta key: %s\n", metaKey)

			firstByte, err := reader.ReadByte()
			if err != nil {
				return fmt.Errorf("failed to read next byte: %w", err)
			}
			// 如果是整数类型 (0xC0，0xC1等)
			if firstByte>>6 == 3 {
				fmt.Printf("Skipping non-string meta value for key: %s\n", metaKey)
				var skipBytes int
				switch firstByte {
				case 0xC0:
					skipBytes = 1
				case 0xC1:
					skipBytes = 2
				case 0xC2:
					skipBytes = 4
				case 0xC3:
					skipBytes = 8
				}
				_, err := reader.Discard(skipBytes)
				if err != nil {
					return fmt.Errorf("failed to skip non-string meta value: %w", err)
				}
			} else {
				reader.UnreadByte()
				metaValue, err := readStringEncoded(reader)
				if err != nil {
					return fmt.Errorf("failed to read meta value: %w", err)
				}
				fmt.Printf("Meta value: %s\n", metaValue)
			}
		case 0xFE:
			dbIndex, err := readSizeEncoded(reader)
			if err != nil {
				return fmt.Errorf("failed to read db index: %w", err)
			}
			fmt.Printf("Loading data for DB %d\n", dbIndex)
			nextByte, _ := reader.ReadByte()
			if nextByte == 0xFB {
				hashTablesize, _ := readSizeEncoded(reader)
				fmt.Printf("Hash table size: %d\n", hashTablesize)
				hashTableExpire, _ := readSizeEncoded(reader)
				fmt.Printf("Hash table expire: %d\n", hashTableExpire)
			} else {
				reader.UnreadByte()
			}
		case 0xFF:
			return nil
		case 0xFC, 0xFD:
			if b == 0xFC { // 毫秒（8 字节）
				err := binary.Read(reader, binary.LittleEndian, &expireTime)
				if err != nil {
					return fmt.Errorf("failed to read expire time: %w", err)
				}
			} else if b == 0xFD { // 秒（4 字节）
				var expireSec int32
				err := binary.Read(reader, binary.LittleEndian, &expireSec)
				if err != nil {
					return fmt.Errorf("failed to read expire time: %w", err)
				}
				expireTime = int64(expireSec) * 1000
			}
			fmt.Printf("Expire time: %d\n", expireTime)
		default:
			fmt.Printf("default: 0x%X\n", b)
			// reader.UnreadByte()
			// 读取键值对
			key, err := readStringEncoded(reader)
			fmt.Printf("Key read: %s\n", key)
			if err != nil {
				return fmt.Errorf("failed to read key: %w", err)
			}

			value, err := readStringEncoded(reader)
			fmt.Printf("Value read: %s\n", value)
			if err != nil {
				return fmt.Errorf("failed to read value: %w", err)
			}

			// 存储键值对
			mu.Lock()
			kvStore[key] = entry{value: value, expiration: expireTime}
			mu.Unlock()
			expireTime = 0
		}
	}
}

// docker run --rm -it redis redis-cli -h host.docker.internal -p 6379
// data\dump.rdb
// go run app/server.go --dir data --dbfilename dump.rdb;

// docker run --name myredis -d redis
// docker exec -it myredis redis-cli
// docker exec -it myredis sh -c "ls /data"
// docker cp myredis:/data/dump.rdb .
