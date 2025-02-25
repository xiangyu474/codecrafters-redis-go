根据提供的RDB文件格式规范，我对你当前的代码进行了分析，发现有几处不匹配的地方：

### 1. 6-bit编码处理问题
```go
if firstByte>>6 == 0 {
    fmt.Println("firstByte>>6 == 0")
    return int(firstByte), nil  // 错误
}
```
在6-bit编码中，你应该只保留低6位，而不是整个字节。正确的代码应该是：
```go
return int(firstByte & 0x3F), nil
```

### 2. 特殊字符串编码（11xxxxxx）
你已经注释掉了对这部分的处理，这是问题的主要原因：
```go
// // 11xxxxxx: 特殊编码格式
// if firstByte>>6 == 3 {
//   ...
// }
```

根据规范，当前两位是`11`时，后6位表示特殊字符串编码类型：
- 0xC0: 8位整数
- 0xC1: 16位整数（小端序）
- 0xC2: 32位整数（小端序）
- 0xC3: LZF压缩字符串

### 3. readStringEncoded函数问题
你的`readStringEncoded`函数假设`readSizeEncoded`总是返回一个长度值，然后尝试读取相应数量的字节。但对于特殊编码类型（如整数编码），这是不正确的处理方式。

### 推荐修改：

1. **修复readSizeEncoded函数**：
```go
func readSizeEncoded(reader *bufio.Reader) (int, error) {
    firstByte, err := reader.ReadByte()
    if err != nil {
        return 0, err
    }

    fmt.Printf("First byte: 0x%X\n", firstByte)
    fmt.Printf("firstByte: %b\n", firstByte)

    // 00xxxxxx: 6-bit encoding
    if firstByte>>6 == 0 {
        return int(firstByte & 0x3F), nil
    }

    // 01xxxxxx: 14-bit encoding
    if firstByte>>6 == 1 {
        secondByte, err := reader.ReadByte()
        if err != nil {
            return 0, err
        }
        return int(firstByte&0x3F)<<8 | int(secondByte), nil
    }

    // 10xxxxxx: 32-bit encoding
    if firstByte>>6 == 2 {
        var data uint32
        err := binary.Read(reader, binary.BigEndian, &data)
        if err != nil {
            return 0, err
        }
        return int(data), nil
    }

    // 11xxxxxx: 特殊编码格式 - 这里应该由readStringEncoded处理
    return 0, fmt.Errorf("special encoding format: 0x%X", firstByte)
}
```

2. **重写readStringEncoded函数**：
```go
func readStringEncoded(reader *bufio.Reader) (string, error) {
    firstByte, err := reader.ReadByte()
    if err != nil {
        return "", err
    }

    // 检查是否是特殊编码格式
    if firstByte>>6 == 3 {
        switch firstByte {
        case 0xC0: // 8-bit整数
            b, err := reader.ReadByte()
            if err != nil {
                return "", err
            }
            return strconv.Itoa(int(int8(b))), nil
        case 0xC1: // 16-bit整数
            var val int16
            err := binary.Read(reader, binary.LittleEndian, &val)
            if err != nil {
                return "", err
            }
            return strconv.Itoa(int(val)), nil
        case 0xC2: // 32-bit整数
            var val int32
            err := binary.Read(reader, binary.LittleEndian, &val)
            if err != nil {
                return "", err
            }
            return strconv.Itoa(int(val)), nil
        default:
            return "", fmt.Errorf("unsupported encoding format: 0x%X", firstByte)
        }
    }
    
    // 普通字符串编码
    reader.UnreadByte() // 把读出来的字节放回去，让readSizeEncoded重新读取
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
```

这些修改将使你的代码能够正确处理RDB文件中的各种编码格式，特别是特殊的字符串编码格式。目前你的代码确实存在问题，这就是为什么你在处理某些RDB文件时会遇到错误。